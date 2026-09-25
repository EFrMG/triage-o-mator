package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The main panel's top border says where you are, e.g. "omacom/omarchy › Batches › b20260919-040202 › #883", so the way back with Esc is always visible.

// breadcrumb is the path to the screen on show, from the repo down.
func (m model) breadcrumb() []string {
	crumbs := []string{m.repo}
	if m.noInstall() {
		crumbs = []string{"triage-o-mator"}
	}

	switch {
	case m.lastError.open:
		return append(crumbs, "Last error")
	case m.themePicker.open:
		return append(crumbs, "Themes")
	case m.editingRepo && m.installing.path != "":
		return append(crumbs, "Switch Repo", "Install")
	case m.editingRepo:
		return append(crumbs, "Switch Repo")
	}

	if m.corpus.open {
		crumbs = append(crumbs, "Local dataset")
		return crumbs
	}

	if m.batches.open {
		crumbs = append(crumbs, "Batches")
		if m.batches.editing {
			crumbs = append(crumbs, "New batch")
		}

		return crumbs
	}

	if m.groups.open {
		// Opened with b from an item or a list: that comes first.
		if m.groups.originFocus != FocusSidebar && len(m.groups.sources) > 0 {
			crumbs = m.placeCrumbs(m.groups.originFocus == FocusDetail)
		}

		return append(crumbs, m.groupCrumbs()...)
	}

	if m.groups.returnToGroup && m.focus == FocusDetail {
		return append(append(crumbs, m.groupCrumbs()...), itemCrumb(m.detail.key))
	}

	if m.dups.open || m.dups.returnToDups {
		source := m.dups.source
		crumbs = m.placeCrumbs(false)
		if !m.dups.fromList {
			crumbs = append(crumbs, itemCrumb(source))
		}

		crumbs = append(crumbs, "Duplicates of "+itemCrumb(source))
		if m.dups.returnToDups && m.focus == FocusDetail {
			crumbs = append(crumbs, itemCrumb(m.detail.key))
		}

		return crumbs
	}

	return m.placeCrumbs(m.focus == FocusDetail)
}

// placeCrumbs is the list on show (or the overview), its search, and the open item when withItem is set.
func (m model) placeCrumbs(withItem bool) []string {
	crumbs := []string{m.repo}
	if m.noInstall() {
		crumbs = []string{"triage-o-mator"}
	}

	switch {
	case !m.listReady:
		crumbs = append(crumbs, "Overview")
	case m.activePairs:
		crumbs = append(crumbs, "Possible Duplicates")
	case m.activeBatch != "":
		crumbs = append(crumbs, "Batches", m.activeBatch)
	default:
		crumbs = append(crumbs, tabs[m.activeTab].Name)
	}

	if q := m.searchQuery(); q != "" && m.listReady {
		crumbs = append(crumbs, fmt.Sprintf("search %q", q))
	}

	if withItem && len(m.detail.sections) > 0 {
		crumbs = append(crumbs, itemCrumb(m.detail.key))
	}

	return crumbs
}

// groupCrumbs is Groups, then the group you're in and what you're editing there.
func (m model) groupCrumbs() []string {
	crumbs := []string{"Groups"}
	g := m.selectedGroup()
	if g != nil && (m.groups.detail || m.groups.returnToGroup || (m.groups.editing != "" && m.groups.editing != "new")) {
		crumbs = append(crumbs, g.Title)
	}

	switch m.groups.editing {
	case "new":
		crumbs = append(crumbs, "New group")
	case "edit":
		crumbs = append(crumbs, "Edit")
	case "add":
		crumbs = append(crumbs, "Add")
	case "notes":
		crumbs = append(crumbs, "Notes")
	}

	return crumbs
}

func itemCrumb(k Key) string { return fmt.Sprintf("#%d", k.Number) }

// titled writes the breadcrumb into panel's top border, centered: the repo muted, the rest in the text color, the last crumb bold. A crumb trail wider than the border loses its leftmost crumbs first.
func (m model) titled(panel string, focused bool) string {
	lines := strings.SplitN(panel, "\n", 2)
	width := lipgloss.Width(lines[0])
	room := width - 6
	if len(lines) < 2 || room < 8 {
		return panel
	}

	color := blurredBorderColor
	if focused {
		color = focusedBorderColor
	}

	border := screenStyle().Foreground(color)
	muted := screenStyle().Foreground(lipgloss.Color(currentTheme.Muted))
	text := screenStyle().Foreground(lipgloss.Color(currentTheme.Foreground))
	separator := muted.Render(" › ")

	crumbs := m.breadcrumb()
	render := func(from int) string {
		parts := make([]string, 0, len(crumbs)-from+1)
		if from > 0 {
			parts = append(parts, muted.Render("…"))
		}

		for i := from; i < len(crumbs); i++ {
			style := text
			switch {
			case i == 0:
				style = muted
			case i == len(crumbs)-1:
				style = text.Bold(true)
			}

			parts = append(parts, style.Render(crumbs[i]))
		}

		return strings.Join(parts, separator)
	}

	trail := render(0)
	for from := 1; lipgloss.Width(trail) > room && from < len(crumbs)-1; from++ {
		trail = render(from)
	}

	trail = ansi.Truncate(trail, room, "…")
	// Centered: the rule on each side of " trail " splits what's left between the corners.
	fill := maxInt(width-2-lipgloss.Width(trail)-2, 0)
	left := fill / 2
	top := border.Render("╭"+strings.Repeat("─", left)+" ") + trail + border.Render(" "+strings.Repeat("─", fill-left)+"╮")

	return top + "\n" + lines[1]
}
