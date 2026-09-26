package main

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestMatchesSearchByWordsAndNumber(t *testing.T) {
	for _, c := range []struct {
		query string
		want  bool
	}{
		{"", true},
		{"hyprland", true},
		{"MONITOR hyprland", true},
		{"monitor waybar", false},
		{"#123", true},
		{"#12", false},
		{"12", true},
		{"#123 monitor", true},
		{"#124", false},
	} {
		if got := matchesSearch("#123 Hyprland crashes on monitor hotplug", c.query); got != c.want {
			t.Errorf("%q: got %v, want %v", c.query, got, c.want)
		}
	}
}

func typeText(m model, s string) model {
	for _, r := range s {
		m = press(m, string(r))
	}

	return m
}

func TestSearchFiltersKeepsAndClears(t *testing.T) {
	m := batchModel(t, batchFixture(t))
	m = send(m, tea.WindowSizeMsg{Width: 110, Height: 30})
	m.focus = FocusSidebar
	m = press(m, "enter")
	m = press(m, "/")
	if !m.searching {
		t.Fatal("/ in a list should start a search")
	}

	// Letters that are keys elsewhere (e, c) are part of the query while typing.
	m = typeText(m, "sec")
	if n := len(m.list.Items()); n != 1 || !strings.Contains(ansi.Strip(m.viewContent()), "/ sec") {
		t.Fatalf("typing should narrow the list to the match, got %d:\n%s", n, ansi.Strip(m.viewContent()))
	}

	m = press(m, "enter")
	if m.searching || len(m.list.Items()) != 1 || m.focus != FocusList {
		t.Fatal("Enter should keep the filtered list and go back to moving through it")
	}

	m = press(m, " ")
	m = press(m, "esc")
	if len(m.list.Items()) != 2 || m.focus != FocusList {
		t.Fatalf("Esc in a searched list should clear the search, not leave the list: %d items, focus %v", len(m.list.Items()), m.focus)
	}

	if li := m.list.SelectedItem().(listItem); li.Number != 2 || !li.ticked {
		t.Fatalf("clearing the search should keep the cursor on the searched item, ticked: %+v", li)
	}

	m = press(m, "/")
	m = typeText(m, "first")
	m = press(m, "enter")
	if targets := m.listTargets(); len(targets) != 1 || targets[0].Number != 2 {
		t.Fatalf("ticks hidden by the search still count for list actions: %+v", targets)
	}

	m = press(m, "/")
	m = typeText(m, "nothing like it")
	if !strings.Contains(ansi.Strip(m.viewContent()), "No titles match") {
		t.Fatalf("a search without matches should say so:\n%s", ansi.Strip(m.viewContent()))
	}

	m = press(m, "esc")
	if m.searching || len(m.list.Items()) != 2 {
		t.Fatal("Esc while typing should clear the search")
	}
}

func TestSearchResetsWhenAnotherListOpens(t *testing.T) {
	m := batchModel(t, batchFixture(t))
	m.focus = FocusSidebar
	m = press(m, "enter")
	m = press(m, "/")
	m = typeText(m, "sec")
	m = press(m, "enter")
	m = press(m, "esc")
	m = press(m, "esc")
	if m.focus != FocusSidebar {
		t.Fatalf("the second Esc should leave the list: %v", m.focus)
	}

	m = press(m, "/")
	m = press(m, "j")
	m = press(m, "enter")
	if m.searchQuery() != "" || len(m.list.Items()) != len(m.listAll) {
		t.Fatalf("a newly opened list starts without a search: %q", m.searchQuery())
	}
}
