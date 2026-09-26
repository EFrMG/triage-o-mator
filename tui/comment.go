package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type commentComposer struct {
	previewText                           string
	open, busy, previewing, close, reopen bool
	key                                   Key
	host, target, requestID, approval     string
	targets                               []commentTarget
	index                                 int
	completed                             []Key
	text                                  textarea.Model
	preview                               viewport.Model
}

type commentTarget struct {
	key  Key
	host string
	url  string
}

type commentMsg struct {
	root, repo string
	publish    bool
	index      int
	out        string
	err        error
}

func (m model) openComment() (tea.Model, tea.Cmd) {
	return m.openCommentFor(false)
}

func (m model) openClose() (tea.Model, tea.Cmd) {
	return m.openCommentFor(true)
}

func (m model) openCommentFor(close bool) (tea.Model, tea.Cmd) {
	it, ok := m.findItem(m.detail.key)

	if !ok {
		return m, nil
	}
	if close && it.State != "open" {
		m.warn("Only an open item can be closed.")
		return m, nil
	}
	if close && (m.refreshing || m.corpus.busy) {
		m.warn("Wait for the current download or refresh before closing an item.")
		return m, nil
	}

	target, err := validateCommentTarget(it, m.repo)
	if err != nil {
		m.status = err.Error()
		return m, nil
	}

	return m.openCommentComposer(target, close, false, nil)
}

func validateCommentTarget(it Item, repo string) (commentTarget, error) {
	u, err := url.Parse(it.URL)
	path := "issues"

	if it.Kind == "pr" {
		path = "pull"
	}

	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != fmt.Sprintf("/%s/%s/%d", repo, path, it.Number) {
		return commentTarget{}, fmt.Errorf("cannot comment: item URL does not match this repository and item")
	}

	return commentTarget{key: it.Key(), host: u.Host, url: it.URL}, nil
}

func (m model) openReopen(items []Item) (tea.Model, tea.Cmd) {
	if len(items) == 0 {
		m.warn("Select a closed issue or PR to reopen.")
		return m, nil
	}
	if m.refreshing || m.corpus.busy {
		m.warn("Wait for the current download or refresh before reopening items.")
		return m, nil
	}

	targets := make([]commentTarget, 0, len(items))
	for _, it := range items {
		if it.State != "closed" {
			m.warn("Every selected item must be closed before reopening.")
			return m, nil
		}
		target, err := validateCommentTarget(it, m.repo)
		if err != nil {
			m.warn(err.Error())
			return m, nil
		}
		targets = append(targets, target)
	}

	return m.openCommentComposer(targets[0], false, true, targets)
}

func (m model) openCommentComposer(target commentTarget, close, reopen bool, targets []commentTarget) (tea.Model, tea.Cmd) {
	text := textarea.New()
	text.Placeholder = "Comment to publish on GitHub"
	text.CharLimit = 65536
	text.ShowLineNumbers = false
	themeTextarea(&text)
	text.SetWidth(maxInt(m.width-8, 20))
	text.SetHeight(maxInt(m.mainHeight()-7, 3))
	m.comment = commentComposer{open: true, close: close, reopen: reopen, key: target.key, host: target.host, target: target.url, targets: targets, text: text, preview: viewport.New(viewport.WithWidth(maxInt(m.width-8, 20)), viewport.WithHeight(maxInt(m.mainHeight()-7, 3)))}
	cmd := m.comment.text.Focus()

	return m, cmd
}

func (m model) commentCmd(publish bool) tea.Cmd {
	root, repo, c := m.installRoot, m.repo, m.comment

	return func() tea.Msg {
		target := commentTarget{key: c.key, host: c.host, url: c.target}
		if c.reopen {
			target = c.targets[c.index]
		}
		args := []string{"--expected-repo", repo, "--host", target.host, "--kind", target.key.Kind, "--number", strconv.Itoa(target.key.Number), "--body=" + c.text.Value()}
		if c.close {
			args = append(args, "--close")
		}
		if c.reopen {
			args = append(args, "--reopen")
		}
		if publish {
			args = append(args, "--publish", "--request-id", c.requestID, "--approve", c.approval)
		}
		out, err := runScript(root, "comment", args...)
		return commentMsg{root: root, repo: repo, publish: publish, index: c.index, out: out, err: err}
	}
}

