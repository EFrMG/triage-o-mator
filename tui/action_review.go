package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

type actionProposalContext struct {
	Repository         string               `json:"repository"`
	Kind               string               `json:"kind"`
	Number             int                  `json:"number"`
	ProposalCheckpoint string               `json:"proposal_checkpoint"`
	Current            bool                 `json:"current"`
	Reason             string               `json:"reason"`
	ItemContext        autoCloseItemContext `json:"item_context"`
	Requests           int                  `json:"requests"`
}

type actionReviewUI struct {
	row      actionProposalRow
	context  *actionProposalContext
	approval string
	busy     bool
	problem  string
	scroll   int
}

type actionReviewMsg struct {
	root, repo, phase, checkpoint string
	generation                    uint64
	key                           Key
	context                       actionProposalContext
	approval                      string
	row                           actionProposalRow
	err                           error
}

func actionReviewCmd(root, repo string, generation uint64, row actionProposalRow, phase, approval string) tea.Cmd {
	return func() tea.Msg {
		msg := actionReviewMsg{root: root, repo: repo, generation: generation, key: Key{Kind: row.Kind, Number: row.Number}, checkpoint: row.Checkpoint, phase: phase}
		args := []string{"--expected-repo", repo, phase, "--kind", row.Kind, "--number", strconv.Itoa(row.Number)}
		if phase == "context" {
			args = append(args, "--checkpoint", row.Checkpoint)
		} else if phase == "execute" {
			args = append(args, "--publish", "--approve", approval)
		}
		out, err := runScript(root, "action-proposals", args...)
		if err != nil {
			msg.err = err
			return msg
		}
		if phase == "context" {
			msg.err = json.Unmarshal([]byte(out), &msg.context)
		} else if phase == "review" {
			var reviewed struct {
				Plan struct {
					Repository string              `json:"repo"`
					Operation  string              `json:"operation"`
					Proposals  []actionProposalRow `json:"proposals"`
				} `json:"plan"`
				Approval string `json:"approval"`
			}
			msg.err = json.Unmarshal([]byte(out), &reviewed)
			if msg.err == nil {
				if reviewed.Plan.Repository != repo || reviewed.Plan.Operation != "conversation-or-state-action" || len(reviewed.Plan.Proposals) != 1 || reviewed.Approval == "" {
					msg.err = fmt.Errorf("action review response identity mismatch")
				} else {
					msg.row, msg.approval = reviewed.Plan.Proposals[0], reviewed.Approval
				}
			}
		} else {
			var result struct {
				Kind, Status string
				Number       int
			}
			msg.err = json.Unmarshal([]byte(out), &result)
			if msg.err == nil && (result.Kind != row.Kind || result.Number != row.Number || result.Status != "executed") {
				msg.err = fmt.Errorf("action execution outcome differs from the approved item")
			}
		}
		return msg
	}
}

func (m model) openActionReview(choice notificationChoice) (tea.Model, tea.Cmd) {
	if choice.actionProposal < 0 || choice.actionProposal >= len(m.notifications.actions.Rows) {
		return m, nil
	}
	row := m.notifications.actions.Rows[choice.actionProposal]
	if row.Kind != choice.key.Kind || row.Number != choice.key.Number || row.Checkpoint == "" {
		m.fail("Action proposal identity changed; reopen Notifications.")
		return m, nil
	}
	m.notifications.actionReview = &actionReviewUI{row: row, busy: true}
	return m, actionReviewCmd(m.installRoot, m.repo, m.notificationsGeneration, row, "context", "")
}

