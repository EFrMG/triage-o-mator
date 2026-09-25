package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// openFixtureItem opens #1 from Untriaged in the batch fixture, on its content.
func openFixtureItem(t *testing.T) model {
	t.Helper()
	m := batchModel(t, batchFixture(t))
	m.activateTab(untriagedTab)
	m.selectCurrentListItem()

	return m
}

func TestChoiceFieldsCycleWithJKAndOpenAList(t *testing.T) {
	m := openFixtureItem(t)
	m = press(m, "tab")
	if m.form.focused != fieldCategory {
		t.Fatalf("Tab from the content should reach category: %v", m.form.focused)
	}

	before := m.form.Category()
	for _, k := range []string{"j", "down"} {
		m = press(m, k)
		if m.form.Category() == before || m.form.focused != fieldCategory {
			t.Fatalf("%s should change the value and stay on the field: %q, %v", k, m.form.Category(), m.form.focused)
		}

		m = press(m, "k")
		if m.form.Category() != before {
			t.Fatalf("k should change it back: %q", m.form.Category())
		}
	}

	for _, k := range []string{"l", "right"} {
		m = press(m, k)
		if !m.form.pick.open || !strings.Contains(m.form.View(80), "› "+before) {
			t.Fatalf("%s should open the list on the current value:\n%s", k, m.form.View(80))
		}

		for _, close := range []string{"esc", "h", "left"} {
			m = press(m, close)
			if m.form.pick.open || m.focus != FocusDetail || m.form.focused != fieldCategory || m.form.Category() != before {
				t.Fatalf("%s should close the list without changing anything or leaving the field", close)
			}

			m = press(m, k)
		}

		m = press(m, "esc")
	}

	m = press(m, "l")
	m = press(m, "j")
	m = press(m, "enter")
	if m.form.pick.open || m.form.Category() == before || m.form.focused != fieldAction || !m.form.dirty {
		t.Fatalf("Enter in the list should pick the value and move on: %q, %v", m.form.Category(), m.form.focused)
	}

	action := m.form.Action()
	m = press(m, "right")
	m = press(m, "j")
	m = press(m, "l")
	if m.form.pick.open || m.form.Action() == action || m.form.focused != fieldConfidence {
		t.Fatalf("l in the list should pick the value and move on too: %q, %v", m.form.Action(), m.form.focused)
	}

	m = press(m, "K")
	if m.form.focused != fieldAction {
		t.Fatalf("K on a choice field should move to the previous field: %v", m.form.focused)
	}

	m = press(m, "enter")
	if m.form.pick.open || m.form.focused != fieldConfidence {
		t.Fatalf("Enter on a choice field should confirm it and move on: %v", m.form.focused)
	}

	m = press(m, "J")
	if m.form.focused != fieldReason || m.form.Reason() != "" {
		t.Fatalf("J on the last choice should move into the reason without typing: %v %q", m.form.focused, m.form.Reason())
	}

	for _, r := range "JKjk" {
		m = press(m, string(r))
	}

	if m.form.focused != fieldReason || m.form.Reason() != "JKjk" {
		t.Fatalf("in the reason field J/K and j/k are text: %v %q", m.form.focused, m.form.Reason())
	}

	m = press(m, "left")
	m = press(m, "x")
	if m.focus != FocusDetail || m.form.focused != fieldReason || m.form.Reason() != "JKjxk" {
		t.Fatalf("in the reason field ← moves the cursor instead of going back: %v %q", m.form.focused, m.form.Reason())
	}

	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); cmd == nil {
		t.Fatal("Enter in the reason field, the last one, should save")
	}
}

func TestContentScrollsWithJKAndTabsJumpByNumber(t *testing.T) {
	m := testPRModel()
	m = press(m, "3")
	if m.detail.sections[m.detail.active].name != "Diff" {
		t.Fatalf("3 should jump to the third tab: %q", m.detail.sections[m.detail.active].name)
	}

	m = press(m, "1")
	m = press(m, "j")
	if m.form.focused != fieldContent {
		t.Fatal("on the content, j scrolls instead of moving to a field")
	}
}

func TestBatchFormEnterWalksFieldsThenCreates(t *testing.T) {
	m := batchModel(t, batchFixture(t))
	m.batches.open = true
	m = press(m, "n")
	m = press(m, "J")
	if m.batches.field != 0 {
		t.Fatalf("J in the size field is text, not a move: field %d", m.batches.field)
	}

	m = press(m, "backspace")
	m = press(m, "enter")
	if m = press(m, "J"); m.batches.field != 2 {
		t.Fatalf("J on a choice should move to the next field: %d", m.batches.field)
	}

	if m = press(m, "K"); m.batches.field != 1 {
		t.Fatalf("K on a choice should move to the previous field: %d", m.batches.field)
	}

	for want := 2; want < batchFormFields; want++ {
		m = press(m, "l")
		if !m.batches.pick.open {
			t.Fatalf("l on choice field %d should open its list", want-1)
		}

		m = press(m, "enter")
		if m.batches.pick.open || m.batches.field != want {
			t.Fatalf("Enter in the list should pick and move to field %d: field %d", want, m.batches.field)
		}
	}

	m = press(m, "l")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m = next.(model); cmd == nil || m.batches.editing || !m.batches.busy {
		t.Fatal("picking on the last field should create the batch")
	}

	// Enter on the last field, list closed, creates it too, like Ctrl-S.
	m.batches.busy = false
	m = press(m, "n")
	for m.batches.field < batchFormFields-1 {
		m = press(m, "tab")
	}

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m = next.(model); cmd == nil || m.batches.editing || !m.batches.busy {
		t.Fatalf("Enter on the last field should create the batch: field %d, editing %v", m.batches.field, m.batches.editing)
	}
}

