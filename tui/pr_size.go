package main

import (
	"encoding/json"
	"fmt"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type prSizePolicy struct {
	Repository string   `json:"repository"`
	Enabled    bool     `json:"enabled"`
	Revision   string   `json:"revision"`
	Pending    []string `json:"pending"`
}

type prSizeMsg struct {
	root, repo string
	request    uint64
	policy     prSizePolicy
	err        error
}

func prSizeCmd(root, repo string, request uint64, before prSizePolicy, toggle bool) tea.Cmd {
	return func() tea.Msg {
		msg := prSizeMsg{root: root, repo: repo, request: request}
		args := []string{"status", "--expected-repo", repo}
		if toggle {
			enabled := "on"
			if before.Enabled {
				enabled = "off"
			}
			args = []string{"set-enabled", "--expected-repo", repo, "--enabled", enabled, "--expected-revision", before.Revision}
		}
		out, err := runScript(root, "pr-size", args...)
		if err == nil {
			err = json.Unmarshal([]byte(out), &msg.policy)
		}
		if err == nil && (msg.policy.Repository != repo || !validCorpusID(msg.policy.Revision)) {
			err = fmt.Errorf("invalid PR size policy response")
		}
		msg.err = err
		return msg
	}
}

func (m model) finishPRSize(msg prSizeMsg) (tea.Model, tea.Cmd) {
	if !m.settings.open || m.settings.section != "pr-size" || msg.root != m.installRoot || msg.repo != m.repo || msg.request != m.settings.request {
		return m, nil
	}
	m.settings.busy = false
	if msg.err != nil {
		m.failErr("Couldn't update Size of PRs", msg.err)
		return m, nil
	}
	m.settings.prSize = msg.policy
	return m, nil
}

func (m model) prSizeView() string {
	state := "OFF"
	if m.settings.prSize.Enabled {
		state = "ON"
	}
	if m.settings.busy {
		state = "Working…"
	}
	view := inset(titleBar("Size of PRs", state, m.menuWidth())) + "\n\n"
	view += inset("Enter/Space toggles automatic GitHub size labels after corpus refresh.\nON creates/colors the four labels and updates PR size labels.\nCounts additions + deletions and files; either limit raises the size.\nOther labels are preserved. No agent pass is needed.") + "\n\n"
	for _, size := range []struct{ name, limit, color string }{
		{"S", "≤50 lines and ≤3 files", "#90EE90"},
		{"M", "≤250 lines and ≤10 files", "#9ACD32"},
		{"L", "≤1,000 lines and ≤25 files", "#FF4500"},
		{"XL", "Above either L limit", "#FF0000"},
	} {
		view += inset(lipgloss.NewStyle().Foreground(lipgloss.Color(size.color)).Bold(true).Render("Size: "+size.name)+"  "+size.limit) + "\n\n"
	}
	if len(m.settings.prSize.Pending) > 0 {
		view += inset("An earlier size write is uncertain. Inspect GitHub, then use bin/pr-size reset --apply --expected-repo " + m.repo + ".")
	}
	return view
}

func (m model) handlePRSizeKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "h", "left":
		m.settings.section = ""
		m.settings.request++
		m.settings.busy = false
	case "q":
		return m.requestQuit()
	case "r", "enter", "space":
		if m.settings.busy {
			return m, nil
		}
		toggle := msg.String() != "r"
		if toggle && m.settings.prSize.Revision == "" {
			return m, nil
		}
		m.settings.busy = true
		m.settings.request++
		return m, prSizeCmd(m.installRoot, m.repo, m.settings.request, m.settings.prSize, toggle)
	}
	return m, nil
}
