package main

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestFriendlyErrorKeepsItShort(t *testing.T) {
	cases := []struct {
		output, want string
	}{
		{"error: config/repo does not look like 'owner/repo': 'x'", "config/repo does not look like 'owner/repo': 'x'"},
		{"usage: group [-h] ...\ngroup: error: argument --status: invalid choice: 'redy'", "argument --status: invalid choice: 'redy'"},
		{"Traceback (most recent call last):\n  File \"bin/group\", line 3\nValueError: invalid group status", "invalid group status"},
		{"To get started with GitHub CLI, please run:  gh auth login", "the GitHub CLI isn't logged in. Run gh auth login, then r."},
		{"error connecting to api.github.com\ncheck your internet connection", "couldn't reach GitHub. Check the connection, then r."},
		{"GraphQL: API rate limit exceeded for user", "GitHub's rate limit is used up. Wait a while, then r."},
		{"Fetching…\nsomething odd happened", "Fetching…"},
	}

	for _, c := range cases {
		err := &scriptError{script: "group", args: []string{"update", "id"}, output: c.output, err: errors.New("exit status 1")}
		if got := friendlyError(err); got != c.want {
			t.Errorf("friendlyError(%q) = %q, want %q", c.output, got, c.want)
		}
	}

	silent := &scriptError{script: "sync", output: "", err: errors.New("exit status 2")}
	if got := friendlyError(silent); !strings.Contains(got, "bin/sync stopped (exit status 2)") {
		t.Errorf("a script that printed nothing: %q", got)
	}
}

// A real bin/group failure shows one short line, and ! shows the command and everything it printed; in a text field ! is typed instead.
func TestScriptErrorsAreShortWithDetailsOnBang(t *testing.T) {
	root, g := groupFixture(t)
	m := testPRModel()
	m.installRoot = root
	m = send(m, windowSize(120, 36))
	m = send(m, groupsCmd(root, "update", g.ID, "--revision", strconv.Itoa(g.Revision+5), "--title", "x", "--by", "tester")())
	if !m.statusIsError() || !strings.Contains(m.status, "Couldn't save the group: someone else changed this group") || strings.Contains(m.status, "exit status") {
		t.Fatalf("stale revision status: %q", m.status)
	}

	m = press(m, "!")
	view := ansi.Strip(m.View())
	if !m.lastError.open || !strings.Contains(view, "Last error") || !strings.Contains(view, "$ bin/group update "+g.ID) || !strings.Contains(view, "exit status") {
		t.Fatalf("! should show the command and its output:\n%s", view)
	}

	m = press(m, "esc")
	if m.lastError.open {
		t.Fatal("Esc should close the details")
	}

	m.groups.open = true
	m.groups.records = []Group{g}
	m = press(m, "n")
	m = press(m, "!")
	if m.lastError.open || m.groups.inputs[0].Value() != "!" {
		t.Fatalf("in a text field ! is a character: open %v, %q", m.lastError.open, m.groups.inputs[0].Value())
	}
}

func windowSize(w, h int) tea.Msg { return tea.WindowSizeMsg{Width: w, Height: h} }

// A full export reports bin/group's progress lines as they arrive, in the status line and the Groups subtitle, then the reloaded groups.
func TestExportShowsProgress(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	script := "#!/bin/sh\nif [ \"$1\" = list ]; then echo '[{\"id\":\"g\",\"title\":\"Wifi\",\"status\":\"draft\",\"members\":[]}]'; exit; fi\necho 'fetching 1/2: issue #1' >&2\necho 'fetching 2/2: pr #2' >&2\n"
	if err := os.WriteFile(filepath.Join(root, "bin", "group"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	m := testPRModel()
	m.installRoot = root
	m = send(m, windowSize(120, 36))
	m.groups = groupUI{open: true, records: []Group{{ID: "g", Title: "Wifi", Status: "draft", Members: []GroupMember{{Kind: "issue", Number: 1}, {Kind: "pr", Number: 2}}}}}
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("X")})
	m = next.(model)
	if !strings.Contains(m.status, `Full export of "Wifi": fetching 0/2`) {
		t.Fatalf("status when the export starts: %q", m.status)
	}

	var seen []string
	for cmd != nil {
		next, cmd = m.Update(cmd())
		m = next.(model)
		if m.groups.busy {
			seen = append(seen, m.status)
			if view := ansi.Strip(m.View()); !strings.Contains(view, m.groups.progress) {
				t.Fatalf("the Groups subtitle should show the progress:\n%s", view)
			}
		}
	}

	if len(seen) != 2 || !strings.Contains(seen[0], "fetching 1/2: issue #1") || !strings.Contains(seen[1], "fetching 2/2: pr #2") {
		t.Fatalf("progress statuses: %q", seen)
	}

	if m.groups.busy || !strings.HasPrefix(m.status, "Exported review packet to ") {
		t.Fatalf("after the export: busy %v, %q", m.groups.busy, m.status)
	}
}
