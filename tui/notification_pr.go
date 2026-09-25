package main

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// A notification opens its subject with a fresh, read-only GitHub read. Keep the reader state and the former item tabs so Back returns to the selected card.
type notificationPRUI struct {
	open   bool
	key    Key
	former detailModel
}

func (m model) openNotificationItem(key Key) (tea.Model, tea.Cmd) {
	if key.Number < 1 || (key.Kind != "issue" && key.Kind != "pr") {
		return m, nil
	}

	m.notificationPR = notificationPRUI{open: true, key: key, former: m.detail}
	m.detail = newDetailModel()
	m.detail.SetItem(Item{Kind: key.Kind, Number: key.Number})
	m.detail.full = true
	m.detail.blockLegacy = true
	m.detail.notificationOnly = true
	m.detail.loading = true
	m.detail.JumpSection(1)
	if m.evidenceLifecycle == nil {
		m.evidenceLifecycle = &readLifecycle{}
	}
	m.evidenceLifecycle.stop()
	m.evidenceLifecycle.current = &readProcess{}
	m.evidenceRequest++
	m.status = fmt.Sprintf("Fetching %s #%d…", key.Kind, key.Number)
	return m, evidenceReadCmd(m.installRoot, m.repo, key, "refresh", m.evidenceRequest, m.detail.generation, m.evidenceLifecycle.current)
}

func (m model) finishNotificationPREvidence(msg evidenceReadMsg) (tea.Model, tea.Cmd) {
	if !m.notificationPR.open || msg.key != m.notificationPR.key || msg.key != m.detail.key || msg.generation != m.detail.generation {
		return m, nil
	}

	m.detail.loading = false
	if msg.err != nil {
		m.detail.loadErr = msg.err
		m.status = "Couldn't fetch item details. ! shows the error."
		m.recordError("Item read unavailable", msg.err)
		return m, nil
	}

	m.detail.item.Title, m.detail.item.State, m.detail.item.URL = msg.data.Title, msg.data.State, msg.data.URL
	m.detail.populate(msg.data)
	m.status = "Item details refreshed. Saved notification records may describe an earlier state."
	return m, nil
}

func (m model) handleNotificationPRKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "h", "left":
		m.evidenceLifecycle.stop()
		m.evidenceRequest++
		m.detail = m.notificationPR.former
		m.notificationPR = notificationPRUI{}
		m.status = ""
	case "q":
		m.evidenceLifecycle.stop()
		return m.requestQuit()
	case "?":
		m.showHelp = !m.showHelp
	case "tab", "l", "right":
		m.detail.CycleSection(1)
	case "shift+tab":
		m.detail.CycleSection(-1)
	case "1", "2", "3", "4":
		m.detail.JumpSection(int(msg.Runes[0] - '1'))
	case "j", "down":
		m.detail.LineDown(1)
	case "k", "up":
		m.detail.LineUp(1)
	case "g":
		m.detail.GotoTop()
	case "G":
		m.detail.GotoBottom()
	case "ctrl+d":
		m.detail.HalfPageDown()
	case "ctrl+u":
		m.detail.HalfPageUp()
	}

	return m, nil
}
