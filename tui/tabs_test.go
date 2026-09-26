package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func listedNumbers(t *testing.T, m model) []int {
	t.Helper()

	numbers := make([]int, 0, len(m.list.Items()))
	for _, entry := range m.list.Items() {
		item, ok := entry.(listItem)
		if !ok {
			t.Fatalf("listed entry is %T, want listItem", entry)
		}

		numbers = append(numbers, item.Number)
	}

	return numbers
}

func TestUntriagedCyclesKindAndAgeOrder(t *testing.T) {
	items := []Item{
		{Kind: "issue", Number: 1, State: "open", CreatedAt: "2026-01-01"},
		{Kind: "pr", Number: 2, State: "open", CreatedAt: "2026-01-02"},
		{Kind: "issue", Number: 3, State: "open", CreatedAt: "2026-01-03"},
		{Kind: "pr", Number: 4, State: "closed", CreatedAt: "2025-01-01"},
		{Kind: "pr", Number: 5, State: "open", CreatedAt: "2025-01-01", Category: "merge-ready"},
	}
	m := newModel("/tmp", "owner/repo", testTaxonomy(), "tester", items)
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 30})
	m.activateTab(untriagedTab)

	if got := listedNumbers(t, m); !equalInts(got, []int{1, 2, 3}) {
		t.Fatalf("default Untriaged order = %v, want both kinds oldest first", got)
	}

	if !strings.Contains(m.list.Title, "Untriaged · Both · Oldest") || m.sidebar.counts[untriagedTab] != 3 {
		t.Fatalf("default view not reflected in title/count: %q, %v", m.list.Title, m.sidebar.counts)
	}

	m = press(m, "i")
	if got := listedNumbers(t, m); !equalInts(got, []int{1, 3}) || !strings.Contains(m.list.Title, "Issues · Oldest") {
		t.Fatalf("issue view = %v, title %q", got, m.list.Title)
	}

	m = press(m, "i")
	if got := listedNumbers(t, m); !equalInts(got, []int{2}) || !strings.Contains(m.list.Title, "PRs · Oldest") {
		t.Fatalf("PR view = %v, title %q", got, m.list.Title)
	}

	m = press(m, "i")
	m = press(m, " ")
	m = press(m, "O")
	if got := listedNumbers(t, m); !equalInts(got, []int{3, 2, 1}) || !strings.Contains(m.list.Title, "Both · Newest") {
		t.Fatalf("newest combined view = %v, title %q", got, m.list.Title)
	}

	if len(m.ticked) != 0 || m.sidebar.counts[untriagedTab] != 3 {
		t.Fatalf("changing the view should clear ticks and update its count: %v, %v", m.ticked, m.sidebar.counts)
	}

	footer := m.footerView()
	if !strings.Contains(footer, "i") || !strings.Contains(footer, "O") {
		t.Fatalf("Untriaged footer omits its toggles:\n%s", footer)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}
