package main

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type proposalReviewStyles struct {
	section lipgloss.Style
	target  lipgloss.Style
	action  lipgloss.Style
	danger  lipgloss.Style
	success lipgloss.Style
	muted   lipgloss.Style
	comment lipgloss.Style
}

func newProposalReviewStyles() proposalReviewStyles {
	return proposalReviewStyles{
		section: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(currentTheme.Accent)),
		target:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(currentTheme.Info)),
		action:  lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Warning)),
		danger:  lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Error)).Bold(true),
		success: lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Success)),
		muted:   lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Muted)),
		comment: lipgloss.NewStyle().BorderLeft(true).BorderForeground(lipgloss.Color(currentTheme.Accent)).PaddingLeft(1),
	}
}

func (s proposalReviewStyles) writeItemHeading(b *strings.Builder, kind string, number int, title string) {
	label := fmt.Sprintf("%s #%d %s", kind, number, sanitize(title))
	if styled, ok := styledItemHeading(label, lipgloss.Color(currentTheme.Info), nil); ok {
		label = styled
	} else {
		label = s.target.Render(label)
	}
	fmt.Fprintf(b, "%s\n", inset(label))
}

func (s proposalReviewStyles) writeComment(b *strings.Builder, body, status string, width int) {
	title := "Comment to publish"
	switch status {
	case "executed":
		title = "Published comment"
	case "uncertain":
		title = "Attempted comment (check outcome)"
	case "rejected":
		title = "Proposed comment (not published)"
	}
	fmt.Fprintf(b, "\n%s\n", inset(s.section.Render(title)))
	fmt.Fprintf(b, "%s\n", inset(s.comment.Render(wrapText(sanitize(body), maxInt(width-2, 1)))))
}

func proposalActionSection(status string) string {
	switch status {
	case "executed":
		return "Completed action"
	case "uncertain":
		return "Action outcome uncertain"
	}
	return "Proposed action"
}

func proposalOutcomeSection(status string) string {
	if status == "executed" {
		return "Saved outcome"
	}
	return "Saved attempt"
}

func (s proposalReviewStyles) writeSavedState(b *strings.Builder, status, operation, kind string) bool {
	switch status {
	case "rejected":
		fmt.Fprintf(b, "%s\n", inset(s.muted.Render("This proposal was rejected; no GitHub action is available.")))
	case "executed":
		message := "This action published the comment."
		if operation == "close" || operation == "reopen" {
			noun := "issue"
			if kind == "PR" {
				noun = "PR"
			}
			verb := "closed"
			if operation == "reopen" {
				verb = "reopened"
			}
			message = "This action published the comment and " + verb + " the " + noun + "."
		}
		fmt.Fprintf(b, "%s\n", inset(s.success.Render(message)))
	case "uncertain":
		fmt.Fprintf(b, "%s\n", inset(s.danger.Render("Write outcome uncertain. Inspect the saved attempt and live item before another action.")))
	default:
		return false
	}
	return true
}

func (s proposalReviewStyles) writeEvidence(b *strings.Builder, inputs proposalInputs, width int) {
	fmt.Fprintf(b, "\n%s\n", inset(s.section.Render("Selected evidence")))
	for _, line := range proposalEvidenceLines(inputs) {
		value := sanitize(line.item)
		if line.complete {
			value += " " + s.success.Render("Complete")
		} else if line.missing != "" {
			value += " " + s.danger.Render("Not found:") + " " + sanitize(line.missing)
		}
		fmt.Fprintf(b, "%s\n", inset(ansi.Wrap(value, width, "")))
	}
}

// proposalReviewState is what a reader knows beyond the saved proposal: its checked local context, any live problem, and where the proposal sits in a batch.
type proposalReviewState struct {
	context  *actionProposalContext
	busy     bool
	problem  string
	approved bool
	paging   bool
	position string
}

