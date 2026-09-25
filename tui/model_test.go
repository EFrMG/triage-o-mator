package main

import (
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func testTaxonomy() Taxonomy {
	return Taxonomy{
		IssueCategories: []string{"bug", "hardware-specific", "support-question"},
		PRCategories:    []string{"merge-ready", "trivial"},
		Actions:         []string{"label-only", "comment-request-info", "escalate-maintainer"},
		Confidence:      []string{"low", "medium", "high"},
	}
}

func testItems() []Item {
	return []Item{
		{Number: 1, Kind: "issue", State: "open", Title: "first"},
		{Number: 2, Kind: "issue", State: "open", Title: "second"},
	}
}

// Selecting a different item, then coming back, must not lose an unsaved edit.
func TestDraftSurvivesSwitchingItems(t *testing.T) {
	m := newModel("/tmp", "owner/repo", testTaxonomy(), "tester", testItems())
	m.activateTab(0) // "Untriaged Issues"

	m.list.Select(0) // item #1
	m.selectCurrentListItem()
	m.form.NextField()   // fieldContent -> fieldCategory
	m.form.CycleValue(1) // change category away from index 0
	if !m.form.dirty {
		t.Fatal("expected form to be dirty after CycleValue")
	}

	editedCategory := m.form.Category()

	m.list.Select(1) // item #2
	m.selectCurrentListItem()
	if m.form.dirty {
		t.Fatal("freshly loaded item #2 should not start dirty")
	}

	if got := m.form.Category(); got == editedCategory && editedCategory != testTaxonomy().IssueCategories[0] {
		t.Fatalf("item #2 unexpectedly inherited item #1's edited category %q", got)
	}

	m.list.Select(0) // back to item #1
	m.selectCurrentListItem()
	if !m.form.dirty {
		t.Fatal("returning to item #1 should restore its unsaved draft (dirty=true)")
	}

	if got := m.form.Category(); got != editedCategory {
		t.Fatalf("item #1's draft category = %q, want restored %q", got, editedCategory)
	}
}

// A successful save must clear the draft, so re-visiting the item later doesn't re-show a stale "unsaved changes" state.
func TestSavedDraftIsCleared(t *testing.T) {
	m := newModel("/tmp", "owner/repo", testTaxonomy(), "tester", testItems())
	m.activateTab(0)
	m.list.Select(0)
	m.selectCurrentListItem()
	m.form.NextField()
	m.form.CycleValue(1)
	m.commitDraftIfDirty()
	if len(m.drafts) != 1 {
		t.Fatalf("expected 1 pending draft, got %d", len(m.drafts))
	}

	key := m.detail.key
	next, _ := m.Update(applyDoneMsg{key: key})
	m2 := next.(model)
	if len(m2.drafts) != 0 {
		t.Fatalf("expected draft for %v to be cleared after a successful save, got %d remaining", key, len(m2.drafts))
	}
}

// Quitting with unsaved drafts must require a second explicit confirmation, and any other key must cancel it rather than silently discarding work.
func TestQuitConfirmation(t *testing.T) {
	m := newModel("/tmp", "owner/repo", testTaxonomy(), "tester", testItems())
	m.activateTab(0)
	m.list.Select(0)
	m.selectCurrentListItem()
	m.form.NextField()
	m.form.CycleValue(1) // dirty, uncommitted

	_, cmd := m.requestQuit()
	if cmd != nil {
		t.Fatal("first quit with unsaved drafts should not quit immediately")
	}

	next, cmd := m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m2 := next.(model)
	if !m2.confirmQuit {
		t.Fatal("expected confirmQuit=true after requesting quit with unsaved drafts")
	}

	if cmd != nil {
		t.Fatal("expected no cmd while awaiting quit confirmation")
	}

	// Any other key should cancel the confirmation, not quit and not lose it silently.
	next, cmd = m2.handleQuitConfirmKey(tea.KeyMsg{Type: tea.KeyEsc})
	m3 := next.(model)
	if m3.confirmQuit {
		t.Fatal("expected confirmQuit to clear after a non-quit key")
	}

	if cmd != nil {
		t.Fatal("cancelling quit confirmation should not itself quit")
	}

	// A second real quit keypress should actually quit.
	_, cmd = m2.handleQuitConfirmKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("expected a quit cmd on confirmed second 'q'")
	}
}

