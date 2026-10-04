package main

import (
	"encoding/json"
	"fmt"

	tea "charm.land/bubbletea/v2"
)

type automationsUI struct {
	loaded, labelingEnabled bool
	pending, paused         []string
}

type automationStatus struct {
	Repository string   `json:"repository"`
	Enabled    bool     `json:"enabled"`
	Pending    []string `json:"pending_items"`
	Paused     []string `json:"paused_items"`
}

type automationPlan struct {
	Repository    string `json:"repository"`
	Operation     string `json:"operation"`
	Before        bool   `json:"before"`
	After         bool   `json:"after"`
	PreviewSHA256 string `json:"preview_sha256"`
}

type automationMsg struct {
	root, repo, operation string
	request               uint64
	status                automationStatus
	err                   error
}

func automationStatusCmd(root, repo string, request uint64) tea.Cmd {
	return func() tea.Msg {
		out, err := runScript(root, "item-labels", "status", "--expected-repo", repo)
		msg := automationMsg{root: root, repo: repo, request: request, operation: "status", err: err}
		if err == nil {
			msg.err = json.Unmarshal([]byte(out), &msg.status)
		}
		return msg
	}
}

func automationToggleCmd(root, repo string, request uint64, before, after bool) tea.Cmd {
	return func() tea.Msg {
		msg := automationMsg{root: root, repo: repo, request: request, operation: "toggle"}
		operation := "disable"
		if after {
			operation = "enable"
		}
		out, err := runScript(root, "item-labels", operation, "--expected-repo", repo)
		if err != nil {
			msg.err = err
			return msg
		}
		var plan automationPlan
		if err := json.Unmarshal([]byte(out), &plan); err != nil {
			msg.err = err
			return msg
		}
		if plan.Repository != repo || plan.Operation != operation || plan.Before != before || plan.After != after || plan.PreviewSHA256 == "" {
			msg.err = fmt.Errorf("labeling policy changed; refresh Automations")
			return msg
		}
		out, err = runScript(root, "item-labels", operation, "--expected-repo", repo, "--apply", "--preview-sha256", plan.PreviewSHA256)
		if err != nil {
			msg.err = err
			return msg
		}
		msg.err = json.Unmarshal([]byte(out), &msg.status)
		return msg
	}
}

func (m model) automationsView() string {
	state := "Loading…"
	labeling := "Reading repository setting…"
	if m.settings.automations.loaded {
		state = "1 available · 1 planned"
		labeling = "OFF · Agent label writes disabled"
		if m.settings.automations.labelingEnabled {
			labeling = "ON · Agent may apply proposed labels to GitHub"
		}
		if pending := len(m.settings.automations.pending); pending > 0 {
			labeling += fmt.Sprintf(" · %d uncertain", pending)
		}
		if paused := len(m.settings.automations.paused); paused > 0 {
			labeling += fmt.Sprintf(" · %d paused", paused)
		}
	}
	if m.settings.busy {
		state = "Working…"
	}
	cards := [][2]string{
		{"Labeling", labeling},
		{"Scoring", "Planned · Individual merit score from 0 to 5"},
	}
	return inset(titleBar("Automations", state, m.menuWidth())) + "\n\n" + cardList(cards, m.settings.selected, m.cardWidth(), m.mainHeight()-2)
}

func (m model) labelingPrompt() string {
	setting := "ON"
	if !m.settings.automations.labelingEnabled {
		setting = "OFF"
	}
	return m.yankHeader("Labeling automation") + fmt.Sprintf(`
Please run one bounded labeling pass for %s from %s.

Follow prompts/label-items.md. Start with open items lacking observed labels, including items that already have a decision. Read their evidence, propose only names in this repository's observed GitHub label catalog, and leave Action unassessed unless you have separately evaluated it. Do not mark decisions reviewed.

Item labeling is currently %s for this repository. If it is OFF, prepare proposals and stop before a GitHub write. If it is ON, preview the exact selected item keys and additions or removals, inspect that list, then run only that matching bounded plan. Revalidate, record outcomes, stop on an uncertain write, and respect later human corrections. Report item keys, labels changed, skipped items, and any gaps. Refresh the ledger before using applied labels to suggest actions.
`, m.repo, m.installRoot, setting)
}

func (m model) handleAutomationsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "h", "left":
		m.settings.section = ""
		m.settings.selected = 0
	case "q":
		return m.requestQuit()
	case "?":
		m.showHelp = !m.showHelp
	case "j", "down", "tab":
		m.settingsMove(1)
	case "G", "end":
		m.settings.selected = 1
	case "k", "up", "shift+tab":
		m.settingsMove(-1)
	case "g", "home":
		m.settings.selected = 0
	case "r":
		m.settings.busy = true
		m.settings.request++
		return m, automationStatusCmd(m.installRoot, m.repo, m.settings.request)
	case "enter", "l", "right", "space", "e":
		if m.settings.selected == 1 {
			m.status = "Scoring automation is planned and cannot be enabled yet."
			return m, nil
		}
		if !m.settings.automations.loaded {
			m.status = "Reading this repository's automation setting…"
			return m, nil
		}
		m.settings.busy = true
		m.settings.request++
		before := m.settings.automations.labelingEnabled
		return m, automationToggleCmd(m.installRoot, m.repo, m.settings.request, before, !before)
	case "y":
		if m.settings.selected == 1 {
			m.status = "The scoring pass and its agent prompt are planned."
			return m, nil
		}
		if !m.settings.automations.loaded {
			m.status = "Reading this repository's automation setting…"
			return m, nil
		}
		m.status = "Taking Labeling automation prompt…"
		return m, yankCmd(m.installRoot, m.repo, "Labeling automation prompt", m.labelingPrompt())
	}
	return m, nil
}

func (m model) finishAutomation(msg automationMsg) (tea.Model, tea.Cmd) {
	if !m.settings.open || m.settings.section != "automations" || msg.root != m.installRoot || msg.repo != m.repo || msg.request != m.settings.request {
		return m, nil
	}
	m.settings.busy = false
	if msg.err != nil {
		m.failErr("Couldn't update Automations", msg.err)
		return m, nil
	}
	if msg.status.Repository != m.repo {
		m.fail("Automation status belongs to another repository.")
		return m, nil
	}
	m.settings.automations = automationsUI{loaded: true, labelingEnabled: msg.status.Enabled, pending: msg.status.Pending, paused: msg.status.Paused}
	if msg.operation == "toggle" {
		state := "OFF"
		if msg.status.Enabled {
			state = "ON"
		}
		m.status = "Labeling automation is " + state + " for " + m.repo + "."
	}
	return m, nil
}
