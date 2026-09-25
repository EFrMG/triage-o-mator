package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// u steps a decision back one layer, asking first: an approval is taken back, then the decision is cleared and the form shows the batch's proposal again.
func TestUndoStepsBackOneLayer(t *testing.T) {
	root := batchFixture(t)
	m := batchModel(t, root)
	m.openBatch("b20260101-000000")
	m.selectCurrentListItem()
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = runCmd(next.(model), cmd)
	m.list.Select(0)
	m.selectCurrentListItem()
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = runCmd(next.(model), cmd)
	if ledgerRow(t, root, 1)["reviewed"] != true {
		t.Fatal("setup: the decision should be approved")
	}

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	m = next.(model)
	if cmd != nil || !strings.Contains(m.status, "Take back the approval of support-question/") {
		t.Fatalf("the first u should ask: %q", m.status)
	}

	m = press(m, "j")
	if m.listConfirm != "" {
		t.Fatal("any other key should disarm the undo")
	}

	m = press(m, "u")
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	m = runCmd(next.(model), cmd)
	row := ledgerRow(t, root, 1)
	if row["reviewed"] != false || row["category"] != "support-question" || m.status != "Took back 1 approval." {
		t.Fatalf("u u should take back the approval only: %v, %q", row, m.status)
	}

	m = press(m, "u")
	if !strings.Contains(m.status, "Clear the decision support-question/") {
		t.Fatalf("the next u should offer to clear: %q", m.status)
	}

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	m = runCmd(next.(model), cmd)
	if row := ledgerRow(t, root, 1); row["category"] != "" || row["triaged_by"] != "" {
		t.Fatalf("u u should clear the decision: %v", row)
	}

	if !m.form.proposed || m.form.Category() != "support-question" || !strings.Contains(m.list.Title, "0/2 triaged") {
		t.Fatalf("the open item should show its proposal again, and the batch its progress: proposed %v, %q, %q", m.form.proposed, m.form.Category(), m.list.Title)
	}

	if m = press(m, "u"); !strings.Contains(m.status, "Nothing to undo") {
		t.Fatalf("an untriaged item has nothing to undo: %q", m.status)
	}
}

// On ticked items, u says what it will do to each kind, and does both in one go.
func TestUndoOnTickedItems(t *testing.T) {
	root := batchFixture(t)
	for _, n := range []string{"1", "2"} {
		if _, err := runScript(root, "apply", "--number", n, "--kind", "issue", "--category", "bug", "--action", "label-only", "--by", "someone"); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := runScript(root, "apply", "--approve", "--key", "issue:1", "--by", "someone"); err != nil {
		t.Fatal(err)
	}

	m := batchModel(t, root)
	m = send(m, reloadLedgerCmd(root, "owner/repo")())
	m.openBatch("b20260101-000000")
	m = press(m, " ")
	m = press(m, " ")
	m = press(m, "u")
	if !strings.Contains(m.status, "Undo 2 ticked items: take back 1 approval and clear 1 decision?") {
		t.Fatalf("bulk undo question: %q", m.status)
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	m = runCmd(next.(model), cmd)
	if one, two := ledgerRow(t, root, 1), ledgerRow(t, root, 2); one["reviewed"] != false || one["category"] != "bug" || two["category"] != "" {
		t.Fatalf("bulk undo: #1 %v, #2 %v", one, two)
	}

	if m.status != "Took back 1 approval and cleared 1 decision." || len(m.ticked) != 0 {
		t.Fatalf("after a bulk undo: %q, %d ticked", m.status, len(m.ticked))
	}
}

// Approving in Pending Review drops the item from the list and the cursor falls on the next one; u right after still steps back the approved item, first its approval, then its decision, and leaves the hovered one alone.
func TestUndoAfterApprovalInPendingReview(t *testing.T) {
	root := batchFixture(t)
	for _, n := range []string{"1", "2"} {
		if _, err := runScript(root, "apply", "--number", n, "--kind", "issue", "--category", "support-question", "--action", "comment-request-info", "--confidence", "medium", "--reason", "asks for help", "--by", "tester"); err != nil {
			t.Fatal(err)
		}
	}

	m := batchModel(t, root)
	m.activateTab(pendingReviewTab)
	m = press(m, "a")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	m = runCmd(next.(model), cmd)
	if ledgerRow(t, root, 1)["reviewed"] != true || len(m.list.Items()) != 1 {
		t.Fatalf("setup: #1 should be approved and gone from Pending Review, %d listed", len(m.list.Items()))
	}

	if m = press(m, "u"); !strings.Contains(m.status, "Take back the approval") || !strings.Contains(m.status, "#1") {
		t.Fatalf("u should offer to take back #1's approval: %q", m.status)
	}

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	m = runCmd(next.(model), cmd)
	if ledgerRow(t, root, 1)["reviewed"] != false || ledgerRow(t, root, 2)["category"] != "support-question" {
		t.Fatal("u u should take back #1's approval and leave #2 alone")
	}

	if li, ok := m.list.SelectedItem().(listItem); !ok || li.Number != 1 {
		t.Fatal("the cursor should be back on #1")
	}

	m = press(m, "u")
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("u")})
	m = runCmd(next.(model), cmd)
	if ledgerRow(t, root, 1)["category"] != "" || ledgerRow(t, root, 2)["category"] != "support-question" {
		t.Fatal("the next u u should clear #1's decision")
	}
}
