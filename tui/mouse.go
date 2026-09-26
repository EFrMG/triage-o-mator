package main

import (
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const mouseDoubleClickInterval = 500 * time.Millisecond

func (m *model) mouseRepeat(x, y int) bool {
	now := time.Now()
	repeat := m.lastMouseX == x && m.lastMouseY == y && now.Sub(m.lastMouseAt) < mouseDoubleClickInterval
	m.lastMouseX, m.lastMouseY, m.lastMouseAt = x, y, now
	return repeat
}

func (m *model) mouseTargetRepeat(target string) bool {
	now := time.Now()
	repeat := m.lastMouseTarget == target && now.Sub(m.lastMouseTargetAt) < mouseDoubleClickInterval
	m.lastMouseTarget, m.lastMouseTargetAt = target, now
	return repeat
}

func mouseKey(name string) tea.KeyPressMsg {
	switch name {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEsc}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "space":
		return tea.KeyPressMsg{Code: tea.KeySpace}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "ctrl+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
	case "ctrl+s":
		return tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
	case "ctrl+p":
		return tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}
	case "ctrl+d":
		return tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}
	case "ctrl+u":
		return tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
	}

	return tea.KeyPressMsg{Text: name}
}

func (m model) mousePress(name string) (tea.Model, tea.Cmd) {
	m.lastMouseTarget = ""
	return m.handleKey(mouseKey(name))
}

func (m model) handleFooterClick(x, row int) (tea.Model, tea.Cmd) {
	width := maxInt(m.width, 1)
	groups := m.footerGroups()
	type placedGroup struct {
		group footerGroup
		lines []string
		x     int
	}
	var pending []placedGroup
	currentWidth, currentRow := 0, 0
	flush := func() (string, bool) {
		if len(pending) == 0 {
			return "", false
		}
		lineWidth := currentWidth
		left := (width - lineWidth) / 2
		if row == currentRow {
			for _, placed := range pending {
				if action := m.footerGroupAction(placed.group, placed.lines[0], x-left-placed.x); action != "" {
					return action, true
				}
			}
		}
		currentRow++
		pending = nil
		currentWidth = 0
		return "", false
	}

	for _, group := range groups {
		lines := m.renderGroup(group, width)
		if len(lines) > 1 {
			if action, ok := flush(); ok {
				return m.mousePress(action)
			}
			for _, line := range lines {
				if row == currentRow {
					left := (width - ansi.StringWidth(line)) / 2
					if action := m.footerGroupAction(group, line, x-left); action != "" {
						return m.mousePress(action)
					}
				}
				currentRow++
			}
			continue
		}

		groupWidth := ansi.StringWidth(lines[0])
		if len(pending) > 0 && currentWidth+3+groupWidth > width {
			if action, ok := flush(); ok {
				return m.mousePress(action)
			}
		}
		if len(pending) > 0 {
			currentWidth += 3
		}
		pending = append(pending, placedGroup{group: group, lines: lines, x: currentWidth})
		currentWidth += groupWidth
	}
	if action, ok := flush(); ok {
		return m.mousePress(action)
	}
	return m, nil
}

func (m model) footerGroupAction(group footerGroup, line string, x int) string {
	plain := ansi.Strip(line)
	if x < 0 || x >= ansi.StringWidth(plain) {
		return ""
	}
	for _, h := range group.hints {
		if h.keys == "" {
			continue
		}
		part := h.keys
		if m.showHelp {
			part += " " + h.desc
		}
		for offset := 0; offset < len(plain); {
			found := strings.Index(plain[offset:], part)
			if found < 0 {
				break
			}
			start := offset + found
			end := start + len(part)
			if (start == 0 || plain[start-1] == ' ') && (end == len(plain) || plain[end] == ' ' || strings.HasPrefix(plain[end:], "│") || strings.HasPrefix(plain[end:], "·")) {
				left := ansi.StringWidth(plain[:start])
				if x >= left && x < left+ansi.StringWidth(part) {
					return mouseHintActionAt(h.keys, x-left)
				}
			}
			offset = start + len(part)
		}
	}
	return ""
}

