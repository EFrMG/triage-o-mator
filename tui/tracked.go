package main

import (
	"encoding/json"
	"fmt"
	"strconv"

	tea "charm.land/bubbletea/v2"
)

type trackedRow struct {
	Identity struct {
		Kind   string `json:"kind"`
		Number int    `json:"number"`
	} `json:"identity"`
	Title     string  `json:"title"`
	NewCount  int     `json:"new_count"`
	CheckedAt string  `json:"checked_at"`
	Error     *string `json:"error"`
}

type trackedPage struct {
	Repository struct {
		Name string `json:"full_name"`
	} `json:"repository"`
	Rows        []trackedRow `json:"rows"`
	Total       int          `json:"total"`
	UnreadTotal int          `json:"unread_total"`
	Offset      int          `json:"offset"`
	Next        *int         `json:"next"`
}

type trackDoneMsg struct {
	root, repo, action string
	key                Key
	alreadyTracking    bool
	err                error
}

func trackCmd(root, repo, action string, target Key, row ...trackedRow) tea.Cmd {
	return func() tea.Msg {
		args := []string{"--expected-repo", repo, "track-" + action, "--kind", target.Kind, "--number", strconv.Itoa(target.Number)}
		if action == "add" {
			args = append(args, "--request-budget", "20")
		} else if action == "read" {
			args = append(args, "--checked-at", row[0].CheckedAt, "--new-count", strconv.Itoa(row[0].NewCount))
		}
		out, err := runScript(root, "cache", args...)
		msg := trackDoneMsg{root: root, repo: repo, action: action, key: target, err: err}
		if action == "add" && err == nil {
			var result struct {
				AlreadyTracking bool `json:"already_tracking"`
			}
			msg.err = json.Unmarshal([]byte(out), &result)
			msg.alreadyTracking = result.AlreadyTracking
		}
		return msg
	}
}

func (m model) startTracking(key Key) (tea.Model, tea.Cmd) {
	if m.trackingBusy {
		return m, nil
	}
	m.trackingBusy = true
	m.status = fmt.Sprintf("Tracking %s #%d and saving its comment baseline…", key.Kind, key.Number)
	return m, trackCmd(m.installRoot, m.repo, "add", key)
}

func (m model) finishTracking(msg trackDoneMsg) (tea.Model, tea.Cmd) {
	if msg.root != m.installRoot || msg.repo != m.repo {
		return m, nil
	}
	m.trackingBusy = false
	if msg.err != nil {
		m.recordError("Couldn't change tracked item", msg.err)
		m.status = "Couldn't change tracked item. ! shows details."
		return m, nil
	}
	if msg.action == "remove" {
		m.status = fmt.Sprintf("Stopped tracking %s #%d; saved evidence remains.", msg.key.Kind, msg.key.Number)
		return m.openNotifications()
	}
	if msg.action == "read" {
		m.status = fmt.Sprintf("Marked %s #%d read; still tracking comments.", msg.key.Kind, msg.key.Number)
		return m.openNotifications()
	}
	if msg.alreadyTracking {
		m.status = fmt.Sprintf("Already tracking %s #%d comments.", msg.key.Kind, msg.key.Number)
	} else {
		m.status = fmt.Sprintf("Tracking %s #%d. Its comments will be checked on the next ledger refresh.", msg.key.Kind, msg.key.Number)
	}
	if m.notifications.open {
		return m.reloadNotifications()
	}
	return m, nil
}

func (row trackedRow) key() Key {
	return Key{Kind: row.Identity.Kind, Number: row.Identity.Number}
}
