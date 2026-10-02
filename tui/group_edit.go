package main

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type groupEditEditor struct {
	groupID, originalTitle, originalDescription, originalStatus, originalAssignee string
	revision, field                                                               int
	request                                                                       uint64
	previewing                                                                    bool
	title, assignee                                                               textinput.Model
	description                                                                   textarea.Model
	status                                                                        string
	preview                                                                       viewport.Model
}

type groupEditEditorMsg struct {
	root, repo, groupID string
	revision, field     int
	request             uint64
	text                string
	err                 error
}

func (m model) openGroupEdit() (tea.Model, tea.Cmd) {
	g := m.selectedGroup()
	if g == nil {
		return m, nil
	}

	title := textinput.New()
	title.Prompt = ""
	title.CharLimit = 0
	title.SetValue(g.Title)
	themeInput(&title)

	description := textarea.New()
	description.Placeholder = "What decision should the maintainer make?"
	description.CharLimit = maxInt(65536, utf8.RuneCountInString(g.Description)+1024)
	description.ShowLineNumbers = false
	description.SetValue(g.Description)
	themeTextarea(&description)

	assignee := textinput.New()
	assignee.Prompt = ""
	assignee.CharLimit = 0
	assignee.SetValue(g.Assignee)
	themeInput(&assignee)

	m.groups.editRequest++
	m.groups.edit = groupEditEditor{groupID: g.ID, revision: g.Revision, originalTitle: g.Title,
		originalDescription: g.Description, originalStatus: g.Status, originalAssignee: g.Assignee,
		request: m.groups.editRequest, title: title, description: description, status: g.Status,
		assignee: assignee, preview: viewport.New()}
	m.groups.editing = "edit"
	m.lastGroupID = g.ID
	m.status = ""
	m.layoutGroupEdit()

	return m, m.groups.edit.title.Focus()
}

func (m *model) focusGroupEditField(delta int) tea.Cmd {
	e := &m.groups.edit
	e.title.Blur()
	e.description.Blur()
	e.assignee.Blur()
	e.field = (e.field + delta + 4) % 4
	if e.previewing {
		return nil
	}

	switch e.field {
	case 0:
		return e.title.Focus()
	case 1:
		return e.description.Focus()
	case 3:
		return e.assignee.Focus()
	}
	return nil
}

func (m *model) cycleGroupEditStatus(delta int) {
	e := &m.groups.edit
	for i, status := range groupStatuses {
		if status == e.status {
			e.status = groupStatuses[(i+delta+len(groupStatuses))%len(groupStatuses)]
			return
		}
	}
	e.status = groupStatuses[0]
}

func (m model) handleGroupEditKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	e := &m.groups.edit
	switch msg.String() {
	case "esc":
		m.groups.editing = ""
		m.groups.edit = groupEditEditor{}
		return m, nil
	case "tab":
		e.previewing = false
		return m, m.focusGroupEditField(1)
	case "shift+tab":
		e.previewing = false
		return m, m.focusGroupEditField(-1)
	case "ctrl+e":
		return m.startGroupEditExternal()
	case "ctrl+p":
		e.previewing = !e.previewing
		if e.previewing {
			e.title.Blur()
			e.description.Blur()
			e.assignee.Blur()
			m.layoutGroupEdit()
			e.preview.GotoTop()
			return m, nil
		}
		return m, m.focusGroupEditField(0)
	case "ctrl+s":
		return m.saveGroupEdit()
	}

	if e.previewing {
		var cmd tea.Cmd
		e.preview, cmd = e.preview.Update(msg)
		return m, cmd
	}

	if e.field == groupStatusField {
		switch msg.String() {
		case "j", "down", "l", "right":
			m.cycleGroupEditStatus(1)
		case "k", "up", "h", "left":
			m.cycleGroupEditStatus(-1)
		}
		return m, nil
	}

	var cmd tea.Cmd
	switch e.field {
	case 0:
		e.title, cmd = e.title.Update(msg)
	case 1:
		e.description, cmd = e.description.Update(msg)
	case 3:
		e.assignee, cmd = e.assignee.Update(msg)
	}
	return m, cmd
}

func (m model) pasteGroupEdit(msg tea.PasteMsg) (tea.Model, tea.Cmd) {
	e := &m.groups.edit
	if e.previewing || e.field == groupStatusField {
		return m, nil
	}

	var cmd tea.Cmd
	switch e.field {
	case 0:
		e.title, cmd = e.title.Update(msg)
	case 1:
		e.description, cmd = e.description.Update(msg)
	case 3:
		e.assignee, cmd = e.assignee.Update(msg)
	}
	return m, cmd
}

