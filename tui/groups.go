package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type GroupMember struct {
	Kind    string `json:"kind"`
	Number  int    `json:"number"`
	Notes   string `json:"notes"`
	AddedBy string `json:"added_by"`
}

func (member GroupMember) Key() Key { return Key{Kind: member.Kind, Number: member.Number} }

type Group struct {
	ID          string        `json:"id"`
	Title       string        `json:"title"`
	Description string        `json:"description"`
	Assignee    string        `json:"assignee"`
	Status      string        `json:"status"`
	Revision    int           `json:"revision"`
	CreatedBy   string        `json:"created_by"`
	UpdatedBy   string        `json:"updated_by"`
	UpdatedAt   string        `json:"updated_at"`
	Members     []GroupMember `json:"members"`
}

type groupUI struct {
	originFocus      Focus
	open, busy       bool
	records          []Group
	selected, member int
	detail           bool
	returnToGroup    bool
	// sources are the items Groups was opened for (the open item, or the ticked / hovered list items); b adds them to the selected group.
	sources []Key
	// ticked are the members ticked with Space inside a group, for bulk removal.
	ticked        map[Key]bool
	editing       string
	inputs        []textinput.Model
	field         int
	previewOffset int
	// confirm is the key ("d") whose second press will act; any other key disarms it.
	confirm string
	// pick is the Status field's list, opened with l / →.
	pick dropdown
	// exporting is what a running export is doing, e.g. "Full export of \"Wifi\"", and progress bin/group's last progress line.
	exporting, progress string
}

type groupsLoadedMsg struct {
	groups     []Group
	selectedID string
	status     string
	err        error
}

// All group persistence goes through bin/group, including optimistic revisions.
func groupsCmd(root string, args ...string) tea.Cmd {
	return func() tea.Msg { return groupsRun(root, nil, args...) }
}

// groupsRun runs bin/group with args (if any), passing its progress lines to onLine, then reloads the groups.
func groupsRun(root string, onLine func(string), args ...string) groupsLoadedMsg {
	selectedID, status := "", "Groups loaded."

	if len(args) > 0 {
		out, err := runScriptLines(root, "group", onLine, args...)
		if err != nil {
			return groupsLoadedMsg{err: err}
		}

		var group Group
		if json.Unmarshal([]byte(out), &group) == nil {
			selectedID = group.ID
		}

		status = "Group saved."
		switch args[0] {
		case "export":
			status = "Exported review packet to " + args[len(args)-1]
		case "delete":
			status = "Group deleted."
		}
	}

	out, err := runScript(root, "group", "list")
	if err != nil {
		return groupsLoadedMsg{err: err}
	}

	var groups []Group
	if err := json.Unmarshal([]byte(out), &groups); err != nil {
		return groupsLoadedMsg{err: err}
	}

	return groupsLoadedMsg{groups: groups, selectedID: selectedID, status: status}
}

// exportProgressMsg is one progress line from a running export; next waits for the one after it (or the export's groupsLoadedMsg).
type exportProgressMsg struct {
	line string
	next tea.Cmd
}

// exportGroupCmd runs an export in the background, reporting each line of bin/group's progress as an exportProgressMsg as it arrives, and the reloaded groups when it's done.
func exportGroupCmd(root string, args ...string) tea.Cmd {
	return func() tea.Msg {
		updates := make(chan tea.Msg)
		var next tea.Cmd
		next = func() tea.Msg { return <-updates }
		go func() {
			done := groupsRun(root, func(line string) { updates <- exportProgressMsg{line: line, next: next} }, args...)
			updates <- done
		}()

		return next()
	}
}

func (m model) openGroups() (tea.Model, tea.Cmd) {
	m.groups.originFocus = m.focus
	m.groups.open = true
	wasBusy := m.groups.busy
	m.groups.busy = true
	m.groups.ticked = map[Key]bool{}
	m.groups.sources = nil
	switch m.focus {
	case FocusDetail:
		m.groups.sources = []Key{m.detail.key}
	case FocusList:
		m.groups.sources = m.listTargetKeys()
	}

	if wasBusy {
		return m, nil
	}

	return m, groupsCmd(m.installRoot)
}

// exportStatus is the status line during an export: what it's doing, and how far it has come.
func (m model) exportStatus() string {
	if m.groups.progress == "" {
		return m.groups.exporting + "…"
	}

	return m.groups.exporting + ": " + m.groups.progress + "…"
}

