package main

import (
	"encoding/json"
	"fmt"

	tea "charm.land/bubbletea/v2"
)

type automationsUI struct {
	loaded, labelingEnabled bool
	pending, paused         []string
	actions                 []automationAction
}

type automationAction struct {
	Name             string `json:"name"`
	Operation        string `json:"operation"`
	Mode             string `json:"mode"`
	DefinitionSHA256 string `json:"definition_sha256"`
	Writable         bool   `json:"writable"`
	Configured       bool   `json:"configured"`
	StaleSetting     bool   `json:"stale_setting"`
}

type actionPolicyStatus struct {
	Repository string             `json:"repository"`
	Actions    []automationAction `json:"actions"`
}

type actionPolicyPlan struct {
	Repository       string `json:"repository"`
	Action           string `json:"action"`
	Operation        string `json:"operation"`
	DefinitionSHA256 string `json:"definition_sha256"`
	Before           string `json:"before"`
	After            string `json:"after"`
	PreviewSHA256    string `json:"preview_sha256"`
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
	actions               actionPolicyStatus
	err                   error
}

func readAutomationStatus(root, repo string, msg *automationMsg) error {
	out, err := runScript(root, "item-labels", "status", "--expected-repo", repo)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(out), &msg.status); err != nil {
		return err
	}
	out, err = runScript(root, "action-policy", "status", "--expected-repo", repo)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(out), &msg.actions)
}

func automationStatusCmd(root, repo string, request uint64) tea.Cmd {
	return func() tea.Msg {
		msg := automationMsg{root: root, repo: repo, request: request, operation: "status"}
		msg.err = readAutomationStatus(root, repo, &msg)
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
		msg.err = readAutomationStatus(root, repo, &msg)
		return msg
	}
}

func automationActionCmd(root, repo string, request uint64, action, operation, definition, before, after string) tea.Cmd {
	return func() tea.Msg {
		msg := automationMsg{root: root, repo: repo, request: request, operation: "action"}
		out, err := runScript(root, "action-policy", "set", "--expected-repo", repo, "--action", action, "--mode", after)
		if err != nil {
			msg.err = err
			return msg
		}
		var plan actionPolicyPlan
		if err := json.Unmarshal([]byte(out), &plan); err != nil {
			msg.err = err
			return msg
		}
		if plan.Repository != repo || plan.Action != action || plan.Operation != operation || plan.DefinitionSHA256 != definition || plan.Before != before || plan.After != after || plan.PreviewSHA256 == "" {
			msg.err = fmt.Errorf("action policy changed; refresh Automations")
			return msg
		}
		if _, err := runScript(root, "action-policy", "set", "--expected-repo", repo, "--action", action, "--mode", after, "--apply", "--preview-sha256", plan.PreviewSHA256); err != nil {
			msg.err = err
			return msg
		}
		msg.err = readAutomationStatus(root, repo, &msg)
		return msg
	}
}

func (m model) automationsView() string {
	state := "Loading…"
	labeling := "Reading repository setting…"
	if m.settings.automations.loaded {
		state = fmt.Sprintf("%d available · 1 planned", 1+len(m.settings.automations.actions))
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
		{"Scoring", "Local · 0–5 quality/readiness · y copies a bounded pass prompt"},
	}
	for _, action := range m.settings.automations.actions {
		summary := "STAGE · Suggested " + action.Operation + " waits in Notifications"
		if action.Mode == "execute" {
			summary = "EXECUTE · Agent pass may publish a checked " + action.Operation
		}
		if action.StaleSetting {
			summary += " · Prior setting expired after action edit"
		}
		cards = append(cards, [2]string{action.Name, summary})
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

func (m model) scoringPrompt() string {
	return m.yankHeader("Item scoring") + fmt.Sprintf(`
Please score a bounded selection for %s from %s.

Follow prompts/score-items.md. Use at most 20 named issues and PRs or the first 20 keys of one selected batch. Read selected immutable evidence offline, score quality/readiness with separate issue and PR dimensions, and record a reason and one suggested next check. Leave missing or unverifiable cases unassessed. Save each result through bin/item-score and report the score, source snapshot/revision and gaps. Do not change triage decisions, mark them reviewed, act on GitHub, or change code or PR diffs.
`, m.repo, m.installRoot)
}

func (m model) actionPrompt(action automationAction) string {
	return m.yankHeader("Automated action pass") + fmt.Sprintf(`
Please prepare one bounded %s action pass for %s from %s.

Follow prompts/automated-actions.md. Ask me for selected item keys when the scope is unclear, then choose a bounded script budget yourself and use saved evidence first. Read each item's saved decision, human guidance, earlier objections and selected evidence. Draft the exact public comment in a UTF-8 file, then save the proposal through bin/action-proposals for any writing operation. Do not mark the ledger decision reviewed.

This repository currently sets %s to %s. Preview only the named keys with bin/action-pass and inspect the exact target, operation, comment, evidence and mode. Run only that matching preview. Stage any disputed or incomplete item for a person even if the type is configured to execute. Stop on an uncertain write and report each outcome. Do not change code or PR diffs.
`, action.Name, m.repo, m.installRoot, action.Name, action.Mode)
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
		m.settings.selected = 1 + len(m.settings.automations.actions)
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
			m.status = "Press y to copy the bounded local scoring prompt."
			return m, nil
		}
		if !m.settings.automations.loaded {
			m.status = "Reading this repository's automation setting…"
			return m, nil
		}
		m.settings.busy = true
		m.settings.request++
		if m.settings.selected >= 2 {
			action := m.settings.automations.actions[m.settings.selected-2]
			after := "execute"
			if action.Mode == "execute" {
				after = "stage"
			}
			return m, automationActionCmd(m.installRoot, m.repo, m.settings.request, action.Name, action.Operation, action.DefinitionSHA256, action.Mode, after)
		}
		before := m.settings.automations.labelingEnabled
		return m, automationToggleCmd(m.installRoot, m.repo, m.settings.request, before, !before)
	case "y":
		if m.settings.selected == 1 {
			m.status = "Taking Item scoring prompt…"
			return m, yankCmd(m.installRoot, m.repo, "Item scoring prompt", m.scoringPrompt())
		}
		if m.settings.selected >= 2 {
			if !m.settings.automations.loaded {
				m.status = "Reading this repository's automation setting…"
				return m, nil
			}
			action := m.settings.automations.actions[m.settings.selected-2]
			m.status = "Taking " + action.Name + " action prompt…"
			return m, yankCmd(m.installRoot, m.repo, "Action automation prompt", m.actionPrompt(action))
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
	if msg.status.Repository != m.repo || msg.actions.Repository != m.repo {
		m.fail("Automation status belongs to another repository.")
		return m, nil
	}
	actions := []automationAction{}
	for _, action := range msg.actions.Actions {
		if action.Writable {
			actions = append(actions, action)
		}
	}
	m.settings.automations = automationsUI{loaded: true, labelingEnabled: msg.status.Enabled, pending: msg.status.Pending, paused: msg.status.Paused, actions: actions}
	m.settings.selected = minInt(m.settings.selected, 1+len(actions))
	if msg.operation == "toggle" {
		state := "OFF"
		if msg.status.Enabled {
			state = "ON"
		}
		m.status = "Labeling automation is " + state + " for " + m.repo + "."
	} else if msg.operation == "action" {
		m.status = "Action policy updated for " + m.repo + "."
	}
	return m, nil
}
