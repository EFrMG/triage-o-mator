package main

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestBracketedPasteReachesFocusedTextFields(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*model)
		value func(model) string
		want  string
	}{
		{"reason", func(m *model) { m.focus = FocusDetail; m.form.FocusField(fieldReason) }, func(m model) string { return m.form.Reason() }, "pasted text"},
		{"comment", func(m *model) {
			m.comment.open = true
			m.comment.text = textarea.New()
			m.comment.text.Focus()
		}, func(m model) string { return m.comment.text.Value() }, "pasted text"},
		{"list search", func(m *model) {
			m.activateTab(untriagedTab)
			m.searching = true
			m.searchInput.Focus()
		}, func(m model) string { return m.searchInput.Value() }, "pasted text"},
		{"theme search", func(m *model) {
			query := textinput.New()
			query.Focus()
			m.themePicker = themePicker{open: true, searching: true, query: query}
		}, func(m model) string { return m.themePicker.query.Value() }, "pasted text"},
		{"group editor", func(m *model) {
			m.groups.open = true
			m.editGroup("new")
		}, func(m model) string { return m.groups.inputs[0].Value() }, "pasted text"},
		{"batch size", func(m *model) {
			m.batches.open = true
			m.startBatchForm()
		}, func(m model) string { return m.batches.size.Value() }, "25p"},
		{"repo picker", func(m *model) {
			m.editingRepo = true
			m.repoInput.Focus()
		}, func(m model) string { return m.repoInput.Value() }, "pasted text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newModel("/tmp", "owner/repo", testTaxonomy(), "tester", testItems())
			m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
			tc.setup(&m)
			paste := "pasted text"
			if tc.name == "batch size" {
				paste = "p"
			}

			m = send(m, tea.PasteMsg{Content: paste})
			if got := tc.value(m); got != tc.want {
				t.Fatalf("paste gave %q, want %q", got, tc.want)
			}
			if tc.name == "reason" && (!m.form.dirty || m.form.saved || !m.form.touched) {
				t.Fatal("pasting a reason must mark the decision as changed")
			}
		})
	}
}

func TestPasteDoesNotEditBehindModal(t *testing.T) {
	m := newModel("/tmp", "owner/repo", testTaxonomy(), "tester", testItems())
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.searching = true
	m.searchInput.Focus()
	m.lastError.open = true
	m = send(m, tea.PasteMsg{Content: "hidden"})
	if m.searchInput.Value() != "" {
		t.Fatal("paste passed through the open error modal")
	}
}

func TestPasteAfterSaveWarningRequiresAnotherWarning(t *testing.T) {
	m := openFixtureItem(t)
	m.form.FocusField(fieldReason)
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if cmd != nil || !m.confirmSave {
		t.Fatal("an empty reason should require confirmation before approval")
	}

	m = send(m, tea.PasteMsg{Content: " "})
	if m.confirmSave || m.form.Reason() != " " {
		t.Fatal("pasting into the reason must clear the earlier confirmation")
	}

	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if cmd != nil || !m.confirmSave || !strings.Contains(m.status, "No reason") {
		t.Fatalf("a whitespace-only pasted reason must warn again before approval: cmd %v, confirmed %v, status %q", cmd != nil, m.confirmSave, m.status)
	}
}

func TestRepoPromptInputBorderHasItsRequestedWidth(t *testing.T) {
	for _, width := range []int{80, 100, 140} {
		m := newModel("/tmp", "owner/repo", testTaxonomy(), "tester", testItems())
		m = send(m, tea.WindowSizeMsg{Width: width, Height: 30})
		m.editingRepo = true
		line := strings.Split(m.repoPromptView(), "\n")[2]
		want := minInt(m.menuWidth(), 60) + 1 // inset adds one column
		if got := ansi.StringWidth(line); got != want {
			t.Fatalf("at %d columns, input border width = %d, want %d: %q", width, got, want, ansi.Strip(line))
		}
	}
}
