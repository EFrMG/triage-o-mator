package main

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

const workflowInboxPageSize = 20

// workflowInbox is the offline list behind Notifications → Workflows: PRs with runs listed as waiting, staged runs, holds and recorded outcomes. Reading it approves or acknowledges nothing.
type workflowInbox struct {
	Repository string             `json:"repository"`
	Mode       string             `json:"mode"`
	Rows       []workflowInboxRow `json:"rows"`
	Pagination struct {
		Total int `json:"total"`
	} `json:"pagination"`
	Requests int `json:"requests"`
}

type workflowInboxRow struct {
	Number  int     `json:"number"`
	Title   *string `json:"title"`
	Waiting []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"waiting"`
	Suspicious int               `json:"suspicious"`
	Staged     []json.RawMessage `json:"staged"`
	Notes      []workflowNote    `json:"notes"`
	Outcomes   []workflowOutcome `json:"outcomes"`
	Uncertain  int               `json:"uncertain"`
}

func readWorkflowInbox(process *readProcess, root, repo string) (workflowInbox, error) {
	var inbox workflowInbox
	out, err := runReadScript(process, root, "workflow-runs", "inbox", "--expected-repo", repo, "--limit", fmt.Sprint(workflowInboxPageSize))
	if err == nil {
		err = json.Unmarshal([]byte(out), &inbox)
	}
	if err == nil && (inbox.Repository != repo || inbox.Requests != 0 || len(inbox.Rows) > workflowInboxPageSize) {
		err = fmt.Errorf("workflow inbox identity or bounds mismatch")
	}

	return inbox, err
}

func workflowChoice(number, row int) notificationChoice {
	choice := itemChoice(Key{Kind: "pr", Number: number})
	choice.kind, choice.workflow = "workflow", row
	return choice
}

func renderWorkflowChoice(n notificationsUI, choice notificationChoice, card func(string, string, cardMark)) {
	row := n.workflows.Rows[choice.workflow]
	label := fmt.Sprintf("PR #%d", row.Number)
	if row.Title != nil && *row.Title != "" {
		label += " " + singleLine(*row.Title)
	}

	var parts []string
	if len(row.Waiting) > 0 {
		parts = append(parts, fmt.Sprintf("%d run(s) waiting", len(row.Waiting)))
	}
	if row.Suspicious > 0 {
		parts = append(parts, fmt.Sprintf("%d suspicious staged for a person", row.Suspicious))
	} else if len(row.Staged) > 0 {
		parts = append(parts, fmt.Sprintf("%d staged", len(row.Staged)))
	}
	if len(row.Notes) > 0 {
		parts = append(parts, fmt.Sprintf("%d hold(s) or concern(s)", len(row.Notes)))
	}
	if count := len(row.Outcomes); count > 0 {
		latest := row.Outcomes[count-1]
		parts = append(parts, map[string]string{"approved": "last approved", "refused": "last refused by GitHub", "unknown": "last outcome uncertain"}[latest.Status])
	}

	mark := cardMark{text: "Waiting", color: currentTheme.Info}
	switch {
	case row.Uncertain > 0:
		mark = cardMark{text: "Uncertain", color: currentTheme.Error}
	case row.Suspicious > 0:
		mark = cardMark{text: "Suspicious", color: currentTheme.Warning}
	case len(row.Waiting) == 0:
		mark = cardMark{}
	}
	card(label, strings.Join(parts, " · "), mark)
}

// openWorkflowNotification opens the PR as a current item, so Checks reads live and W lists and approves there; it approves nothing itself.
func (m model) openWorkflowNotification(number int) (tea.Model, tea.Cmd) {
	key := Key{Kind: "pr", Number: number}
	it, ok := m.findItem(key)
	if !ok {
		m.warn(fmt.Sprintf("PR #%d is missing from the ledger. Refresh the repository, then open it here again.", number))
		return m, nil
	}

	m.notificationsLifecycle.stop()
	m.notificationsGeneration++
	m.notifications = notificationsUI{}
	if m.detail.key != key {
		m.commitDraftIfDirty()
	}
	cmd := m.openItem(it)
	m.focus = FocusDetail
	m.checksOnLoad = key
	m.status = fmt.Sprintf("Opening PR #%d on its Checks tab…", number)
	return m, cmd
}
