package main

import (
	"encoding/json"
	"fmt"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
)

const actionHistoryPageSize = 5

type actionHistoryRow struct {
	ID                string `json:"id"`
	Label             string `json:"label"`
	Preview           string `json:"preview"`
	HistoryCheckpoint string `json:"history_checkpoint"`
	Omitted           int    `json:"omitted_bytes"`
	LabelOmitted      int    `json:"label_omitted_bytes"`
	Number            int    `json:"number"`
	Selectable        bool   `json:"selectable"`
}

type actionHistoryPage struct {
	Schema     int    `json:"schema_version"`
	Policy     string `json:"policy"`
	Repository struct {
		Host string `json:"host"`
		Name string `json:"full_name"`
	} `json:"repository"`
	Section    string             `json:"section"`
	Number     int                `json:"number"`
	Checkpoint string             `json:"checkpoint"`
	Requests   int                `json:"requests"`
	Rows       []actionHistoryRow `json:"rows"`
	Pagination attentionWindow    `json:"pagination"`
}

// The reader opens one PR's imported closure explanations. Its source pages stay CLI-only.
type actionHistoryLocation struct {
	section, checkpoint string
	number, offset      int
}

type actionHistoryUI struct {
	open, busy       bool
	location         actionHistoryLocation
	page             *actionHistoryPage
	selected, scroll int
	problem          string
}

type actionHistoryMsg struct {
	root, repo string
	generation uint64
	page       actionHistoryPage
	err        error
}

func actionHistoryCommand(root, repo string, generation uint64, at actionHistoryLocation, process *readProcess) tea.Cmd {
	return func() tea.Msg {
		msg := actionHistoryMsg{root: root, repo: repo, generation: generation}
		args := []string{"--expected-repo", repo, "action-read", "--number", strconv.Itoa(at.number), "--section", at.section}
		if at.checkpoint != "" {
			args = append(args, "--checkpoint", at.checkpoint)
		}
		args = append(args, "--offset", strconv.Itoa(at.offset), "--limit", strconv.Itoa(actionHistoryPageSize))
		out, err := runReadScript(process, root, "cache", args...)
		if err == nil {
			err = json.Unmarshal([]byte(out), &msg.page)
		}
		if err == nil {
			err = validateActionHistoryPage(msg.page, at, repo)
		}
		msg.err = err
		return msg
	}
}

func validateActionHistoryPage(p actionHistoryPage, at actionHistoryLocation, repo string) error {
	bad := fmt.Errorf("action history response changed bindings or exceeded bounds; restart")
	if p.Schema != 1 || p.Policy != "action-history-reader-v1" || p.Repository.Name != repo || p.Repository.Host != "github.com" || p.Requests != 0 || p.Section != at.section || p.Number != at.number || !validCorpusID(p.Checkpoint) || (at.checkpoint != "" && at.checkpoint != p.Checkpoint) {
		return bad
	}
	if !validAttentionWindow(p.Pagination, at.offset, actionHistoryPageSize, true) || len(p.Rows) != p.Pagination.Returned {
		return bad
	}
	ids := map[string]bool{}
	for _, row := range p.Rows {
		if row.ID == "" || len(row.ID) > 100 || ids[row.ID] || len([]byte(row.Label)) > 240 || len([]byte(row.Preview)) > 1200 || row.Omitted < 0 || row.LabelOmitted < 0 {
			return bad
		}
		ids[row.ID] = true
		if at.section == "list" && (row.Number < 1 || row.ID != strconv.Itoa(row.Number) || (row.Selectable && !validCorpusID(row.HistoryCheckpoint))) {
			return bad
		}
	}
	return nil
}

func (m model) readActionHistory(at actionHistoryLocation) (tea.Model, tea.Cmd) {
	if m.actionHistoryLifecycle != nil {
		m.actionHistoryLifecycle.stop()
	}
	m.actionHistoryGeneration++
	m.actionHistory.open, m.actionHistory.busy = true, true
	m.actionHistory.location = at
	m.actionHistory.page, m.actionHistory.problem = nil, ""
	m.actionHistory.selected, m.actionHistory.scroll = 0, 0
	if m.actionHistoryLifecycle == nil {
		m.actionHistoryLifecycle = &readLifecycle{}
	}
	m.actionHistoryLifecycle.current = &readProcess{}
	return m, actionHistoryCommand(m.installRoot, m.repo, m.actionHistoryGeneration, at, m.actionHistoryLifecycle.current)
}