func mouseHintActionAt(label string, x int) string {
	parts := strings.Split(label, "/")
	chosen := parts[0]
	position := 0
	for _, part := range parts {
		if x < position+ansi.StringWidth(part)+1 {
			chosen = part
			break
		}
		position += ansi.StringWidth(part) + 1
	}
	if strings.HasPrefix(parts[0], "Ctrl-") && len(chosen) == 1 {
		chosen = "Ctrl-" + chosen
	}
	switch chosen {
	case "↑":
		return "up"
	case "↓":
		return "down"
	case "←":
		return "left"
	case "→":
		return "right"
	case "Space":
		return "space"
	case "any key":
		return "esc"
	case "1–4", "1-4":
		return "1"
	}
	if strings.HasPrefix(chosen, "Ctrl-") {
		return "ctrl+" + strings.ToLower(strings.TrimPrefix(chosen, "Ctrl-"))
	}
	if chosen == "Shift-Tab" {
		return "shift+tab"
	}
	if len(chosen) == 1 || chosen == "Enter" || chosen == "Esc" || chosen == "Tab" {
		return strings.ToLower(chosen)
	}
	return ""
}

func (m model) mouseMainX() int {
	if m.width >= 100 && !m.noInstall() {
		return sidebarContentWidth + 4
	}

	return 0
}

func (m model) mouseHasSidebar() bool {
	return m.width >= 100 && !m.noInstall() && !m.comment.open && !m.lastError.open && !m.themePicker.open && !m.notificationPR.open && !m.dups.open && (m.groups.open || m.batches.open || m.notifications.open || m.attention.open || m.actionHistory.open || m.corpus.open || m.focus != FocusDetail)
}

func (m model) mouseSidebarRow(y int) int {
	boxHeight := lipgloss.Height(panelStyle(true).Width(sidebarContentWidth+4).Padding(1, 1).Render(m.sidebar.View(true)))
	start, padding := maxInt((m.mainHeight()+2-boxHeight)/2, 0), 2
	if boxHeight > m.mainHeight()+2 {
		start, padding = 0, 1
	}
	row := y - start - padding
	switch {
	case row >= 0 && row < len(tabs):
		return row
	case row >= len(tabs)+1 && row < len(tabs)+5:
		return row - 1
	case row == len(tabs)+6:
		return switchRepoIndex
	}

	return -1
}

func mouseCardIndex(y, first, selected, count, height int) int {
	visible := maxInt(height/cardHeight, 1)
	start := maxInt(minInt(selected-visible+1, count-visible), 0)
	if selected >= 0 && selected < start {
		start = selected
	}
	index := start + (y-first)/cardHeight
	if y < first || y >= first+visible*cardHeight || index < 0 || index >= count {
		return -1
	}

	return index
}

func (m model) handleMouseClick(event tea.Mouse) (tea.Model, tea.Cmd) {
	if !m.ready || event.X < 0 || event.X >= m.width || event.Y < 0 || event.Y >= m.height {
		return m, nil
	}
	if event.Button != tea.MouseLeft && event.Button != tea.MouseRight {
		return m, nil
	}

	repeat := m.mouseRepeat(event.X, event.Y)
	footerTop := m.height - lipgloss.Height(m.footerView())
	if m.width >= 60 && m.height >= 24 && event.Y == footerTop-1 && event.X >= m.width-ansi.StringWidth("? help") {
		m.showHelp = !m.showHelp
		return m, nil
	}
	if event.Y >= footerTop {
		return m.handleFooterClick(event.X, event.Y-footerTop)
	}
	if m.width < 60 || m.height < 24 {
		return m, nil
	}
	if event.Y >= footerTop-1 || event.Y >= m.mainHeight()+2 {
		return m, nil
	}

	if m.confirmQuit {
		return m, nil
	}
	if m.lastError.open {
		return m, nil
	}
	if m.comment.open {
		return m, nil
	}
	if m.themePicker.open {
		return m.clickTheme(event, repeat)
	}
	if m.notificationPR.open || m.focus == FocusDetail && !m.groups.open && !m.batches.open && !m.dups.open && !m.notifications.open && !m.attention.open && !m.actionHistory.open && !m.corpus.open {
		return m.clickDetail(event, repeat)
	}
	if m.dups.open {
		return m.clickDuplicates(event, repeat)
	}

	mainX := m.mouseMainX()
	if m.mouseHasSidebar() && event.X < mainX {
		if row := m.mouseSidebarRow(event.Y); row >= 0 {
			if m.groups.editing != "" || m.batches.editing {
				return m, nil
			}
			for i := 0; i < 4 && (m.groups.open || m.batches.open || m.notifications.open || m.attention.open || m.actionHistory.open || m.corpus.open || m.editingRepo); i++ {
				next, _ := m.mousePress("esc")
				m = next.(model)
			}
			if m.groups.open || m.batches.open || m.notifications.open || m.attention.open || m.actionHistory.open || m.corpus.open || m.editingRepo {
				return m, nil
			}
			m.sidebar.selected = row
			m.overview = false
			return m, m.enterSidebarSelection()
		}
		return m, nil
	}
	if event.X < mainX+1 {
		return m, nil
	}
	if m.editingRepo {
		return m.clickRepo(event, repeat)
	}

	switch {
	case m.groups.open:
		return m.clickGroups(event, repeat)
	case m.batches.open:
		return m.clickBatches(event, repeat)
	case m.notifications.open:
		return m.clickNotifications(event, repeat)
	case m.attention.open, m.actionHistory.open:
		return m.clickReader(event, repeat)
	case m.corpus.open:
		return m, nil
	case m.focus == FocusList && m.listReady:
		return m.clickList(event, repeat)
	case m.focus == FocusSidebar && m.width < 100 && !m.overview:
		if row := m.mouseSidebarRow(event.Y); row >= 0 {
			m.sidebar.selected = row
			return m, m.enterSidebarSelection()
		}
	}

	return m, nil
}