func (m model) onExportProgress(msg exportProgressMsg) (tea.Model, tea.Cmd) {
	m.groups.progress = msg.line
	m.status = m.exportStatus()

	return m, msg.next
}

func (m model) onGroupsLoaded(msg groupsLoadedMsg) (tea.Model, tea.Cmd) {
	what := "Couldn't save the group"
	if m.groups.exporting != "" {
		what = "Couldn't export the group"
	}

	m.groups.busy = false
	m.groups.exporting, m.groups.progress = "", ""
	m.dups.busy = false
	if msg.err != nil {
		m.failErr(what, msg.err)

		return m, nil
	}

	old := m.selectedGroup()
	m.groups.records = msg.groups
	m.sidebar.groupCount = len(msg.groups)
	id := msg.selectedID
	if id == "" && old != nil {
		id = old.ID
	}

	for i, group := range msg.groups {
		if group.ID == id {
			m.groups.selected = i
		}
	}

	if m.groups.selected >= len(msg.groups) {
		m.groups.selected = maxInt(len(msg.groups)-1, 0)
	}
	if g := m.selectedGroup(); g != nil {
		m.groups.member = minInt(m.groups.member, maxInt(len(g.Members)-1, 0))
	}
	if msg.selectedID != "" {
		m.lastGroupID = msg.selectedID
	}

	// A deleted group cannot stay the quick-add target: B would run bin/group add against a file that is gone.
	if m.lastGroupID != "" && m.lastGroup() == nil {
		m.lastGroupID = ""
	}

	m.groups.editing = ""
	m.status = msg.status
	return m, nil
}

func (m model) selectedGroup() *Group {
	if m.groups.selected < 0 || m.groups.selected >= len(m.groups.records) {
		return nil
	}
	return &m.groups.records[m.groups.selected]
}

func (m *model) editGroup(mode string) {
	if g := m.selectedGroup(); g != nil && mode != "new" {
		m.lastGroupID = g.ID
	}

	values := []string{"", "", ""}
	if group := m.selectedGroup(); mode == "edit" && group != nil {
		// Status (a choice) sits before Assignee, so the last field is text and Enter there saves.
		values = []string{group.Title, group.Description, group.Status, group.Assignee}
	}

	if mode == "add" || mode == "notes" {
		values = []string{""}
		if group := m.selectedGroup(); group != nil {
			for _, member := range group.Members {
				if mode == "notes" && m.groups.member < len(group.Members) {
					values[0] = group.Members[m.groups.member].Notes
					break
				}

				if len(m.groups.sources) == 1 && member.Key() == m.groups.sources[0] {
					values[0] = member.Notes
				}
			}
		}
	}

	m.groups.inputs = make([]textinput.Model, len(values))
	for i, value := range values {
		input := textinput.New()
		// The editor's labels mark the focused field, as in the new-batch form.
		input.Prompt = ""
		input.CharLimit = 0
		input.SetValue(value)
		input.Width = maxInt(m.width-24, 1)
		themeInput(&input)

		m.groups.inputs[i] = input
	}

	m.groups.inputs[0].Focus()
	m.groups.editing, m.groups.field = mode, 0
	m.groups.pick.open = false
}

func (m model) saveGroupForm() (tea.Model, tea.Cmd) {
	g := m.selectedGroup()
	var args []string

	switch m.groups.editing {
	case "new", "edit":
		args = []string{"create"}
		if m.groups.editing == "edit" {
			if g == nil {
				return m, nil
			}

			args = []string{"update", g.ID, "--revision", strconv.Itoa(g.Revision), "--status", m.groups.inputs[groupStatusField].Value()}
		}

		assignee := m.groups.inputs[len(m.groups.inputs)-1].Value()
		args = append(args, "--title", m.groups.inputs[0].Value(), "--description", m.groups.inputs[1].Value(), "--assignee", assignee)

	case "add", "notes":
		if g == nil {
			return m, nil
		}

		targets := m.groups.sources
		if m.groups.editing == "notes" && m.groups.member < len(g.Members) {
			targets = []Key{g.Members[m.groups.member].Key()}
		}

		if len(targets) == 0 {
			return m, nil
		}

		m.groups.busy = true

		return m, groupBulkCmd(m.installRoot, *g, "add", targets, m.groups.inputs[0].Value(), m.reviewer)
	}

	args = append(args, "--by", m.reviewer)
	m.groups.busy = true

	return m, groupsCmd(m.installRoot, args...)
}