// writeProposal renders one saved proposal the same way in the single action reader and in batch closure review: heading, state, target, comment, human context, evidence and outcome.
func (s proposalReviewStyles) writeProposal(b *strings.Builder, row actionProposalRow, state proposalReviewState, width int) {
	context := state.context
	kind := "Issue"
	if row.Kind == "pr" {
		kind = "PR"
	}
	s.writeItemHeading(b, kind, row.Number, row.Title)
	if state.position != "" {
		fmt.Fprintf(b, "%s\n", inset(s.muted.Render(state.position)))
	}

	fmt.Fprintf(b, "\n%s\n", inset(s.section.Render(proposalActionSection(row.Status))))
	if !s.writeSavedState(b, row.Status, row.Operation, kind) {
		if context != nil && !context.Current || state.problem != "" {
			fmt.Fprintf(b, "%s\n", inset(s.danger.Render("Changed context: prepare a fresh proposal and review.")))
		} else if row.Status != "pending" || !row.Active || context == nil || state.busy {
			fmt.Fprintf(b, "%s\n", inset(s.action.Render("Approval unavailable until the saved local context is current.")))
		} else {
			operation := "Publish the comment below."
			if row.Operation == "close" || row.Operation == "reopen" {
				operation = "Publish the comment below, then " + row.Operation + " this " + kind + "."
			}
			fmt.Fprintf(b, "%s\n", inset(s.action.Render(operation)))
		}
	}

	fmt.Fprintf(b, "%s\n", inset(wrapText("Target: "+sanitize(row.Target), width)))
	if row.DecisionQuestion != "" {
		fmt.Fprintf(b, "%s\n", inset(wrapText("Question for this action: "+sanitize(row.DecisionQuestion), width)))
	}
	if row.DecisionResolution != nil {
		resolution := row.DecisionResolution
		fmt.Fprintf(b, "\n%s\n", inset(s.section.Render("Human decision resolution")))
		fmt.Fprintf(b, "%s\n", inset(wrapText("By: "+sanitize(resolution.By)+" · At: "+sanitize(resolution.At), width)))
		fmt.Fprintf(b, "%s\n", inset(wrapText(sanitize(resolution.Reason), width)))
	}
	if row.Reference != nil {
		fmt.Fprintf(b, "%s\n", inset(s.muted.Render(fmt.Sprintf("Reference: %s #%d", row.Reference.Kind, row.Reference.Number))))
	}
	s.writeComment(b, row.Comment, row.Status, width)

	fmt.Fprintf(b, "\n%s\n", inset(s.section.Render("Human context")))
	if context == nil {
		fmt.Fprintf(b, "%s\n", inset(s.muted.Render("Local guidance has not been checked.")))
	} else {
		if !context.Current && row.Status == "pending" {
			detail := strings.TrimSuffix(context.Reason, "; prepare a fresh proposal and review")
			fmt.Fprintf(b, "%s\n", inset(s.action.Render(wrapText("Why: "+sanitize(detail), width))))
		}
		for _, block := range context.guidanceBlocks() {
			fmt.Fprintf(b, "\n%s\n", inset(s.muted.Bold(true).Render(sanitize(block.title))))
			for _, line := range block.lines {
				fmt.Fprintf(b, "%s\n", inset(wrapText(sanitize(line), width)))
			}
		}
		if context.LatestRejection != nil && row.Status != "rejected" {
			fmt.Fprintf(b, "\n%s\n", inset(s.muted.Bold(true).Render("Earlier objection · By: "+sanitize(context.LatestRejection.By))))
			fmt.Fprintf(b, "%s\n", inset(wrapText(sanitize(context.LatestRejection.Reason), width)))
		}
		if state.paging && (context.ItemContext.Pagination.Offset > 0 || context.ItemContext.Pagination.Next != nil) {
			fmt.Fprintf(b, "%s\n", inset(s.muted.Render(fmt.Sprintf("Local context page %d · [ and ] move between pages", context.ItemContext.Pagination.Offset/10+1))))
		}
	}
	if row.Reconsideration != nil {
		fmt.Fprintf(b, "\n%s\n", inset(s.muted.Bold(true).Render("Reconsideration · "+sanitize(row.Reconsideration.By))))
		fmt.Fprintf(b, "%s\n", inset(wrapText(sanitize(row.Reconsideration.Reason), width)))
	}

	s.writeEvidence(b, row.Inputs, width)
	if row.Rejection != nil {
		fmt.Fprintf(b, "\n%s\n", inset(s.section.Render("Rejection")))
		fmt.Fprintf(b, "%s\n", inset(wrapText("By: "+sanitize(row.Rejection.By)+" · At: "+sanitize(row.Rejection.At), width)))
		fmt.Fprintf(b, "%s\n", inset(wrapText(orPlaceholder(sanitize(row.Rejection.Reason), "(no reason given)"), width)))
	}
	if state.problem != "" {
		fmt.Fprintf(b, "\n%s\n", inset(s.danger.Render(wrapText("Action unavailable: "+sanitize(state.problem), width))))
	}
	if state.approved {
		fmt.Fprintf(b, "\n%s\n", inset(s.action.Render("Exact review is ready. Press a again to publish.")))
	}
	if row.Outcome != nil {
		fmt.Fprintf(b, "\n%s\n", inset(s.section.Render(proposalOutcomeSection(row.Status))))
		fmt.Fprintf(b, "%s\n", inset(s.muted.Render("Comment: "+sanitize(row.Outcome.Comment.Status))))
		fmt.Fprintf(b, "%s\n", inset(s.muted.Render("State: "+sanitize(row.Outcome.StateChange.Status))))
	}
}
