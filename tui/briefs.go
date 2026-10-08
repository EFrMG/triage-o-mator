package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var briefSections = []struct {
	key, label string
}{
	{"master", "Master"},
	{"item", "Items"},
	{"work", "Groups & batches"},
}

type briefRecord struct {
	ID      string `json:"id"`
	Section string `json:"section"`
	Type    string `json:"type"`
	Date    string `json:"date"`
	Title   string `json:"title"`
}

type briefListReply struct {
	Schema     int           `json:"schema_version"`
	Repository string        `json:"repository"`
	Records    []briefRecord `json:"records"`
}

type briefReadReply struct {
	Schema     int         `json:"schema_version"`
	Repository string      `json:"repository"`
	Record     briefRecord `json:"record"`
	Content    string      `json:"content"`
}

type briefMarkReply struct {
	Schema        int    `json:"schema_version"`
	Repository    string `json:"repository"`
	Applied       bool   `json:"applied"`
	PreviewSHA256 string `json:"preview_sha256"`
	Renames       []struct {
		Source string `json:"source"`
		Target string `json:"target"`
	} `json:"renames"`
}

type briefMarkMsg struct {
	root, repo string
	generation uint64
	apply      bool
	reply      *briefMarkReply
	err        error
}

// briefReadSuffix mirrors READ_SUFFIX in bin/_briefed.py: a brief marked read is renamed from NAME.md to NAME_READ.md.
const briefReadSuffix = "_READ.md"

type briefsMsg struct {
	root, repo string
	generation uint64
	id         string
	list       *briefListReply
	read       *briefReadReply
	err        error
}

type briefsUI struct {
	open, busy, reading  bool
	cards, returnToBrief bool
	originFocus          Focus
	section              int
	selected             [3]int
	cardSelected         int
	records              []briefRecord
	ticked               map[string]bool
	document             *briefReadReply
	items                []Key
	markIDs              []string
	markPreview          *briefMarkReply
	markViewport         viewport.Model
	requestedID          string
	viewport             viewport.Model
	renderedID           string
	renderedWidth        int
	renderedTheme        string
	problem              string
}

func briefMarkCommand(root, repo string, generation uint64, ids []string, apply bool, token string) tea.Cmd {
	return func() tea.Msg {
		msg := briefMarkMsg{root: root, repo: repo, generation: generation, apply: apply}
		args := []string{"--expected-repo", repo, "mark-read"}
		if apply {
			args = append(args, "--apply", "--preview-sha256", token)
		}
		args = append(args, ids...)
		out, err := runScript(root, "briefs", args...)
		if err == nil {
			var reply briefMarkReply
			err = json.Unmarshal([]byte(out), &reply)
			if err == nil && (reply.Schema != 1 || reply.Repository != repo || reply.Applied != apply || len(reply.Renames) != len(ids) || len(reply.PreviewSHA256) != 64) {
				err = fmt.Errorf("brief rename preview changed repository or selection")
			}
			if err == nil {
				for i, row := range reply.Renames {
					if row.Source != ids[i] || row.Target != strings.TrimSuffix(ids[i], ".md")+briefReadSuffix {
						err = fmt.Errorf("brief rename preview changed target")
						break
					}
				}
			}
			msg.reply = &reply
		}
		msg.err = err
		return msg
	}
}

func briefsCommand(root, repo string, generation uint64, id string, process *readProcess) tea.Cmd {
	return func() tea.Msg {
		msg := briefsMsg{root: root, repo: repo, generation: generation, id: id}
		args := []string{"--expected-repo", repo}
		if id == "" {
			args = append(args, "list")
		} else {
			args = append(args, "read", id)
		}
		out, err := runReadScript(process, root, "briefs", args...)
		if err == nil && id == "" {
			var reply briefListReply
			err = json.Unmarshal([]byte(out), &reply)
			if err == nil {
				err = validateBriefList(reply, repo)
			}
			msg.list = &reply
		}
		if err == nil && id != "" {
			var reply briefReadReply
			err = json.Unmarshal([]byte(out), &reply)
			if err == nil {
				err = validateBriefRead(reply, repo, id)
			}
			msg.read = &reply
		}
		msg.err = err
		return msg
	}
}

func validBriefRecord(row briefRecord) bool {
	if row.ID == "" || len(row.ID) > 160 || strings.ContainsAny(row.ID, "/\\") || row.Date == "" || len(row.Date) != 10 || row.Title == "" || len([]byte(row.Title)) > 960 {
		return false
	}
	switch row.Section {
	case "master":
		return row.Type == "master"
	case "item":
		return row.Type == "issue" || row.Type == "pr"
	case "work":
		return row.Type == "group" || row.Type == "batch"
	}
	return false
}