func (m model) handleCommentKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	c := &m.comment
	if c.busy {
		return m, nil
	}

	if msg.String() == "esc" {
		if c.previewing {
			c.previewing = false
			cmd := c.text.Focus()

			return m, cmd
		}

		c.open = false
		return m, nil
	}

	if c.previewing {
		editorKey := "C"
		if c.close {
			editorKey = "X"
		}
		if c.reopen {
			editorKey = "V"
		}
		if msg.String() == editorKey {
			return m.startCommentEditor()
		}
	}

	switch msg.String() {
	case "ctrl+p":
		c.previewing = !c.previewing
		if !c.previewing {
			cmd := c.text.Focus()

			return m, cmd
		}

		c.text.Blur()
		c.setPreview(c.text.Value())
		c.preview.GotoTop()
		return m, nil

	case "ctrl+s":
		if strings.TrimSpace(c.text.Value()) == "" {
			m.warn("Write a comment before publishing.")
			return m, nil
		}

		if c.reopen && len(c.targets) > 1 && !c.previewing {
			c.previewing = true
			c.text.Blur()
			c.setPreview(c.text.Value())
			c.preview.GotoTop()
			m.status = "Review every target and the shared explanation. Ctrl-S approves all of them."
			return m, nil
		}

		// This key approves the visible targets and current text; the script still binds each dry-run plan before publishing.
		c.busy = true
		m.status = "Publishing comment…"
		if c.close {
			m.status = "Publishing explanation and closing item…"
		}
		if c.reopen {
			m.status = "Reopening " + pluralize(len(c.targets), "item", "items") + "…"
		}
		return m, m.commentCmd(false)
	}

	var cmd tea.Cmd
	if c.previewing {
		c.preview, cmd = c.preview.Update(msg)
	} else {
		c.text, cmd = c.text.Update(msg)
	}

	return m, cmd
}

func (m model) finishComment(msg commentMsg) (tea.Model, tea.Cmd) {
	if !m.comment.open || msg.root != m.installRoot || msg.repo != m.repo {
		return m, nil
	}
	if m.comment.reopen {
		return m.finishReopenComment(msg)
	}

	c := &m.comment
	c.busy = false

	if msg.err != nil {
		if msg.publish {
			c.open = false
			m.failErr("Write outcome uncertain; inspect GitHub and the saved record before retrying", msg.err)
		} else {
			m.failErr("Couldn't prepare comment", msg.err)
		}
		return m, nil
	}

	if msg.publish {
		c.open = false
		var result struct {
			Comment struct {
				Status string `json:"status"`
				URL    string `json:"url"`
			} `json:"comment"`
			StateChange struct {
				Status string `json:"status"`
				State  string `json:"state"`
			} `json:"state_change"`
		}
		if err := json.Unmarshal([]byte(msg.out), &result); err != nil || result.Comment.URL == "" || (c.close && (result.Comment.Status != "succeeded" || result.StateChange.Status != "succeeded" || result.StateChange.State != "closed")) {
			m.fail("Write outcome could not be read; inspect GitHub before retrying.")
			return m, nil
		}

		for i, section := range m.detail.sections {
			if section.kind == commentsSection {
				m.detail.JumpSection(i)
				break
			}
		}

		delete(m.detail.cache, c.key)
		m.status = "Comment published."
		if c.close {
			// The script confirmed the PATCH. Reflect that result in this view while fetch/sync updates the ledger in the background.
			for i := range m.items {
				if m.items[i].Key() == c.key {
					m.items[i].State = "closed"
					break
				}
			}
			if m.detail.key == c.key {
				m.detail.item.State = "closed"
			}
			m.recomputeSidebarCounts()
			m.refreshActiveList()
			readCmd := m.refreshLiveDetail()

			next, syncCmd := m.startRefresh(false)
			updated := next.(model)
			updated.status = "Comment published and item closed. Refreshing ledger…"
			updated.refreshStatus = updated.status
			if readCmd == nil {
				return updated, syncCmd
			}

			return updated, tea.Batch(syncCmd, func() tea.Msg {
				result := readCmd().(enrichedMsg)
				result.afterClose = true
				return result
			})
		}
		cmd := m.refreshLiveDetail()
		if cmd == nil {
			return m, nil
		}

		return m, func() tea.Msg {
			result := cmd().(enrichedMsg)
			result.afterComment = true
			return result
		}
	}

	var result struct {
		Approval string `json:"approval"`
		Plan     struct {
			RequestID   string `json:"request_id"`
			Target      string `json:"target"`
			Body        string `json:"body"`
			Operation   string `json:"operation"`
			StateChange string `json:"state_change"`
		} `json:"plan"`
	}

	operation, state := "comment", "none"
	if c.close {
		operation, state = "close", "closed"
	}
	if err := json.Unmarshal([]byte(msg.out), &result); err != nil || result.Approval == "" || result.Plan.RequestID == "" || result.Plan.Target != c.target || result.Plan.Body != c.text.Value() || result.Plan.Operation != operation || result.Plan.StateChange != state {
		m.status = "Invalid comment plan; nothing published."
		return m, nil
	}
	c.approval, c.requestID = result.Approval, result.Plan.RequestID
	c.busy = true

	return m, m.commentCmd(true)
}

