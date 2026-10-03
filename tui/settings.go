package main

import (
	"fmt"
	"strconv"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

type settingsRow struct {
	kind, name, description, guidance string
	id                                int
}

type settingsEditor struct {
	row            settingsRow
	original       string
	text           textarea.Model
	confirmDiscard bool
}

type settingsUI struct {
	open, busy       bool
	selected, offset int
	request          uint64
	editor           *settingsEditor
}

type settingsDoneMsg struct {
	root, repo string
	request    uint64
	operation  string
	taxonomy   Taxonomy
	err        error
}

func (m model) settingsRows() []settingsRow {
	var rows []settingsRow
	if catalog := m.taxonomy.LabelCatalog; catalog.Repository == m.repo && catalog.Status == "observed" {
		for _, label := range catalog.Labels {
			rows = append(rows, settingsRow{kind: "label", id: label.ID, name: label.Name, description: label.Description, guidance: label.Guidance})
		}
	}
	for _, action := range m.taxonomy.Actions {
		rows = append(rows, settingsRow{kind: "action", name: action, guidance: m.taxonomy.ActionGuidance[action]})
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
	m.form.taxonomy = taxonomy
	m.settings = settingsUI{open: true}
	m.status = ""
}

func (m *model) settingsMove(delta int) {
	rows := m.settingsRows()
	if len(rows) == 0 {
		return
	}
	m.settings.selected = (m.settings.selected + delta + len(rows)) % len(rows)
	visible := maxInt((m.mainHeight()-10)/cardHeight, 1)
	if m.settings.selected < m.settings.offset {
		m.settings.offset = m.settings.selected
	} else if m.settings.selected >= m.settings.offset+visible {
		m.settings.offset = m.settings.selected - visible + 1
	}
}

func (m model) settingsView() string {
	w := m.cardWidth()
	catalog := m.taxonomy.LabelCatalog
	state := "Labels pending · r reads GitHub"
	if catalog.Repository == m.repo && catalog.Status == "observed" {
		state = fmt.Sprintf("%d GitHub labels · %d local actions", len(catalog.Labels), len(m.taxonomy.Actions))
	}
	if m.settings.busy {
		state = "Working…"
	}
	view := titleBar("Settings", m.repo, w) + "\n" + mutedText(state) + "\n"

	if editor := m.settings.editor; editor != nil {
		target := sanitize(editor.row.kind + ": " + editor.row.name)
		view += "\n" + ansi.Truncate(target, w, "…") + "\n"
		if editor.row.description != "" {
			view += ansi.Wrap(sanitize(editor.row.description), w, "") + "\n"
		}
		view += "\nLocal guidance\n" + editor.text.View()
		return view
	}

	rows := m.settingsRows()
	if len(rows) == 0 {
		return view + "\nNo entries yet. Press r to read the selected repository's labels."
	}
	visible := maxInt((m.mainHeight()-10)/cardHeight, 1)
	start := minInt(m.settings.offset, maxInt(len(rows)-visible, 0))
	end := minInt(start+visible, len(rows))
	for i := start; i < end; i++ {
		row := rows[i]
		summary := row.description
		if row.guidance != "" {
			summary = "Local: " + row.guidance
		}
		view += markedCard(sanitize(row.kind+": "+row.name), singleLine(sanitize(summary)), cardMark{}, i == m.settings.selected, w) + "\n"
	}
	selected := rows[m.settings.selected]
	view += fmt.Sprintf("%d/%d · %s guidance: ", m.settings.selected+1, len(rows), selected.kind)
	if selected.guidance == "" {
		view += "none"
	} else {
		view += ansi.Wrap(sanitize(selected.guidance), w, "")
	}
	return view
}

func (m model) handleSettingsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.settings.busy {
		return m, nil
	}
	if editor := m.settings.editor; editor != nil {
		switch msg.String() {
		case "ctrl+s":
			value := editor.text.Value()
			if value == editor.original {
				m.settings.editor = nil
				m.status = "Guidance unchanged."
				return m, nil
			}
			m.settings.busy = true
			m.settings.request++
			m.status = "Saving local guidance…"
			return m, settingsSaveCmd(m.installRoot, m.repo, m.settings.request, editor.row, editor.original, value)
		case "esc":
			if editor.text.Value() != editor.original && !editor.confirmDiscard {
				editor.confirmDiscard = true
				m.status = "Unsaved guidance. Esc again discards it; Ctrl-S saves."
				return m, nil
			}
			m.settings.editor = nil
			m.status = ""
			return m, nil
		case "ctrl+c":
			return m, tea.Quit
		}
		editor.confirmDiscard = false
		var cmd tea.Cmd
		editor.text, cmd = editor.text.Update(msg)
		return m, cmd
	}

	switch msg.String() {
	case "esc", "h", "left":
		m.settings.open = false
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
		m.settings.selected, m.settings.offset = 0, 0
	case "G", "end":
		m.settings.selected = maxInt(len(m.settingsRows())-1, 0)
		m.settingsMove(0)
	case "r":
		m.settings.busy = true
		m.settings.request++
		m.status = "Reading GitHub labels…"
		return m, settingsSyncCmd(m.installRoot, m.repo, m.settings.request)
	case "enter", "l", "right", "e":
		rows := m.settingsRows()
		if len(rows) == 0 {
			return m, nil
		}
		row := rows[m.settings.selected]
		text := textarea.New()
		text.Placeholder = "Optional local guidance"
		text.CharLimit = 4000
		text.ShowLineNumbers = false
		themeTextarea(&text)
		text.SetWidth(maxInt(m.cardWidth()-2, 1))
		text.SetHeight(maxInt(minInt(m.mainHeight()-8, 8), 3))
		text.SetValue(row.guidance)
		m.settings.editor = &settingsEditor{row: row, original: row.guidance, text: text}
		return m, text.Focus()
	}
	return m, nil
}

func settingsSaveCmd(root, repo string, request uint64, row settingsRow, original, value string) tea.Cmd {
	return func() tea.Msg {
		args := []string{"set-guidance", "--expected-repo", repo, "--expected", original, "--value", value}
		if row.kind == "label" {
			args = append(args, "--label-id", strconv.Itoa(row.id), "--expected-name", row.name)
		} else {
			args = append(args, "--action", row.name)
		}
		_, err := runScript(root, "taxonomy-settings", args...)
		if err != nil {
			return settingsDoneMsg{root: root, repo: repo, request: request, operation: "save", err: err}
		}
		taxonomy, err := LoadTaxonomy(root)
		return settingsDoneMsg{root: root, repo: repo, request: request, operation: "save", taxonomy: taxonomy, err: err}
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

func (m model) finishSettings(msg settingsDoneMsg) (tea.Model, tea.Cmd) {
	if !m.settings.open || msg.root != m.installRoot || msg.repo != m.repo || msg.request != m.settings.request {
		return m, nil
	}
	m.settings.busy = false
	if msg.err != nil {
		m.failErr("Couldn't update Settings", msg.err)
		return m, nil
	}
	m.taxonomy = msg.taxonomy
	m.form.taxonomy = msg.taxonomy
	if rows := m.settingsRows(); m.settings.selected >= len(rows) {
		m.settings.selected = maxInt(len(rows)-1, 0)
		m.settings.offset = 0
	}
	if msg.operation == "save" {
		m.settings.editor = nil
		m.status = "Local guidance saved."
	} else {
		m.status = "GitHub labels refreshed."
	}
	return m, nil
}