func validateBriefList(reply briefListReply, repo string) error {
	if reply.Schema != 1 || reply.Repository != repo || len(reply.Records) > 5000 {
		return fmt.Errorf("brief list changed repository or exceeded bounds")
	}
	seen := map[string]bool{}
	for _, row := range reply.Records {
		if !validBriefRecord(row) || seen[row.ID] {
			return fmt.Errorf("brief list contains an invalid or repeated entry")
		}
		seen[row.ID] = true
	}
	return nil
}

func validateBriefRead(reply briefReadReply, repo, id string) error {
	if reply.Schema != 1 || reply.Repository != repo || !validBriefRecord(reply.Record) || reply.Record.ID != id ||
		len(reply.Content) > 1024*1024 || strings.TrimSpace(reply.Content) == "" {
		return fmt.Errorf("brief content changed repository, target or size")
	}
	return nil
}

func (m model) startBriefsRead(id string) (tea.Model, tea.Cmd) {
	m.briefsLifecycle.stop()
	m.briefsGeneration++
	m.briefs.busy = true
	m.briefs.problem = ""
	if id != "" {
		m.briefs.reading = true
		m.briefs.cards = false
		m.briefs.document = nil
		m.briefs.items = nil
		m.briefs.requestedID = id
		m.briefs.renderedID = ""
	} else {
		m.briefs.markIDs = nil
		m.briefs.markPreview = nil
	}
	if m.briefsLifecycle == nil {
		m.briefsLifecycle = &readLifecycle{}
	}
	m.briefsLifecycle.current = &readProcess{}
	return m, briefsCommand(m.installRoot, m.repo, m.briefsGeneration, id, m.briefsLifecycle.current)
}

func (m model) openBriefs() (tea.Model, tea.Cmd) {
	m.briefs = briefsUI{open: true, originFocus: m.focus, viewport: viewport.New(), markViewport: viewport.New(), ticked: map[string]bool{}}
	m.sidebar.selected = briefsIndex
	return m.startBriefsRead("")
}

func (m model) finishBriefs(msg briefsMsg) (tea.Model, tea.Cmd) {
	if !m.briefs.open || msg.root != m.installRoot || msg.repo != m.repo || msg.generation != m.briefsGeneration {
		return m, nil
	}
	m.briefs.busy = false
	if msg.err != nil {
		m.briefs.problem = "Couldn't read saved briefs. Press r to retry; ! shows details."
		m.recordError("Brief read unavailable", msg.err)
		return m, nil
	}
	if msg.list != nil {
		m.briefs.records = msg.list.Records
		present := map[string]bool{}
		for _, row := range m.briefs.records {
			present[row.ID] = true
		}
		for id := range m.briefs.ticked {
			if !present[id] {
				delete(m.briefs.ticked, id)
			}
		}
		for section := range m.briefs.selected {
			count := len(m.briefsRows(section))
			m.briefs.selected[section] = minInt(m.briefs.selected[section], maxInt(count-1, 0))
		}
	}
	if msg.read != nil {
		m.briefs.document = msg.read
		m.layoutBriefs()
	}
	return m, nil
}

func (m model) briefMarkTargets() []string {
	var ids []string
	for _, row := range m.briefs.records {
		if m.briefs.ticked[row.ID] {
			ids = append(ids, row.ID)
		}
	}
	if len(ids) > 0 {
		return ids
	}

	rows := m.briefsRows(m.briefs.section)
	if len(rows) > 0 {
		return []string{rows[m.briefs.selected[m.briefs.section]].ID}
	}
	return nil
}

func (m model) startBriefMark(ids []string, apply bool, token string) (tea.Model, tea.Cmd) {
	m.briefsGeneration++
	m.briefs.busy = true
	if !apply {
		m.briefs.markIDs = ids
	}
	return m, briefMarkCommand(m.installRoot, m.repo, m.briefsGeneration, ids, apply, token)
}

func (m model) finishBriefMark(msg briefMarkMsg) (tea.Model, tea.Cmd) {
	if !m.briefs.open || msg.root != m.installRoot || msg.repo != m.repo || msg.generation != m.briefsGeneration {
		return m, nil
	}
	m.briefs.busy = false
	if msg.err != nil {
		m.recordError("Brief rename unavailable", msg.err)
		m.status = "Couldn't mark briefs read. Press ! for details; reload the list before retrying."
		m.briefs.markPreview = nil
		m.briefs.markIDs = nil
		if msg.apply {
			m.briefs.ticked = map[string]bool{}
			return m.startBriefsRead("")
		}
		return m, nil
	}
	if !msg.apply {
		m.briefs.markPreview = msg.reply
		m.layoutBriefs()
		return m, nil
	}

	m.status = fmt.Sprintf("Marked %s read.", pluralize(len(msg.reply.Renames), "brief", "briefs"))
	m.briefs.markPreview = nil
	m.briefs.markIDs = nil
	m.briefs.ticked = map[string]bool{}
	return m.startBriefsRead("")
}