func (m model) clickList(event tea.Mouse, _ bool) (tea.Model, tea.Cmd) {
	if event.Y == 3 {
		if m.searching {
			return m, nil
		}
		return m.mousePress("/")
	}
	page := m.list.Paginator.Page
	perPage := m.list.Paginator.PerPage
	first := page * perPage
	index := first + (event.Y-5)/cardHeight
	if event.Y < 5 || index < first || index >= len(m.list.Items()) || index >= first+perPage {
		return m, nil
	}
	repeat := m.mouseTargetRepeat("list:" + m.list.Items()[index].FilterValue())

	if m.list.Index() != index {
		m.list.Select(index)
		if event.Button != tea.MouseRight {
			return m, nil
		}
	}
	if event.Button == tea.MouseRight {
		return m.mousePress("space")
	}
	if repeat {
		return m.mousePress("enter")
	}

	return m, nil
}

func (m model) clickDetail(event tea.Mouse, repeat bool) (tea.Model, tea.Cmd) {
	if !m.form.pick.open && (event.Y == 4 || event.Y == 5) {
		x := event.X - 2
		for i := range m.detail.sections {
			label := m.detail.renderTabLabel(i, lipgloss.NewStyle())
			width := ansi.StringWidth(label) + 7
			if x >= 0 && x < width {
				if i == m.detail.active && repeat {
					return m.mousePress("enter")
				}
				m.detail.JumpSection(i)
				m.form.FocusField(fieldContent)
				return m, m.diffIfNeeded()
			}
			x -= width
		}
	}
	if event.Y < 6 || m.notificationPR.open || m.detail.full {
		return m, nil
	}

	formY, formX := 0, 2
	if m.sideBySide() {
		formY, formX = 6, 2+m.detail.width+3
	} else {
		formY = 6 + m.detail.height + 2
	}
	if event.X < formX || event.Y < formY {
		m.form.FocusField(fieldContent)
		return m, nil
	}
	row := event.Y - formY
	if m.form.pick.open && formFieldIsEnum(m.form.focused) {
		field := int(m.form.focused - fieldCategory)
		first := formY + field + 2
		start := maxInt(minInt(m.form.pick.cursor-dropdownRows/2, len(m.form.pick.options)-dropdownRows), 0)
		index := start + event.Y - first
		if event.Y >= first && event.Y < first+minInt(dropdownRows, len(m.form.pick.options)) && event.X >= formX+formLabelWidth && index < len(m.form.pick.options) {
			m.form.pick.cursor = index
			return m.mousePress("enter")
		}
		return m, nil
	}
	if row >= 0 && row <= 2 {
		field := formField(int(fieldCategory) + row)
		if m.form.focused == field && repeat {
			return m.mousePress("l")
		}
		m.form.FocusField(field)
	} else if row < lipgloss.Height(m.form.View(m.formPanelWidth())) {
		m.form.FocusField(fieldReason)
	}

	return m, nil
}