func (m model) finishReopenComment(msg commentMsg) (tea.Model, tea.Cmd) {
	c := &m.comment
	if msg.index != c.index {
		return m, nil
	}
	c.busy = false
	target := c.targets[c.index]
	if msg.err != nil {
		return m.stopReopening(target, msg.err, msg.publish)
	}

	if !msg.publish {
		var preview struct {
			Approval string `json:"approval"`
			Plan     struct {
				RequestID   string `json:"request_id"`
				Target      string `json:"target"`
				Body        string `json:"body"`
				Operation   string `json:"operation"`
				StateChange string `json:"state_change"`
			} `json:"plan"`
		}
		if err := json.Unmarshal([]byte(msg.out), &preview); err != nil || preview.Approval == "" || preview.Plan.RequestID == "" || preview.Plan.Target != target.url || preview.Plan.Body != c.text.Value() || preview.Plan.Operation != "reopen" || preview.Plan.StateChange != "open" {
			return m.stopReopening(target, fmt.Errorf("invalid reopen plan; nothing sent for this item"), false)
		}

		c.approval, c.requestID = preview.Approval, preview.Plan.RequestID
		c.busy = true
		return m, m.commentCmd(true)
	}

	var result struct {
		Comment struct {
			Status string `json:"status"`
			URL    string `json:"url"`
		} `json:"comment"`
		StateChange struct {
			Status string `json:"status"`
			State  string `json:"state"`
			URL    string `json:"url"`
		} `json:"state_change"`
	}
	if err := json.Unmarshal([]byte(msg.out), &result); err != nil || result.Comment.Status != "succeeded" || result.Comment.URL == "" || result.StateChange.Status != "succeeded" || result.StateChange.State != "open" || result.StateChange.URL != target.url {
		return m.stopReopening(target, fmt.Errorf("write outcome could not be read; inspect GitHub and the saved record"), true)
	}

	c.completed = append(c.completed, target.key)
	for i := range m.items {
		if m.items[i].Key() == target.key {
			m.items[i].State = "open"
			break
		}
	}
	if m.detail.key == target.key {
		m.detail.item.State = "open"
	}
	m.recomputeSidebarCounts()
	m.refreshActiveList()
	c.index++
	if c.index < len(c.targets) {
		c.approval, c.requestID = "", ""
		c.busy = true
		m.status = fmt.Sprintf("Reopened %d/%d; preparing next item…", len(c.completed), len(c.targets))
		return m, m.commentCmd(false)
	}

	c.open = false
	m.clearTicks()
	if m.detail.key == target.key {
		for i, section := range m.detail.sections {
			if section.kind == commentsSection {
				m.detail.JumpSection(i)
				break
			}
		}
	}
	readCmd := m.refreshLiveDetail()
	next, syncCmd := m.startRefresh(false)
	updated := next.(model)
	updated.status = "Reopened " + pluralize(len(c.completed), "item", "items") + "; refreshing ledger…"
	updated.refreshStatus = updated.status
	if readCmd == nil {
		return updated, syncCmd
	}

	return updated, tea.Batch(syncCmd, func() tea.Msg {
		result := readCmd().(enrichedMsg)
		result.afterReopen = true
		return result
	})
}