func (m model) briefsRows(section int) []briefRecord {
	if section < 0 || section >= len(briefSections) {
		return nil
	}
	var rows []briefRecord
	for _, row := range m.briefs.records {
		if row.Section == briefSections[section].key {
			rows = append(rows, row)
		}
	}
	return rows
}

func (m *model) layoutBriefs() {
	if m.briefs.open && m.briefs.markPreview != nil {
		width := m.cardWidth()
		m.briefs.markViewport.SetWidth(width)
		m.briefs.markViewport.SetHeight(maxInt(m.mainHeight()-3, 1))
		lines := make([]string, len(m.briefs.markPreview.Renames))
		for i, row := range m.briefs.markPreview.Renames {
			lines[i] = fmt.Sprintf("%s → %s", row.Source, row.Target)
		}
		m.briefs.markViewport.SetContent(wrapText(strings.Join(lines, "\n"), width))
		return
	}
	if !m.briefs.open || m.briefs.document == nil {
		return
	}
	width := m.cardWidth()
	height := maxInt(m.mainHeight()-3, 1)
	offset := m.briefs.viewport.YOffset()
	m.briefs.viewport.SetWidth(width)
	m.briefs.viewport.SetHeight(height)
	if m.briefs.renderedID != m.briefs.document.Record.ID || m.briefs.renderedWidth != width || m.briefs.renderedTheme != currentTheme.Name {
		content, items := renderBriefMarkdown(m.briefs.document.Content, m.repo, width)
		m.briefs.viewport.SetContent(content)
		m.briefs.items = items
		m.briefs.viewport.SetYOffset(offset)
		m.briefs.renderedID = m.briefs.document.Record.ID
		m.briefs.renderedWidth = width
		m.briefs.renderedTheme = currentTheme.Name
	}
}

func (m model) briefsView() string {
	width, height := m.menuWidth(), m.mainHeight()
	if m.briefs.markPreview != nil {
		body := inset(titleBar("Mark briefs read", fmt.Sprintf("%d exact renames", len(m.briefs.markPreview.Renames)), width)) + "\n\n"
		body += inset(m.briefs.markViewport.View())
		return m.withSidebar(body, true)
	}
	if m.briefs.reading {
		title := "Reading brief…"
		if m.briefs.document != nil {
			title = singleLine(sanitize(m.briefs.document.Record.Title))
		}
		if m.briefs.cards {
			body := inset(titleBar("Brief items", title, width)) + "\n\n"
			cards := make([][2]string, len(m.briefs.items))
			for i, key := range m.briefs.items {
				name := fmt.Sprintf("%s #%d", strings.ToUpper(key.Kind[:1])+key.Kind[1:], key.Number)
				if it, ok := m.findItem(key); ok {
					cards[i] = [2]string{name + " · " + singleLine(sanitize(it.Title)), it.State}
				} else {
					cards[i] = [2]string{name, "Unavailable in the local ledger"}
				}
			}
			body += cardList(cards, m.briefs.cardSelected, m.cardWidth(), height-2)
			return m.withSidebar(body, true)
		}
		body := inset(titleBar("Briefs", title, width)) + "\n\n"
		switch {
		case m.briefs.problem != "":
			body += inset(m.briefs.problem)
		case m.briefs.document != nil:
			body += inset(m.briefs.viewport.View())
		default:
			body += inset("Reading saved Markdown…")
		}
		return m.withSidebar(body, true)
	}

	counts := [3]int{}
	for _, row := range m.briefs.records {
		for i, section := range briefSections {
			if row.Section == section.key {
				counts[i]++
			}
		}
	}
	tabs := make([]string, len(briefSections))
	for i, section := range briefSections {
		label := fmt.Sprintf("%s (%d)", section.label, counts[i])
		if i == m.briefs.section {
			label = lipgloss.NewStyle().Foreground(focusedBorderColor).Bold(true).Render(label)
		} else {
			label = mutedText(label)
		}
		tabs[i] = label
	}
	subtitle := "saved for " + m.repo
	if len(m.briefs.ticked) > 0 {
		subtitle += fmt.Sprintf(" · %d ticked", len(m.briefs.ticked))
	}
	body := inset(titleBar("Briefs", subtitle, width)) + "\n" + inset(strings.Join(tabs, "  ")) + "\n\n"
	if m.briefs.problem != "" {
		body += inset(m.briefs.problem)
		return m.withSidebar(body, true)
	}
	if m.briefs.busy {
		message := "Reading saved briefs…"
		if len(m.briefs.markIDs) > 0 {
			message = "Checking exact rename targets…"
		}
		body += inset(message)
		return m.withSidebar(body, true)
	}
	rows := m.briefsRows(m.briefs.section)
	if len(rows) == 0 {
		empty := []string{"No master briefs yet.", "No item briefs yet.", "No group or batch briefs yet."}
		body += inset(empty[m.briefs.section])
		return m.withSidebar(body, true)
	}
	cards := make([][2]string, len(rows))
	for i, row := range rows {
		label := strings.ToUpper(row.Type[:1]) + row.Type[1:]
		title := singleLine(sanitize(row.Title))
		if m.briefs.ticked[row.ID] {
			title = "✓ " + title
		}
		cards[i] = [2]string{title, row.Date + " · " + label}
	}
	body += cardList(cards, m.briefs.selected[m.briefs.section], m.cardWidth(), height-3)
	return m.withSidebar(body, true)
}

