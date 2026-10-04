package main

import (
	"fmt"
	"strconv"
	"strings"

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
	section          string
	menuSelected     int
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
	if catalog := m.taxonomy.LabelCatalog; m.settings.section == "label" && catalog.Repository == m.repo && catalog.Status == "observed" {
		for _, label := range catalog.Labels {
			rows = append(rows, settingsRow{kind: "label", id: label.ID, name: label.Name, description: label.Description, guidance: label.Guidance})
		}
	}
	if m.settings.section == "action" {
		for _, action := range m.taxonomy.Actions {
			rows = append(rows, settingsRow{kind: "action", name: action, guidance: m.taxonomy.ActionGuidance[action]})
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
	m.form.taxonomy = taxonomy
	m.settings = settingsUI{open: true}
	m.status = ""
}

func (m *model) settingsMove(delta int) {
	if m.settings.section == "" {
		m.settings.menuSelected = (m.settings.menuSelected + delta%2 + 2) % 2
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
			{"Labels", "View GitHub labels and edit local guidance"},
			{"Actions", "View available actions and edit local guidance"},
		}
		return inset(titleBar("Settings", "", m.menuWidth())) + "\n\n" + cardList(cards, m.settings.menuSelected, w, h-2)
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

	if editor := m.settings.editor; editor != nil {
		target := sanitize(editor.row.kind + ": " + editor.row.name)
		view += inset(ansi.Truncate(target, m.menuWidth(), "…")) + "\n"
		if editor.row.description != "" {
			view += inset(ansi.Wrap(sanitize(editor.row.description), m.menuWidth(), "")) + "\n"
		}
		view += "\n" + inset("Local guidance (optional advice for triage)") + "\n" + editor.text.View()
		return view
	}

	rows := m.settingsRows()
	if len(rows) == 0 {
		message := "No actions configured."
		if m.settings.section == "label" {
			message = "No labels saved. Press r to read this repository's labels from GitHub."
		}
		return view + inset(message)
	}
	visible := maxInt((h-4)/cardHeight, 1)
	start := minInt(m.settings.offset, maxInt(len(rows)-visible, 0))
	end := minInt(start+visible, len(rows))
	for i := start; i < end; i++ {
		row := rows[i]
		summary := row.description
		if row.kind == "action" {
			summary = "Local guidance: " + row.guidance
			if row.guidance == "" {
				summary = "No local guidance added"
			}
		} else if summary == "" {
			summary = "No GitHub description"
		}
		view += markedCard(sanitize(row.name), singleLine(sanitize(summary)), cardMark{}, i == m.settings.selected, w) + "\n"
	}
	selected := rows[m.settings.selected]
	footer := fmt.Sprintf("%d of %d · Local guidance: ", m.settings.selected+1, len(rows))
	if selected.guidance == "" {
		footer += "None added"
	} else {
		footer += singleLine(sanitize(selected.guidance))
	}
	footer = inset(mutedText(ansi.Truncate(footer, m.menuWidth(), "…")))
	view = strings.TrimRight(view, "\n")
	return view + strings.Repeat("\n", maxInt(h-ansiHeight(view), 1)) + footer
}

func ansiHeight(s string) int { return strings.Count(s, "\n") + 1 }

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
			m.settings.menuSelected = 1
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
	case "enter", "l", "right", "e":
		if m.settings.section == "" {
			m.settings.section = []string{"label", "action"}[m.settings.menuSelected]
			m.settings.selected, m.settings.offset = 0, 0
			return m, nil
		}

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
