package main

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// u steps a saved decision back one layer, on the open item, the hovered one, or the ticked ones: an approval is taken back (the decision stays, unreviewed), and an unreviewed decision is cleared (the item is untriaged again; its agent and reviewer notes stay). It always asks first. bin/apply --unapprove / --clear do the work; the ledger's git diff is the record.

// undoPlan sorts targets into approvals to take back and decisions to clear; untriaged items have nothing to undo.
func undoPlan(targets []Item) (unapprove, clear []Key) {
	for _, it := range targets {
		switch {
		case it.Reviewed:
			unapprove = append(unapprove, it.Key())
		case !it.Untriaged():
			clear = append(clear, it.Key())
		}
	}

	return unapprove, clear
}

// undoTargets are what u acts on in a list: the items of the step just taken (lastStep), else the ticked or hovered ones.
func (m model) undoTargets() []Item {
	if len(m.lastStep) == 0 {
		return m.listTargets()
	}

	var targets []Item
	for _, k := range m.lastStep {
		if it, ok := m.findItem(k); ok {
			targets = append(targets, it)
		}
	}

	return targets
}

func (m model) requestUndo(targets []Item) (tea.Model, tea.Cmd) {
	unapprove, clear := undoPlan(targets)
	if len(unapprove)+len(clear) == 0 {
		m.listConfirm = ""
		m.status = "Nothing to undo: no saved decision here yet."

		return m, nil
	}

	if m.listConfirm != "u" {
		m.listConfirm = "u"
		m.status = undoQuestion(targets, unapprove, clear)

		return m, nil
	}

	m.listConfirm = ""

	return m, undoCmd(m.installRoot, m.repo, unapprove, clear, m.reviewer)
}

// undoQuestion says what a second u will do, naming the decision when it's one item.
func undoQuestion(targets []Item, unapprove, clear []Key) string {
	if len(targets) == 1 {
		it := targets[0]
		decision := it.Category + "/" + it.Action
		if it.Reviewed {
			return fmt.Sprintf("Take back the approval of %s on %s #%d? The decision stays, unreviewed. Press u again.", decision, it.Kind, it.Number)
		}

		return fmt.Sprintf("Clear the decision %s on %s #%d? It becomes untriaged again. Press u again.", decision, it.Kind, it.Number)
	}

	var parts []string
	if n := len(unapprove); n > 0 {
		parts = append(parts, "take back "+pluralize(n, "approval", "approvals"))
	}

	if n := len(clear); n > 0 {
		parts = append(parts, "clear "+pluralize(n, "decision", "decisions"))
	}

	question := fmt.Sprintf("Undo %s: %s?", describeTargets(targets), strings.Join(parts, " and "))
	if skipped := len(targets) - len(unapprove) - len(clear); skipped > 0 {
		question += fmt.Sprintf(" %s untriaged, left alone.", pluralize(skipped, "is", "are"))
	}

	return question + " Press u again."
}

// undoDoneMsg reports an undo, with the ledger re-read after it.
type undoDoneMsg struct {
	repo                string
	unapproved, cleared []Key
	items               []Item
	err                 error
}

func undoCmd(root, repo string, unapprove, clear []Key, by string) tea.Cmd {
	return func() tea.Msg {
		msg := undoDoneMsg{repo: repo}
		for _, step := range []struct {
			flag string
			keys []Key
			done *[]Key
		}{{"--unapprove", unapprove, &msg.unapproved}, {"--clear", clear, &msg.cleared}} {
			if len(step.keys) == 0 {
				continue
			}

			if _, err := runScript(root, "apply", append([]string{step.flag, "--by", by}, keyArgs(step.keys)...)...); err != nil {
				msg.err = err

				break
			}

			*step.done = step.keys
		}

		msg.items, _ = LoadLedger(root, repo)

		return msg
	}
}

func (m model) onUndoDone(msg undoDoneMsg) (tea.Model, tea.Cmd) {
	if msg.repo != m.repo {
		return m, nil
	}

	if msg.items != nil {
		m.items = msg.items
		m.recomputeSidebarCounts()
		m.refreshActiveList()
	}

	// The open item shows its decision as it is now: the proposal again, if a batch has one.
	if it, ok := m.findItem(m.detail.key); ok && m.focus == FocusDetail {
		for _, k := range append(append([]Key{}, msg.unapproved...), msg.cleared...) {
			if k == it.Key() {
				m.loadForm(it)
			}
		}
	}

	if msg.err != nil {
		m.failErr("Couldn't undo", msg.err)

		return m, nextCmd(m.installRoot, m.repo)
	}

	var parts []string
	if n := len(msg.unapproved); n > 0 {
		parts = append(parts, "took back "+pluralize(n, "approval", "approvals"))
	}

	if n := len(msg.cleared); n > 0 {
		parts = append(parts, "cleared "+pluralize(n, "decision", "decisions"))
	}

	done := strings.Join(parts, " and ")
	m.status = strings.ToUpper(done[:1]) + done[1:] + "."
	m.clearTicks()
	// A u right after taking back approvals clears those decisions next, and a single item is back under the cursor.
	m.lastStep = nil
	if len(msg.cleared) == 0 {
		m.lastStep = msg.unapproved
		if len(msg.unapproved) == 1 && m.itemListActive() {
			m.selectKey(msg.unapproved[0])
		}
	}

	return m, nextCmd(m.installRoot, m.repo)
}