func (m model) finishActionReview(msg actionReviewMsg) (tea.Model, tea.Cmd) {
	current := m.notifications.actionReview
	if !m.notifications.open || current == nil || msg.root != m.installRoot || msg.repo != m.repo || msg.generation != m.notificationsGeneration ||
		msg.key != (Key{Kind: current.row.Kind, Number: current.row.Number}) || msg.checkpoint != current.row.Checkpoint {
		return m, nil
	}
	current.busy = false
	if msg.err != nil {
		if msg.phase == "execute" {
			m.recordError("Action outcome needs inspection", msg.err)
			return m.openNotifications()
		}
		current.problem = msg.err.Error()
		return m, nil
	}
	if msg.phase == "context" {
		if msg.context.Repository != m.repo || msg.context.Kind != current.row.Kind || msg.context.Number != current.row.Number ||
			msg.context.ProposalCheckpoint != current.row.Checkpoint || msg.context.Requests != 0 ||
			msg.context.ItemContext.Repository != m.repo || msg.context.ItemContext.Item.Kind != current.row.Kind ||
			msg.context.ItemContext.Item.Number != current.row.Number || msg.context.ItemContext.Requests != 0 ||
			current.row.Inputs == nil || current.row.Status == "pending" && msg.context.ItemContext.Checkpoint != current.row.Inputs.ContextCheckpoint {
			current.problem = "Saved action context differs from the selected proposal."
			return m, nil
		}
		current.context = &msg.context
	} else if msg.phase == "review" {
		if current.context == nil || !current.context.Current || msg.row.Checkpoint != current.row.Checkpoint || msg.row.Target != current.row.Target ||
			msg.row.Comment != current.row.Comment || msg.row.Action != current.row.Action || msg.row.Operation != current.row.Operation ||
			msg.row.Kind != current.row.Kind || msg.row.Number != current.row.Number {
			current.problem = "Action proposal changed; reopen Notifications before approval."
			return m, nil
		}
		current.approval = msg.approval
		m.status = "Review the exact target, operation and comment; press a again to publish."
	} else {
		target, err := validateCommentTarget(Item{Kind: current.row.Kind, Number: current.row.Number, URL: current.row.Target}, m.repo)
		if err != nil {
			m.failErr("Published action target is unavailable", err)
			return m.openNotifications()
		}
		key := Key{Kind: current.row.Kind, Number: current.row.Number}
		m.status = "Approved action completed. Refreshing item…"
		next, notificationCmd := m.openNotifications()
		updated, syncCmd := next.(model).startRefreshItemsAtHost(false, target.host, key)
		return updated, tea.Batch(notificationCmd, syncCmd)
	}
	return m, nil
}

func (m model) handleActionReviewKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	review := m.notifications.actionReview
	if review == nil {
		return m, nil
	}
	row := review.row
	if review.busy && msg.String() != "?" {
		return m, nil
	}
	switch msg.String() {
	case "?":
		m.showHelp = !m.showHelp
	case "esc", "h", "left":
		m.notifications.actionReview = nil
	case "q":
		return m.requestQuit()
	case "j", "down":
		review.scroll++
	case "k", "up":
		review.scroll = maxInt(review.scroll-1, 0)
	case "ctrl+d":
		review.scroll += maxInt(m.mainHeight()/2, 1)
	case "ctrl+u":
		review.scroll = maxInt(review.scroll-maxInt(m.mainHeight()/2, 1), 0)
	case "enter", "l", "right":
		return m.openNotificationItemAt(Key{Kind: row.Kind, Number: row.Number}, 0)
	case "a":
		if row.Status != "pending" || !row.Active || review.context == nil || !review.context.Current || review.problem != "" {
			m.warn("Current action context is required before approval.")
			return m, nil
		}
		if row.DecisionQuestion != "" && row.DecisionResolution == nil {
			m.warn("Answer the action question before exact approval.")
			return m, nil
		}
		review.busy = true
		if review.approval == "" {
			return m, actionReviewCmd(m.installRoot, m.repo, m.notificationsGeneration, row, "review", "")
		}
		return m, actionReviewCmd(m.installRoot, m.repo, m.notificationsGeneration, row, "execute", review.approval)
	case "e":
		if choice, ok := m.notifications.actionProposalChoice(Key{Kind: row.Kind, Number: row.Number}); ok {
			return m.openProposalEdit(choice)
		}
	case "r":
		if row.DecisionQuestion != "" {
			if choice, ok := m.notifications.actionProposalChoice(Key{Kind: row.Kind, Number: row.Number}); ok {
				return m.openAnswerComposer(choice)
			}
		}
	case "d", "D":
		if choice, ok := m.notifications.actionProposalChoice(Key{Kind: row.Kind, Number: row.Number}); ok {
			if row.Status == "pending" {
				if msg.String() == "D" {
					return m.openExternalRejectionComposer(choice)
				}
				return m.openRejectionComposer(choice)
			}
			if msg.String() == "d" {
				m.notifications.actionReview = nil
				return m.changeNotificationItem(choice, "dismiss")
			}
		}
	}
	return m, nil
}

