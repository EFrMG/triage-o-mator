package main

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
)

type reviewClearDoneMsg struct {
	root, repo string
	cleared    []Key
	items      []Item
	err        error
}

func (m model) requestReviewClear(targets []Item) (tea.Model, tea.Cmd) {
	var chosen []Item
	for _, item := range targets {
		if item.PendingReview() {
			chosen = append(chosen, item)
		}
	}
	if len(chosen) == 0 {
		m.listConfirm = ""
		m.status = "No explicit Pending review request on the selected item(s)."

		return m, nil
	}
	if len(chosen) > 20 {
		m.status = "Clear at most 20 Pending review requests at once."

		return m, nil
	}
	if m.listConfirm != "a" {
		m.listConfirm = "a"
		m.status = fmt.Sprintf("Clear the Pending review request on %s? This does not approve a decision or GitHub write. Press a again.", describeTargets(chosen))

		return m, nil
	}

	m.listConfirm = ""
	m.pendingApply++
	return m, reviewClearCmd(m.installRoot, m.repo, m.contributor, chosen)
}

func reviewClearCmd(root, repo, by string, chosen []Item) tea.Cmd {
	return func() tea.Msg {
		msg := reviewClearDoneMsg{root: root, repo: repo}
		for _, item := range chosen {
			request := item.ReviewRequest
			if request == nil {
				continue
			}
			_, msg.err = runScript(root, "review-request", "clear", "--expected-repo", repo, "--kind", item.Kind,
				"--number", fmt.Sprint(item.Number), "--by", by, "--expected-by", request.By,
				"--expected-at", request.At, "--expected-reason", request.Reason)
			if msg.err != nil {
				break
			}

			msg.cleared = append(msg.cleared, item.Key())
		}
		items, err := LoadLedger(root, repo)
		if err != nil && msg.err == nil {
			msg.err = err
		}
		msg.items = items

		return msg
	}
}

func (m model) onReviewClearDone(msg reviewClearDoneMsg) (tea.Model, tea.Cmd) {
	if msg.root != m.installRoot || msg.repo != m.repo {
		return m, nil
	}
	if m.pendingApply > 0 {
		m.pendingApply--
	}
	if msg.items != nil {
		m.items = msg.items
		m.recomputeSidebarCounts()
		m.refreshActiveList()
	}
	m.clearTicks()
	if msg.err != nil {
		m.failErr(fmt.Sprintf("Cleared %d request(s), then stopped", len(msg.cleared)), msg.err)
	} else {
		m.status = fmt.Sprintf("Cleared %d Pending review request(s).", len(msg.cleared))
	}

	return m, nextCmd(m.installRoot, m.repo)
}