func (m model) clickTheme(event tea.Mouse, _ bool) (tea.Model, tea.Cmd) {
	if event.Y == 3 {
		if m.themePicker.searching {
			return m, nil
		}
		return m.mousePress("/")
	}
	shown := m.themePicker.shown()
	available := maxInt(m.mainHeight()-4, 1)
	start := maxInt(m.themePicker.selected-available+1, 0)
	index := start + event.Y - 5
	if event.Y < 5 || index >= len(shown) || index >= start+available {
		return m, nil
	}
	repeat := m.mouseTargetRepeat("theme:" + shown[index])

	if index == m.themePicker.selected && repeat {
		return m.mousePress("enter")
	}
	m.themePicker.selected = index
	m.previewSelectedTheme()
	return m, nil
}

func (m model) clickRepo(event tea.Mouse, _ bool) (tea.Model, tea.Cmd) {
	if m.installing.path != "" {
		return m, nil
	}
	if event.Y >= 3 && event.Y <= 5 {
		m.repoPick = -1
		m.repoInput.Focus()
		return m, nil
	}
	index := mouseCardIndex(event.Y, 9, m.repoPick, len(m.repoRecent), m.mainHeight()-10)
	if index < 0 {
		return m, nil
	}
	repeat := m.mouseTargetRepeat("repo:" + m.repoRecent[index].root + ":" + m.repoRecent[index].name)
	if m.repoPick == index && repeat {
		return m.mousePress("enter")
	}
	m.repoPick = index
	m.repoLastPick = index
	return m, nil
}

func (m model) clickBatches(event tea.Mouse, repeat bool) (tea.Model, tea.Cmd) {
	if m.batches.busy {
		return m, nil
	}
	if m.batches.editing {
		return m.clickBatchForm(event, repeat)
	}
	index := mouseCardIndex(event.Y, 3, m.batches.selected, len(m.batches.records), m.mainHeight()-2)
	if index < 0 {
		return m, nil
	}
	repeat = m.mouseTargetRepeat("batch:" + m.batches.records[index].ID)
	if m.batches.selected != index {
		m.batches.selected = index
		if event.Button != tea.MouseRight {
			return m, nil
		}
	}
	if event.Button == tea.MouseRight {
		return m.mousePress("space")
	}
	if repeat {
		return m.mousePress("enter")
	}
	return m, nil
}

func (m model) clickBatchForm(event tea.Mouse, repeat bool) (tea.Model, tea.Cmd) {
	if m.batches.pick.open {
		first := 5 + m.batches.field
		start := maxInt(minInt(m.batches.pick.cursor-dropdownRows/2, len(m.batches.pick.options)-dropdownRows), 0)
		index := start + event.Y - first
		if event.Y >= first && event.Y < first+minInt(dropdownRows, len(m.batches.pick.options)) && index < len(m.batches.pick.options) {
			m.batches.pick.cursor = index
			return m.mousePress("enter")
		}
		return m, nil
	}
	row := event.Y - 3
	if row < 0 || row >= batchFormFields {
		return m, nil
	}
	if m.batches.field == row && repeat && row > 0 {
		return m.mousePress("l")
	}
	m.batches.field = row
	if row == 0 {
		m.batches.size.Focus()
	} else {
		m.batches.size.Blur()
	}
	return m, nil
}

func (m model) clickGroups(event tea.Mouse, repeat bool) (tea.Model, tea.Cmd) {
	if m.groups.busy {
		return m, nil
	}
	if m.groups.editing != "" {
		return m.clickGroupForm(event, repeat)
	}
	if m.groups.detail {
		g := m.selectedGroup()
		if g == nil {
			return m, nil
		}
		contextHeight := minInt(6, maxInt(m.mainHeight()/3, 2))
		first := 4 + contextHeight
		index := mouseCardIndex(event.Y, first, m.groups.member, len(g.Members), m.mainHeight()-(first-1))
		if index < 0 {
			return m, nil
		}
		repeat := m.mouseTargetRepeat("group-member:" + g.ID + ":" + g.Members[index].Kind + ":" + strconv.Itoa(g.Members[index].Number))
		if m.groups.member != index {
			m.groups.member = index
			if event.Button != tea.MouseRight {
				return m, nil
			}
		}
		if event.Button == tea.MouseRight {
			return m.mousePress("space")
		}
		if repeat {
			return m.mousePress("enter")
		}
		return m, nil
	}

	index := mouseCardIndex(event.Y, 3, m.groups.selected, len(m.groups.records), m.mainHeight()-2)
	if index < 0 {
		return m, nil
	}
	repeat = m.mouseTargetRepeat("group:" + m.groups.records[index].ID)
	if m.groups.selected == index && repeat {
		return m.mousePress("enter")
	}
	m.groups.selected = index
	return m, nil
}

