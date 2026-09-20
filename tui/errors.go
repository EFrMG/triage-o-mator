package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Failures show as one short sentence in the status line; ! opens the last one in full (the command, and everything it printed), until the next failure replaces it.

// scriptError is a bin/* script that failed: which one, with what arguments, and what it printed.
type scriptError struct {
	script string
	args   []string
	output string
	err    error
}

func (e *scriptError) Error() string { return fmt.Sprintf("%s: %s (%v)", e.script, e.output, e.err) }

func (e *scriptError) Unwrap() error { return e.err }

// errorDetails is the last failure, kept for the ! screen.
type errorDetails struct {
	what, text string
	at         time.Time
	open       bool
	offset     int
}

// knownFailures turn the messages scripts and gh print for common problems into what to do about them; the first match wins.
var knownFailures = []struct{ match, say string }{
	{"gh auth login", "the GitHub CLI isn't logged in. Run gh auth login, then r."},
	{"http 401", "GitHub refused the login. Run gh auth login again, then r."},
	{"bad credentials", "GitHub refused the login. Run gh auth login again, then r."},
	{"rate limit", "GitHub's rate limit is used up. Wait a while, then r."},
	{"error connecting to", "couldn't reach GitHub. Check the connection, then r."},
	{"could not resolve host", "couldn't reach GitHub. Check the connection, then r."},
	{"dial tcp", "couldn't reach GitHub. Check the connection, then r."},
	{"i/o timeout", "GitHub took too long to answer. Try again with r."},
	{"executable file not found", "gh (the GitHub CLI) isn't installed or isn't on PATH."},
	{"group changed since it was loaded", "someone else changed this group since you opened it. Reopen Groups and redo the edit."},
	{"is not in the ledger", "the item isn't in the ledger yet. Fetch with r first."},
	{"missing from the ledger", "the item isn't in the ledger yet. Fetch with r first."},
	{"no raw fetch found", "nothing has been fetched for this repo yet. Fetch with r first."},
}

// friendlyError is a short, plain reason for err: a known problem's advice, else the script's own "error:" line or a traceback's last line, else the first line of whatever it printed.
func friendlyError(err error) string {
	text := err.Error()
	var se *scriptError
	if errors.As(err, &se) {
		text = se.output
	}

	lower := strings.ToLower(text)
	for _, f := range knownFailures {
		if strings.Contains(lower, f.match) {
			return f.say
		}
	}

	lines := strings.Split(strings.TrimSpace(text), "\n")
	reason := ""
	for i := len(lines) - 1; i >= 0 && reason == ""; i-- {
		line := strings.TrimSpace(lines[i])
		if j := strings.Index(line, "error: "); j >= 0 {
			// "error: …" from sys.exit, or "prog: error: …" from argparse.
			reason = line[j+len("error: "):]
		} else if k := strings.Index(line, "Error: "); k > 0 && !strings.Contains(line[:k], " ") {
			// A traceback's last line, e.g. "ValueError: invalid group status".
			reason = line[k+len("Error: "):]
		}
	}

	if reason == "" {
		reason = strings.TrimSpace(lines[0])
	}

	if reason == "" && se != nil {
		reason = fmt.Sprintf("bin/%s stopped (%v) without saying why", se.script, se.err)
	}

	return truncateSentence(reason, 160)
}

func truncateSentence(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}

	return s
}

// failErr shows what failed and why in one line, as an error, and keeps the details for !.
func (m *model) failErr(what string, err error) {
	m.fail(what + ": " + friendlyError(err) + " (! for details)")
	m.recordError(what, err)
}

// recordError keeps err's details for ! without a status message, for failures the screen already shows where they happened.
func (m *model) recordError(what string, err error) {
	m.lastError = errorDetails{what: what, text: errorText(err), at: time.Now()}
}

// errorText is everything known about err: for a script, the command line and its whole output.
func errorText(err error) string {
	var se *scriptError
	if !errors.As(err, &se) {
		return err.Error()
	}

	command := "bin/" + se.script
	for _, arg := range se.args {
		if arg == "" || strings.ContainsAny(arg, " \t\"'") {
			arg = fmt.Sprintf("%q", arg)
		}

		command += " " + arg
	}

	output := strings.TrimSpace(se.output)
	if output == "" {
		output = "(it printed nothing)"
	}

	return fmt.Sprintf("$ %s\n%v\n\n%s", command, se.err, output)
}

// typingText reports whether keys are going into a text field, where ! is just a character.
func (m model) typingText() bool {
	return m.typingReason() || m.editingRepo || m.searching || m.themePicker.searching || m.groups.editing != "" || m.batches.editing
}

func (m model) handleErrorKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Back), key.Matches(msg, keys.ErrorDetails), key.Matches(msg, keys.Quit):
		m.lastError.open = false
	case key.Matches(msg, keys.Down):
		m.lastError.offset++
	case key.Matches(msg, keys.Up):
		m.lastError.offset = maxInt(m.lastError.offset-1, 0)
	case key.Matches(msg, keys.Top):
		m.lastError.offset = 0
	case key.Matches(msg, keys.HalfDown):
		m.lastError.offset += m.mainHeight() / 2
	case key.Matches(msg, keys.HalfUp):
		m.lastError.offset = maxInt(m.lastError.offset-m.mainHeight()/2, 0)
	}

	return m, nil
}

// errorView is the ! screen: the last failure in full, scrollable.
func (m model) errorView() string {
	w, h := m.width-4, m.mainHeight()
	e := m.lastError
	head := titleBar("Last error", e.what+" · "+e.at.Format("15:04:05"), w)
	vp := viewport.New(w, maxInt(h-2, 1))
	vp.SetContent(wrapText(e.text, w))
	vp.SetYOffset(e.offset)

	return m.titled(panelStyle(true).Width(w+2).Height(h).Padding(0, 1).Render(lipgloss.JoinVertical(lipgloss.Left, head, "", vp.View())), true)
}
