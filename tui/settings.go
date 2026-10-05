package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type settingsRow struct {
	kind, name, description, color, operation string
	id                                        int
}

type settingsEditor struct {
	row            settingsRow
	creating       bool
	field          int
	operation      string
	title          textinput.Model
	description    textarea.Model
	preview        viewport.Model
	previewing     bool
	previewHash    string
	plan           labelDefinitionPlan
	confirmDiscard bool
}

type settingsUI struct {
	open, busy       bool
	section          string
	menuSelected     int
	selected, offset int
	request          uint64
	editor           *settingsEditor
	defaults         *settingsDefaults
	automations      automationsUI
}

type settingsDefaults struct {
	plan    labelDefaultsPlan
	preview viewport.Model
}

type labelDefaultsPlan struct {
	Repository    string                `json:"repository"`
	Operation     string                `json:"operation"`
	Current       []GitHubLabel         `json:"current"`
	Create        []GitHubLabel         `json:"create"`
	Update        []labelDefinitionPlan `json:"update"`
	Delete        []GitHubLabel         `json:"delete"`
	PreviewSHA256 string                `json:"preview_sha256"`
}

type settingsDefaultsMsg struct {
	root, repo, operation string
	request               uint64
	plan                  labelDefaultsPlan
	err                   error
}

type settingsDoneMsg struct {
	root, repo      string
	request         uint64
	operation, name string
	taxonomy        Taxonomy
	err             error
}

type settingsPreviewMsg struct {
	root, repo, name, description string
	request                       uint64
	plan                          labelDefinitionPlan
	err                           error
}

type settingsEditorMsg struct {
	root, repo, kind, originalName string
	id, field                      int
	request                        uint64
	creating                       bool
	text                           string
	err                            error
}

type labelDefinitionPlan struct {
	Repository    string       `json:"repository"`
	Operation     string       `json:"operation"`
	Current       *GitHubLabel `json:"current"`
	Proposed      GitHubLabel  `json:"proposed"`
	PreviewSHA256 string       `json:"preview_sha256"`
}

func nextActionOperation(current string, delta int) string {
	for i, operation := range actionOperations {
		if operation == current {
			return actionOperations[(i+delta+len(actionOperations))%len(actionOperations)]
		}
	}
	return actionOperations[0]
}

func (m model) settingsRows() []settingsRow {
	var rows []settingsRow
	if catalog := m.taxonomy.LabelCatalog; m.settings.section == "label" && catalog.Repository == m.repo && catalog.Status == "observed" {
		for _, label := range catalog.Labels {
			rows = append(rows, settingsRow{kind: "label", id: label.ID, name: label.Name, description: label.Description, color: label.Color})
		}
	}
	if m.settings.section == "action" {
		for _, action := range m.taxonomy.Actions {
			rows = append(rows, settingsRow{kind: "action", name: action, description: m.taxonomy.ActionGuidance[action], operation: m.taxonomy.ActionOperations[action]})
		}
	}
	return rows
}

func (m *model) openSettings() {
	taxonomy, err := LoadTaxonomy(m.installRoot)
	if err != nil {
		m.failErr("Couldn't read Settings", err)
		return
	}
	m.taxonomy = taxonomy
	m.form.SetTaxonomy(taxonomy)
	m.settings = settingsUI{open: true}
	m.status = ""
}

func (m *model) settingsMove(delta int) {
	if m.settings.section == "" {
		m.settings.menuSelected = (m.settings.menuSelected + delta%3 + 3) % 3
		return
	}
	if m.settings.section == "automations" {
		count := 2 + len(m.settings.automations.actions)
		m.settings.selected = (m.settings.selected + delta%count + count) % count
		return
	}

	rows := m.settingsRows()
	if len(rows) == 0 {
		return
	}
	m.settings.selected = (m.settings.selected + delta%len(rows) + len(rows)) % len(rows)
	visible := maxInt((m.mainHeight()-4)/cardHeight, 1)
	if m.settings.selected < m.settings.offset {
		m.settings.offset = m.settings.selected
	} else if m.settings.selected >= m.settings.offset+visible {
		m.settings.offset = m.settings.selected - visible + 1
	}
}

