package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Space ticks items in a list; list actions (a, b, B, and d inside a batch) then act on every ticked item, or on the hovered one when nothing is ticked. Ticks belong to the list on screen: switching lists, or finishing a bulk action, clears them.

var tickSuffix = regexp.MustCompile(` · \d+ ticked$`)

// itemListActive reports whether the list on screen holds ledger items (not duplicate pairs), which is where item ticks and list actions apply.
func (m model) itemListActive() bool {
	return m.listReady && !m.activePairs
}

// listTargets are the items a list action applies to: the ticked ones in list order, else the hovered one.
func (m model) listTargets() []Item {
	if !m.itemListActive() {
		return nil
	}

	var ticked []Item
	for _, entry := range m.listAll {
		if li, ok := entry.(listItem); ok && m.ticked[li.Key()] {
			ticked = append(ticked, li.Item)
		}
	}

	if len(ticked) > 0 {
		return ticked
	}

	if li, ok := m.list.SelectedItem().(listItem); ok {
		return []Item{li.Item}
	}

	return nil
}

func (m model) listTargetKeys() []Key {
	items := m.listTargets()
	keys := make([]Key, len(items))
	for i, it := range items {
		keys[i] = it.Key()
	}

	return keys
}

// toggleTick ticks or unticks the hovered item and moves down, so a run of items can be ticked with repeated Space.
func (m *model) toggleTick() {
	li, ok := m.list.SelectedItem().(listItem)
	if !ok {
		return
	}

	k := li.Key()
	m.ticked[k] = !m.ticked[k]
	if !m.ticked[k] {
		delete(m.ticked, k)
	}

	index := m.list.Index()
	m.showList()
	m.list.Select(index)
	m.list.CursorDown()
	m.updateTickTitle()
}

// applyTicks re-marks ticked items after the list was rebuilt (a save or sync reloads it), and forgets ticks on items that left the list.
func (m *model) applyTicks() {
	present := map[Key]bool{}
	for _, entry := range m.listAll {
		if li, ok := entry.(listItem); ok {
			present[li.Key()] = true
		}
	}

	for k := range m.ticked {
		if !present[k] {
			delete(m.ticked, k)
		}
	}

	m.updateTickTitle()
}

// clearTicks drops every tick, e.g. when another list opens or a bulk action finished.
func (m *model) clearTicks() {
	m.ticked = map[Key]bool{}
	m.listConfirm = ""
	if m.itemListActive() {
		m.showList()
		m.updateTickTitle()
	}
}

// updateTickTitle keeps "· N ticked" at the end of the list title in step with the ticks.
func (m *model) updateTickTitle() {
	m.list.Title = tickSuffix.ReplaceAllString(m.list.Title, "")
	if n := len(m.ticked); n > 0 {
		m.list.Title += fmt.Sprintf(" · %d ticked", n)
	}
}

// describeTargets names what a bulk action applies to, for its confirmation: "#12" or "3 ticked items".
func describeTargets(items []Item) string {
	if len(items) == 1 {
		return fmt.Sprintf("%s #%d", items[0].Kind, items[0].Number)
	}

	return fmt.Sprintf("%d ticked items", len(items))
}

// requestListApprove approves the targets' saved decisions on a second a. From a list you haven't opened the items, so it always asks first, and it says what it skips.
func (m model) requestListApprove() (tea.Model, tea.Cmd) {
	targets := m.listTargets()
	var approvable []Key
	skipped := 0
	for _, it := range targets {
		if it.PendingReview() {
			approvable = append(approvable, it.Key())
		} else {
			skipped++
		}
	}

	if len(approvable) == 0 {
		m.listConfirm = ""
		m.status = "Nothing to approve: none of these has a saved, unreviewed decision."

		return m, nil
	}

	if m.listConfirm != "a" {
		m.listConfirm = "a"
		what := describeTargets(targets)
		if len(targets) == 1 {
			what += " (" + targets[0].Category + "/" + targets[0].Action + ")"
		}

		m.status = fmt.Sprintf("Approve %s? Press a again.", what)
		if skipped > 0 {
			m.status = fmt.Sprintf("Approve %d saved decisions? %d untriaged or already reviewed will be skipped. Press a again.", len(approvable), skipped)
		}

		return m, nil
	}

	m.listConfirm = ""

	return m, approveKeysCmd(m.installRoot, approvable, m.reviewer)
}

// requestBatchRemove drops the targets from the open batch (their proposals too) on a second d; the ledger is untouched.
func (m model) requestBatchRemove() (tea.Model, tea.Cmd) {
	b := m.batchByID(m.activeBatch)
	targets := m.listTargets()
	if b == nil || len(targets) == 0 {
		return m, nil
	}

	if m.listConfirm != "d" {
		m.listConfirm = "d"
		m.status = fmt.Sprintf("Remove %s from batch %s, with their proposals? Saved decisions stay in the ledger. Press d again.", describeTargets(targets), b.ID)

		return m, nil
	}

	m.listConfirm = ""
	m.batches.busy = true
	keys := make([]Key, len(targets))
	for i, it := range targets {
		keys[i] = it.Key()
	}

	return m, removeFromBatchCmd(m.installRoot, m.repo, b.ID, keys)
}

func keyArgs(keys []Key) []string {
	args := make([]string, 0, 2*len(keys))
	for _, k := range keys {
		args = append(args, "--key", k.Kind+":"+strconv.Itoa(k.Number))
	}

	return args
}

// approveKeysCmd approves several saved decisions in one bin/apply call (one ledger write).
func approveKeysCmd(root string, keys []Key, by string) tea.Cmd {
	return func() tea.Msg {
		args := append([]string{"--approve", "--by", by}, keyArgs(keys)...)
		_, err := runScript(root, "apply", args...)

		return applyDoneMsg{approval: true, count: len(keys), approved: keys, err: err}
	}
}

func removeFromBatchCmd(root, repo, id string, keys []Key) tea.Cmd {
	return func() tea.Msg {
		out, err := runScript(root, "batch", append([]string{"--remove", id}, keyArgs(keys)...)...)
		if err != nil {
			return batchesLoadedMsg{err: err}
		}

		msg := loadBatchesCmd(root, repo, id, strings.TrimSpace(out))().(batchesLoadedMsg)
		msg.clearTicks = true

		return msg
	}
}
