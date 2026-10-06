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
	Repository         string               `json:"repository"`
	Kind               string               `json:"kind"`
	Number             int                  `json:"number"`
	ProposalCheckpoint string               `json:"proposal_checkpoint"`
	Current            bool                 `json:"current"`
	Reason             string               `json:"reason"`
	ItemContext        autoCloseItemContext `json:"item_context"`
	LatestRejection    *autoCloseRejection  `json:"latest_rejection"`
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
			msg.context.ItemContext.Pagination.Offset != msg.offset || len(msg.context.ItemContext.Rows) > 10 ||
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
	case "j", "down":
		review.scroll++
	case "k", "up":
		review.scroll = maxInt(review.scroll-1, 0)
	case "ctrl+d":
		review.scroll += maxInt(m.mainHeight()/2, 1)
	case "ctrl+u":
		review.scroll = maxInt(review.scroll-maxInt(m.mainHeight()/2, 1), 0)
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
	styles := newProposalReviewStyles()
	width := maxInt(m.menuWidth()-4, 1)
	var b strings.Builder
	title := "Action proposal"
	switch row.Status {
	case "executed":
		title = "Completed action"
	case "uncertain":
		title = "Action outcome uncertain"
	case "rejected":
		title = "Rejected action proposal"
	}
	fmt.Fprintf(&b, "%s\n\n", inset(titleBar(title, "", m.menuWidth())))
	kind := "Issue"
	if row.Kind == "pr" {
		kind = "PR"
	}
	styles.writeItemHeading(&b, kind, row.Number, row.Title)
	fmt.Fprintf(&b, "\n%s\n", inset(styles.section.Render(proposalActionSection(row.Status))))
	if !styles.writeSavedState(&b, row.Status, row.Operation, kind) {
		if review.context != nil && !review.context.Current || review.problem != "" {
			fmt.Fprintf(&b, "%s\n", inset(styles.danger.Render("Changed context: prepare a fresh proposal and review.")))
		} else if row.Status != "pending" || !row.Active || review.context == nil || !review.context.Current || review.busy {
			fmt.Fprintf(&b, "%s\n", inset(styles.action.Render("Approval unavailable until the saved local context is current.")))
		} else {
			operation := "Publish the comment below."
			if row.Operation == "close" || row.Operation == "reopen" {
				operation = "Publish the comment below, then " + row.Operation + " this " + kind + "."
			}
			fmt.Fprintf(&b, "%s\n", inset(styles.action.Render(operation)))
		}
	}
	fmt.Fprintf(&b, "%s\n", inset(wrapText("Target: "+sanitize(row.Target), width)))
	if row.DecisionQuestion != "" {
		fmt.Fprintf(&b, "%s\n", inset(wrapText("Question for this action: "+sanitize(row.DecisionQuestion), width)))
	}
	if row.DecisionResolution != nil {
		resolution := row.DecisionResolution
		fmt.Fprintf(&b, "\n%s\n", inset(styles.section.Render("Human decision resolution")))
		fmt.Fprintf(&b, "%s\n", inset(wrapText("By: "+sanitize(resolution.By)+" · At: "+sanitize(resolution.At), width)))
		fmt.Fprintf(&b, "%s\n", inset(wrapText(sanitize(resolution.Reason), width)))
	}
	if row.Reference != nil {
		fmt.Fprintf(&b, "%s\n", inset(styles.muted.Render(fmt.Sprintf("Reference: %s #%d", row.Reference.Kind, row.Reference.Number))))
	}
	styles.writeComment(&b, row.Comment, row.Status, width)
	fmt.Fprintf(&b, "\n%s\n", inset(styles.section.Render("Human context")))
	if review.context == nil {
		fmt.Fprintf(&b, "%s\n", inset(styles.muted.Render("Local guidance has not been checked.")))
	} else {
		if !review.context.Current && row.Status == "pending" {
			fmt.Fprintf(&b, "%s\n", inset(styles.action.Render(wrapText("Why: "+sanitize(review.context.Reason), width))))
		}
		context := autoCloseContext{ItemContext: review.context.ItemContext}
		for _, block := range context.guidanceBlocks() {
			fmt.Fprintf(&b, "\n%s\n", inset(styles.muted.Bold(true).Render(sanitize(block.title))))
			for _, line := range block.lines {
				fmt.Fprintf(&b, "%s\n", inset(wrapText(sanitize(line), width)))
			}
		}
		if review.context.LatestRejection != nil && row.Status != "rejected" {
			fmt.Fprintf(&b, "\n%s\n", inset(styles.muted.Bold(true).Render("Earlier objection · By: "+sanitize(review.context.LatestRejection.By))))
			fmt.Fprintf(&b, "%s\n", inset(wrapText(sanitize(review.context.LatestRejection.Reason), width)))
		}
		if review.context.ItemContext.Pagination.Offset > 0 || review.context.ItemContext.Pagination.Next != nil {
			fmt.Fprintf(&b, "%s\n", inset(styles.muted.Render(fmt.Sprintf("Local context page %d · [ and ] move between pages", review.context.ItemContext.Pagination.Offset/10+1))))
		}
	}
	if row.Reconsideration != nil {
		fmt.Fprintf(&b, "\n%s\n", inset(styles.muted.Bold(true).Render("Reconsideration · "+sanitize(row.Reconsideration.By))))
		fmt.Fprintf(&b, "%s\n", inset(wrapText(sanitize(row.Reconsideration.Reason), width)))
	}
	styles.writeEvidence(&b, row.Inputs, width)
	if row.Rejection != nil {
		fmt.Fprintf(&b, "\n%s\n", inset(styles.section.Render("Rejection")))
		fmt.Fprintf(&b, "%s\n", inset(wrapText("By: "+sanitize(row.Rejection.By)+" · At: "+sanitize(row.Rejection.At), width)))
		fmt.Fprintf(&b, "%s\n", inset(wrapText(orPlaceholder(sanitize(row.Rejection.Reason), "(no reason given)"), width)))
	}
	if review.problem != "" {
		fmt.Fprintf(&b, "\n%s\n", inset(styles.danger.Render(wrapText("Action unavailable: "+sanitize(review.problem), width))))
	}
	if review.approval != "" {
		fmt.Fprintf(&b, "\n%s\n", inset(styles.action.Render("Exact review is ready. Press a again to publish.")))
	}
	if row.Outcome != nil {
		fmt.Fprintf(&b, "\n%s\n", inset(styles.section.Render(proposalOutcomeSection(row.Status))))
		fmt.Fprintf(&b, "%s\n", inset(styles.muted.Render("Comment: "+sanitize(row.Outcome.Comment.Status))))
		fmt.Fprintf(&b, "%s\n", inset(styles.muted.Render("State: "+sanitize(row.Outcome.StateChange.Status))))
	}
	vp := viewport.New(viewport.WithWidth(m.cardWidth()), viewport.WithHeight(maxInt(m.mainHeight()-1, 1)))
	vp.SetContent(b.String())
	vp.SetYOffset(review.scroll)
	footer := proposalRevisionFooter(autoCloseRow{UpdatedAt: row.UpdatedAt, HeadSHA: row.HeadSHA}, maxInt(m.cardWidth()-1, 1))
	return vp.View() + "\n" + inset(styles.muted.Render(footer))
}