func (m model) saveGroupEdit() (tea.Model, tea.Cmd) {
	e := m.groups.edit
	g := m.selectedGroup()
	if g == nil || g.ID != e.groupID || g.Revision != e.revision {
		m.fail("Group changed; reopen its editor before saving.")
		return m, nil
	}

	if e.title.Value() == e.originalTitle && e.description.Value() == e.originalDescription && e.status == e.originalStatus && e.assignee.Value() == e.originalAssignee {
		m.groups.editing = ""
		m.groups.edit = groupEditEditor{}
		m.status = "Group is unchanged."
		return m, nil
	}

	args := []string{"update", e.groupID, "--revision", strconv.Itoa(e.revision), "--title", e.title.Value(),
		"--description", e.description.Value(), "--status", e.status, "--assignee", e.assignee.Value(), "--by", m.reviewer}
	m.groups.busy = true
	m.status = "Saving group…"
	return m, groupsCmd(m.installRoot, args...)
}

func (m model) startGroupEditExternal() (tea.Model, tea.Cmd) {
	e := &m.groups.edit
	if e.field == groupStatusField {
		return m, nil
	}

	value := e.description.Value()
	if e.field == 0 {
		value = e.title.Value()
	} else if e.field == 3 {
		value = e.assignee.Value()
	}
	command, path, err := prepareCommentEditor(value)
	if err != nil {
		m.failErr("Couldn't open group field editor", err)
		return m, nil
	}

	root, repo, groupID, revision, field, request := m.installRoot, m.repo, e.groupID, e.revision, e.field, e.request
	charLimit := maxInt(65536, utf8.RuneCountInString(value)+1024)
	m.groups.busy = true
	m.status = "Editing group field in $EDITOR…"
	return m, tea.ExecProcess(command, func(err error) tea.Msg {
		text, readErr := readCommentEditor(path, err, charLimit)
		return groupEditEditorMsg{root: root, repo: repo, groupID: groupID, revision: revision,
			field: field, request: request, text: text, err: readErr}
	})
}

func (m model) finishGroupEditExternal(msg groupEditEditorMsg) (tea.Model, tea.Cmd) {
	e := &m.groups.edit
	if m.groups.editing != "edit" || msg.root != m.installRoot || msg.repo != m.repo || msg.groupID != e.groupID ||
		msg.revision != e.revision || msg.field != e.field || msg.request != e.request {
		return m, nil
	}

	m.groups.busy = false
	if msg.err != nil {
		m.failErr("Couldn't load edited group field; draft retained", msg.err)
		return m, nil
	}

	value := msg.text
	if msg.field != 1 {
		value = strings.TrimSuffix(value, "\n")
		if strings.Contains(value, "\n") {
			m.fail("Title and assignee must stay on one line; current draft retained.")
			return m, nil
		}
	}
	switch msg.field {
	case 0:
		e.title.SetValue(value)
	case 1:
		e.description.SetValue(value)
	case 3:
		e.assignee.SetValue(value)
	}
	e.previewing = true
	m.layoutGroupEdit()
	e.preview.GotoTop()
	m.status = "Group field loaded. Review it before saving."
	return m, nil
}

func (m *model) layoutGroupEdit() {
	if m.groups.editing != "edit" {
		return
	}

	e := &m.groups.edit
	width := maxInt(m.commentWidth()-4, 1)
	e.title.SetWidth(width)
	e.assignee.SetWidth(width)
	e.description.SetWidth(width)
	e.description.SetHeight(maxInt(m.commentHeight()-11, 3))
	e.preview.SetWidth(width)
	e.preview.SetHeight(maxInt(m.commentHeight()-4, 3))
	if e.previewing {
		offset := e.preview.YOffset()
		e.preview.SetContent(m.groupEditPreviewText(width))
		e.preview.SetYOffset(offset)
	}
}

func (m model) groupEditPreviewText(width int) string {
	e := m.groups.edit
	return strings.Join([]string{
		"Title", wrapText(e.title.Value(), width), "",
		"Description", renderMarkdownWithLineBreaks(e.description.Value(), width, true), "",
		"Status", e.status, "",
		"Assignee", wrapText(orPlaceholder(e.assignee.Value(), "(none)"), width),
	}, "\n")
}

func (m model) groupEditOverlay(background string) string {
	e := m.groups.edit
	width := maxInt(m.commentWidth()-4, 1)
	header := composerHeader("Edit group", "Preview group", e.originalTitle, e.previewing, width)
	if e.previewing {
		return m.composerOverlay(background, m.composerPanel(header, e.preview.View()))
	}

	label := func(index int, name string) string {
		if e.field == index {
			return lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Accent)).Bold(true).Render("  " + name)
		}
		return mutedText("  " + name)
	}
	status := e.status
	if e.field == groupStatusField {
		status = lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Accent)).Bold(true).Render(status + "  ‹ / › to change")
	}
	content := strings.Join([]string{
		label(0, "Title"), e.title.View(),
		label(1, "Description"), e.description.View(),
		label(2, "Status"), status,
		label(3, "Assignee"), e.assignee.View(),
	}, "\n")
	return m.composerOverlay(background, m.composerPanel(header, content))
}
