package main

import (
	"encoding/json"
	"fmt"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
)

const attentionPageSize = 5

type attentionWindow struct {
	Total    int  `json:"total"`
	Offset   int  `json:"offset"`
	Returned int  `json:"returned"`
	Before   int  `json:"omitted_before"`
	After    int  `json:"omitted_after"`
	Next     *int `json:"next_offset"`
}

type attentionRow struct {
	ID              string `json:"id"`
	Label           string `json:"label"`
	Preview         string `json:"preview"`
	Omitted         int    `json:"omitted_bytes"`
	LabelOmitted    int    `json:"label_omitted_bytes"`
	Number          int    `json:"number"`
	Selectable      bool   `json:"selectable"`
	Attention       bool   `json:"attention"`
	WatchCheckpoint string `json:"watch_checkpoint"`
}

type attentionPage struct {
	Schema     int    `json:"schema_version"`
	Policy     string `json:"policy"`
	Repository struct {
		Host string `json:"host"`
		Name string `json:"full_name"`
	} `json:"repository"`
	Section    string          `json:"section"`
	Number     int             `json:"number"`
	Checkpoint string          `json:"checkpoint"`
	Requests   int             `json:"requests"`
	Rows       []attentionRow  `json:"rows"`
	Pagination attentionWindow `json:"pagination"`
}

// The reader opens one watched PR's saved discussion. Its other sections stay CLI-only.
type attentionLocation struct {
	section, checkpoint string
	number, offset      int
}

type attentionUI struct {
	open, busy       bool
	location         attentionLocation
	page             *attentionPage
	selected, scroll int
	problem          string
}

type attentionMsg struct {
	root, repo string
	generation uint64
	page       attentionPage
	err        error
}

func attentionCommand(root, repo string, generation uint64, at attentionLocation, process *readProcess) tea.Cmd {
	return func() tea.Msg {
		msg := attentionMsg{root: root, repo: repo, generation: generation}
		args := []string{"--expected-repo", repo, "attention-read", "--number", strconv.Itoa(at.number), "--section", at.section}
		if at.checkpoint != "" {
			args = append(args, "--checkpoint", at.checkpoint)
		}
		args = append(args, "--offset", strconv.Itoa(at.offset), "--limit", strconv.Itoa(attentionPageSize))
		out, err := runReadScript(process, root, "cache", args...)
		if err == nil {
			err = json.Unmarshal([]byte(out), &msg.page)
		}
		if err == nil {
			err = validateAttentionPage(msg.page, at, repo)
		}
		msg.err = err
		return msg
	}
}

func validAttentionWindow(p attentionWindow, offset, limit int, exact bool) bool {
	if p.Offset != offset || p.Total < offset || p.Returned < 0 || p.Returned > limit || p.Before != offset || p.After != p.Total-offset-p.Returned || p.After < 0 {
		return false
	}
	if exact && p.Returned != minInt(limit, p.Total-offset) {
		return false
	}
	end := offset + p.Returned
	return (p.After == 0 && p.Next == nil) || (p.After > 0 && p.Returned > 0 && p.Next != nil && *p.Next == end)
}

func validateAttentionPage(p attentionPage, at attentionLocation, repo string) error {
	bad := fmt.Errorf("attention response changed bindings or exceeded its bounds; restart the watch list")
	if p.Schema != 1 || p.Policy != "attention-reader-v1" || p.Repository.Name != repo || p.Repository.Host != "github.com" || p.Requests != 0 || p.Section != at.section || p.Number != at.number || !validCorpusID(p.Checkpoint) || (at.checkpoint != "" && p.Checkpoint != at.checkpoint) {
		return bad
	}
	if !validAttentionWindow(p.Pagination, at.offset, attentionPageSize, true) || len(p.Rows) != p.Pagination.Returned {
		return bad
	}
	ids := map[string]bool{}
	for _, row := range p.Rows {
		if row.ID == "" || len(row.ID) > 100 || ids[row.ID] || len([]byte(row.Preview)) > 1200 || len([]byte(row.Label)) > 240 || row.Omitted < 0 || row.LabelOmitted < 0 {
			return bad
		}
		ids[row.ID] = true
		if at.section == "list" && (row.Number < 1 || row.ID != strconv.Itoa(row.Number) || (row.Selectable && !validCorpusID(row.WatchCheckpoint))) {
			return bad
		}
	}
	return nil
}

func (m model) readAttention(at attentionLocation) (tea.Model, tea.Cmd) {
	m.attentionLifecycle.stop()
	m.attentionGeneration++
	m.attention.open, m.attention.busy = true, true
	m.attention.location = at
	m.attention.page, m.attention.problem = nil, ""
	m.attention.selected, m.attention.scroll = 0, 0
	if m.attentionLifecycle == nil {
		m.attentionLifecycle = &readLifecycle{}
	}
	m.attentionLifecycle.current = &readProcess{}
	return m, attentionCommand(m.installRoot, m.repo, m.attentionGeneration, at, m.attentionLifecycle.current)
}