// groupStatuses are the values bin/group accepts, in the order the editor's Status field cycles through them.
var groupStatuses = []string{"draft", "ready", "archived"}

// groupStatusField is the Status field's index in the group editor ("edit" mode only); it's a choice, not free text.
const groupStatusField = 2

func (m model) onGroupStatusField() bool {
	return m.groups.editing == "edit" && m.groups.field == groupStatusField
}

func (m *model) cycleGroupStatus(delta int) {
	input := &m.groups.inputs[groupStatusField]
	i := 0
	for j, status := range groupStatuses {
		if status == input.Value() {
			i = j
		}
	}

	input.SetValue(groupStatuses[(i+delta+len(groupStatuses))%len(groupStatuses)])
}

// handleGroupEditorKey edits a group or a member's notes: Tab / Enter move down the fields and Enter on the last one saves; on the Status choice, j/k (or the arrows) change the value and l / → open the list of statuses.
func (m model) handleGroupEditorKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	last := len(m.groups.inputs) - 1
	move := func(delta int) {
		m.groups.inputs[m.groups.field].Blur()
		m.groups.field = (m.groups.field + delta + len(m.groups.inputs)) % len(m.groups.inputs)
		m.groups.inputs[m.groups.field].Focus()
	}

	onStatus := m.onGroupStatusField()
	// A choice isn't typing, so q quits there as everywhere else, list open or not.
	if onStatus && key.Matches(msg, keys.Quit) {
		return m.requestQuit()
	}

	if onStatus && m.groups.pick.open {
		if m.groups.pick.Key(msg) {
			m.groups.inputs[groupStatusField].SetValue(groupStatuses[m.groups.pick.cursor])
			move(1)
		}

		return m, nil
	}

	switch {
	case key.Matches(msg, keys.Cancel):
		m.groups.editing = ""
	case onStatus && key.Matches(msg, keys.OpenList):
		current := 0
		for i, s := range groupStatuses {
			if s == m.groups.inputs[groupStatusField].Value() {
				current = i
			}
		}

		m.groups.pick.Open(groupStatuses, current)
	case onStatus && key.Matches(msg, keys.ChoiceNext):
		move(1)
	case onStatus && key.Matches(msg, keys.ChoicePrev):
		move(-1)
	case key.Matches(msg, keys.FormSubmit), key.Matches(msg, keys.Confirm) && m.groups.field == last:
		return m.saveGroupForm()
	case key.Matches(msg, keys.FieldNext), key.Matches(msg, keys.Confirm):
		move(1)
	case key.Matches(msg, keys.FieldPrev):
		move(-1)
	case onStatus && key.Matches(msg, keys.ValueNext):
		m.cycleGroupStatus(1)
	case onStatus && key.Matches(msg, keys.ValuePrev):
		m.cycleGroupStatus(-1)
	case onStatus:
		// A choice, not text: typing into it would only produce a status bin/group rejects.
	default:
		var cmd tea.Cmd
		m.groups.inputs[m.groups.field], cmd = m.groups.inputs[m.groups.field].Update(msg)

		return m, cmd
	}

	return m, nil
}

