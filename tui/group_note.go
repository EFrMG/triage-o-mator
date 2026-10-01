package main

import (
	"fmt"
	"unicode/utf8"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

type groupNoteEditor struct {
	groupID    string
	revision   int
	member     Key
	original   string
	request    uint64
	previewing bool
	text       textarea.Model
	preview    viewport.Model
}

type groupNoteEditorMsg struct {
	root, repo, groupID string
	revision            int
	member              Key
	request             uint64
	text                string
	err                 error
}

func (m model) openGroupNote() (tea.Model, tea.Cmd) {
	g := m.selectedGroup()
	if g == nil || !m.groups.detail || m.groups.member >= len(g.Members) {
		return m, nil
	}

	member := g.Members[m.groups.member]
	text := textarea.New()
	text.Placeholder = "Why this item belongs in the group"
	text.CharLimit = maxInt(65536, utf8.RuneCountInString(member.Notes)+1024)
	text.ShowLineNumbers = false
	themeTextarea(&text)
	text.SetValue(member.Notes)
	m.groups.noteRequest++
	m.groups.note = groupNoteEditor{groupID: g.ID, revision: g.Revision, member: member.Key(), original: member.Notes,
		request: m.groups.noteRequest, text: text, preview: viewport.New()}
	m.groups.editing = "notes"
	m.status = ""
	m.layoutGroupNote()
	return m, m.groups.note.text.Focus()
}

func (m model) handleGroupNoteKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	note := &m.groups.note
	switch msg.String() {
	case "esc":
		m.groups.editing = ""
		m.groups.note = groupNoteEditor{}
		return m, nil
	case "ctrl+e":
		return m.startGroupNoteEditor()
	case "ctrl+p":
		note.previewing = !note.previewing
		if !note.previewing {
			return m, note.text.Focus()
		}
		note.text.Blur()
		m.layoutGroupNote()
		note.preview.SetContent(renderMarkdownWithLineBreaks(note.text.Value(), note.preview.Width(), true))
		note.preview.GotoTop()
		return m, nil
	case "ctrl+s":
		if note.text.Value() == note.original {
			m.groups.editing = ""
			m.groups.note = groupNoteEditor{}
			m.status = "Member note is unchanged."
			return m, nil
		}
		g := m.selectedGroup()
		if g == nil || g.ID != note.groupID {
			m.fail("Group changed; reopen its member note before saving.")
			return m, nil
		}
		group := *g
		group.Revision = note.revision
		m.groups.busy = true
		m.status = "Saving member note…"
		return m, groupBulkCmd(m.installRoot, group, "add", []Key{note.member}, note.text.Value(), m.reviewer)
	}

	var cmd tea.Cmd
	if note.previewing {
		note.preview, cmd = note.preview.Update(msg)
	} else {
		note.text, cmd = note.text.Update(msg)
	}
	return m, cmd
}

func (m model) startGroupNoteEditor() (tea.Model, tea.Cmd) {
	note := &m.groups.note
	command, path, err := prepareCommentEditor(note.text.Value())
	if err != nil {
		m.failErr("Couldn't open member note editor", err)
		return m, nil
	}

	root, repo, groupID, revision, member, request := m.installRoot, m.repo, note.groupID, note.revision, note.member, note.request
	charLimit := note.text.CharLimit
	m.groups.busy = true
	m.status = "Editing member note in $EDITOR…"
	return m, tea.ExecProcess(command, func(err error) tea.Msg {
		text, readErr := readCommentEditor(path, err, charLimit)
		return groupNoteEditorMsg{root: root, repo: repo, groupID: groupID, revision: revision, member: member,
			request: request, text: text, err: readErr}
	})
}

func (m model) finishGroupNoteEditor(msg groupNoteEditorMsg) (tea.Model, tea.Cmd) {
	note := &m.groups.note
	if m.groups.editing != "notes" || msg.root != m.installRoot || msg.repo != m.repo || msg.groupID != note.groupID ||
		msg.revision != note.revision || msg.member != note.member || msg.request != note.request {
		return m, nil
	}

	m.groups.busy = false
	if msg.err != nil {
		m.failErr("Couldn't load edited member note; draft retained", msg.err)
		return m, nil
	}

	note.text.SetValue(msg.text)
	note.text.Blur()
	note.previewing = true
	m.layoutGroupNote()
	note.preview.SetContent(renderMarkdownWithLineBreaks(note.text.Value(), note.preview.Width(), true))
	note.preview.GotoTop()
	m.status = "Member note loaded. Review it before saving."
	return m, nil
}

func (m *model) layoutGroupNote() {
	if m.groups.editing != "notes" {
		return
	}

	note := &m.groups.note
	width := maxInt(m.commentWidth()-4, 1)
	height := maxInt(m.commentHeight()-4, 3)
	note.text.SetWidth(width)
	note.text.SetHeight(height)
	resized := note.preview.Width() != width
	note.preview.SetWidth(width)
	note.preview.SetHeight(height)
	if resized && note.previewing {
		offset := note.preview.YOffset()
		note.preview.SetContent(renderMarkdownWithLineBreaks(note.text.Value(), width, true))
		note.preview.SetYOffset(offset)
	}
}

func (m model) groupNoteOverlay(background string) string {
	note := m.groups.note
	label := fmt.Sprintf("%s #%d", note.member.Kind, note.member.Number)
	if g := m.selectedGroup(); g != nil {
		label += " · " + sanitize(g.Title)
	}
	content := note.text.View()
	if note.previewing {
		content = note.preview.View()
	}
	header := composerHeader("Member note", "Preview member note", label, note.previewing, m.commentWidth()-4)
	return m.composerOverlay(background, m.composerPanel(header, content))
}