func (m model) settingsView() string {
	w, h := m.cardWidth(), m.mainHeight()
	if m.settings.section == "" {
		cards := [][2]string{
			{"Labels", "Create and edit GitHub label names and descriptions"},
			{"Actions", "Create and edit local actions tied to GitHub operations"},
			{"Automations", "Control which agent passes may run for this repository"},
		}
		return inset(titleBar("Settings", "", m.menuWidth())) + "\n\n" + cardList(cards, m.settings.menuSelected, w, h-2)
	}
	if m.settings.section == "automations" {
		return m.automationsView()
	}

	catalog := m.taxonomy.LabelCatalog
	title, state := "Labels", "Labels pending · r reads GitHub"
	if m.settings.section == "label" && catalog.Repository == m.repo && catalog.Status == "observed" {
		state = pluralize(len(catalog.Labels), "GitHub label", "GitHub labels")
	}
	if m.settings.section == "action" {
		title, state = "Actions", pluralize(len(m.taxonomy.Actions), "local action", "local actions")
	}
	if m.settings.busy {
		state = "Working…"
	}
	view := inset(titleBar(title, state, m.menuWidth())) + "\n\n"

	rows := m.settingsRows()
	if len(rows) == 0 {
		message := "No actions configured."
		if m.settings.section == "label" {
			message = "No labels saved. Press r to read GitHub labels, i to add defaults, I to use the saved catalog, or n to create one."
		}
		return view + inset(message)
	}
	visible := maxInt((h-4)/cardHeight, 1)
	start := minInt(m.settings.offset, maxInt(len(rows)-visible, 0))
	end := minInt(start+visible, len(rows))
	for i := start; i < end; i++ {
		row := rows[i]
		summary := row.description
		if summary == "" {
			summary = "No description"
		}
		if row.kind == "action" {
			operation := m.taxonomy.OperationFor(row.name)
			if operation == "" {
				operation = "unmapped"
			} else if row.operation == "" {
				operation = "legacy " + operation
			}
			summary = operation + " · " + summary
		}
		view += markedCard(sanitize(row.name), singleLine(sanitize(summary)), cardMark{}, i == m.settings.selected, w) + "\n"
	}
	footer := fmt.Sprintf("%d of %d", m.settings.selected+1, len(rows))
	footer = inset(mutedText(ansi.Truncate(footer, m.menuWidth(), "…")))
	view = strings.TrimRight(view, "\n")
	return view + strings.Repeat("\n", maxInt(h-ansiHeight(view), 1)) + footer
}

func ansiHeight(s string) int { return strings.Count(s, "\n") + 1 }

