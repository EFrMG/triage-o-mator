package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type proposalNote struct {
	row, field, label, preview string
	omitted                    int
}

type proposalNotesMsg struct {
	root, repo, proposalCheckpoint, contextCheckpoint string
	kind                                              string
	generation, request                               uint64
	number                                            int
	text                                              string
	err                                               error
}

func proposalNotes(context *actionProposalContext) []proposalNote {
	var notes []proposalNote
	add := func(rowID string, fields map[string]json.RawMessage, field, label string) {
		raw := fields[field]
		if len(raw) == 0 {
			return
		}
		var preview struct {
			Text    string `json:"preview"`
			Omitted int    `json:"omitted_bytes"`
		}
		if json.Unmarshal(raw, &preview) != nil || preview.Text == "" && preview.Omitted == 0 {
			return
		}
		notes = append(notes, proposalNote{row: rowID, field: field, label: label, preview: preview.Text, omitted: preview.Omitted})
	}

	for _, row := range context.ItemContext.Rows {
		switch row.Kind {
		case "ledger":
			add(row.ID, row.Fields, "reason", "Decision reason")
			add(row.ID, row.Fields, "reviewer_notes", "Reviewer notes")
			add(row.ID, row.Fields, "agent_notes", "Agent notes")
		case "group":
			add(row.ID, row.Fields, "description", "Group guidance · "+contextField(row.Fields, "title"))
		case "member":
			if row.Selected {
				label := "Member note"
				if by := contextField(row.Fields, "updated_by"); by != "" {
					label += " · By: " + by
				}
				add(row.ID, row.Fields, "notes", label)
			}
		}
	}
	return notes
}

func hasExpandableProposalNotes(context *actionProposalContext) bool {
	if context == nil {
		return false
	}
	for _, note := range proposalNotes(context) {
		if note.omitted > 0 || contextExcerpt(note.preview) != note.preview {
			return true
		}
	}
	return false
}

func proposalNotesCmd(root, repo, kind string, generation, request uint64, number int, proposalCheckpoint, contextCheckpoint string, notes []proposalNote) tea.Cmd {
	return func() tea.Msg {
		msg := proposalNotesMsg{root: root, repo: repo, kind: kind, generation: generation, request: request, number: number,
			proposalCheckpoint: proposalCheckpoint, contextCheckpoint: contextCheckpoint}
		var output strings.Builder
		for _, note := range notes {
			content := note.preview
			if note.omitted > 0 {
				args := []string{"--expected-repo", repo, "source", "--kind", kind, "--number", strconv.Itoa(number),
					"--row", note.row, "--field", note.field, "--checkpoint", contextCheckpoint,
					"--max-bytes", "65536"}
				out, err := runScript(root, "item-context", args...)
				if err != nil {
					msg.err = err
					return msg
				}
				var source struct {
					Repository string `json:"repository"`
					Item       struct {
						Kind   string `json:"kind"`
						Number int    `json:"number"`
					} `json:"item"`
					Checkpoint string `json:"checkpoint"`
					Row        string `json:"row"`
					Field      string `json:"field"`
					Text       string `json:"text"`
					Bytes      struct {
						Offset   int `json:"offset"`
						Returned int `json:"returned"`
					} `json:"bytes"`
					Continuation *struct {
						ByteOffset int `json:"byte_offset"`
					} `json:"continuation"`
					Requests int `json:"requests"`
				}
				if json.Unmarshal([]byte(out), &source) != nil || source.Repository != repo || source.Item.Kind != kind || source.Item.Number != number ||
					source.Checkpoint != contextCheckpoint || source.Row != note.row || source.Field != note.field || source.Requests != 0 ||
					source.Bytes.Offset != 0 || source.Bytes.Returned != len([]byte(source.Text)) || source.Bytes.Returned > 65536 ||
					source.Continuation != nil && source.Continuation.ByteOffset != source.Bytes.Returned {
					msg.err = fmt.Errorf("local note source changed during read")
					return msg
				}
				content = source.Text
				if source.Continuation != nil {
					content += "\n\n[More text exists beyond this bounded reader.]"
				}
			}
			if output.Len() > 0 {
				output.WriteString("\n\n")
			}
			fmt.Fprintf(&output, "## %s\n\n%s", note.label, content)
		}
		if output.Len() == 0 {
			output.WriteString("No local notes on this context page.")
		}
		msg.text = output.String()
		return msg
	}
}