func TestGroupEditorStatusIsAChoice(t *testing.T) {
	root, g := groupFixture(t)
	m := testPRModel()
	m.installRoot = root
	m.groups = groupUI{open: true, records: []Group{g}}
	m = press(m, "e")
	for m.groups.field != groupStatusField {
		m = press(m, "tab")
	}

	m = press(m, "x")
	if got := m.groups.inputs[groupStatusField].Value(); got != "draft" {
		t.Fatalf("typing into the status should be ignored: %q", got)
	}

	m = press(m, "j")
	if got := m.groups.inputs[groupStatusField].Value(); got != "ready" {
		t.Fatalf("j should move draft to ready: %q", got)
	}

	m = press(m, "l")
	m = press(m, "enter")
	if m.groups.field != groupStatusField+1 || m.groups.inputs[groupStatusField].Value() != "ready" {
		t.Fatalf("l, Enter should keep ready and move on to the assignee: field %d", m.groups.field)
	}

	assignee := m.groups.inputs[groupStatusField+1].Value()
	m = press(m, "K")
	if m.groups.field != groupStatusField+1 || m.groups.inputs[groupStatusField+1].Value() != assignee+"K" {
		t.Fatalf("K in a text field is a letter: field %d, %q", m.groups.field, m.groups.inputs[groupStatusField+1].Value())
	}

	m = press(m, "backspace")
	m = press(m, "shift+tab")
	m = press(m, "K")
	if m.groups.field != groupStatusField-1 {
		t.Fatalf("K on the status should move to the previous field: field %d", m.groups.field)
	}

	m = press(m, "tab")
	m = press(m, "enter")
	if m.groups.pick.open || m.groups.field != groupStatusField+1 {
		t.Fatalf("Enter on the status should confirm it and move on: field %d", m.groups.field)
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = send(next.(model), cmd())
	if len(m.groups.records) != 1 || m.groups.records[0].Status != "ready" {
		t.Fatalf("Enter on the last field should save the new status: %+v", m.groups.records)
	}
}

func TestGroupAddKeyOpensFromCurrentItem(t *testing.T) {
	root, g := groupFixture(t)
	m := testPRModel()
	m.installRoot = root
	key := m.detail.key
	m.groups = groupUI{open: true, records: []Group{g}, sources: []Key{key}}
	if m = press(m, "b"); m.groups.editing != "add" {
		t.Fatal("b in Groups should add the item Groups was opened from")
	}
}

func TestRemovingAGroupMemberNeedsASecondPress(t *testing.T) {
	root, g := groupFixture(t)
	out, err := runScript(root, "group", "add", g.ID, "--kind", "pr", "--number", "1", "--by", "tester")
	if err != nil {
		t.Fatal(err)
	}

	if err := json.Unmarshal([]byte(out), &g); err != nil || len(g.Members) != 1 {
		t.Fatalf("fixture member not added: %s", out)
	}

	m := testPRModel()
	m.installRoot = root
	m.groups = groupUI{open: true, records: []Group{g}, detail: true}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if m = next.(model); cmd != nil || !strings.Contains(m.status, "Press d again") {
		t.Fatalf("the first d should only ask: %q", m.status)
	}

	if m = press(m, "j"); m.groups.confirm != "" {
		t.Fatal("any other key should cancel the removal")
	}
}

func TestDeletingAGroupFromTheListNeedsASecondPress(t *testing.T) {
	root, g := groupFixture(t)
	out, err := runScript(root, "group", "add", g.ID, "--kind", "pr", "--number", "1", "--by", "tester")
	if err != nil {
		t.Fatal(err)
	}

	if err := json.Unmarshal([]byte(out), &g); err != nil || len(g.Members) != 1 {
		t.Fatalf("fixture member not added: %s", out)
	}

	m := testPRModel()
	m.installRoot = root
	m.lastGroupID = g.ID
	m.groups = groupUI{open: true, records: []Group{g}, ticked: map[Key]bool{}}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	if m = next.(model); cmd != nil || !strings.Contains(m.status, "1 member") || !strings.Contains(m.status, "Press d again") {
		t.Fatalf("the first d should only ask, naming what would go: %q", m.status)
	}

	if m = press(m, "j"); m.groups.confirm != "" {
		t.Fatal("any other key should cancel the deletion")
	}

	m = press(m, "d")
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = send(next.(model), cmd())
	if len(m.groups.records) != 0 || m.sidebar.groupCount != 0 || !strings.Contains(m.status, "deleted") {
		t.Fatalf("the second d should delete the group: %d left, %q", len(m.groups.records), m.status)
	}

	if m.lastGroupID != "" {
		t.Fatal("a deleted group must not stay the quick-add target")
	}

	if _, err := runScript(root, "group", "show", g.ID); err == nil {
		t.Fatal("the group file survived the deletion")
	}

	data, err := os.ReadFile(filepath.Join(root, "data", "owner", "repo", "ledger.jsonl"))
	if err != nil || string(data) != "{\"kind\":\"pr\",\"number\":1}\n" {
		t.Fatal("deleting a group changed the item ledger")
	}
}

func TestApplyProposalsFromInsideABatch(t *testing.T) {
	m := batchModel(t, batchFixture(t))
	m.openBatch("b20260101-000000")
	m = press(m, "A")
	if m.batches.confirm != "A" || !strings.Contains(m.status, "Press A again") {
		t.Fatalf("A inside an open batch should ask to apply its proposals: %q", m.status)
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("A")})
	m = send(next.(model), cmd())
	if row := ledgerRow(t, m.installRoot, 1); row["category"] != "support-question" {
		t.Fatalf("the second A should apply the proposal: %v", row)
	}
}