func (m model) handleSettingsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.settings.busy {
		return m, nil
	}
	if defaults := m.settings.defaults; defaults != nil {
		switch msg.String() {
		case "esc":
			m.settings.defaults = nil
			m.status = ""
			return m, nil
		case "ctrl+s":
			m.settings.busy = true
			m.settings.request++
			m.status = "Updating GitHub labels…"
			return m, settingsDefaultsApplyCmd(m.installRoot, m.repo, m.settings.request, defaults.plan.Operation, defaults.plan.PreviewSHA256)
		case "ctrl+c":
			return m, tea.Quit
		}
		defaults.preview, _ = defaults.preview.Update(msg)
		return m, nil
	}
	if editor := m.settings.editor; editor != nil {
		switch msg.String() {
		case "ctrl+s":
			return m.saveSettingsEditor()
		case "ctrl+e":
			if editor.row.kind == "action" && editor.field == 2 {
				return m, nil
			}
			return m.startSettingsExternal()
		case "ctrl+p":
			if editor.previewing {
				editor.previewing = false
				return m, m.focusSettingsEditor()
			}
			if editor.row.kind == "label" && editor.previewHash == "" {
				return m.startSettingsLabelPreview()
			}
			m.showSettingsPreview()
			return m, nil
		case "tab", "shift+tab":
			delta := 1
			if msg.String() == "shift+tab" {
				delta = -1
			}
			editor.previewing = false
			fields := 2
			if editor.row.kind == "action" {
				fields = 3
			}
			editor.field = (editor.field + delta + fields) % fields
			return m, m.focusSettingsEditor()
		case "left", "right", "space":
			if editor.row.kind == "action" && editor.field == 2 && !editor.previewing {
				delta := 1
				if msg.String() == "left" {
					delta = -1
				}
				editor.operation = nextActionOperation(editor.operation, delta)
				return m, nil
			}
		case "esc":
			if m.settingsEditorChanged() && !editor.confirmDiscard {
				editor.confirmDiscard = true
				m.status = "Unsaved changes. Esc again discards them; Ctrl-S saves."
				return m, nil
			}
			m.settings.editor = nil
			m.status = ""
			return m, nil
		case "ctrl+c":
			return m, tea.Quit
		}
		editor.confirmDiscard = false
		beforeTitle, beforeDescription := editor.title.Value(), editor.description.Value()
		var cmd tea.Cmd
		if editor.previewing {
			editor.preview, cmd = editor.preview.Update(msg)
		} else if editor.field == 0 {
			editor.title, cmd = editor.title.Update(msg)
		} else if editor.field == 1 {
			editor.description, cmd = editor.description.Update(msg)
		}
		if editor.title.Value() != beforeTitle || editor.description.Value() != beforeDescription {
			editor.previewHash = ""
			editor.plan = labelDefinitionPlan{}
		}
		return m, cmd
	}
	if m.settings.section == "automations" {
		return m.handleAutomationsKey(msg)
	}

	switch msg.String() {
	case "esc", "h", "left":
		if m.settings.section == "" {
			m.settings.open = false
		} else {
			m.settings.section = ""
			m.settings.selected, m.settings.offset = 0, 0
		}
		return m, nil
	case "q":
		return m.requestQuit()
	case "?":
		m.showHelp = !m.showHelp
	case "j", "down", "tab":
		m.settingsMove(1)
	case "k", "up", "shift+tab":
		m.settingsMove(-1)
	case "g", "home":
		if m.settings.section == "" {
			m.settings.menuSelected = 0
		} else {
			m.settings.selected, m.settings.offset = 0, 0
		}
	case "G", "end":
		if m.settings.section == "" {
			m.settings.menuSelected = 2
		} else {
			m.settings.selected = maxInt(len(m.settingsRows())-1, 0)
			m.settingsMove(0)
		}
	case "r":
		if m.settings.section != "label" {
			return m, nil
		}
		m.settings.busy = true
		m.settings.request++
		m.status = "Reading GitHub labels…"
		return m, settingsSyncCmd(m.installRoot, m.repo, m.settings.request)
	case "i":
		if m.settings.section != "label" {
			return m, nil
		}
		m.settings.busy = true
		m.settings.request++
		m.status = "Checking starter labels on GitHub…"
		return m, settingsDefaultsPreviewCmd(m.installRoot, m.repo, m.settings.request, "initialize-defaults")
	case "I":
		if m.settings.section != "label" {
			return m, nil
		}
		m.settings.busy = true
		m.settings.request++
		m.status = "Comparing GitHub labels with the saved local catalog…"
		return m, settingsDefaultsPreviewCmd(m.installRoot, m.repo, m.settings.request, "reconcile-local")
	case "n":
		if m.settings.section != "" {
			return m.openSettingsEditor(settingsRow{kind: m.settings.section}, true)
		}
	case "enter", "l", "right":
		if m.settings.section == "" {
			m.settings.section = []string{"label", "action", "automations"}[m.settings.menuSelected]
			m.settings.selected, m.settings.offset = 0, 0
			if m.settings.section == "automations" {
				m.settings.automations = automationsUI{}
				m.settings.busy = true
				m.settings.request++
				return m, automationStatusCmd(m.installRoot, m.repo, m.settings.request)
			}
			return m, nil
		}
		return m.openSelectedSettingsEditor()
	case "e":
		if m.settings.section == "" {
			return m, nil
		}
		return m.openSelectedSettingsEditor()
	}
	return m, nil
}

func (m model) openSelectedSettingsEditor() (tea.Model, tea.Cmd) {
	rows := m.settingsRows()
	if len(rows) == 0 {
		return m, nil
	}
	return m.openSettingsEditor(rows[m.settings.selected], false)
}