func (m model) finishActionHistory(msg actionHistoryMsg) (tea.Model, tea.Cmd) {
	if !m.actionHistory.open || msg.root != m.installRoot || msg.repo != m.repo || msg.generation != m.actionHistoryGeneration {
		return m, nil
	}
	m.actionHistory.busy = false
	if msg.err != nil {
		m.actionHistory.problem = "Offline action read unavailable or changed. o restarts the action list; Esc returns."
		m.recordError("Action history read unavailable", msg.err)
		return m, nil
	}
	m.actionHistory.page = &msg.page
	m.actionHistory.location.checkpoint = msg.page.Checkpoint
	return m, nil
}

func (m model) handleActionHistoryKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	ui := m.actionHistory
	at := ui.location
	switch msg.String() {
	case "?":
		m.showHelp = !m.showHelp
		return m, nil
	case "q":
		m.actionHistoryLifecycle.stop()
		m.actionHistoryGeneration++
		return m.requestQuit()
	case "esc", "x", "h":
		m.actionHistoryLifecycle.stop()
		m.actionHistoryGeneration++
		m.actionHistory = actionHistoryUI{}
		return m, nil
	}
	if ui.busy || ui.page == nil {
		return m, nil
	}
	count := len(ui.page.Rows) + boolInt(ui.page.Pagination.Offset > 0) + boolInt(ui.page.Pagination.Next != nil)
	switch msg.String() {
	case "j", "down":
		if count > 0 {
			m.actionHistory.selected = minInt(ui.selected+1, count-1)
			m.actionHistory.scroll = 0
		}
	case "k", "up":
		if count > 0 {
			m.actionHistory.selected = maxInt(0, ui.selected-1)
			m.actionHistory.scroll = 0
		}
	case "ctrl+d":
		m.actionHistory.scroll += maxInt(m.mainHeight()/2, 1)
	case "ctrl+u":
		m.actionHistory.scroll = maxInt(0, ui.scroll-maxInt(m.mainHeight()/2, 1))
	case "tab":
		if count > 0 {
			m.actionHistory.selected = (ui.selected + 1) % count
			m.actionHistory.scroll = 0
		}
	case "shift+tab":
		if count > 0 {
			m.actionHistory.selected = (ui.selected - 1 + count) % count
			m.actionHistory.scroll = 0
		}
	case "enter", "l", "right":
		if count == 0 {
			break
		}
		if ui.page.Pagination.Offset > 0 && ui.selected == 0 {
			at.offset = maxInt(0, at.offset-actionHistoryPageSize)
			return m.readActionHistory(at)
		}
		if ui.page.Pagination.Next != nil && ui.selected == count-1 {
			at.offset = *ui.page.Pagination.Next
			return m.readActionHistory(at)
		}
		return m.openNotificationItem(Key{Kind: "pr", Number: at.number})
	}
	return m, nil
}

func (m model) actionHistoryView() string {
	ui := m.actionHistory
	subtitle := fmt.Sprintf("PR #%d · Closure explanations · %s", ui.location.number, m.repo)
	note := "Imported explanations are attributed claims; closure operation and actor may be unknown. Each card shows its explanation."
	if ui.busy {
		return inset(titleBar("Notifications", subtitle, m.menuWidth())) + "\n\n" + inset("Reading retained action history…")
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
		rows = append(rows, notificationReaderCard{label: "Previous explanations", summary: "Show the preceding saved page", mark: cardMark{}})
	}
	for _, row := range p.Rows {
		rows = append(rows, notificationReaderCard{label: sanitize(row.Label), summary: firstReaderLine(humanActionPreview(row)), mark: cardMark{text: "CLOSURE", color: currentTheme.Info}})
	}
	if p.Pagination.Next != nil {
		rows = append(rows, notificationReaderCard{label: "More explanations", summary: "Show the next saved page", mark: cardMark{}})
	}
	return m.notificationReaderView("Closure explanations", subtitle, note, rows, ui.selected, ui.scroll, p.Pagination)
}
