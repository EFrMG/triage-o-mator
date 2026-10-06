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

func (s proposalReviewStyles) writeEvidence(b *strings.Builder, inputs *proposalInputs, width int) {
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
