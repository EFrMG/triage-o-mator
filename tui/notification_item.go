package main

import (
	"encoding/json"
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

type notificationRejectionDoneMsg struct {
	root, repo, checkpoint string
	generation             uint64
	key                    Key
	rejected               bool
	action                 bool
	completed, total       int
	err                    error
}

func (m model) rejectNotificationCmd() tea.Cmd {
	root, repo, generation := m.installRoot, m.repo, m.notificationsGeneration
	choice, checkpoint := m.comment.rejectionChoice, m.comment.rejectionCheckpoint
	reason, by := m.comment.text.Value(), m.reviewer
	operations := m.notifications.itemOperations(repo, choice, "dismiss")

	return func() tea.Msg {
		msg := notificationRejectionDoneMsg{root: root, repo: repo, generation: generation, key: choice.key, checkpoint: checkpoint, total: len(operations)}
		args := []string{"--expected-repo", repo, "reject", "--kind", choice.key.Kind, "--number", strconv.Itoa(choice.key.Number), "--checkpoint", checkpoint, "--by", by, "--reason", reason}
		msg.action = true
		out, err := runScript(root, "action-proposals", args...)
		if err != nil {
			msg.err = err
			return msg
		}

		var rejected autoCloseRow
		if err := json.Unmarshal([]byte(out), &rejected); err != nil {
			msg.rejected = true
			msg.err = fmt.Errorf("could not read saved rejection: %w", err)
			return msg
		}
		msg.rejected = true
		if rejected.Number != choice.key.Number || rejected.Status != "rejected" || rejected.Checkpoint == "" || rejected.Rejection == nil || rejected.Rejection.ProposalCheckpoint != checkpoint || rejected.Rejection.By != by || rejected.Rejection.Reason != reason {
			msg.err = fmt.Errorf("saved rejection did not match the displayed proposal")
			return msg
		}

		for _, operation := range operations {
			args := operation.args
			if msg.action && operation.source == "action proposal" || !msg.action && operation.source == "proposal" {
				args = append([]string(nil), args...)
				args[len(args)-1] = rejected.Checkpoint
			}
			if _, err := runScript(root, operation.script, args...); err != nil {
				msg.err = fmt.Errorf("%s dismissal: %w", operation.source, err)
				return msg
			}
			msg.completed++
		}
		return msg
	}
}

func (m model) finishNotificationRejection(msg notificationRejectionDoneMsg) (tea.Model, tea.Cmd) {
	if !m.comment.open || m.comment.rejectionCheckpoint != msg.checkpoint || msg.root != m.installRoot || msg.repo != m.repo || msg.generation != m.notificationsGeneration {
		return m, nil
	}
	m.comment.busy = false
	if !msg.rejected {
		m.failErr("Couldn't reject proposal; reason retained", msg.err)
		return m, nil
	}

	m.comment.open = false
	m.notifications.review = nil
	if msg.err != nil {
		m.recordError("Proposal rejected; notification dismissal was incomplete", msg.err)
		m.status = fmt.Sprintf("Proposal rejected; dismissed %d of %d notification sources. ! shows details.", msg.completed, msg.total)
	} else {
		m.status = fmt.Sprintf("Rejected %s #%d and dismissed its notification.", msg.key.Kind, msg.key.Number)
	}
	next, cmd := m.openNotifications()
	updated := next.(model)
	updated.notifications.selectItem = msg.key
	return updated, cmd
}

func (n notificationsUI) itemOperations(repo string, choice notificationChoice, action string) []notificationItemOperation {
	var operations []notificationItemOperation
	if choice.actionProposal >= 0 {
		row := n.actions.Rows[choice.actionProposal]
		if action == "dismiss" || row.Needs {
			operations = append(operations, notificationItemOperation{script: "action-proposals", source: "action proposal", args: []string{"--expected-repo", repo, action, "--kind", row.Kind, "--number", strconv.Itoa(row.Number), "--checkpoint", row.Checkpoint}})
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
	switch source {
	case "proposal":
		if choice.proposal >= 0 {
			row := m.notifications.proposals.Rows[choice.proposal]
			m.notifications.review = &autoCloseReview{}
			m.notifications.review.Plan.Repository = m.repo
			m.notifications.review.Plan.Proposals = []autoCloseRow{row}
			m.notifications.reviewKey = ""
			m.notifications.reviewScroll = 0
			m.notifications.notesOpen = false
			m.notifications.notesBusy = false
			m.notifications.notesText = ""
			m.notifications.notesError = ""
			return m.beginAutoCloseContext(row.Number, row.Checkpoint, 0, "")
		}
	case "item":
		return m.openNotificationItem(choice.key)
	case "watch":
		if choice.attention >= 0 {
			row := m.notifications.attention.Rows[choice.attention]
			if row.Selectable {
				return m.readAttention(attentionLocation{section: "history", number: row.Number, checkpoint: row.WatchCheckpoint})
			}
			m.warn("Retained activity is unavailable; inspect its saved record.")
		}
	case "action":
		if choice.closure >= 0 {
			row := m.notifications.closures.Rows[choice.closure]
			if row.Selectable {
				return m.readActionHistory(actionHistoryLocation{section: "entries", number: row.Number, checkpoint: row.HistoryCheckpoint})
			}
			m.warn("Imported actions are unavailable; inspect their saved record.")
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