func (m model) finishAttention(msg attentionMsg) (tea.Model, tea.Cmd) {
	if !m.attention.open || msg.root != m.installRoot || msg.repo != m.repo || msg.generation != m.attentionGeneration {
		return m, nil
	}
	m.attention.busy = false
	if msg.err != nil {
		m.attention.problem = "Offline read unavailable or changed. o restarts the watch list; Esc returns. No live fallback."
		m.recordError("Needs attention read unavailable", msg.err)
		return m, nil
	}
	m.attention.page = &msg.page
	m.attention.location.checkpoint = msg.page.Checkpoint
	return m, nil
}

func (m model) handleAttentionKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	ui := m.attention
	at := ui.location
	switch msg.String() {
	case "?":
		m.showHelp = !m.showHelp
		return m, nil
	case "q":
		m.attentionLifecycle.stop()
		m.attentionGeneration++
		m.attention.busy = false
		return m.requestQuit()
	case "esc", "x", "h":
		m.attentionLifecycle.stop()
		m.attentionGeneration++
		m.attention = attentionUI{}
		return m, nil
	}
	if ui.busy {
		return m, nil
	}
	count := 0
	if ui.page != nil {
		count = len(ui.page.Rows) + boolInt(ui.page.Pagination.Offset > 0) + boolInt(ui.page.Pagination.Next != nil)
	}
	switch msg.String() {
	case "j", "down":
		if count > 0 {
			m.attention.selected = minInt(ui.selected+1, count-1)
			m.attention.scroll = 0
		}
	case "k", "up":
		if count > 0 {
			m.attention.selected = maxInt(0, ui.selected-1)
			m.attention.scroll = 0
		}
	case "ctrl+d":
		m.attention.scroll += maxInt(m.mainHeight()/2, 1)
	case "ctrl+u":
		m.attention.scroll = maxInt(0, ui.scroll-maxInt(m.mainHeight()/2, 1))
	case "tab":
		if count > 0 {
			m.attention.selected = (ui.selected + 1) % count
			m.attention.scroll = 0
		}
	case "shift+tab":
		if count > 0 {
			m.attention.selected = (ui.selected - 1 + count) % count
			m.attention.scroll = 0
		}
	case "enter", "l", "right":
		if ui.page == nil || count == 0 {
			break
		}
		if ui.page.Pagination.Offset > 0 && ui.selected == 0 {
			at.offset = maxInt(0, at.offset-attentionPageSize)
			return m.readAttention(at)
		}
		if ui.page.Pagination.Next != nil && ui.selected == count-1 {
			at.offset = *ui.page.Pagination.Next
			return m.readAttention(at)
		}
		return m.openNotificationItem(Key{Kind: "pr", Number: at.number})
	}
	return m, nil
}

func (m model) attentionView() string {
	ui := m.attention
	subtitle := fmt.Sprintf("PR #%d · Discussion · %s", ui.location.number, m.repo)
	note := "Saved closure note and later comments. Excerpts are shown in each card; full sources remain in the offline cache."
	if ui.busy {
		return inset(titleBar("Notifications", subtitle, m.menuWidth())) + "\n\n" + inset("Reading retained evidence…")
	}
	if ui.problem != "" {
		return inset(titleBar("Notifications", subtitle, m.menuWidth())) + "\n\n" + inset(ui.problem)
	}
	if ui.page == nil {
		return ""
	}
	p := ui.page
	rows := make([]notificationReaderCard, 0, len(p.Rows))
	if p.Pagination.Offset > 0 {
		rows = append(rows, notificationReaderCard{label: "Previous comments", summary: "Show the preceding saved page", mark: cardMark{}})
	}
	for _, row := range p.Rows {
		mark := cardMark{text: "COMMENT", color: currentTheme.Info}
		if detail, ok := parseAttentionHistory(row); ok && detail.SuppliedClosureReference {
			mark = cardMark{text: "CLOSURE", color: currentTheme.Accent}
		}
		rows = append(rows, notificationReaderCard{label: attentionHistoryLabel(row), summary: attentionHistorySummary(row), mark: mark})
	}
	if p.Pagination.Next != nil {
		rows = append(rows, notificationReaderCard{label: "More comments", summary: "Show the next saved page", mark: cardMark{}})
	}
	return m.notificationReaderView("Discussion", subtitle, note, rows, ui.selected, ui.scroll, p.Pagination)
}