func (m model) toggleActionNotes(row actionProposalRow) (tea.Model, tea.Cmd) {
	if m.notifications.notesOpen {
		m.notifications.notesOpen = false
		m.notifications.notesRequest++
		return m, nil
	}
	if m.notifications.actionReview == nil || m.notifications.actionReview.context == nil || m.notifications.actionReview.busy {
		m.status = "Read local context before opening its notes."
		return m, nil
	}
	context := m.notifications.actionReview.context
	if !hasExpandableProposalNotes(context) {
		m.status = "Full local notes are already shown in the proposal."
		return m, nil
	}
	m.notifications.notesOpen = true
	m.notifications.notesBusy = true
	m.notifications.notesError = ""
	m.notifications.notesScroll = 0
	m.notifications.notesRequest++
	return m, proposalNotesCmd(m.installRoot, m.repo, row.Kind, m.notificationsGeneration, m.notifications.notesRequest,
		row.Number, row.Checkpoint, context.ItemContext.Checkpoint, proposalNotes(context))
}

func (m model) finishProposalNotes(msg proposalNotesMsg) (tea.Model, tea.Cmd) {
	if !m.notifications.open || !m.notifications.notesOpen || msg.root != m.installRoot || msg.repo != m.repo ||
		msg.generation != m.notificationsGeneration || msg.request != m.notifications.notesRequest {
		return m, nil
	}
	review := m.notifications.actionReview
	if review == nil || review.context == nil || msg.kind != review.row.Kind || msg.number != review.row.Number || msg.proposalCheckpoint != review.row.Checkpoint ||
		review.context.ItemContext.Checkpoint == "" || review.context.ItemContext.Checkpoint != msg.contextCheckpoint {
		return m, nil
	}
	m.notifications.notesBusy = false
	if msg.err != nil {
		m.notifications.notesError = "Local notes changed or are unavailable. Close and reopen this window to retry."
		m.recordError("Proposal notes unavailable", msg.err)
		return m, nil
	}
	m.notifications.notesText = msg.text
	return m, nil
}

func (m model) proposalNotesViewport() viewport.Model {
	width := maxInt(m.commentWidth()-4, 1)
	height := maxInt(m.commentHeight()-4, 1)
	vp := viewport.New(viewport.WithWidth(width), viewport.WithHeight(height))
	content := m.notifications.notesText
	if m.notifications.notesBusy {
		content = "Reading local notes…"
	} else if m.notifications.notesError != "" {
		content = m.notifications.notesError
	}
	vp.SetContent(renderMarkdownWithLineBreaks(content, width, true))
	vp.SetYOffset(m.notifications.notesScroll)
	return vp
}

func (m model) proposalNotesOverlay(background string) string {
	x, y := m.commentPosition()
	header := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Info)).Bold(true).Render("Local notes")
	panel := panelStyle(true).BorderBackground(lipgloss.Color(currentTheme.Background)).Width(m.commentWidth()).Height(m.commentHeight()).Padding(0, 1).Render(
		header + "\n\n" + m.proposalNotesViewport().View())
	base := screenStyle().Width(m.width).Height(m.mainHeight() + 2).Render(fitScreen(background, m.width, m.mainHeight()+2))
	return lipgloss.NewCompositor(lipgloss.NewLayer(base), lipgloss.NewLayer(panel).X(x).Y(y).Z(1)).Render()
}
