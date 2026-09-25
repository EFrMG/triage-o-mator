package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// age pretends the current status was set long ago, then lets one status tick run.
func age(m model, by time.Duration) model {
	m.statusAt = time.Now().Add(-by)

	return send(m, statusTickMsg{})
}

func TestStatusExpiresUnlessPinned(t *testing.T) {
	m := batchModel(t, batchFixture(t))
	m.status = "Opened https://example.com in the browser."
	if m = age(m, 2*time.Second); m.status == "" {
		t.Fatal("a fresh message should stay")
	}

	if m = age(m, statusTTL+time.Second); m.status != "" {
		t.Fatalf("an old informational message should expire: %q", m.status)
	}

	m.status = "apply failed: boom"
	if m = age(m, statusTTL+time.Second); m.status == "" {
		t.Fatal("errors should stay longer than informational messages")
	}

	m.status, m.confirmSave = "Defaults unchanged: Ctrl-S again to save anyway.", true
	if m = age(m, time.Minute); m.status == "" {
		t.Fatal("a confirmation waiting for its second key press must not expire")
	}
}

func TestExpiredStatusFallsBackToRunningFetch(t *testing.T) {
	m := batchModel(t, batchFixture(t))
	m = press(m, "R")
	m.status = "Saved."
	if m = age(m, statusTTL+time.Second); m.status != fullFetchStatus {
		t.Fatalf("while a fetch runs, an expired message should give way to it: %q", m.status)
	}
}

func TestNavigationDropsStaleStatus(t *testing.T) {
	m := batchModel(t, batchFixture(t))
	m.activateTab(0)
	m.selectCurrentListItem()
	m = press(m, "a")
	if !strings.Contains(m.status, "Nothing to approve yet") {
		t.Fatalf("expected the approve hint: %q", m.status)
	}

	m = press(m, "esc")
	if m.status != "" {
		t.Fatalf("leaving the item should drop its message: %q", m.status)
	}
}

func TestInvalidProposalIsShownAndCannotBeSaved(t *testing.T) {
	root := batchFixture(t)
	decisions := filepath.Join(root, "data/owner/repo/batches/b20260101-000000.decisions.jsonl")
	if err := os.WriteFile(decisions, []byte(`{"number":1,"kind":"issue","category":"wontfix","action":"close-forever","confidence":"medium","reason":"x"}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	m := batchModel(t, root)
	m.openBatch("b20260101-000000")
	m.selectCurrentListItem()
	if view := m.form.View(80); !strings.Contains(view, "wontfix") || !strings.Contains(view, "close-forever") || !strings.Contains(view, "not in taxonomy") {
		t.Fatalf("the form should show the proposal's real values, flagged:\n%s", view)
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = next.(model)
	if cmd != nil || !strings.Contains(m.status, "Can't save") {
		t.Fatalf("saving an invalid proposal unchanged should be refused: %q", m.status)
	}

	m = press(m, "tab")
	m = press(m, "down")
	if m.form.InvalidValues() != `action "close-forever"` {
		t.Fatalf("picking a category should clear only that flag: %q", m.form.InvalidValues())
	}
}

func TestOpenReportsOpenerFailure(t *testing.T) {
	original := openerCommand
	defer func() { openerCommand = original }()
	openerCommand = func(string) *exec.Cmd { return exec.Command("sh", "-c", "echo 'no method available' >&2; exit 3") }

	m := batchModel(t, batchFixture(t))
	m.activateTab(0)
	m.selectCurrentListItem()
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	m = send(next.(model), cmd())
	if !strings.HasPrefix(m.status, "Couldn't open the browser") || !strings.Contains(m.status, "no method available") {
		t.Fatalf("a failing opener should be reported, with its reason: %q", m.status)
	}
}

func TestEmptyListShowsOneCenteredMessage(t *testing.T) {
	m := batchModel(t, batchFixture(t))
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m.untriagedKind = untriagedPR
	m.activateTab(untriagedTab) // The fixture has no PRs.
	view := m.View()
	if strings.Count(view, "Nothing here yet.") != 1 || strings.Contains(view, "No items") {
		t.Fatalf("expected exactly one empty-state message:\n%s", view)
	}
}