func (m model) openSettingsEditor(row settingsRow, creating bool) (tea.Model, tea.Cmd) {
	title := textinput.New()
	title.Prompt = ""
	title.CharLimit = 255
	title.SetValue(row.name)
	themeInput(&title)

	description := textarea.New()
	description.Placeholder = "Short description"
	description.CharLimit = 4000
	if row.kind == "label" {
		description.CharLimit = 100
	}
	description.ShowLineNumbers = false
	description.SetValue(row.description)
	themeTextarea(&description)

	operation := row.operation
	if creating && row.kind == "action" {
		operation = actionOperations[0]
	}
	m.settings.editor = &settingsEditor{row: row, creating: creating, title: title, description: description, operation: operation, preview: viewport.New()}
	m.status = ""
	m.layoutSettingsEditor()
	return m, m.settings.editor.title.Focus()
}

func (m *model) focusSettingsEditor() tea.Cmd {
	e := m.settings.editor
	e.title.Blur()
	e.description.Blur()
	if e.previewing {
		return nil
	}
	if e.field == 0 {
		return e.title.Focus()
	}
	return e.description.Focus()
}

func (m model) settingsEditorChanged() bool {
	e := m.settings.editor
	if e == nil {
		return false
	}
	if e.creating {
		return e.title.Value() != "" || e.description.Value() != ""
	}

	return e.title.Value() != e.row.name || e.description.Value() != e.row.description || e.operation != e.row.operation
}

func (m *model) layoutSettingsEditor() {
	e := m.settings.editor
	if e == nil {
		return
	}
	width := maxInt(m.commentWidth()-4, 1)
	e.title.SetWidth(width)
	e.description.SetWidth(width)
	padding := 8
	if e.row.kind == "action" {
		padding += 3
	}
	e.description.SetHeight(maxInt(m.commentHeight()-padding, 3))
	resized := e.preview.Width() != width
	e.preview.SetWidth(width)
	e.preview.SetHeight(maxInt(m.commentHeight()-4, 3))
	if resized && e.previewing {
		offset := e.preview.YOffset()
		e.preview.SetContent(m.settingsDraftPreview(width))
		e.preview.SetYOffset(offset)
	}
}

func (m model) settingsDraftPreview(width int) string {
	e := m.settings.editor
	heading := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Accent)).Bold(true)
	field := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Accent))
	bodyWidth := maxInt(width-2, 1)
	lines := []string{}

	currentName, currentDescription := e.row.name, e.row.description
	if e.row.kind == "label" && e.plan.Current != nil {
		currentName, currentDescription = e.plan.Current.Name, e.plan.Current.Description
	}
	lines = append(lines, heading.Render("Current"))
	if e.creating {
		lines = append(lines, "No existing "+e.row.kind)
	} else {
		lines = append(lines, field.Render("Title"), wrapText(currentName, bodyWidth), "",
			field.Render("Description"), wrapText(settingsDescription(currentDescription), bodyWidth))
		if e.row.kind == "action" {
			currentOperation := e.row.operation
			if currentOperation == "" {
				currentOperation = "Unmapped"
			}
			lines = append(lines, "", field.Render("GitHub operation"), currentOperation)
		}
	}
	lines = append(lines, "", heading.Render("After save"), field.Render("Title"), wrapText(e.title.Value(), bodyWidth), "",
		field.Render("Description"), wrapText(settingsDescription(e.description.Value()), bodyWidth))
	if e.row.kind == "label" {
		lines = append(lines, "", field.Render("Color"), e.plan.Proposed.Color)
		return heading.Render("GitHub repository") + "\n" + wrapText(e.plan.Repository, width) + "\n\n" + inset(inset(strings.Join(lines, "\n")))
	}
	lines = append(lines, "", field.Render("GitHub operation"), e.operation)

	return inset(inset(strings.Join(lines, "\n")))
}

func settingsDescription(value string) string {
	if value == "" {
		return "No description"
	}
	return value
}

func (m *model) showSettingsPreview() {
	e := m.settings.editor
	e.previewing = true
	e.title.Blur()
	e.description.Blur()
	m.layoutSettingsEditor()
	e.preview.SetContent(m.settingsDraftPreview(e.preview.Width()))
	e.preview.GotoTop()
}