func (m model) handleBriefsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.briefs.markPreview != nil {
		if m.briefs.busy {
			return m, nil
		}
		switch msg.String() {
		case "esc", "h", "left":
			m.briefs.markPreview = nil
			m.briefs.markIDs = nil
		case "j", "down":
			m.briefs.markViewport.ScrollDown(1)
		case "k", "up":
			m.briefs.markViewport.ScrollUp(1)
		case "ctrl+d":
			m.briefs.markViewport.ScrollDown(maxInt(m.briefs.markViewport.Height()/2, 1))
		case "ctrl+u":
			m.briefs.markViewport.ScrollUp(maxInt(m.briefs.markViewport.Height()/2, 1))
		case "d":
			return m.startBriefMark(m.briefs.markIDs, true, m.briefs.markPreview.PreviewSHA256)
		}
		return m, nil
	}
	switch msg.String() {
	case "q":
		return m.requestQuit()
	case "?":
		m.showHelp = !m.showHelp
		return m, nil
	case "esc", "h", "left":
		if m.briefs.reading {
			if m.briefs.cards {
				m.briefs.cards = false
				return m, nil
			}
			m.briefs.reading = false
			m.briefs.busy = false
			m.briefs.requestedID = ""
			m.briefs.problem = ""
			m.briefsLifecycle.stop()
			m.briefsGeneration++
			return m, nil
		}
		if msg.String() != "left" && len(m.briefs.ticked) > 0 {
			m.briefs.ticked = map[string]bool{}
			return m, nil
		}
		m.briefsLifecycle.stop()
		m.briefsGeneration++
		focus := m.briefs.originFocus
		m.briefs = briefsUI{}
		m.focus = focus
		if focus == FocusList {
			m.showList()
		}
		return m, nil
	case "r":
		if m.briefs.reading {
			return m.startBriefsRead(m.briefs.requestedID)
		}
		return m.startBriefsRead("")
	case "t":
		m.openThemePicker()
		return m, nil
	}
	if m.briefs.busy || m.briefs.problem != "" {
		return m, nil
	}
	if m.briefs.reading {
		if m.briefs.cards {
			switch msg.String() {
			case "j", "down":
				m.briefs.cardSelected = minInt(m.briefs.cardSelected+1, maxInt(len(m.briefs.items)-1, 0))
			case "k", "up":
				m.briefs.cardSelected = maxInt(m.briefs.cardSelected-1, 0)
			case "g", "home":
				m.briefs.cardSelected = 0
			case "G", "end":
				m.briefs.cardSelected = maxInt(len(m.briefs.items)-1, 0)
			case "enter", "l", "right":
				key := m.briefs.items[m.briefs.cardSelected]
				it, ok := m.findItem(key)
				if !ok {
					m.status = "Item unavailable in the local ledger; refresh the repository first."
					return m, nil
				}

				m.commitDraftIfDirty()
				cmd := m.openItem(it)
				m.focus = FocusDetail
				m.briefs.open = false
				m.briefs.returnToBrief = true
				m.layout()
				return m, cmd
			}
			return m, nil
		}

		switch msg.String() {
		case "j", "down":
			m.briefs.viewport.ScrollDown(1)
		case "k", "up":
			m.briefs.viewport.ScrollUp(1)
		case "ctrl+d":
			m.briefs.viewport.ScrollDown(maxInt(m.briefs.viewport.Height()/2, 1))
		case "ctrl+u":
			m.briefs.viewport.ScrollUp(maxInt(m.briefs.viewport.Height()/2, 1))
		case "g", "home":
			m.briefs.viewport.SetYOffset(0)
		case "G", "end":
			m.briefs.viewport.SetYOffset(1 << 30)
		case "enter", "l", "right":
			if len(m.briefs.items) == 0 {
				m.status = "This brief has no linked issues or PRs in the selected repository."
				return m, nil
			}
			m.briefs.cards = true
			m.briefs.cardSelected = 0
		}
		return m, nil
	}
	switch msg.String() {
	case "H", "shift+tab":
		m.briefs.section = (m.briefs.section + len(briefSections) - 1) % len(briefSections)
	case "L", "tab":
		m.briefs.section = (m.briefs.section + 1) % len(briefSections)
	case "1", "2", "3":
		m.briefs.section = int(msg.String()[0] - '1')
	case "j", "down":
		m.briefs.selected[m.briefs.section] = minInt(m.briefs.selected[m.briefs.section]+1, maxInt(len(m.briefsRows(m.briefs.section))-1, 0))
	case "k", "up":
		m.briefs.selected[m.briefs.section] = maxInt(m.briefs.selected[m.briefs.section]-1, 0)
	case "g", "home":
		m.briefs.selected[m.briefs.section] = 0
	case "G", "end":
		m.briefs.selected[m.briefs.section] = maxInt(len(m.briefsRows(m.briefs.section))-1, 0)
	case "space":
		rows := m.briefsRows(m.briefs.section)
		if len(rows) > 0 {
			id := rows[m.briefs.selected[m.briefs.section]].ID
			m.briefs.ticked[id] = !m.briefs.ticked[id]
			if !m.briefs.ticked[id] {
				delete(m.briefs.ticked, id)
			}
			m.briefs.selected[m.briefs.section] = minInt(m.briefs.selected[m.briefs.section]+1, len(rows)-1)
		}
	case "d":
		ids := m.briefMarkTargets()
		if len(ids) > 0 {
			return m.startBriefMark(ids, false, "")
		}
	case "enter", "l", "right":
		rows := m.briefsRows(m.briefs.section)
		if len(rows) > 0 {
			return m.startBriefsRead(rows[m.briefs.selected[m.briefs.section]].ID)
		}
	}
	return m, nil
}

