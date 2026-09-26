package main

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// A card may contain several script-owned records. Keep their updates separate and reload once all requested local changes finish.
type notificationItemOperation struct {
	script, source string
	args           []string
}

type notificationItemDoneMsg struct {
	root, repo, action string
	generation         uint64
	key                Key
	completed, total   int
	err                error
}

func (n notificationsUI) itemOperations(repo string, choice notificationChoice, action string) []notificationItemOperation {
	var operations []notificationItemOperation
	if choice.proposal >= 0 {
		row := n.proposals.Rows[choice.proposal]
		if action == "dismiss" || row.Needs {
			operations = append(operations, notificationItemOperation{script: "auto-close", source: "proposal", args: []string{"--expected-repo", repo, action, "--number", strconv.Itoa(row.Number), "--checkpoint", row.Checkpoint}})
		}
	}
	if choice.tracked >= 0 {
		row := n.tracked.Rows[choice.tracked]
		if action == "dismiss" {
			operations = append(operations, notificationItemOperation{script: "cache", source: "tracked comments", args: []string{"--expected-repo", repo, "track-remove", "--kind", choice.key.Kind, "--number", strconv.Itoa(choice.key.Number)}})
		} else if row.NewCount > 0 {
			operations = append(operations, notificationItemOperation{script: "cache", source: "tracked comments", args: []string{"--expected-repo", repo, "track-read", "--kind", choice.key.Kind, "--number", strconv.Itoa(choice.key.Number), "--checked-at", row.CheckedAt, "--new-count", strconv.Itoa(row.NewCount)}})
		}
	}
	if choice.attention >= 0 {
		row := n.attention.Rows[choice.attention]
		if action == "dismiss" || row.Selectable && n.watchNeeds(row) {
			checkpoint := row.WatchCheckpoint
			if !row.Selectable {
				checkpoint = n.attention.Checkpoint
			}
			operations = append(operations, notificationItemOperation{script: "cache", source: "watch", args: []string{"--expected-repo", repo, "notification-" + action, "--source", "watch", "--number", strconv.Itoa(row.Number), "--checkpoint", checkpoint}})
		}
	}
	if choice.closure >= 0 {
		row := n.closures.Rows[choice.closure]
		if action == "dismiss" || row.Selectable && n.actionNeeds(row) {
			checkpoint := row.HistoryCheckpoint
			if !row.Selectable {
				checkpoint = n.closures.Checkpoint
			}
			operations = append(operations, notificationItemOperation{script: "cache", source: "action", args: []string{"--expected-repo", repo, "notification-" + action, "--source", "action", "--number", strconv.Itoa(row.Number), "--checkpoint", checkpoint}})
		}
	}
	return operations
}

func notificationItemChangeCmd(root, repo string, generation uint64, choice notificationChoice, action string, operations []notificationItemOperation) tea.Cmd {
	return func() tea.Msg {
		msg := notificationItemDoneMsg{root: root, repo: repo, generation: generation, key: choice.key, action: action, total: len(operations)}
		for _, operation := range operations {
			if _, err := runScript(root, operation.script, operation.args...); err != nil {
				msg.err = fmt.Errorf("%s: %w", operation.source, err)
				return msg
			}
			msg.completed++
		}
		return msg
	}
}

func (m model) changeNotificationItem(choice notificationChoice, action string) (tea.Model, tea.Cmd) {
	if m.trackingBusy || choice.kind != "item" {
		return m, nil
	}
	operations := m.notifications.itemOperations(m.repo, choice, action)
	if len(operations) == 0 {
		return m, nil
	}
	m.trackingBusy = true
	return m, notificationItemChangeCmd(m.installRoot, m.repo, m.notificationsGeneration, choice, action, operations)
}

func (m model) openNotificationSource(choice notificationChoice, source string) (tea.Model, tea.Cmd) {
	if choice.kind != "item" {
		return m, nil
	}
	if source == "default" {
		source = "PR"
		switch {
		case choice.proposal >= 0:
			source = "proposal"
		case choice.attention >= 0:
			source = "watch"
		case choice.closure >= 0:
			source = "action"
		}
	}
	switch source {
	case "proposal":
		if choice.proposal >= 0 {
			row := m.notifications.proposals.Rows[choice.proposal]
			m.notifications.review = &autoCloseReview{}
			m.notifications.review.Plan.Repository = m.repo
			m.notifications.review.Plan.Proposals = []autoCloseRow{row}
			m.notifications.reviewKey = ""
			m.notifications.reviewScroll = 0
		}
	case "PR":
		return m.openNotificationItem(choice.key)
	case "watch":
		if choice.attention >= 0 {
			row := m.notifications.attention.Rows[choice.attention]
			if row.Selectable {
				return m.readAttention(attentionLocation{section: "history", number: row.Number, checkpoint: row.WatchCheckpoint})
			}
		}
	case "action":
		if choice.closure >= 0 {
			row := m.notifications.closures.Rows[choice.closure]
			if row.Selectable {
				return m.readActionHistory(actionHistoryLocation{section: "entries", number: row.Number, checkpoint: row.HistoryCheckpoint})
			}
		}
	}
	return m, nil
}

func (m model) finishNotificationItem(msg notificationItemDoneMsg) (tea.Model, tea.Cmd) {
	if msg.root != m.installRoot || msg.repo != m.repo {
		return m, nil
	}
	m.trackingBusy = false
	if !m.notifications.open || msg.generation != m.notificationsGeneration {
		return m, nil
	}
	if msg.err != nil {
		m.recordError("Couldn't update notification item", msg.err)
		m.status = fmt.Sprintf("Updated %d of %d records for %s #%d; inspect the item. ! shows details.", msg.completed, msg.total, msg.key.Kind, msg.key.Number)
	} else {
		verb := "Viewed"
		if msg.action == "dismiss" {
			verb = "Dismissed"
		}
		m.status = fmt.Sprintf("%s %s #%d in Notifications.", verb, strings.ToUpper(msg.key.Kind), msg.key.Number)
	}
	next, cmd := m.openNotifications()
	updated := next.(model)
	updated.notifications.selectItem = msg.key
	return updated, cmd
}
