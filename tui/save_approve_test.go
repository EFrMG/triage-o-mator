package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestSaveAndApproveRecordsHumanReviewAndLeavesNoPendingItem(t *testing.T) {
	for _, approve := range []bool{false, true} {
		name, shortcut := "save", "s"
		if approve {
			name, shortcut = "save and approve", "S"
		}

		t.Run(name, func(t *testing.T) {
			m := openFixtureItem(t)
			m.form.FocusField(fieldReason)
			m.form.reason.SetValue("Confirmed from the reproduction.")
			m.form.touched, m.form.dirty = true, true
			m.commitDraftIfDirty()
			m = press(m, "tab")

			next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(shortcut)})
			if cmd == nil {
				t.Fatal("the save shortcut did not produce a command after leaving the reason")
			}

			m = send(next.(model), cmd())
			row := ledgerRow(t, m.installRoot, 1)
			if row["reviewed"] != approve || row["triaged_by"] != "tester" || row["reason"] != "Confirmed from the reproduction." {
				t.Fatalf("wrong saved decision: %v", row)
			}

			if approve && (row["reviewed_by"] != "tester" || row["reviewed_at"] == "" || m.status != "Saved and approved.") {
				t.Fatalf("human review missing: %v, %q", row, m.status)
			}

			if m.focus != FocusList || m.form.dirty || len(m.drafts) != 0 {
				t.Fatal("successful save should clear the saved draft and return to the list")
			}

			items, err := LoadLedger(m.installRoot, m.repo)
			if err != nil {
				t.Fatal(err)
			}

			m = send(m, ledgerReloadedMsg{repo: m.repo, items: items})
			if items[0].PendingReview() == approve {
				t.Fatal("only a save without approval should enter Pending Review")
			}

			if approve && len(m.undoTargets()) != 1 {
				t.Fatal("undo should target the decision just saved and approved")
			}
		})
	}
}

func TestSaveAndApprovePreservesProposalAuthorUnlessEdited(t *testing.T) {
	for _, edit := range []bool{false, true} {
		m := openFixtureItem(t)
		m.form.ApplyProposal(proposal{Category: "bug", Action: "label-only", Confidence: "high", Reason: "Reproduced.", AgentNotes: "Logs match.", ProposedBy: "agent:alice"})
		wantAuthor := "agent:alice"
		if edit {
			m.form.reason.SetValue("Different explanation after review.")
			m.form.dirty = true
			wantAuthor = "tester"
		}

		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("S")})
		m = send(next.(model), cmd())
		row := ledgerRow(t, m.installRoot, 1)
		if row["triaged_by"] != wantAuthor || row["reviewed_by"] != "tester" || row["reviewed"] != true || row["agent_notes"] != "Logs match." {
			t.Fatalf("edit=%v: proposal attribution or review lost: %v", edit, row)
		}
	}
}

func TestSaveAndApproveOfUnchangedLedgerDecisionPreservesTriage(t *testing.T) {
	root := batchFixture(t)
	_, err := runScript(root, "apply", "--kind", "issue", "--number", "1", "--category", "bug", "--action", "label-only", "--confidence", "high", "--reason", "Reproduced.", "--by", "agent:alice", "--batch-id", "original-batch")
	if err != nil {
		t.Fatal(err)
	}

	before := ledgerRow(t, root, 1)
	m := batchModel(t, root)
	m.openItem(m.items[0])
	m.focus = FocusDetail
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("S")})
	m = send(next.(model), cmd())
	after := ledgerRow(t, root, 1)
	for _, field := range []string{"triaged_by", "triaged_at", "batch_id"} {
		if before[field] != after[field] {
			t.Fatalf("unchanged decision lost %s: %v", field, after)
		}
	}

	if after["reviewed_by"] != "tester" || after["reviewed"] != true {
		t.Fatal("unchanged decision was not confirmed")
	}
}

func TestSaveApprovalWarningsCannotConfirmTheOtherOperation(t *testing.T) {
	m := openFixtureItem(t)
	for _, shortcut := range []string{"s", "S", "s", "S"} {
		next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(shortcut)})
		m = next.(model)
		if cmd != nil || !m.confirmSave {
			t.Fatal("switching save operations must ask again for untouched defaults")
		}
	}

	if !strings.Contains(m.status, "S again") {
		t.Fatal("warning should name the approving shortcut")
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("S")})
	m = send(next.(model), cmd())
	if ledgerRow(t, m.installRoot, 1)["reviewed"] != true {
		t.Fatal("repeating the explicit approving shortcut should confirm")
	}
}

func TestLateCombinedSavePreservesNewerEdits(t *testing.T) {
	m := openFixtureItem(t)
	m.form.reason.SetValue("Checked.")
	m.form.touched, m.form.dirty = true, true
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("S")})
	m = next.(model)
	m.form.reason.SetValue("Newer evidence.")
	m.commitDraftIfDirty()
	m = send(m, cmd())
	if !m.form.dirty || m.form.Reason() != "Newer evidence." || len(m.drafts) != 1 || m.focus != FocusDetail {
		t.Fatal("completion of the older save must not discard new edits")
	}
}

func TestSaveAndApproveWorksFromDuplicateComparison(t *testing.T) {
	m := openFixtureItem(t)
	m.form.reason.SetValue("Compared both reports.")
	m.form.touched, m.form.dirty = true, true
	m.dups.open, m.dups.source = true, m.detail.key
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("S")})
	m = send(next.(model), cmd())
	if !m.dups.open || ledgerRow(t, m.installRoot, 1)["reviewed"] != true {
		t.Fatal("save and approve should work without leaving the comparison")
	}
}

func TestSaveLettersRemainTextInTheReason(t *testing.T) {
	m := openFixtureItem(t)
	m.form.FocusField(fieldReason)
	m = press(m, "s")
	m = press(m, "S")
	if m.form.Reason() != "sS" || ledgerRow(t, m.installRoot, 1)["category"] != "" {
		t.Fatal("save letters must be ordinary text while editing the reason")
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter should save without needing a modifier")
	}

	m = send(next.(model), cmd())
	row := ledgerRow(t, m.installRoot, 1)
	if row["reason"] != "sS" || row["reviewed"] != true || row["reviewed_by"] != "tester" {
		t.Fatalf("Enter should save and approve the completed decision: %v", row)
	}
}

func TestEnterApprovalWarningNamesEnterAndCannotBeConfirmedBySaveOnly(t *testing.T) {
	m := openFixtureItem(t)
	m.form.FocusField(fieldReason)
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if cmd != nil || !strings.Contains(m.status, "Enter again to save and approve") {
		t.Fatalf("an empty reason should explain what Enter will do: %q", m.status)
	}

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = next.(model)
	if cmd != nil || m.confirmSaveApproval {
		t.Fatal("save-only must not confirm a save-and-approve warning")
	}

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(model)
	if cmd != nil || !m.confirmSaveApproval {
		t.Fatal("switching back to approval must warn again")
	}

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = send(next.(model), cmd())
	if ledgerRow(t, m.installRoot, 1)["reviewed"] != true {
		t.Fatal("repeated Enter should save and approve")
	}
}