// Regression test for a real panic: activate a tab, move the sidebar cursor down onto "Switch Repo" (without pressing Enter, the cursor and the displayed list are independent), then let a background sync complete. refreshActiveList used to index tabs[] with the sidebar cursor instead of the actually-displayed tab, and "Switch Repo" sits one past the end of tabs, so this panicked with "index out of range".
func TestRefreshActiveListSurvivesCursorOnSwitchRepo(t *testing.T) {
	m := newModel("/tmp", "owner/repo", testTaxonomy(), "tester", testItems())
	m.activateTab(0) // displays "Untriaged Issues"

	for m.sidebar.selected != switchRepoIndex {
		m.sidebar.Next()
	}

	if !m.listReady {
		t.Fatal("expected the previously-activated list to still be displayed")
	}

	next, _ := m.Update(ledgerReloadedMsg{items: testItems()})
	m2 := next.(model)
	if m2.activeTab != 0 {
		t.Fatalf("activeTab changed to %d just from moving the sidebar cursor", m2.activeTab)
	}

	if got := m2.list.Title; got == "" {
		t.Fatal("expected the list to still have a title after refresh")
	}
}

func TestWriteRepoRejectsInvalidValue(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "config"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := WriteRepo(dir, "not-a-valid-repo"); err == nil {
		t.Fatal("expected an error for a value without a slash")
	}

	if err := WriteRepo(dir, "owner/repo"); err != nil {
		t.Fatalf("WriteRepo: %v", err)
	}

	got, err := ReadRepo(dir)
	if err != nil {
		t.Fatalf("ReadRepo: %v", err)
	}

	if got != "owner/repo" {
		t.Fatalf("ReadRepo after WriteRepo = %q, want %q", got, "owner/repo")
	}
}

func TestLateSaveDoesNotClearOtherItemOrNewerDraft(t *testing.T) {
	m := newModel("/tmp", "owner/repo", testTaxonomy(), "tester", testItems())
	m.activateTab(0)
	m.selectCurrentListItem()
	m.form.NextField()
	m.form.CycleValue(1)
	saved := m.form.Snapshot()
	key := m.detail.key
	m.list.Select(1)
	m.selectCurrentListItem()
	m.form.NextField()
	m.form.CycleValue(1)
	m = send(m, applyDoneMsg{key: key, snapshot: &saved})
	if m.focus != FocusDetail {
		t.Fatalf("focus after another item's save = %v, want the item still open", m.focus)
	}

	if !m.form.dirty {
		t.Fatal("late save cleared another item's unsaved changes")
	}

	m.list.Select(0)
	m.selectCurrentListItem()
	m.form.NextField()
	m.form.CycleValue(1)
	m.form.reason.SetValue("newer unsaved reason")
	m.form.dirty = true
	newer := m.form.Snapshot()
	m = send(m, applyDoneMsg{key: key, snapshot: &saved})
	if newer != saved && !m.form.dirty {
		t.Fatal("late save cleared newer draft")
	}

	m = send(m, applyDoneMsg{key: key, approval: true})
	if !m.form.dirty {
		t.Fatal("approval cleared unsaved decision edits")
	}
}

// A save goes back to the list with the cursor on the next item, and stays on it when the ledger reload drops the saved item from Untriaged.
func TestSaveReturnsToListOnNextItem(t *testing.T) {
	items := append(testItems(), Item{Number: 3, Kind: "issue", State: "open", Title: "third"})
	m := newModel("/tmp", "owner/repo", testTaxonomy(), "tester", items)
	m.activateTab(0)
	m.list.Select(0)
	m.selectCurrentListItem()
	m = send(m, applyDoneMsg{key: m.detail.key})
	if m.focus != FocusList {
		t.Fatalf("focus after a save = %v, want the list", m.focus)
	}

	if got := m.list.SelectedItem().(listItem).Number; got != 2 {
		t.Fatalf("cursor after saving #1 is on #%d, want #2", got)
	}

	items[0].Category, items[0].Action = "bug", "label-only"
	m = send(m, ledgerReloadedMsg{repo: "owner/repo", items: items})
	if got := m.list.SelectedItem().(listItem).Number; got != 2 {
		t.Fatalf("cursor after the reload is on #%d, want #2", got)
	}
}