func (m model) clickGroupForm(event tea.Mouse, repeat bool) (tea.Model, tea.Cmd) {
	if m.groups.pick.open {
		first := 5 + m.groups.field
		start := maxInt(minInt(m.groups.pick.cursor-dropdownRows/2, len(m.groups.pick.options)-dropdownRows), 0)
		index := start + event.Y - first
		if event.Y >= first && event.Y < first+minInt(dropdownRows, len(m.groups.pick.options)) && index < len(m.groups.pick.options) {
			m.groups.pick.cursor = index
			return m.mousePress("enter")
		}
		return m, nil
	}
	row := event.Y - 3
	if row < 0 || row >= len(m.groups.inputs) {
		return m, nil
	}
	if m.groups.field == row && repeat && m.onGroupStatusField() {
		return m.mousePress("l")
	}
	m.groups.inputs[m.groups.field].Blur()
	m.groups.field = row
	m.groups.inputs[row].Focus()
	return m, nil
}

func (m model) clickDuplicates(event tea.Mouse, _ bool) (tea.Model, tea.Cmd) {
	if m.dups.busy {
		return m, nil
	}
	if event.Y >= 3 && event.Y < 7 {
		repeat := m.mouseTargetRepeat("duplicate:source")
		m.dups.selected = sourceRow
		if repeat {
			return m.mousePress("enter")
		}
		return m, nil
	}
	index := mouseCardIndex(event.Y, 8, m.dups.selected, len(m.dupCandidates()), m.mainHeight()-7)
	if index < 0 {
		return m, nil
	}
	candidate := m.dupCandidates()[index]
	repeat := m.mouseTargetRepeat("duplicate:" + candidate.Kind + ":" + strconv.Itoa(candidate.Number))
	if m.dups.selected != index {
		m.dups.selected = index
		if event.Button != tea.MouseRight {
			return m, nil
		}
	}
	if event.Button == tea.MouseRight {
		return m.mousePress("space")
	}
	if repeat {
		return m.mousePress("enter")
	}
	return m, nil
}

func (m model) clickNotifications(event tea.Mouse, _ bool) (tea.Model, tea.Cmd) {
	n := m.notifications
	if n.busy || n.problem != "" {
		return m, nil
	}
	choices := n.choices()
	if len(choices) == 0 {
		return m, nil
	}
	selected := minInt(n.selected, len(choices)-1)
	line := 5 // title, blank, description, blank, Needs attention
	hasNeeds := false
	for _, choice := range choices {
		hasNeeds = hasNeeds || n.choiceNeeds(choice)
	}
	if !hasNeeds {
		line++
	}

	starts := make([]int, len(choices))
	for i, choice := range choices {
		if n.choiceNeeds(choice) {
			starts[i] = line
			line += cardHeight
		}
	}
	line += 2 // blank and Past actions heading
	if n.tracked != nil && n.tracked.Total == 0 {
		line++
	}
	hasPast := false
	for i, choice := range choices {
		if !n.choiceNeeds(choice) {
			hasPast = true
			starts[i] = line
			line += cardHeight
		}
	}
	if !hasPast {
		line++
	}
	offset := minInt(maxInt(0, starts[selected]-m.mainHeight()/2), maxInt(line+1-m.mainHeight(), 0))
	clicked := event.Y - 1 + offset
	for i, start := range starts {
		if clicked < start || clicked >= start+cardHeight {
			continue
		}
		repeat := m.mouseTargetRepeat("notification:" + choices[i].kind + ":" + strconv.Itoa(choices[i].row))
		if n.selected == i && repeat {
			return m.mousePress("enter")
		}
		m.notifications.selected = i
		return m, nil
	}
	return m, nil
}

