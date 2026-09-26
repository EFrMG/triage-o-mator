package main

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func click(m model, x, y int, button tea.MouseButton) model {
	return send(m, tea.MouseClickMsg{X: x, Y: y, Button: button})
}

func wheel(m model, x, y int, button tea.MouseButton) model {
	return send(m, tea.MouseWheelMsg{X: x, Y: y, Button: button})
}

func TestMouseSidebarAndList(t *testing.T) {
	m := newModel("/tmp", "owner/repo", testTaxonomy(), "tester", testItems())
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 30})
	y := -1
	for row := 0; row < m.mainHeight()+2; row++ {
		if m.mouseSidebarRow(row) == allItemsTab {
			y = row
			break
		}
	}
	if y < 0 {
		t.Fatal("All Items sidebar row was not found")
	}
	if !strings.Contains(ansi.Strip(strings.Split(m.viewContent(), "\n")[y]), "All Items") {
		t.Fatalf("mapped sidebar row %d is not All Items", y)
	}
	m = click(m, 3, y, tea.MouseLeft)
	if m.focus != FocusList || m.activeTab != allItemsTab {
		t.Fatalf("sidebar click did not open All Items: focus=%d tab=%d", m.focus, m.activeTab)
	}

	x := m.mouseMainX() + 5
	m = click(m, x, 9, tea.MouseLeft)
	if m.list.Index() != 1 || m.focus != FocusList {
		t.Fatalf("first card click should select second item: index=%d focus=%d", m.list.Index(), m.focus)
	}
	m = click(m, x, 9, tea.MouseLeft)
	if m.focus != FocusDetail || m.detail.key.Number != 2 {
		t.Fatalf("second card click should open item #2: focus=%d key=%v", m.focus, m.detail.key)
	}
}

func TestMouseRightClickAndWheelOnList(t *testing.T) {
	m := newModel("/tmp", "owner/repo", testTaxonomy(), "tester", testItems())
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.activateTab(untriagedTab)
	m = click(m, m.mouseMainX()+5, 5, tea.MouseRight)
	if !m.ticked[Key{Kind: "issue", Number: 1}] {
		t.Fatal("right click on the selected card should tick it")
	}
	m = wheel(m, m.mouseMainX()+5, 6, tea.MouseWheelDown)
	if m.list.Index() != 1 {
		t.Fatalf("wheel should move the list selection: %d", m.list.Index())
	}
	m = click(m, m.mouseMainX()+5, 9, tea.MouseRight)
	if !m.ticked[Key{Kind: "issue", Number: 2}] {
		t.Fatal("right click should tick a newly selected card")
	}
}

func TestMouseActiveSearchDoesNotChangeQuery(t *testing.T) {
	m := newModel("/tmp", "owner/repo", testTaxonomy(), "tester", testItems())
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.activateTab(untriagedTab)
	m = send(m, mouseKey("/"))
	m = send(m, tea.KeyPressMsg{Text: "needle"})
	m = click(m, m.mouseMainX()+5, 3, tea.MouseLeft)
	if got := m.searchInput.Value(); got != "needle" {
		t.Fatalf("clicking active list search changed query to %q", got)
	}

	m.themePicker.open = true
	m.themePicker.searching = true
	m.themePicker.query.SetValue("dark")
	m = click(m, 5, 3, tea.MouseLeft)
	if got := m.themePicker.query.Value(); got != "dark" {
		t.Fatalf("clicking active theme search changed query to %q", got)
	}
}

func TestMouseDetailTabsAndScroll(t *testing.T) {
	m := testPRModel()
	bar := ansi.Strip(strings.Split(m.detail.TabBar(m.detailInnerWidth(), true), "\n")[0])
	x := 2 + strings.Index(bar, "[2]") + 1
	if x < 2 {
		t.Fatal("second tab missing")
	}
	m = click(m, x, 4, tea.MouseLeft)
	if m.detail.active != 1 {
		t.Fatalf("tab click selected section %d", m.detail.active)
	}
	m.detail.JumpSection(0)
	before := m.detail.sections[0].viewport.YOffset()
	m = wheel(m, 5, 10, tea.MouseWheelDown)
	if m.detail.sections[0].viewport.YOffset() <= before {
		t.Fatal("wheel did not scroll the active detail viewport")
	}
}

func TestMouseDecisionChoice(t *testing.T) {
	m := testPRModel()
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 30})
	x := 2 + m.detail.width + 3 + formLabelWidth
	m = click(m, x, 6, tea.MouseLeft)
	if m.form.focused != fieldCategory {
		t.Fatalf("clicking category focused %d", m.form.focused)
	}
	m = click(m, x, 6, tea.MouseLeft)
	if !m.form.pick.open {
		t.Fatal("second click should open category choices")
	}
	m = click(m, x, 9, tea.MouseLeft)
	if m.form.pick.open || m.form.focused != fieldAction || m.form.Category() != testTaxonomy().PRCategories[1] {
		t.Fatalf("clicking a choice did not select it: open=%v focus=%d category=%q", m.form.pick.open, m.form.focused, m.form.Category())
	}
}