func (m model) stopReopening(target commentTarget, err error, uncertain bool) (tea.Model, tea.Cmd) {
	c := &m.comment
	c.open, c.busy = false, false
	completed, total := len(c.completed), len(c.targets)
	m.clearTicks()
	label := fmt.Sprintf("Reopening stopped after %d/%d; %s was not published", completed, total, target.url)
	if uncertain {
		label = fmt.Sprintf("Reopening stopped after %d/%d; %s outcome is uncertain", completed, total, target.url)
	}
	m.failErr(label, err)
	return m, nil
}

func (m model) commentHeader(width int) string {
	heading, color := "Compose comment", currentTheme.Info
	if m.comment.close {
		heading = "Close with comment"
	}
	if m.comment.reopen {
		heading = "Reopen with comment"
	}
	if m.comment.previewing {
		heading, color = "Preview Markdown", currentTheme.Success
		if m.comment.close {
			heading = "Preview closure"
		}
		if m.comment.reopen {
			heading = "Review reopening"
		}
	}

	title := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true).Render(heading)
	linkText := m.comment.target
	if m.comment.reopen && len(m.comment.targets) > 1 {
		linkText = pluralize(len(m.comment.targets), "target", "targets")
	}
	link := ansi.Truncate(linkText, maxInt(width-ansi.StringWidth(title)-1, 1), "…")
	gap := strings.Repeat(" ", maxInt(width-ansi.StringWidth(title)-ansi.StringWidth(link), 1))
	return title + gap + link
}

func (m model) commentView() string {
	content := m.comment.text.View()
	if m.comment.previewing {
		content = m.comment.preview.View()
	}

	return panelStyle(true).Width(m.width).Height(m.mainHeight()+2).Padding(0, 1).Render(m.commentHeader(maxInt(m.width-4, 20)) + "\n\n" + content)
}

func (c *commentComposer) setPreview(text string) {
	c.previewText = text
	if c.reopen && len(c.targets) > 1 {
		var b strings.Builder
		fmt.Fprintf(&b, "Reopen %s with this comment:\n\n", pluralize(len(c.targets), "item", "items"))
		for _, target := range c.targets {
			fmt.Fprintf(&b, "- %s\n", target.url)
		}
		b.WriteString("\nComment:\n\n")
		b.WriteString(text)
		text = b.String()
	}
	if c.previewing {
		c.preview.SetContent(renderMarkdown(text, c.preview.Width()))
	} else {
		c.preview.SetContent(ansi.Wrap(text, maxInt(c.preview.Width(), 1), ""))
	}
}

func (m *model) layoutComment() {
	if !m.comment.open {
		return
	}

	c := &m.comment
	width := maxInt(m.width-4, 20)
	height := maxInt(m.mainHeight()-3, 3)
	c.text.SetWidth(width)
	c.text.SetHeight(height)
	resized := c.preview.Width() != width
	c.preview.SetWidth(width)
	c.preview.SetHeight(height)
	if resized {
		offset := c.preview.YOffset()
		c.setPreview(c.previewText)
		c.preview.SetYOffset(offset)
	}
}