func (m model) clickReader(event tea.Mouse, _ bool) (tea.Model, tea.Cmd) {
	var count, selected, scroll int
	var note string
	if m.attention.open {
		ui := m.attention
		if ui.busy || ui.page == nil {
			return m, nil
		}
		count = len(ui.page.Rows) + boolInt(ui.page.Pagination.Offset > 0) + boolInt(ui.page.Pagination.Next != nil)
		selected, scroll = ui.selected, ui.scroll
		note = "Saved closure note and later comments. Excerpts are shown in each card; full sources remain in the offline cache."
	} else {
		ui := m.actionHistory
		if ui.busy || ui.page == nil {
			return m, nil
		}
		count = len(ui.page.Rows) + boolInt(ui.page.Pagination.Offset > 0) + boolInt(ui.page.Pagination.Next != nil)
		selected, scroll = ui.selected, ui.scroll
		note = "Imported explanations are attributed claims; closure operation and actor may be unknown. Each card shows its explanation."
	}
	if count == 0 {
		return m, nil
	}

	cardStart := strings.Count(ansi.Wrap(note, m.menuWidth()-2, ""), "\n") + 4
	height := maxInt(m.mainHeight()-1, 1)
	offset := minInt(maxInt(0, cardStart+selected*cardHeight-height/2+scroll), maxInt(cardStart+count*cardHeight+1-height, 0))
	index := (event.Y - 2 + offset - cardStart) / cardHeight
	if event.Y < 2 || event.Y-2+offset < cardStart || index < 0 || index >= count {
		return m, nil
	}
	target := "reader:action:"
	if m.attention.open {
		target = "reader:attention:"
	}
	repeat := m.mouseTargetRepeat(target + strconv.Itoa(index))
	if index == selected && repeat {
		return m.mousePress("enter")
	}
	if m.attention.open {
		m.attention.selected, m.attention.scroll = index, 0
	} else {
		m.actionHistory.selected, m.actionHistory.scroll = index, 0
	}
	return m, nil
}

func (m model) handleMouseWheel(event tea.Mouse) (tea.Model, tea.Cmd) {
	if !m.ready || m.width < 60 || m.height < 24 || event.X < 0 || event.X >= m.width || event.Y < 0 || event.Y >= m.height || event.Y >= m.mainHeight()+2 {
		return m, nil
	}
	name := "down"
	if event.Button == tea.MouseWheelUp {
		name = "up"
	} else if event.Button != tea.MouseWheelDown {
		return m, nil
	}
	if m.mouseHasSidebar() && event.X < m.mouseMainX() {
		m.overview = false
		if name == "down" {
			m.sidebar.Next()
		} else {
			m.sidebar.Prev()
		}
		return m, nil
	}

	if m.lastError.open {
		for i := 0; i < 3; i++ {
			next, _ := m.handleErrorKey(mouseKey(name))
			m = next.(model)
		}
		return m, nil
	}
	if m.comment.open && m.comment.previewing {
		for i := 0; i < 3; i++ {
			m.comment.preview, _ = m.comment.preview.Update(mouseKey(name))
		}
		return m, nil
	}
	if m.comment.open {
		if name == "down" {
			m.comment.text.PageDown()
		} else {
			m.comment.text.PageUp()
		}
		return m, nil
	}
	if m.focus == FocusDetail && !m.groups.open && !m.batches.open && !m.dups.open && !m.notifications.open && !m.attention.open && !m.actionHistory.open {
		formX := 2 + m.detail.width + 3
		formY := 6 + m.detail.height + 2
		if !m.detail.full && !m.notificationPR.open && m.form.focused == fieldReason && (m.sideBySide() && event.X >= formX || !m.sideBySide() && event.Y >= formY) {
			if name == "down" {
				m.form.reason.PageDown()
			} else {
				m.form.reason.PageUp()
			}
			return m, nil
		}
		if name == "down" {
			m.detail.LineDown(3)
		} else {
			m.detail.LineUp(3)
		}
		return m, nil
	}

	var cmd tea.Cmd
	for i := 0; i < 3; i++ {
		var next tea.Model
		next, cmd = m.mousePress(name)
		m = next.(model)
	}
	return m, cmd
}
