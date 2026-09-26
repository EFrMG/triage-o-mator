package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

type commentEditorMsg struct {
	root, repo string
	key        Key
	body       string
	err        error
}

// prepareCommentEditor creates a private, disposable Markdown draft. Only the user's EDITOR setting is shell code; the filename is passed as a separate argument.
func prepareCommentEditor(body string) (*exec.Cmd, string, error) {
	file, err := os.CreateTemp("", "triage-comment-*.md")
	if err != nil {
		return nil, "", err
	}

	path := file.Name()
	_, writeErr := file.WriteString(body)
	closeErr := file.Close()
	if writeErr != nil || closeErr != nil {
		os.Remove(path)
		if writeErr != nil {
			return nil, "", writeErr
		}

		return nil, "", closeErr
	}

	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		editor = "vi"
	}

	return exec.Command("sh", "-c", "exec "+editor+` "$1"`, "triage-comment-editor", path), path, nil
}

func readCommentEditor(path string, editorErr error) (string, error) {
	if editorErr != nil {
		return "", fmt.Errorf("editor failed: %w; draft retained at %s", editorErr, path)
	}

	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("reading editor draft: %w; draft path: %s", err, path)
	}
	defer file.Close()

	// Bound bytes before loading the editor buffer; a Unicode character may occupy four bytes.
	data, err := io.ReadAll(io.LimitReader(file, 4*65536+1))
	if err != nil {
		return "", fmt.Errorf("reading editor draft: %w; draft retained at %s", err, path)
	}

	body := strings.ReplaceAll(strings.ReplaceAll(string(data), "\r\n", "\n"), "\t", "    ")
	if !utf8.ValidString(body) || utf8.RuneCountInString(body) > 65536 || strings.Count(body, "\n") >= 10000 {
		return "", fmt.Errorf("editor draft exceeds the editor limits or is not UTF-8; draft retained at %s", path)
	}
	for _, r := range body {
		if (r < 32 && r != '\n') || (r >= 127 && r <= 159) {
			return "", fmt.Errorf("editor draft contains unsupported control characters; draft retained at %s", path)
		}
	}

	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("removing editor draft: %w; draft retained at %s", err, path)
	}

	return body, nil
}

func (m model) openExternalComment() (tea.Model, tea.Cmd) {
	next, _ := m.openComment()
	m = next.(model)
	if !m.comment.open {
		return m, nil
	}

	return m.startCommentEditor()
}

func (m model) openExternalClose() (tea.Model, tea.Cmd) {
	next, _ := m.openClose()
	m = next.(model)
	if !m.comment.open {
		return m, nil
	}

	return m.startCommentEditor()
}

func (m model) openExternalReopen(items []Item) (tea.Model, tea.Cmd) {
	next, _ := m.openReopen(items)
	m = next.(model)
	if !m.comment.open {
		return m, nil
	}

	return m.startCommentEditor()
}

func (m model) startCommentEditor() (tea.Model, tea.Cmd) {
	command, path, err := prepareCommentEditor(m.comment.text.Value())
	if err != nil {
		m.failErr("Couldn't open comment editor", err)
		return m, nil
	}

	root, repo, key := m.installRoot, m.repo, m.comment.key
	m.comment.busy = true
	m.status = "Editing comment in $EDITOR…"

	return m, tea.ExecProcess(command, func(err error) tea.Msg {
		body, readErr := readCommentEditor(path, err)
		return commentEditorMsg{root: root, repo: repo, key: key, body: body, err: readErr}
	})
}

func (m model) finishCommentEditor(msg commentEditorMsg) (tea.Model, tea.Cmd) {
	if !m.comment.open || msg.root != m.installRoot || msg.repo != m.repo || msg.key != m.comment.key {
		return m, nil
	}

	m.comment.busy = false
	if msg.err != nil {
		m.failErr("Couldn't load edited comment", msg.err)
		return m, nil
	}

	c := &m.comment
	c.text.SetValue(msg.body)
	c.text.Blur()
	c.previewing = true
	c.approval, c.requestID = "", ""
	c.setPreview(c.text.Value())
	c.preview.GotoTop()
	m.status = "Comment loaded. Ctrl-S publishes; C reopens $EDITOR."
	if c.close {
		m.status = "Comment loaded. Ctrl-S closes with comment; X reopens $EDITOR."
	}
	if c.reopen {
		m.status = "Comment loaded. Review targets, then Ctrl-S reopens; V reopens $EDITOR."
	}

	return m, nil
}