func TestHAndLMoveBetweenContentAndForm(t *testing.T) {
	m := testPRModel()
	if m.focus != FocusDetail || m.form.focused != fieldContent {
		t.Fatalf("an opened item starts on its content: %v %v", m.focus, m.form.focused)
	}

	if m = press(m, "l"); m.focus != FocusDetail || m.form.focused != fieldCategory {
		t.Fatalf("l on the content should move to the form's first field: %v", m.form.focused)
	}

	if m = press(m, "left"); m.focus != FocusDetail || m.form.focused != fieldContent {
		t.Fatalf("← on the form should move back to the content: %v", m.form.focused)
	}

	m = press(m, "right")
	if m = press(m, "esc"); m.focus == FocusDetail {
		t.Fatal("Esc on the form should still leave the item")
	}

	m = testPRModel()
	if m = press(m, "h"); m.focus == FocusDetail {
		t.Fatal("h on the content should still leave the item")
	}
}

// Groups has its own sidebar entry: Enter opens the screen beside the sidebar, the entry counts the groups once loaded, and Esc goes back to the sidebar.
func TestGroupsOpenFromTheSidebar(t *testing.T) {
	root, g := groupFixture(t)
	m := testPRModel()
	m.installRoot = root
	m = send(m, tea.WindowSizeMsg{Width: 130, Height: 36})
	m.focus = FocusSidebar
	m.sidebar.selected = groupsIndex
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = runCmd(next.(model), cmd)
	if !m.groups.open || m.groups.busy || len(m.groups.records) != 1 || m.sidebar.groupCount != 1 {
		t.Fatalf("Enter on Groups should open and load the groups: open %v, %d loaded, count %d", m.groups.open, len(m.groups.records), m.sidebar.groupCount)
	}

	view := ansi.Strip(m.View())
	if !strings.Contains(view, "◇ Groups (1)") || !strings.Contains(view, g.Title) || !strings.Contains(view, "Untriaged") {
		t.Fatalf("Groups should show its cards beside the sidebar:\n%s", view)
	}

	m = press(m, "esc")
	if m.groups.open || m.focus != FocusSidebar {
		t.Fatal("Esc should go back to the sidebar")
	}
}

// The documented ways to move through a long body: j/k by the line, Ctrl-D/Ctrl-U by the half screen, g/G to the ends.
func TestContentScrollKeys(t *testing.T) {
	m := testPRModel()
	offset := func() int {
		vp := m.detail.activeViewport()
		if vp == nil {
			t.Fatal("the opened item should have a section to scroll")
		}

		return vp.YOffset
	}

	if offset() != 0 {
		t.Fatal("an item opens at the top of its body")
	}

	m = press(m, "j")
	afterLine := offset()
	if afterLine == 0 {
		t.Fatal("j should scroll the content down a line")
	}

	m = press(m, "ctrl+d")
	afterHalf := offset()
	if afterHalf <= afterLine {
		t.Fatalf("Ctrl-D should scroll further than a line: %d then %d", afterLine, afterHalf)
	}

	m = press(m, "ctrl+u")
	if back := offset(); back >= afterHalf {
		t.Fatalf("Ctrl-U should scroll back up: %d then %d", afterHalf, back)
	}

	m = press(m, "G")
	bottom := offset()
	if bottom <= afterHalf {
		t.Fatalf("G should reach the bottom: %d", bottom)
	}

	if m = press(m, "g"); offset() != 0 {
		t.Fatalf("g should return to the top: %d", offset())
	}

	m = press(m, "k")
	if offset() != 0 {
		t.Fatal("k at the top should stay there")
	}
}