func (m model) actionReviewView() string {
	review := m.notifications.actionReview
	if review == nil {
		return ""
	}
	row := review.row
	width := maxInt(m.cardWidth()-2, 1)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", inset(titleBar("Action proposal", fmt.Sprintf("%s #%d", strings.ToUpper(row.Kind), row.Number), m.menuWidth())))
	fmt.Fprintf(&b, "%s\n", inset(wrapText(sanitize(row.Title), width)))
	fmt.Fprintf(&b, "\n%s\n", inset(wrapText("Action: "+sanitize(row.Action)+" · "+sanitize(row.Operation), width)))
	fmt.Fprintf(&b, "%s\n", inset(wrapText("Target: "+sanitize(row.Target), width)))
	if row.DecisionQuestion != "" {
		fmt.Fprintf(&b, "\n%s\n", inset(wrapText("Question for this action: "+sanitize(row.DecisionQuestion), width)))
	}
	if row.DecisionResolution != nil {
		resolution := row.DecisionResolution
		fmt.Fprintf(&b, "\n%s\n", inset(wrapText("Human decision resolved by "+sanitize(resolution.By)+" at "+sanitize(resolution.At), width)))
		fmt.Fprintf(&b, "%s\n", inset(wrapText(sanitize(resolution.Reason), width)))
	}
	fmt.Fprintf(&b, "\n%s\n", inset("Exact comment:"))
	fmt.Fprintf(&b, "%s\n", inset(wrapText(sanitize(row.Comment), width)))
	if row.Inputs != nil {
		fmt.Fprintf(&b, "\n%s\n", inset(fmt.Sprintf("Selected evidence: %d snapshot(s)", len(row.Inputs.Evidence))))
		for _, selected := range row.Inputs.Evidence {
			fmt.Fprintf(&b, "%s\n", inset(wrapText(fmt.Sprintf("%s #%d · %s", selected.Kind, selected.Number, selected.SnapshotID), width)))
			names := make([]string, 0, len(selected.Components))
			for name := range selected.Components {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				component := selected.Components[name]
				fmt.Fprintf(&b, "%s\n", inset(wrapText("  "+name+": "+component.Status, width)))
			}
		}
		for _, gap := range row.Inputs.EvidenceGaps {
			fmt.Fprintf(&b, "%s\n", inset(wrapText("Gap: "+sanitize(gap), width)))
		}
	}
	if review.context != nil {
		if !review.context.Current {
			label := "Changed context: "
			if row.Status != "pending" {
				label = "Saved outcome: "
			}
			fmt.Fprintf(&b, "\n%s\n", inset(wrapText(label+sanitize(review.context.Reason), width)))
		} else {
			fmt.Fprintf(&b, "\n%s\n", inset("Current local guidance:"))
			for _, source := range review.context.ItemContext.Rows {
				for _, field := range []string{"action", "reason", "agent_notes", "reviewer_notes", "notes"} {
					if value := contextField(source.Fields, field); value != "" {
						fmt.Fprintf(&b, "%s\n", inset(wrapText(source.Kind+" "+field+": "+sanitize(value), width)))
					}
				}
			}
		}
	}
	if review.problem != "" {
		fmt.Fprintf(&b, "\n%s\n", inset(wrapText("Action unavailable: "+sanitize(review.problem), width)))
	}
	if review.approval != "" {
		fmt.Fprintf(&b, "\n%s\n", inset("Exact review is ready. Press a again to publish."))
	}
	if row.Outcome != nil {
		fmt.Fprintf(&b, "\n%s\n", inset("Previous attempt: comment "+sanitize(row.Outcome.Comment.Status)+", state "+sanitize(row.Outcome.StateChange.Status)))
	}
	vp := viewport.New(viewport.WithWidth(m.cardWidth()), viewport.WithHeight(m.mainHeight()))
	vp.SetContent(b.String())
	vp.SetYOffset(review.scroll)
	return vp.View()
}