func (m model) clickBriefs(event tea.Mouse) (tea.Model, tea.Cmd) {
	if m.briefs.markPreview != nil {
		return m, nil
	}
	if m.briefs.reading {
		if !m.briefs.cards {
			return m, nil
		}
		index := mouseCardIndex(event.Y, 2, m.briefs.cardSelected, len(m.briefs.items), m.mainHeight()-2)
		if index < 0 {
			return m, nil
		}
		repeat := m.mouseTargetRepeat(fmt.Sprintf("brief-item:%s:%d", m.briefs.items[index].Kind, m.briefs.items[index].Number))
		if m.briefs.cardSelected == index && repeat {
			return m.handleBriefsKey(mouseKey("enter"))
		}
		m.briefs.cardSelected = index
		return m, nil
	}
	if m.briefs.busy || m.briefs.problem != "" {
		return m, nil
	}
	if event.Y == 2 {
		// The section labels share one row. Exact hit testing uses the rendered widths rather than fixed columns.
		x := event.X - m.mouseMainX() - 1
		for i, section := range briefSections {
			label := fmt.Sprintf("%s (%d)", section.label, len(m.briefsRows(i)))
			width := ansi.StringWidth(label) + 2
			if x >= 0 && x < width {
				m.briefs.section = i
				return m, nil
			}
			x -= width
		}
	}
	rows := m.briefsRows(m.briefs.section)
	index := mouseCardIndex(event.Y, 4, m.briefs.selected[m.briefs.section], len(rows), m.mainHeight()-3)
	if index < 0 {
		return m, nil
	}
	repeat := m.mouseTargetRepeat("brief:" + rows[index].ID)
	if m.briefs.selected[m.briefs.section] != index {
		m.briefs.selected[m.briefs.section] = index
		if event.Button != tea.MouseRight {
			return m, nil
		}
	}
	if event.Button == tea.MouseRight {
		return m.handleBriefsKey(mouseKey("space"))
	}
	if m.briefs.selected[m.briefs.section] == index && repeat {
		return m.handleBriefsKey(mouseKey("enter"))
	}
	return m, nil
}
