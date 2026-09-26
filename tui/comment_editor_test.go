package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestExternalCommentEditorRoundTrip(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "drafts with spaces")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", directory)
	editor := filepath.Join(t.TempDir(), "fake editor")
	script := "#!/bin/sh\n[ \"$1\" = '--wait' ] || exit 2\n[ \"$(cat \"$2\")\" = 'original draft' ] || exit 3\nprintf '# Edited\\n\\n```go\\nvar answer = 42\\n```\\n' > \"$2\"\n"
	if err := os.WriteFile(editor, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("EDITOR", "'"+editor+"' --wait")

	command, path, err := prepareCommentEditor("original draft")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 || filepath.Ext(path) != ".md" {
		t.Fatalf("expected private Markdown draft: %v, %v", info, err)
	}
	body, err := readCommentEditor(path, command.Run())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("successful editor draft was not removed")
	}

	m := commentEditorFixture(t)
	m.comment.busy = true
	next, cmd := m.Update(commentEditorMsg{root: m.installRoot, repo: m.repo, key: m.comment.key, body: body})
	m = next.(model)
	if cmd != nil || m.comment.busy || !m.comment.previewing || m.comment.text.Value() != body {
		t.Fatal("editor return must load preview without publishing")
	}
	rendered := ansi.Strip(m.comment.preview.View())
	if !strings.Contains(rendered, "Edited") || strings.Contains(rendered, "```") {
		t.Fatalf("editor draft did not render: %q", rendered)
	}
}

func TestExternalCommentBindingAndLiteralCapitalC(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("EDITOR", "vi")
	m := commentEditorFixture(t)
	m.comment.open = false
	m.focus = FocusDetail
	next, cmd := m.Update(tea.KeyPressMsg{Text: "C"})
	m = next.(model)
	if cmd == nil || !m.comment.open || !m.comment.busy {
		t.Fatal("C on item must launch external comment editor")
	}

	m = commentEditorFixture(t)
	m = send(m, tea.KeyPressMsg{Text: "C"})
	if m.comment.text.Value() != "C" || m.comment.busy {
		t.Fatal("C in the inline editor must remain text")
	}
	m = send(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	next, cmd = m.Update(tea.KeyPressMsg{Text: "C"})
	if cmd == nil || !next.(model).comment.busy {
		t.Fatal("C in preview must reopen the draft in the editor")
	}
}

func TestExternalCommentFailurePreservesDraft(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	t.Setenv("EDITOR", "exit 7")
	command, path, err := prepareCommentEditor("retained")
	if err != nil {
		t.Fatal(err)
	}
	_, err = readCommentEditor(path, command.Run())
	if err == nil || !strings.Contains(err.Error(), path) {
		t.Fatal("failure must report retained draft path")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "retained" {
		t.Fatal("failed editor lost its draft")
	}

	m := commentEditorFixture(t)
	m.comment.text.SetValue("original")
	m.comment.busy = true
	m = send(m, commentEditorMsg{root: m.installRoot, repo: m.repo, key: m.comment.key, err: errors.New("editor failed")})
	if m.comment.busy || m.comment.text.Value() != "original" {
		t.Fatal("failed editor replaced the current draft")
	}

	m = send(m, commentEditorMsg{root: "other install", repo: m.repo, key: m.comment.key, body: "stale"})
	if m.comment.text.Value() != "original" {
		t.Fatal("stale reply replaced the current draft")
	}
}

func TestExternalCommentRejectsOversizedDraftWithoutTruncation(t *testing.T) {
	t.Setenv("TMPDIR", t.TempDir())
	_, path, err := prepareCommentEditor(strings.Repeat("x", 65537))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := readCommentEditor(path, nil); err == nil {
		t.Fatal("oversized draft must not be silently truncated")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("invalid draft must remain recoverable")
	}
}
