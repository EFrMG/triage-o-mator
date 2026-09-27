package main

import (
	"fmt"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// A notification opens its subject with a fresh, read-only GitHub read. Keep the reader state and the former item tabs so Back returns to the selected card.
type notificationPRUI struct {
	open   bool
	key    Key
	former detailModel
}

func (m model) openNotificationItem(key Key) (tea.Model, tea.Cmd) {
	return m.openNotificationItemAt(key, 1)
}

func (m model) openNotificationItemAt(key Key, section int) (tea.Model, tea.Cmd) {
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
	m.detail.JumpSection(section)
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
	m.status = ""
	return m, nil
}

// notificationActionItem uses only the selected fresh read for a comment target; the notification card and ledger may describe older or missing items.
func (m model) notificationActionItem() (Item, bool) {
	if !m.notificationPR.open || m.detail.key != m.notificationPR.key || m.detail.loading || m.detail.loadErr != nil || m.detail.enriched.Evidence == nil || m.detail.enriched.Evidence.Mode != "refresh" {
		return Item{}, false
	}

	it := m.detail.item
	if it.Key() != m.notificationPR.key || (it.State != "open" && it.State != "closed") {
		return Item{}, false
	}

	target, err := validateCommentTarget(it, m.repo)
	return it, err == nil && target.host == evidenceHost
}

func (m *model) refreshNotificationItem() tea.Cmd {
	if !m.notificationPR.open || m.detail.key != m.notificationPR.key {
		return nil
	}

	m.evidenceLifecycle.stop()
	m.evidenceLifecycle.current = &readProcess{}
	m.evidenceRequest++
	m.detail.loading = true
	m.detail.loadErr = nil
	return evidenceReadCmd(m.installRoot, m.repo, m.notificationPR.key, "refresh", m.evidenceRequest, m.detail.generation, m.evidenceLifecycle.current)
}

func (m model) openNotificationCommentAction(action string) (tea.Model, tea.Cmd) {
	it, ok := m.notificationActionItem()
	if !ok {
		m.warn("Current item details are unavailable. Reopen the item after it loads.")
		return m, nil
	}

	var next tea.Model
	var cmd tea.Cmd
	if action == "v" || action == "V" {
		next, cmd = m.openReopen([]Item{it})
	} else {
		next, cmd = m.openCommentForItem(it, action == "x" || action == "X")
	}

	m = next.(model)
	if !m.comment.open || action != "C" && action != "X" && action != "V" {
		return m, cmd
	}

	return m.startCommentEditor()
}

func (m model) handleNotificationPRKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, keys.TabPrev) {
		m.detail.CycleSection(-1)
		return m, nil
	}
	if key.Matches(msg, keys.TabNext) {
		m.detail.CycleSection(1)
		return m, nil
	}

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
	case "w":
		return m.startTracking(m.notificationPR.key)
	case "c", "C", "v", "V", "x", "X":
		return m.openNotificationCommentAction(msg.String())
	case "tab", "right":
		m.detail.CycleSection(1)
	case "shift+tab":
		m.detail.CycleSection(-1)
	case "1", "2", "3", "4":
		m.detail.JumpSection(int(msg.String()[0] - '1'))
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
