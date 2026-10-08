package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

type actionProposalContext struct {
	Repository         string              `json:"repository"`
	Kind               string              `json:"kind"`
	Number             int                 `json:"number"`
	ProposalCheckpoint string              `json:"proposal_checkpoint"`
	Current            bool                `json:"current"`
	Reason             string              `json:"reason"`
	ItemContext        proposalItemContext `json:"item_context"`
	LatestRejection    *proposalRejection  `json:"latest_rejection"`
	Requests           int                 `json:"requests"`
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
	offset                        int
	approval                      string
	row                           actionProposalRow
	err                           error
}

func actionReviewCmd(root, repo string, generation uint64, row actionProposalRow, phase, approval string) tea.Cmd {
	return func() tea.Msg {
		msg := actionReviewMsg{root: root, repo: repo, generation: generation, key: Key{Kind: row.Kind, Number: row.Number}, checkpoint: row.Checkpoint, phase: phase}
		args := []string{"--expected-repo", repo, phase, "--kind", row.Kind, "--number", strconv.Itoa(row.Number)}
		if phase == "execute" {
			args = append(args, "--publish", "--approve", approval)
		}
		out, err := runScript(root, "action-proposals", args...)
		if err != nil {
			msg.err = err
			return msg
		}
		if phase == "review" {
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

func actionReviewContextCmd(root, repo string, generation uint64, row actionProposalRow, offset int, contextCheckpoint string) tea.Cmd {
	return func() tea.Msg {
		msg := actionReviewMsg{root: root, repo: repo, generation: generation, key: Key{Kind: row.Kind, Number: row.Number}, checkpoint: row.Checkpoint, phase: "context", offset: offset}
		args := []string{"--expected-repo", repo, "context", "--kind", row.Kind, "--number", strconv.Itoa(row.Number), "--checkpoint", row.Checkpoint, "--offset", strconv.Itoa(offset)}
		if contextCheckpoint != "" {
			args = append(args, "--context-checkpoint", contextCheckpoint)
		}
		out, err := runScript(root, "action-proposals", args...)
		if err == nil {
			err = json.Unmarshal([]byte(out), &msg.context)
		}
		msg.err = err
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
	m.notifications.notesOpen = false
	m.notifications.notesBusy = false
	m.notifications.notesText = ""
	m.notifications.notesError = ""
	m.notifications.notesRequest++
	return m, actionReviewContextCmd(m.installRoot, m.repo, m.notificationsGeneration, row, 0, "")
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
			next, cmd := m.openNotifications()
			updated := next.(model)
			updated.failErr("Approved action did not complete", msg.err)

			return updated, cmd
		}
		current.problem = msg.err.Error()
		return m, nil
	}
	if msg.phase == "context" {
		if msg.context.Repository != m.repo || msg.context.Kind != current.row.Kind || msg.context.Number != current.row.Number ||
			msg.context.ProposalCheckpoint != current.row.Checkpoint || msg.context.Requests != 0 ||
			msg.context.ItemContext.Repository != m.repo || msg.context.ItemContext.Item.Kind != current.row.Kind ||
			msg.context.ItemContext.Item.Number != current.row.Number || msg.context.ItemContext.Requests != 0 ||
			msg.context.ItemContext.Pagination.Offset != msg.offset || len(msg.context.ItemContext.Rows) > 10 ||
			current.row.Status == "pending" && msg.context.ItemContext.Checkpoint != current.row.Inputs.ContextCheckpoint {
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
	if m.notifications.notesOpen && msg.String() != "q" {
		switch msg.String() {
		case "m", "esc", "x", "h", "left":
			m.notifications.notesOpen = false
			m.notifications.notesRequest++
		case "j", "down":
			m.notifications.notesScroll++
		case "k", "up":
			m.notifications.notesScroll--
		case "ctrl+d":
			m.notifications.notesScroll += maxInt(m.commentHeight()/2, 1)
		case "ctrl+u":
			m.notifications.notesScroll -= maxInt(m.commentHeight()/2, 1)
		}
		if m.notifications.notesOpen {
			vp := m.proposalNotesViewport()
			m.notifications.notesScroll = vp.YOffset()
		}
		return m, nil
	}
	switch msg.String() {
	case "?":
		m.showHelp = !m.showHelp
	case "esc", "h", "left":
		m.notifications.actionReview = nil
	case "q":
		return m.requestQuit()
	case "j", "down", "k", "up", "ctrl+d", "ctrl+u":
		switch msg.String() {
		case "j", "down":
			review.scroll++
		case "k", "up":
			review.scroll--
		case "ctrl+d":
			review.scroll += maxInt(m.mainHeight()/2, 1)
		case "ctrl+u":
			review.scroll -= maxInt(m.mainHeight()/2, 1)
		}
		// The viewport clamps to the rendered proposal, so no scroll steps accumulate beyond its last line.
		vp := m.actionReviewViewport()
		review.scroll = vp.YOffset()
	case "[", "]":
		if review.context == nil {
			return m, nil
		}
		offset := maxInt(0, review.context.ItemContext.Pagination.Offset-10)
		if msg.String() == "]" {
			if review.context.ItemContext.Pagination.Next == nil {
				return m, nil
			}
			offset = *review.context.ItemContext.Pagination.Next
		}
		review.busy = true
		return m, actionReviewContextCmd(m.installRoot, m.repo, m.notificationsGeneration, row, offset, review.context.ItemContext.Checkpoint)
	case "enter", "l", "right":
		return m.openNotificationItemAt(Key{Kind: row.Kind, Number: row.Number}, 0)
	case "y":
		text, what := m.yankActionProposal(row)
		m.status = "Taking " + what + "…"
		return m, yankCmd(m.installRoot, m.repo, what, text)
	case "m":
		return m.toggleActionNotes(row)
	case "w":
		if choice, ok := m.notifications.actionProposalChoice(Key{Kind: row.Kind, Number: row.Number}); ok && choice.tracked >= 0 {
			m.status = fmt.Sprintf("Already tracking %s #%d comments.", strings.ToUpper(row.Kind), row.Number)
			return m, nil
		}
		return m.startTracking(Key{Kind: row.Kind, Number: row.Number})
	case "t", "i":
		if choice, ok := m.notifications.actionProposalChoice(Key{Kind: row.Kind, Number: row.Number}); ok {
			if msg.String() == "t" {
				return m.openNotificationSource(choice, "watch")
			}
			return m.openNotificationSource(choice, "action")
		}
	case "a":
		if row.Status == "executed" {
			m.status = "This action already ran."
			return m, nil
		}
		if row.Status == "pending" && row.DecisionQuestion != "" && row.DecisionResolution == nil {
			if choice, ok := m.notifications.actionProposalChoice(Key{Kind: row.Kind, Number: row.Number}); ok {
				return m.openAnswerComposer(choice)
			}
			m.warn("This action question is no longer available; refresh Notifications.")
			return m, nil
		}
		if row.OutOfDate != nil {
			m.warn("The item changed on GitHub after this proposal was prepared; press y to copy it for an agent to prepare a fresh one.")
			return m, nil
		}
		if row.Status != "pending" || !row.Active || review.context == nil || !review.context.Current || review.problem != "" {
			m.warn("Current action context is required before approval.")
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
	footer := proposalRevisionFooter(review.row, maxInt(m.cardWidth()-1, 1))
	return m.actionReviewViewport().View() + "\n" + inset(mutedText(footer))
}

func (m model) actionReviewViewport() viewport.Model {
	review := m.notifications.actionReview
	row := review.row
	styles := newProposalReviewStyles()
	width := maxInt(m.menuWidth()-4, 1)
	var b strings.Builder
	title := "Action proposal"
	if row.OutOfDate != nil {
		title = "Action proposal out of date"
	}
	switch row.Status {
	case "executed":
		title = "Completed action"
	case "uncertain":
		title = "Action outcome uncertain"
	case "rejected":
		title = "Rejected action proposal"
	}
	fmt.Fprintf(&b, "%s\n\n", inset(titleBar(title, "", m.menuWidth())))
	styles.writeProposal(&b, row, proposalReviewState{context: review.context, busy: review.busy, problem: review.problem, approved: review.approval != "", paging: true}, width)
	vp := viewport.New(viewport.WithWidth(m.cardWidth()), viewport.WithHeight(maxInt(m.mainHeight()-1, 1)))
	vp.SetContent(b.String())
	vp.SetYOffset(review.scroll)
	return vp
}
