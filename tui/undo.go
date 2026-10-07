package main

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

// Undo clears saved triage decisions. Explicit Pending review requests and notes stay in the ledger.
func (m model) undoTargets() []Item {
	return m.listTargets()
}

func (m model) requestUndo(targets []Item) (tea.Model, tea.Cmd) {
	var keys []Key
	for _, it := range targets {
		if !it.Untriaged() {
			keys = append(keys, it.Key())
		}
	}

	if len(keys) == 0 {
		m.listConfirm = ""
		m.status = "Nothing to undo: no saved decision here yet."

		return m, nil
	}

	if m.listConfirm != "u" {
		m.listConfirm = "u"
		m.status = fmt.Sprintf("Clear the saved decision on %s? Pending review requests and notes stay. Press u again.", describeTargets(targets))

		return m, nil
	}

	m.listConfirm = ""
	m.pendingApply++
	return m, undoCmd(m.installRoot, m.repo, keys)
}

type undoDoneMsg struct {
	root, repo string
	cleared    []Key
	items      []Item
	err        error
}

func undoCmd(root, repo string, keys []Key) tea.Cmd {
	return func() tea.Msg {
		msg := undoDoneMsg{root: root, repo: repo, cleared: keys}
		_, msg.err = runScript(root, "apply", append([]string{"--expected-repo", repo, "--clear"}, keyArgs(keys)...)...)
		if msg.err == nil {
			msg.items, msg.err = LoadLedger(root, repo)
		}

		return msg
	}
}

func (m model) onUndoDone(msg undoDoneMsg) (tea.Model, tea.Cmd) {
	if msg.root != m.installRoot || msg.repo != m.repo {
		return m, nil
	}

	if m.pendingApply > 0 {
		m.pendingApply--
	}
	if msg.err != nil {
		m.failErr("Couldn't undo", msg.err)

		return m, nextCmd(m.installRoot, m.repo)
	}

	if msg.items != nil {
		m.items = msg.items
		m.recomputeSidebarCounts()
		m.refreshActiveList()
	}
	if it, ok := m.findItem(m.detail.key); ok && m.focus == FocusDetail {
		for _, key := range msg.cleared {
			if key == it.Key() {
				m.loadForm(it)
				break
			}
		}
	}

	m.status = fmt.Sprintf("Cleared %d saved decision(s).", len(msg.cleared))
	m.clearTicks()

	return m, nextCmd(m.installRoot, m.repo)
}
