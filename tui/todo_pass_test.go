package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// q isn't typing in an open choice list, so it quits there as everywhere else, asking first when decisions are unsaved.
func TestQuitFromOpenChoiceList(t *testing.T) {
	m := testPRModel()
	m = press(m, "l")
	m = press(m, "l")
	if !m.form.pick.open {
		t.Fatal("setup: l, l should open the Category list")
	}

	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")}); cmd == nil {
		t.Fatal("q in an open list should quit")
	} else if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("q in an open list should quit")
	}

	m = press(m, "j")
	m = press(m, "enter")
	m = press(m, "l")
	if m = press(m, "q"); !m.confirmQuit {
		t.Fatal("q in an open list with an unsaved decision should ask first")
	}
}

// The sidebar lists Batches, Groups, then Possible Duplicates, with Switch Repo alone after a blank line, and loads the Batches and Groups counts at startup.
func TestSidebarOrderAndStartupCounts(t *testing.T) {
	root := batchFixture(t)
	data, err := os.ReadFile(filepath.Join("..", "bin", "group"))
	if err != nil || os.WriteFile(filepath.Join(root, "bin", "group"), data, 0o755) != nil {
		t.Fatal("couldn't copy bin/group into the fixture")
	}

	m := batchModel(t, root)
	m.sidebar.batchCount, m.sidebar.groupCount = -1, -1
	m = runCmd(m, sidebarCountsCmd(root, "owner/repo"))
	if m.sidebar.batchCount != 1 || m.sidebar.groupCount != 0 {
		t.Fatalf("counts at startup: %d batches, %d groups", m.sidebar.batchCount, m.sidebar.groupCount)
	}

	view := m.sidebar.View(false)
	batches, groups, pairs, repo := strings.Index(view, "Batches (1)"), strings.Index(view, "Groups (0)"), strings.Index(view, "Possible Duplicates"), strings.Index(view, "Switch Repo")
	if batches < 0 || !(batches < groups && groups < pairs && pairs < repo) {
		t.Fatalf("sidebar order is off:\n%s", view)
	}

	if !strings.Contains(view, "Possible Duplicates\n\n") {
		t.Fatalf("Switch Repo should follow a blank line:\n%s", view)
	}
}

// / searches the theme list: typing narrows it and previews the first match, Enter keeps the matches, Esc clears the search before it cancels the picker.
func TestThemePickerSearch(t *testing.T) {
	themeFixture(t)
	m := testPRModel()
	m.installRoot = ".."
	original := themeName
	m = press(m, "t")
	m = press(m, "/")
	for _, r := range "light" {
		m = press(m, string(r))
	}

	if shown := m.themePicker.shown(); len(shown) != 1 || themeName != "gruvbox-light" {
		t.Fatalf("search should narrow to Gruvbox Light and preview it: %v, previewing %s", shown, themeName)
	}

	m = press(m, "enter")
	if m.themePicker.searching || !m.themePicker.open || len(m.themePicker.shown()) != 1 {
		t.Fatal("Enter should keep the matches listed")
	}

	m = press(m, "esc")
	if !m.themePicker.open || len(m.themePicker.shown()) != len(m.themePicker.names) || m.themePicker.names[m.themePicker.selected] != "gruvbox-light" {
		t.Fatal("Esc should clear the search first, keeping the cursor on the previewed theme")
	}

	if m = press(m, "esc"); m.themePicker.open || themeName != original {
		t.Fatal("a second Esc should cancel the picker and restore the theme")
	}
}

// An item with an unsaved draft reads "unsaved", in the warning color, in place of its decision.
func TestUnsavedDraftMarkedInList(t *testing.T) {
	m := batchModel(t, batchFixture(t))
	m.activateTab(0)
	m.selectCurrentListItem()
	m = press(m, "l")
	m = press(m, "j")
	m = press(m, "esc")
	li, ok := m.list.SelectedItem().(listItem)
	if !ok || !li.unsaved || li.Mark().text != "unsaved" || li.Mark().color != currentTheme.Warning || strings.Contains(li.Description(), "untriaged") {
		t.Fatalf("the draft should be marked unsaved in the list: %+v %q", li.Mark(), li.Description())
	}
}
