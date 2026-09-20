package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// `/` searches the list on screen: every word of the query must appear in an entry's title (ignoring case), and a "#123" word matches item number 123 exactly. The list itself holds only the matches, while m.listAll keeps every entry, so ticks and bulk actions still cover the whole list and indexes into the list stay plain.

// listHeaderHeight is the rows above the entries: the title, a blank line, the count or search line, and another blank line.
const listHeaderHeight = 4

var numberRef = regexp.MustCompile(`#(\d+)`)

// matchesSearch reports whether an entry's filter value (e.g. "#123 Some title") matches every word of the query.
func matchesSearch(value, query string) bool {
	lower := strings.ToLower(value)
	numbers := numberRef.FindAllStringSubmatch(value, -1)
	for _, term := range strings.Fields(strings.ToLower(query)) {
		if len(term) > 1 && term[0] == '#' {
			found := false
			for _, n := range numbers {
				found = found || n[1] == term[1:]
			}

			if !found {
				return false
			}

			continue
		}

		if !strings.Contains(lower, term) {
			return false
		}
	}

	return true
}

func (m model) searchQuery() string { return strings.TrimSpace(m.searchInput.Value()) }

// setListEntries replaces the list's entries (a new list, a save, a sync), keeping the search and the cursor on the entry it was on. When that entry has left the list (a saved item leaving Untriaged), the cursor stays at the same position, which is now the next entry.
func (m *model) setListEntries(entries []list.Item) {
	selected := ""
	if entry := m.list.SelectedItem(); entry != nil {
		selected = entry.FilterValue()
	}

	m.listAll = entries
	m.showList()
	m.selectEntry(selected)
}

// selectEntry moves the cursor to the entry whose FilterValue is value, if it's on screen.
func (m *model) selectEntry(value string) {
	for i, entry := range m.list.Items() {
		if entry.FilterValue() == value {
			m.list.Select(i)

			return
		}
	}
}

// selectKey moves the cursor to key's entry, if it's on screen.
func (m *model) selectKey(key Key) {
	for i, entry := range m.list.Items() {
		if li, ok := entry.(listItem); ok && li.Key() == key {
			m.list.Select(i)

			return
		}
	}
}

// showList puts the entries matching the search on screen, marked with their ticks and unsaved drafts.
func (m *model) showList() {
	query := m.searchQuery()
	visible := make([]list.Item, 0, len(m.listAll))
	for _, entry := range m.listAll {
		if query != "" && !matchesSearch(entry.FilterValue(), query) {
			continue
		}

		if li, ok := entry.(listItem); ok {
			_, li.unsaved = m.drafts[li.Key()]
			li.ticked = m.ticked[li.Key()]
			entry = li
		}

		visible = append(visible, entry)
	}

	m.list.SetItems(visible)
	if m.list.Index() >= len(visible) {
		m.list.Select(maxInt(len(visible)-1, 0))
	}
}

// resetSearch forgets the search without touching the list, for when another list replaces it.
func (m *model) resetSearch() {
	m.searching = false
	m.searchInput.Reset()
	m.searchInput.Blur()
}

// clearSearch shows the whole list again, keeping the cursor on the entry it was on.
func (m *model) clearSearch() {
	selected := ""
	if entry := m.list.SelectedItem(); entry != nil {
		selected = entry.FilterValue()
	}

	m.resetSearch()
	m.showList()
	m.selectEntry(selected)
}

func (m *model) startSearch() {
	m.searching = true
	m.searchInput.Focus()
	m.searchInput.CursorEnd()
}

// handleSearchKey takes every key while the query is being typed: Enter keeps the matches listed, Esc clears the search, ↑/↓ move through the matches, and anything else edits the query.
func (m model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Cancel):
		m.clearSearch()
	case key.Matches(msg, keys.Enter):
		m.searching = false
		m.searchInput.Blur()
		if m.searchQuery() == "" {
			m.clearSearch()
		}
	case msg.Type == tea.KeyUp:
		m.list.CursorUp()
	case msg.Type == tea.KeyDown:
		m.list.CursorDown()
	default:
		before := m.searchInput.Value()
		var cmd tea.Cmd
		m.searchInput, cmd = m.searchInput.Update(msg)
		if m.searchInput.Value() != before {
			m.showList()
			m.list.Select(0)
		}

		return m, cmd
	}

	return m, nil
}

// listHeader is the list's title and, under it, the entry count or the search with its match count.
func (m model) listHeader(width int) string {
	accent := lipgloss.NewStyle().Foreground(focusedBorderColor).Bold(true)
	line := mutedText(fmt.Sprintf("%d items · / to search", len(m.listAll)))
	switch {
	case m.searching:
		line = accent.Render("/ ") + m.searchInput.View() + "  " + mutedText(fmt.Sprintf("%d of %d · Enter keeps, Esc clears", len(m.list.Items()), len(m.listAll)))
	case m.searchQuery() != "":
		line = accent.Render("/ "+m.searchQuery()) + "  " + mutedText(fmt.Sprintf("%d of %d · / edits, Esc clears", len(m.list.Items()), len(m.listAll)))
	}

	// The line starts where the cards' text does, two columns in.
	return inset(titleBar(m.list.Title, "", width-2)) + "\n\n" + inset(inset(ansi.Truncate(line, width-4, "…"))) + "\n"
}
