package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// rowCount is len(tabs) real tabs plus Batches, Groups, Possible Duplicates, Notifications, and Switch Repo.
func rowCount() int { return len(tabs) + 5 }

type sidebarModel struct {
	selected int
	counts   []int // len(tabs), item count per tab
	// pairCount is the Possible Duplicates count, or -1 until that view has been computed once; batchCount and groupCount likewise until they're loaded, at startup (sidebarCountsCmd).
	pairCount, batchCount, groupCount, notificationCount int
}

func newSidebar() sidebarModel {
	return sidebarModel{counts: make([]int, len(tabs)), pairCount: -1, batchCount: -1, groupCount: -1, notificationCount: -1}
}

func (s *sidebarModel) RecomputeCounts(items []Item) {
	for i, t := range tabs {
		s.counts[i] = len(t.Filter(items))
	}
}

func (s *sidebarModel) Next() { s.selected = (s.selected + 1) % rowCount() }
func (s *sidebarModel) Prev() { s.selected = (s.selected - 1 + rowCount()) % rowCount() }

func (s sidebarModel) View(focused bool) string {
	var b strings.Builder
	renderRow := func(i int, line string) {
		style := lipgloss.NewStyle()

		if i == s.selected {
			style = style.Bold(true)
			if focused {
				style = style.Foreground(focusedBorderColor)
				line = "> " + line
			} else {
				line = "* " + line
			}
		} else {
			line = "  " + line
		}

		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}

	for i, t := range tabs {
		renderRow(i, fmt.Sprintf("%s (%d)", t.Name, s.counts[i]))
	}

	b.WriteString("\n")

	// ≡ rather than ▤: most fonts draw ▤ from a fallback font, offset to the right and crowding the text.
	renderRow(batchesIndex, withCount("≡ Batches", s.batchCount))
	renderRow(groupsIndex, withCount("◇ Groups", s.groupCount))
	renderRow(pairsIndex, withCount("≈ Possible Duplicates", s.pairCount))
	renderRow(notificationsIndex, withCount("! Notifications", s.notificationCount))
	b.WriteString("\n")
	renderRow(switchRepoIndex, "⇄ Switch Repo")

	return strings.TrimSuffix(b.String(), "\n")
}

// withCount appends "(n)" to a row's label, or nothing while n is unknown (-1).
func withCount(label string, n int) string {
	if n < 0 {
		return label
	}

	return fmt.Sprintf("%s (%d)", label, n)
}

// sidebarCountsMsg carries the Batches and Groups counts, -1 for one that couldn't be read (its screen reports why when opened).
type sidebarCountsMsg struct {
	repo            string
	batches, groups int
}

// sidebarCountsCmd counts the batches (their items files, without reading them) and the groups (bin/group list), so the sidebar shows both before either screen is opened.
func sidebarCountsCmd(root, repo string) tea.Cmd {
	return func() tea.Msg {
		msg := sidebarCountsMsg{repo: repo, batches: -1, groups: -1}
		if paths, err := filepath.Glob(filepath.Join(batchesDir(root, repo), "*.items.jsonl")); err == nil {
			msg.batches = len(paths)
		}

		var groups []Group
		if out, err := runScript(root, "group", "list"); err == nil && json.Unmarshal([]byte(out), &groups) == nil {
			msg.groups = len(groups)
		}

		return msg
	}
}

func (m model) onSidebarCounts(msg sidebarCountsMsg) (tea.Model, tea.Cmd) {
	if msg.repo != m.repo {
		return m, nil
	}

	// A screen that loaded in the meantime has the fresher count.
	if m.sidebar.batchCount < 0 {
		m.sidebar.batchCount = msg.batches
	}

	if m.sidebar.groupCount < 0 {
		m.sidebar.groupCount = msg.groups
	}

	return m, nil
}