func (m model) settingsOverlay(background string) string {
	e := m.settings.editor
	mode := "Edit"
	if e.creating {
		mode = "New"
	}
	kind := "label"
	if e.row.kind == "action" {
		kind = "action"
	}
	header := composerHeader(mode+" "+kind, "Preview "+kind, e.row.name, e.previewing, m.commentWidth()-4)
	if e.previewing {
		return m.composerOverlay(background, m.composerPanel(header, e.preview.View()))
	}
	label := func(field int, name string) string {
		if e.field == field {
			return lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Accent)).Bold(true).Render("  " + name)
		}
		return mutedText("  " + name)
	}
	parts := []string{label(0, "Title"), e.title.View(), label(1, "Description"), e.description.View()}
	if e.row.kind == "action" {
		operation := e.operation
		if operation == "" {
			operation = "Choose with ← / →"
		}
		parts = append(parts, label(2, "GitHub operation"), "  "+operation+"  ← / → to change")
	}
	content := strings.Join(parts, "\n")
	return m.composerOverlay(background, m.composerPanel(header, content))
}

func (m model) saveSettingsEditor() (tea.Model, tea.Cmd) {
	e := m.settings.editor
	if problem := m.settingsEditorProblem(); problem != "" {
		m.fail(problem)
		return m, nil
	}
	if !e.creating && !m.settingsEditorChanged() {
		m.settings.editor = nil
		m.status = "No changes to save."
		return m, nil
	}

	if e.row.kind == "label" {
		if e.previewHash == "" {
			return m.startSettingsLabelPreview()
		}
		m.settings.busy = true
		m.settings.request++
		m.status = "Saving GitHub label…"
		return m, settingsLabelApplyCmd(m.installRoot, m.repo, m.settings.request, e.title.Value(), settingsLabelArgs(m.repo, *e), e.previewHash)
	}
	m.settings.busy = true
	m.settings.request++
	m.status = "Saving action…"
	return m, settingsActionSaveCmd(m.installRoot, m.repo, m.settings.request, *e)
}

func (m model) settingsEditorProblem() string {
	e := m.settings.editor
	name, description := e.title.Value(), e.description.Value()
	if strings.TrimSpace(name) == "" || name != strings.TrimSpace(name) || strings.ContainsAny(name, "\n\x00") {
		return "Title must be a nonempty single line without leading or trailing spaces."
	}
	if e.row.kind == "label" && (utf8.RuneCountInString(description) > 100 || strings.ContainsAny(description, "\n\x00")) {
		return "GitHub label descriptions must be one line and at most 100 characters."
	}
	if e.row.kind == "action" && !slices.Contains(actionOperations, e.operation) {
		return "Choose a GitHub operation for this action."
	}
	return ""
}

func (m model) startSettingsLabelPreview() (tea.Model, tea.Cmd) {
	if problem := m.settingsEditorProblem(); problem != "" {
		m.fail(problem)
		return m, nil
	}
	e := m.settings.editor
	m.settings.busy = true
	m.settings.request++
	m.status = "Checking GitHub label change…"
	return m, settingsLabelPreviewCmd(m.installRoot, m.repo, m.settings.request, e.title.Value(), e.description.Value(), settingsLabelArgs(m.repo, *e))
}

func settingsActionSaveCmd(root, repo string, request uint64, e settingsEditor) tea.Cmd {
	return func() tea.Msg {
		name := e.title.Value()
		args := []string{"create-action", "--expected-repo", repo, "--name", name, "--description", e.description.Value(), "--operation", e.operation}
		if !e.creating {
			args = append(args, "--action", e.row.name, "--expected", e.row.description, "--expected-operation", e.row.operation)
			args[0] = "update-action"
		}
		_, err := runScript(root, "taxonomy-settings", args...)
		if err != nil {
			return settingsDoneMsg{root: root, repo: repo, request: request, operation: "save", err: err}
		}
		taxonomy, err := LoadTaxonomy(root)
		return settingsDoneMsg{root: root, repo: repo, request: request, operation: "save", name: name, taxonomy: taxonomy, err: err}
	}
}

