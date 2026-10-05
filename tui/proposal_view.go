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
	if status == "rejected" {
		title = "Proposed comment (not published)"
	}
	fmt.Fprintf(b, "\n%s\n", inset(s.section.Render(title)))
	fmt.Fprintf(b, "%s\n", inset(s.comment.Render(wrapText(sanitize(body), maxInt(width-2, 1)))))
}

func (s proposalReviewStyles) writeEvidence(b *strings.Builder, inputs *autoCloseInputs, width int) {
	fmt.Fprintf(b, "\n%s\n", inset(s.section.Render("Selected evidence")))
	for _, line := range proposalEvidenceLines(autoCloseRow{Inputs: inputs}) {
		value := sanitize(line.item)
		if line.complete {
			value += " " + s.success.Render("Complete")
		} else if line.missing != "" {
			value += " " + s.danger.Render("Not found:") + " " + sanitize(line.missing)
		}
		fmt.Fprintf(b, "%s\n", inset(ansi.Wrap(value, width, "")))
	}
}