func (m model) handleGroupKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.groups.busy {
		return m, nil
	}

	if m.groups.editing != "" {
		return m.handleGroupEditorKey(msg)
	}

	if m.groups.confirm != "" && !key.Matches(msg, keys.Delete) {
		m.groups.confirm = ""
		m.status = ""
	}

	g := m.selectedGroup()
	switch {
	case key.Matches(msg, keys.Back):
		if m.groups.detail {
			m.groups.detail = false

			return m, nil
		}

		m.groups.open = false
		m.groups.returnToGroup = false
		m.commitDraftIfDirty()
		m.focus = m.groups.originFocus
		if m.focus == FocusList {
			m.showList()
		}
		if m.focus == FocusDetail && len(m.groups.sources) > 0 && m.detail.key != m.groups.sources[0] {
			if it, ok := m.findItem(m.groups.sources[0]); ok {
				return m, m.openItem(it)
			}
		}
	case key.Matches(msg, keys.Quit):
		return m.requestQuit()
	case key.Matches(msg, keys.Help):
		m.showHelp = !m.showHelp
	case key.Matches(msg, keys.Theme):
		m.openThemePicker()
	case key.Matches(msg, keys.Refresh), key.Matches(msg, keys.RefreshFull):
		return m.startRefresh(key.Matches(msg, keys.RefreshFull))
	case key.Matches(msg, keys.New):
		m.editGroup("new")
	case key.Matches(msg, keys.Edit):
		// e edits the selection: a member's notes inside a group, the group itself in the list.
		if m.groups.detail && g != nil && len(g.Members) > 0 {
			m.editGroup("notes")
		} else if g != nil {
			m.editGroup("edit")
		}
	case key.Matches(msg, keys.Group):
		// b means "put into a group": here, the item Groups was opened from goes into the selected group.
		if g != nil && len(m.groups.sources) > 0 {
			m.editGroup("add")
		} else {
			m.status = "Open or tick items first, then b to add them to a group."
		}
	case key.Matches(msg, keys.Tick):
		if m.groups.detail && g != nil && m.groups.member < len(g.Members) {
			k := g.Members[m.groups.member].Key()
			m.groups.ticked[k] = !m.groups.ticked[k]
			m.groups.member = minInt(m.groups.member+1, len(g.Members)-1)
		}
	case key.Matches(msg, keys.Delete):
		if g == nil {
			return m, nil
		}

		// In the list the selection is the group itself, so d deletes it; inside a group the selection is a member, so d removes that instead.
		if !m.groups.detail {
			return m.requestDeleteGroup(*g)
		}

		if m.groups.member >= len(g.Members) {
			return m, nil
		}

		targets := m.tickedMembers(*g)
		if len(targets) == 0 {
			targets = []Key{g.Members[m.groups.member].Key()}
		}

		if m.groups.confirm != "d" {
			m.groups.confirm = "d"
			what := fmt.Sprintf("%s #%d", targets[0].Kind, targets[0].Number)
			if len(targets) > 1 {
				what = fmt.Sprintf("%d ticked members", len(targets))
			}

			m.status = fmt.Sprintf("Remove %s from \"%s\"? Decisions stay in the ledger. Press d again to remove.", what, g.Title)

			return m, nil
		}

		m.groups.confirm = ""
		m.groups.busy = true
		m.groups.ticked = map[Key]bool{}
		m.groups.member = maxInt(m.groups.member-len(targets), 0)

		return m, groupBulkCmd(m.installRoot, *g, "remove", targets, "", m.reviewer)
	case key.Matches(msg, keys.Export), key.Matches(msg, keys.ExportFull):
		if g != nil {
			args := []string{"export", g.ID}
			if key.Matches(msg, keys.ExportFull) {
				args = append(args, "--diff")
			}

			args = append(args, "--output", filepath.Join(DataDir(m.installRoot, m.repo), "exports", "group-"+g.ID+".md"))
			m.groups.busy = true
			m.groups.exporting, m.groups.progress = fmt.Sprintf("Exporting %q", g.Title), ""
			if key.Matches(msg, keys.ExportFull) {
				m.groups.exporting = fmt.Sprintf("Full export of %q", g.Title)
				m.groups.progress = fmt.Sprintf("fetching 0/%d", len(g.Members))
			}

			m.status = m.exportStatus()

			return m, exportGroupCmd(m.installRoot, args...)
		}
	case key.Matches(msg, keys.Down), key.Matches(msg, keys.Up), key.Matches(msg, keys.Top), key.Matches(msg, keys.Bottom):
		m.moveGroupCursor(msg)
	case key.Matches(msg, keys.HalfDown):
		m.groups.previewOffset += 4
	case key.Matches(msg, keys.HalfUp):
		m.groups.previewOffset = maxInt(0, m.groups.previewOffset-4)
	case key.Matches(msg, keys.Enter), key.Matches(msg, keys.Forward):
		if g == nil {
			return m, nil
		}

		m.lastGroupID = g.ID
		if !m.groups.detail {
			m.groups.detail = true
			m.groups.member = 0
			m.groups.previewOffset = 0

			return m, nil
		}

		if m.groups.member >= len(g.Members) {
			return m, nil
		}

		it, ok := m.findItem(g.Members[m.groups.member].Key())
		if !ok {
			m.status = "Item missing from ledger; refresh the repository first."

			return m, nil
		}

		m.commitDraftIfDirty()
		cmd := m.openItem(it)
		m.focus = FocusDetail
		m.groups.open, m.groups.returnToGroup = false, true

		return m, cmd
	}

	return m, nil
}