func (m model) startSettingsExternal() (tea.Model, tea.Cmd) {
	e := m.settings.editor
	value := e.description.Value()
	limit := e.description.CharLimit
	if e.field == 0 {
		value = e.title.Value()
		limit = e.title.CharLimit
	}
	if e.field == 0 || e.row.kind == "label" {
		limit++
	}
	command, path, err := prepareCommentEditor(value)
	if err != nil {
		m.failErr("Couldn't open Settings field editor", err)
		return m, nil
	}

	m.settings.busy = true
	m.settings.request++
	m.status = "Editing Settings field in $EDITOR…"
	root, repo, request := m.installRoot, m.repo, m.settings.request
	kind, originalName, id, field, creating := e.row.kind, e.row.name, e.row.id, e.field, e.creating
	return m, tea.ExecProcess(command, func(err error) tea.Msg {
		value, readErr := readCommentEditor(path, err, limit)
		return settingsEditorMsg{root: root, repo: repo, kind: kind, originalName: originalName, id: id,
			field: field, request: request, creating: creating, text: value, err: readErr}
	})
}

func (m model) finishSettingsExternal(msg settingsEditorMsg) (tea.Model, tea.Cmd) {
	e := m.settings.editor
	if !m.settings.open || e == nil || msg.root != m.installRoot || msg.repo != m.repo || msg.request != m.settings.request ||
		msg.kind != e.row.kind || msg.originalName != e.row.name || msg.id != e.row.id || msg.field != e.field || msg.creating != e.creating {
		return m, nil
	}
	m.settings.busy = false
	if msg.err != nil {
		m.failErr("Couldn't load edited Settings field; draft retained", msg.err)
		return m, nil
	}

	value := msg.text
	if msg.field == 0 || e.row.kind == "label" {
		value = strings.TrimSuffix(value, "\n")
		if strings.Contains(value, "\n") {
			m.fail("Title and GitHub label description must stay on one line; draft retained.")
			return m, nil
		}
	}
	limit := e.description.CharLimit
	if msg.field == 0 {
		limit = e.title.CharLimit
	}
	if utf8.RuneCountInString(value) > limit {
		m.fail("Edited Settings field exceeds its length limit; draft retained.")
		return m, nil
	}
	if msg.field == 0 {
		e.title.SetValue(value)
	} else {
		e.description.SetValue(value)
	}
	e.previewHash = ""
	e.plan = labelDefinitionPlan{}
	if e.row.kind == "label" {
		return m.startSettingsLabelPreview()
	}
	m.showSettingsPreview()
	m.status = "Settings field loaded. Review it before saving."
	return m, nil
}

func settingsLabelArgs(repo string, e settingsEditor) []string {
	args := []string{"--expected-repo", repo, "--name", e.title.Value(), "--description", e.description.Value()}
	if !e.creating {
		args = append(args, "--label-id", strconv.Itoa(e.row.id), "--expected-name", e.row.name, "--expected-description", e.row.description)
	}
	return args
}

func settingsLabelPreviewCmd(root, repo string, request uint64, name, description string, args []string) tea.Cmd {
	return func() tea.Msg {
		out, err := runScript(root, "label-definitions", args...)
		var plan labelDefinitionPlan
		if err == nil {
			err = json.Unmarshal([]byte(out), &plan)
		}
		return settingsPreviewMsg{root: root, repo: repo, request: request, name: name, description: description, plan: plan, err: err}
	}
}

func settingsLabelApplyCmd(root, repo string, request uint64, name string, args []string, hash string) tea.Cmd {
	return func() tea.Msg {
		_, err := runScript(root, "label-definitions", append(args, "--apply", "--preview-sha256", hash)...)
		if err != nil {
			return settingsDoneMsg{root: root, repo: repo, request: request, operation: "save", err: err}
		}
		taxonomy, err := LoadTaxonomy(root)
		return settingsDoneMsg{root: root, repo: repo, request: request, operation: "save", name: name, taxonomy: taxonomy, err: err}
	}
}

func settingsSyncCmd(root, repo string, request uint64) tea.Cmd {
	return func() tea.Msg {
		_, err := runScript(root, "label-catalog", "sync", "--expected-repo", repo)
		if err != nil {
			return settingsDoneMsg{root: root, repo: repo, request: request, operation: "sync", err: err}
		}
		taxonomy, err := LoadTaxonomy(root)
		return settingsDoneMsg{root: root, repo: repo, request: request, operation: "sync", taxonomy: taxonomy, err: err}
	}
}

