package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type commentComposer struct {
	previewText                       string
	open, busy, previewing            bool
	key                               Key
	host, target, requestID, approval string
	text                              textarea.Model
	preview                           viewport.Model
}

type commentMsg struct {
	root, repo string
	publish    bool
	out        string
	err        error
}

func (m model) openComment() (tea.Model, tea.Cmd) {
	it, ok := m.findItem(m.detail.key)

	if !ok {
		return m, nil
	}

	u, err := url.Parse(it.URL)
	path := "issues"

	if it.Kind == "pr" {
		path = "pull"
	}

	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != fmt.Sprintf("/%s/%s/%d", m.repo, path, it.Number) {
		m.status = "Cannot comment: item URL does not match this repository and item."
		return m, nil
	}

	text := textarea.New()
	text.Placeholder = "Comment to publish on GitHub"
	text.CharLimit = 65536
	text.ShowLineNumbers = false
	themeTextarea(&text)
	text.SetWidth(maxInt(m.width-8, 20))
	text.SetHeight(maxInt(m.mainHeight()-7, 3))
	m.comment = commentComposer{open: true, key: it.Key(), host: u.Host, target: it.URL, text: text, preview: viewport.New(maxInt(m.width-8, 20), maxInt(m.mainHeight()-7, 3))}
	cmd := m.comment.text.Focus()

	return m, cmd
}

func (m model) commentCmd(publish bool) tea.Cmd {
	root, repo, c := m.installRoot, m.repo, m.comment

	return func() tea.Msg {
		args := []string{"--expected-repo", repo, "--host", c.host, "--kind", c.key.Kind, "--number", strconv.Itoa(c.key.Number), "--body=" + c.text.Value()}
		if publish {
			args = append(args, "--publish", "--request-id", c.requestID, "--approve", c.approval)
		}
		out, err := runScript(root, "comment", args...)
		return commentMsg{root: root, repo: repo, publish: publish, out: out, err: err}
	}
}

func (m model) handleCommentKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

	if c.previewing && msg.String() == "C" {
		return m.startCommentEditor()
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

		// This key approves the visible target and current text; the script still binds its dry-run plan before publishing.
		c.busy = true
		m.status = "Publishing comment…"
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

	c := &m.comment
	c.busy = false

	if msg.err != nil {
		if msg.publish {
			c.open = false
			m.failErr("Comment outcome uncertain; inspect GitHub before retrying", msg.err)
		} else {
			m.failErr("Couldn't prepare comment", msg.err)
		}
		return m, nil
	}

	if msg.publish {
		c.open = false
		var result struct {
			Comment struct {
				URL string `json:"url"`
			} `json:"comment"`
		}
		if err := json.Unmarshal([]byte(msg.out), &result); err != nil || result.Comment.URL == "" {
			m.fail("Comment outcome could not be read; inspect GitHub before retrying.")
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
			RequestID string `json:"request_id"`
			Target    string `json:"target"`
			Body      string `json:"body"`
		} `json:"plan"`
	}

	if err := json.Unmarshal([]byte(msg.out), &result); err != nil || result.Approval == "" || result.Plan.RequestID == "" || result.Plan.Target != c.target || result.Plan.Body != c.text.Value() {
		m.status = "Invalid comment plan; nothing published."
		return m, nil
	}
	c.approval, c.requestID = result.Approval, result.Plan.RequestID
	c.busy = true

	return m, m.commentCmd(true)
}

func (m model) commentHeader(width int) string {
	heading, color := "Compose comment", currentTheme.Info
	if m.comment.previewing {
		heading, color = "Preview Markdown", currentTheme.Success
	}

	title := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true).Render(heading)
	link := ansi.Truncate(m.comment.target, maxInt(width-ansi.StringWidth(title)-1, 1), "…")
	gap := strings.Repeat(" ", maxInt(width-ansi.StringWidth(title)-ansi.StringWidth(link), 1))
	return title + gap + link
}

func (m model) commentView() string {
	content := m.comment.text.View()
	if m.comment.previewing {
		content = m.comment.preview.View()
	}

	return panelStyle(true).Width(m.width-2).Height(m.mainHeight()).Padding(0, 1).Render(m.commentHeader(maxInt(m.width-4, 20)) + "\n\n" + content)
}

func (c *commentComposer) setPreview(text string) {
	c.previewText = text
	if c.previewing {
		c.preview.SetContent(renderMarkdown(text, c.preview.Width))
	} else {
		c.preview.SetContent(ansi.Wrap(text, maxInt(c.preview.Width, 1), ""))
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
	resized := c.preview.Width != width
	c.preview.Width, c.preview.Height = width, height
	if resized {
		offset := c.preview.YOffset
		c.setPreview(c.previewText)
		c.preview.SetYOffset(offset)
	}
}