func TestMouseStatusHelpAndFooter(t *testing.T) {
	m := newModel("/tmp", "owner/repo", testTaxonomy(), "tester", testItems())
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	footerTop := m.height - lipgloss.Height(m.footerView())
	m = click(m, m.width-2, footerTop-1, tea.MouseLeft)
	if !m.showHelp {
		t.Fatal("status help click did not expand footer")
	}
	footer := strings.Split(ansi.Strip(m.footerView()), "\n")
	row, col := -1, -1
	for i, line := range footer {
		if at := strings.Index(line, "q quit"); at >= 0 {
			row, col = i, at+1
			break
		}
	}
	if row < 0 {
		t.Fatal("quit hint missing from footer")
	}
	next, cmd := m.Update(tea.MouseClickMsg{X: col, Y: m.height - len(footer) + row, Button: tea.MouseLeft})
	if next.(model).confirmQuit || cmd == nil {
		t.Fatal("clicking the quit hint should invoke the normal quit action")
	}
}

func TestMouseHelpDoesNotTypeIntoComment(t *testing.T) {
	m := testPRModel()
	m.comment.open = true
	m.comment.text = textarea.New()
	m.comment.text.SetValue("draft")
	m.comment.text.Focus()
	footerTop := m.height - lipgloss.Height(m.footerView())
	m = click(m, m.width-2, footerTop-1, tea.MouseLeft)
	if !m.showHelp || m.comment.text.Value() != "draft" {
		t.Fatalf("help click changed comment: help=%v text=%q", m.showHelp, m.comment.text.Value())
	}
}

func TestMouseFooterHintsAddressEachDirection(t *testing.T) {
	for _, tc := range []struct {
		label string
		x     int
		want  string
	}{
		{"j/k", 0, "j"},
		{"j/k", 2, "k"},
		{"Ctrl-D/U", 1, "ctrl+d"},
		{"Ctrl-D/U", 7, "ctrl+u"},
		{"Enter/l/→", 8, "right"},
		{"1/2/3/4", 4, "3"},
	} {
		if got := mouseHintActionAt(tc.label, tc.x); got != tc.want {
			t.Fatalf("%s at %d gives %q, want %q", tc.label, tc.x, got, tc.want)
		}
	}
}

func TestMouseNotificationCardOpensReader(t *testing.T) {
	m := notificationsFixture(t)
	cmd := m.enterSidebarSelection()
	m = send(m, cmd())
	if !m.notifications.open {
		t.Fatal("notifications did not open")
	}
	y := -1
	for i, line := range strings.Split(ansi.Strip(m.viewContent()), "\n") {
		if strings.Contains(line, "PR #8028: retained activity") {
			y = i
			break
		}
	}
	if y < 0 {
		t.Fatal("attention card not visible")
	}
	x := m.mouseMainX() + 5
	m = click(m, x, y, tea.MouseLeft)
	next, cmd := m.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m = next.(model)
	if !m.attention.open || cmd == nil {
		t.Fatal("second click on notification did not open its reader")
	}
}

func TestMouseReaderSelectsVisibleCard(t *testing.T) {
	m := actionHistoryFixture(t)
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 36})
	next, cmd := m.readActionHistory(actionHistoryLocation{section: "entries", number: 1, checkpoint: strings.Repeat("b", 64)})
	m = finishActionCommand(t, next.(model), cmd)
	y := -1
	for i, line := range strings.Split(ansi.Strip(m.viewContent()), "\n") {
		if strings.Contains(line, "record 2") {
			y = i
			break
		}
	}
	if y < 0 {
		t.Fatal("second explanation card not visible")
	}
	m = click(m, m.mouseMainX()+5, y, tea.MouseLeft)
	if m.actionHistory.selected != 1 {
		t.Fatalf("clicked explanation selected index %d", m.actionHistory.selected)
	}
	m.actionHistory.selected = 6
	y = -1
	for i, line := range strings.Split(ansi.Strip(m.viewContent()), "\n") {
		if strings.Contains(line, "record 5") {
			y = i
			break
		}
	}
	if y < 0 {
		t.Fatal("fifth explanation card not visible after scrolling")
	}
	m = click(m, m.mouseMainX()+5, y, tea.MouseLeft)
	if m.actionHistory.selected != 4 {
		t.Fatalf("clicked scrolled explanation selected index %d", m.actionHistory.selected)
	}
}

func TestMouseGroupAndBatchCards(t *testing.T) {
	root, group := groupFixture(t)
	m := testPRModel()
	m.installRoot = root
	m.groups = groupUI{open: true, records: []Group{group}}
	x := m.mouseMainX() + 5
	m = click(m, x, 3, tea.MouseLeft)
	m = click(m, x, 3, tea.MouseLeft)
	if !m.groups.detail {
		t.Fatal("double click did not open group members")
	}

	b := batchModel(t, batchFixture(t))
	b.batches.open = true
	x = b.mouseMainX() + 5
	b = click(b, x, 3, tea.MouseLeft)
	b = click(b, x, 3, tea.MouseLeft)
	if b.activeBatch == "" || b.batches.open {
		t.Fatal("double click did not open batch items")
	}
}