// moveGroupCursor moves through members inside a group, or through groups in the list.
func (m *model) moveGroupCursor(msg tea.KeyMsg) {
	move := func(cur, count int) int {
		last := maxInt(count-1, 0)
		switch {
		case key.Matches(msg, keys.Down):
			return minInt(cur+1, last)
		case key.Matches(msg, keys.Up):
			return maxInt(cur-1, 0)
		case key.Matches(msg, keys.Top):
			return 0
		}

		return last
	}

	if g := m.selectedGroup(); m.groups.detail && g != nil {
		m.groups.member = move(m.groups.member, len(g.Members))
	} else {
		m.groups.selected = move(m.groups.selected, len(m.groups.records))
		if chosen := m.selectedGroup(); chosen != nil {
			m.lastGroupID = chosen.ID
		}
	}

	m.groups.previewOffset = 0
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// groupsView is the Groups screen, beside the sidebar like Batches: the group editor, a group's members, or the groups as cards.
func (m model) groupsView() string {
	w, h := m.menuWidth(), m.mainHeight()
	switch g := m.selectedGroup(); {
	case m.groups.editing != "":
		return m.withSidebar(inset(m.groupEditorView(w)), true)
	case m.groups.detail && g != nil:
		return m.withSidebar(m.groupMembersView(*g, w, h), true)
	}

	subtitle := "no groups yet: n creates one"
	if n := len(m.groups.records); n > 0 {
		subtitle = pluralize(n, "group", "groups")
	}

	if n := len(m.groups.sources); n > 0 {
		subtitle += " · b adds " + pluralize(n, "item", "items") + " to the selected one"
	}

	if m.groups.busy {
		subtitle = "working…"
		if m.groups.exporting != "" {
			subtitle = m.exportStatus()
		}
	}

	cards := make([][2]string, len(m.groups.records))
	marks := make([]cardMark, len(m.groups.records))
	for i, g := range m.groups.records {
		cards[i] = [2]string{g.Title, m.groupSummary(g)}
		marks[i] = groupStatusMark(g.Status)
	}

	body := inset(titleBar("Groups", subtitle, w)) + "\n\n" + markedCardList(cards, marks, m.groups.selected, m.cardWidth(), h-2)

	return m.withSidebar(body, true)
}

// groupStatusMark tags a group card with its status: ready groups stand out for maintainers, archived ones recede.
func groupStatusMark(status string) cardMark {
	switch status {
	case "ready":
		return cardMark{status, currentTheme.Success}
	case "archived":
		return cardMark{status, currentTheme.Muted}
	}

	return cardMark{status, currentTheme.Foreground}
}

// groupSummary is a group card's second line: how far its members have come, who it's assigned to, and whether the items Groups was opened for are already in it.
func (m model) groupSummary(g Group) string {
	triaged, reviewed := 0, 0
	for _, member := range g.Members {
		if it, ok := m.findItem(member.Key()); ok && !it.Untriaged() {
			triaged++
			if it.Reviewed {
				reviewed++
			}
		}
	}

	parts := []string{pluralize(len(g.Members), "item", "items")}
	if len(g.Members) > 0 {
		parts = append(parts, fmt.Sprintf("%d triaged, %d reviewed", triaged, reviewed))
	}

	if g.Assignee != "" {
		parts = append(parts, "assignee: "+g.Assignee)
	}

	if n := countMembers(g, m.groups.sources); n > 0 && len(m.groups.sources) == 1 {
		parts = append(parts, "current item belongs here")
	} else if n > 0 {
		parts = append(parts, fmt.Sprintf("%d of the %d items belong here", n, len(m.groups.sources)))
	}

	return strings.Join(parts, " · ")
}

// groupMembersView is a group's context (its description, who made it, the selected member's notes; Ctrl-D/U scroll it) above its members, drawn as the item lists' cards.
func (m model) groupMembersView(g Group, w, h int) string {
	subtitle := g.Status + " · " + pluralize(len(g.Members), "member", "members")
	if g.Assignee != "" {
		subtitle += " · assignee: " + g.Assignee
	}

	if t := len(m.tickedMembers(g)); t > 0 {
		subtitle += " · " + pluralize(t, "ticked", "ticked")
	}

	if m.groups.busy {
		subtitle = "working…"
		if m.groups.exporting != "" {
			subtitle = m.exportStatus()
		}
	}

	context := []string{orPlaceholder(g.Description, "(no description)"), mutedText(fmt.Sprintf("created by %s · updated by %s, %s · revision %d", g.CreatedBy, g.UpdatedBy, shortDate(g.UpdatedAt), g.Revision))}
	if m.groups.member < len(g.Members) {
		member := g.Members[m.groups.member]
		context = append(context, "", fmt.Sprintf("Notes on #%d: %s", member.Number, orPlaceholder(member.Notes, "(none)")), mutedText("added by "+member.AddedBy))
	}

	vp := viewport.New(w, minInt(6, maxInt(h/3, 2)))
	vp.SetContent(wrapText(strings.Join(context, "\n"), w))
	vp.SetYOffset(m.groups.previewOffset)

	cards := make([][2]string, len(g.Members))
	marks := make([]cardMark, len(g.Members))
	for i, member := range g.Members {
		it, ok := m.findItem(member.Key())
		if !ok {
			cards[i] = [2]string{fmt.Sprintf("#%d (missing from the ledger: r fetches)", member.Number), member.Kind}

			continue
		}

		_, unsaved := m.drafts[member.Key()]
		li := listItem{Item: it, ticked: m.groups.ticked[member.Key()], unsaved: unsaved}
		cards[i] = [2]string{li.Title(), li.Description()}
		marks[i] = li.Mark()
	}

	top := inset(titleBar(g.Title, subtitle, w) + "\n\n" + vp.View())
	list := inset(mutedText("No members yet: b on an item or a list adds it."))
	if len(g.Members) > 0 {
		list = markedCardList(cards, marks, m.groups.member, m.cardWidth(), h-lipgloss.Height(top)-1)
	}

	return top + "\n\n" + list
}

// groupEditorView is the group editor, styled like the new-batch form: muted labels, the focused one marked, the Status list under its field.
func (m model) groupEditorView(w int) string {
	labels := []string{"Title", "Description", "Assignee"}
	title, subtitle := "New group", "a named set of issues and PRs to hand to maintainers together"
	switch m.groups.editing {
	case "edit":
		labels = []string{"Title", "Description", "Status", "Assignee"}
		title, subtitle = "Edit group", "ready is for maintainers to read first; it never approves the members' decisions"
	case "add":
		labels = []string{"Notes"}
		title, subtitle = "Add to group", "why these items belong here, for whoever reads the group"
	case "notes":
		labels = []string{"Notes"}
		title, subtitle = "Member notes", "why this item belongs here, for whoever reads the group"
	}

	if g := m.selectedGroup(); g != nil && m.groups.editing != "new" {
		title += ": " + g.Title
	}

	accent := lipgloss.NewStyle().Foreground(focusedBorderColor).Bold(true)
	rows := []string{titleBar(title, subtitle, w), ""}
	for i, input := range m.groups.inputs {
		label := mutedText(fmt.Sprintf("  %-12s", labels[i]))
		if i == m.groups.field {
			label = accent.Render(fmt.Sprintf("› %-12s", labels[i]))
		}

		input.Width = maxInt(w-17, 1)
		value := input.View()
		if m.groups.editing == "edit" && i == groupStatusField {
			value = input.Value()
			if i == m.groups.field {
				value = accent.Render(value)
			}
		}

		rows = append(rows, label+" "+value)
		if m.groups.editing == "edit" && i == groupStatusField && m.groups.pick.open {
			for _, line := range strings.Split(m.groups.pick.View(24), "\n") {
				rows = append(rows, strings.Repeat(" ", 15)+line)
			}
		}
	}

	return strings.Join(rows, "\n")
}

func (m model) lastGroup() *Group {
	for i := range m.groups.records {
		if m.groups.records[i].ID == m.lastGroupID {
			return &m.groups.records[i]
		}
	}
	return nil
}

func (m model) lastGroupLabel() string {
	if g := m.lastGroup(); g != nil {
		return "Last group: " + ansi.Truncate(g.Title, 40, "…")
	}

	return "Last group: none selected"
}

// Re-read the target group before adding, and never replace an existing note.
// Revision checks still reject a concurrent edit between this read and the write.
// quickAddGroupCmd adds keys to group id with empty notes, re-reading the group first so existing members (and their notes) are kept rather than overwritten.
func quickAddGroupCmd(root, id string, keys []Key, by string) tea.Cmd {
	return func() tea.Msg {
		out, err := runScript(root, "group", "show", id)
		if err != nil {
			return groupsLoadedMsg{err: err}
		}

		var group Group
		if err := json.Unmarshal([]byte(out), &group); err != nil {
			return groupsLoadedMsg{err: err}
		}

		var missing []Key
		for _, k := range keys {
			if countMembers(group, []Key{k}) == 0 {
				missing = append(missing, k)
			}
		}

		if len(missing) == 0 {
			msg := groupsCmd(root)().(groupsLoadedMsg)
			msg.selectedID = id
			msg.status = "Already in group: " + group.Title

			return msg
		}

		msg := groupBulkCmd(root, group, "add", missing, "", by)().(groupsLoadedMsg)
		if msg.err == nil && len(missing) < len(keys) {
			msg.status += fmt.Sprintf(" (%d already there)", len(keys)-len(missing))
		}

		return msg
	}
}

// groupBulkCmd runs bin/group add or remove for each key in turn, passing each call the revision the previous one produced, so the stale-revision check still guards against someone else's edit in between.
func groupBulkCmd(root string, group Group, op string, keys []Key, notes, by string) tea.Cmd {
	return func() tea.Msg {
		revision := group.Revision
		for i, k := range keys {
			args := []string{op, group.ID, "--revision", strconv.Itoa(revision), "--kind", k.Kind, "--number", strconv.Itoa(k.Number), "--by", by}
			if op == "add" {
				args = append(args, "--notes", notes)
			}

			out, err := runScript(root, "group", args...)
			if err != nil {
				if i > 0 {
					err = fmt.Errorf("%d of %d done, then: %w", i, len(keys), err)
				}

				return groupsLoadedMsg{err: err}
			}

			var saved Group
			if json.Unmarshal([]byte(out), &saved) == nil {
				revision = saved.Revision
			}
		}

		msg := groupsCmd(root)().(groupsLoadedMsg)
		msg.selectedID = group.ID
		verb := map[string]string{"add": "Added", "remove": "Removed"}[op]
		msg.status = fmt.Sprintf("%s %d item(s): %s", verb, len(keys), group.Title)
		if len(keys) == 1 {
			msg.status = fmt.Sprintf("%s %s #%d: %s", verb, keys[0].Kind, keys[0].Number, group.Title)
		}

		return msg
	}
}

// countMembers counts how many of keys are members of g.
func countMembers(g Group, keys []Key) int {
	n := 0
	for _, member := range g.Members {
		for _, k := range keys {
			if member.Key() == k {
				n++
			}
		}
	}

	return n
}

// requestDeleteGroup deletes the whole group on a second d: the group's file and the notes written into it go, and nothing else does. Every decision it collected was written to the ledger by bin/apply and stays there, and data/ is git-tracked, so the group itself can be brought back from history.
func (m model) requestDeleteGroup(g Group) (tea.Model, tea.Cmd) {
	if m.groups.confirm != "d" {
		m.groups.confirm = "d"
		members := fmt.Sprintf("%d members", len(g.Members))
		if len(g.Members) == 1 {
			members = "1 member"
		}

		m.status = fmt.Sprintf("Delete the group %q (%s) and its notes? Decisions stay in the ledger. Press d again to delete.", g.Title, members)

		return m, nil
	}

	m.groups.confirm = ""
	m.groups.busy = true
	m.groups.ticked = map[Key]bool{}

	return m, groupsCmd(m.installRoot, "delete", g.ID, "--revision", strconv.Itoa(g.Revision))
}

// tickedMembers returns g's ticked members, in member order.
func (m model) tickedMembers(g Group) []Key {
	var out []Key
	for _, member := range g.Members {
		if m.groups.ticked[member.Key()] {
			out = append(out, member.Key())
		}
	}

	return out
}

// quickAddLastGroup adds keys (the open item, or the ticked / hovered list items) to the last group used.
func (m model) quickAddLastGroup(keys []Key) (tea.Model, tea.Cmd) {
	if m.groups.busy || len(keys) == 0 {
		return m, nil
	}

	if m.lastGroupID == "" {
		m.status = "No last group yet: add something with b first."

		return m, nil
	}

	m.groups.busy = true
	m.status = "Adding to last group…"

	return m, quickAddGroupCmd(m.installRoot, m.lastGroupID, keys, m.reviewer)
}