func settingsDefaultsPreviewCmd(root, repo string, request uint64, operation string) tea.Cmd {
	return func() tea.Msg {
		flag := "--initialize-defaults"
		if operation == "reconcile-local" {
			flag = "--reconcile-local"
		}
		out, err := runScript(root, "label-definitions", "--expected-repo", repo, flag)
		var plan labelDefaultsPlan
		if err == nil {
			err = json.Unmarshal([]byte(out), &plan)
		}
		return settingsDefaultsMsg{root: root, repo: repo, operation: operation, request: request, plan: plan, err: err}
	}
}

func settingsDefaultsApplyCmd(root, repo string, request uint64, operation, hash string) tea.Cmd {
	return func() tea.Msg {
		flag, doneOperation := "--initialize-defaults", "initialize"
		if operation == "reconcile-local" {
			flag, doneOperation = "--reconcile-local", "reconcile"
		}
		_, err := runScript(root, "label-definitions", "--expected-repo", repo, flag, "--apply", "--preview-sha256", hash)
		taxonomy, loadErr := LoadTaxonomy(root)
		if err == nil {
			err = loadErr
		}
		return settingsDoneMsg{root: root, repo: repo, request: request, operation: doneOperation, taxonomy: taxonomy, err: err}
	}
}

func (m model) finishSettingsDefaults(msg settingsDefaultsMsg) (tea.Model, tea.Cmd) {
	if !m.settings.open || m.settings.section != "label" || m.settings.editor != nil || msg.root != m.installRoot || msg.repo != m.repo || msg.request != m.settings.request {
		return m, nil
	}
	m.settings.busy = false
	if msg.err != nil {
		m.failErr("Couldn't preview starter labels", msg.err)
		return m, nil
	}
	if msg.plan.Repository != m.repo || msg.plan.Operation != msg.operation || msg.plan.PreviewSHA256 == "" {
		m.fail("Starter label preview did not match this repository; try again.")
		return m, nil
	}
	preview := viewport.New()
	m.settings.defaults = &settingsDefaults{plan: msg.plan, preview: preview}
	m.layoutSettingsDefaults()
	m.settings.defaults.preview.SetContent(m.settingsDefaultsContent(m.settings.defaults.preview.Width()))
	m.status = "Review the exact GitHub label changes, then Ctrl-S to confirm."
	return m, nil
}

func (m model) settingsDefaultsContent(width int) string {
	plan := m.settings.defaults.plan
	heading := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Accent)).Bold(true)
	field := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Accent))
	bodyWidth := maxInt(width-2, 1)
	lines := []string{heading.Render("Current"), "Existing labels stay unchanged.", "", heading.Render("After save")}
	if plan.Operation == "reconcile-local" {
		lines = []string{heading.Render("Current"), "The saved local catalog is authoritative.", "Deleting a GitHub label removes it from issues and PRs.", "", heading.Render("After save")}
		appendLabels := func(title string, labels []GitHubLabel) {
			if len(labels) == 0 {
				return
			}
			lines = append(lines, "", heading.Render(title))
			for _, label := range labels {
				lines = append(lines, "", field.Render("Title"), wrapText(label.Name, bodyWidth), "", field.Render("Description"), wrapText(settingsDescription(label.Description), bodyWidth), "", field.Render("Color"), label.Color)
			}
		}
		appendLabels("Create", plan.Create)
		for _, change := range plan.Update {
			if change.Current == nil {
				continue
			}
			lines = append(lines, "", heading.Render("Edit "+change.Current.Name), field.Render("Current"), wrapText(change.Current.Name+" · "+settingsDescription(change.Current.Description)+" · "+change.Current.Color, bodyWidth), "", field.Render("After save"), wrapText(change.Proposed.Name+" · "+settingsDescription(change.Proposed.Description)+" · "+change.Proposed.Color, bodyWidth))
		}
		appendLabels("Delete from GitHub", plan.Delete)
		if len(plan.Create)+len(plan.Update)+len(plan.Delete) == 0 {
			lines = append(lines, "No GitHub label changes are needed.")
		}
		return heading.Render("GitHub repository") + "\n" + wrapText(plan.Repository, width) + "\n\n" + inset(inset(strings.Join(lines, "\n")))
	}
	if len(plan.Create) == 0 {
		lines = append(lines, "All starter labels already exist. No GitHub write is needed.")
	} else {
		for i, label := range plan.Create {
			if i > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, field.Render("Title"), wrapText(label.Name, bodyWidth), "",
				field.Render("Description"), wrapText(settingsDescription(label.Description), bodyWidth), "",
				field.Render("Color"), label.Color)
		}
	}
	return heading.Render("GitHub repository") + "\n" + wrapText(plan.Repository, width) + "\n\n" + inset(inset(strings.Join(lines, "\n")))
}

func (m *model) layoutSettingsDefaults() {
	if m.settings.defaults == nil {
		return
	}
	preview := &m.settings.defaults.preview
	width := maxInt(m.commentWidth()-4, 1)
	resized := preview.Width() != width
	preview.SetWidth(width)
	preview.SetHeight(maxInt(m.commentHeight()-4, 3))
	if resized {
		offset := preview.YOffset()
		preview.SetContent(m.settingsDefaultsContent(width))
		preview.SetYOffset(offset)
	}
}

func (m model) settingsDefaultsOverlay(background string) string {
	title := "Preview starter labels"
	if m.settings.defaults.plan.Operation == "reconcile-local" {
		title = "Preview local label catalog"
	}
	header := composerHeader("Initialize defaults", title, m.repo, true, m.commentWidth()-4)
	return m.composerOverlay(background, m.composerPanel(header, m.settings.defaults.preview.View()))
}

func (m model) finishSettingsPreview(msg settingsPreviewMsg) (tea.Model, tea.Cmd) {
	e := m.settings.editor
	if !m.settings.open || e == nil || msg.root != m.installRoot || msg.repo != m.repo || msg.request != m.settings.request ||
		msg.name != e.title.Value() || msg.description != e.description.Value() {
		return m, nil
	}
	m.settings.busy = false
	if msg.err != nil {
		m.failErr("Couldn't preview GitHub label change", msg.err)
		return m, nil
	}
	if msg.plan.Repository != m.repo || msg.plan.PreviewSHA256 == "" || msg.plan.Proposed.Name != msg.name || msg.plan.Proposed.Description != msg.description {
		m.fail("GitHub label preview did not match this editor; try again.")
		return m, nil
	}
	e.plan = msg.plan
	e.previewHash = msg.plan.PreviewSHA256
	m.showSettingsPreview()
	m.status = "Review the exact GitHub label change, then Ctrl-S to confirm."
	return m, nil
}

func (m model) finishSettings(msg settingsDoneMsg) (tea.Model, tea.Cmd) {
	if !m.settings.open || msg.root != m.installRoot || msg.repo != m.repo || msg.request != m.settings.request {
		return m, nil
	}
	m.settings.busy = false
	if msg.err != nil {
		if msg.operation == "initialize" || msg.operation == "reconcile" {
			m.settings.defaults = nil
			if msg.taxonomy.LabelCatalog.Repository == m.repo {
				m.taxonomy = msg.taxonomy
				m.form.SetTaxonomy(msg.taxonomy)
			}
		}
		if m.settings.editor != nil && m.settings.editor.row.kind == "label" {
			m.settings.editor.previewHash = ""
			m.settings.editor.previewing = false
			m.focusSettingsEditor()
		}
		m.failErr("Couldn't update Settings", msg.err)
		return m, nil
	}
	m.taxonomy = msg.taxonomy
	m.form.SetTaxonomy(msg.taxonomy)
	if msg.name != "" {
		for i, row := range m.settingsRows() {
			if row.name == msg.name {
				m.settings.selected, m.settings.offset = i, 0
				m.settingsMove(0)
				break
			}
		}
	}
	if rows := m.settingsRows(); m.settings.selected >= len(rows) {
		m.settings.selected = maxInt(len(rows)-1, 0)
		m.settings.offset = 0
	}
	if msg.operation == "reconcile" {
		m.settings.defaults = nil
		m.status = "GitHub labels now match the saved local catalog."
	} else if msg.operation == "initialize" {
		m.settings.defaults = nil
		m.status = "Starter labels initialized; GitHub labels refreshed."
	} else if msg.operation == "save" {
		m.settings.editor = nil
		m.status = "Setting saved."
	} else {
		m.status = "GitHub labels refreshed."
	}
	return m, nil
}
