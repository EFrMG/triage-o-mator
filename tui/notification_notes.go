package main

import (
	"encoding/json"
	"fmt"
	"strconv"

	tea "charm.land/bubbletea/v2"
)

type proposalNoteKey struct {
	row, field string
}

type proposalNote struct {
	key proposalNoteKey
}

type proposalNotesMsg struct {
	root, repo, proposalCheckpoint, contextCheckpoint string
	kind                                              string
	generation, request                               uint64
	number                                            int
	values                                            map[proposalNoteKey]string
	err                                               error
}

// proposalNotes selects only fields whose bounded context preview omitted text; the full values are read from the same pinned local context.
func proposalNotes(context *actionProposalContext) []proposalNote {
	var notes []proposalNote
	add := func(rowID string, fields map[string]json.RawMessage, field string) {
		var preview struct {
			Omitted int `json:"omitted_bytes"`
		}
		if json.Unmarshal(fields[field], &preview) == nil && preview.Omitted > 0 {
			notes = append(notes, proposalNote{key: proposalNoteKey{row: rowID, field: field}})
		}
	}

	for _, row := range context.ItemContext.Rows {
		switch row.Kind {
		case "ledger":
			for _, field := range []string{"reason", "maintainer_notes", "pending_review_reason", "agent_notes"} {
				add(row.ID, row.Fields, field)
			}
		case "group":
			add(row.ID, row.Fields, "description")
		case "member":
			if row.Selected {
				add(row.ID, row.Fields, "notes")
			}
		}
	}
	return notes
}

func proposalNotesCmd(root, repo, kind string, generation, request uint64, number int, proposalCheckpoint, contextCheckpoint string, notes []proposalNote) tea.Cmd {
	return func() tea.Msg {
		msg := proposalNotesMsg{root: root, repo: repo, kind: kind, generation: generation, request: request, number: number,
			proposalCheckpoint: proposalCheckpoint, contextCheckpoint: contextCheckpoint, values: map[proposalNoteKey]string{}}
		for _, note := range notes {
			args := []string{"--expected-repo", repo, "source", "--kind", kind, "--number", strconv.Itoa(number),
				"--row", note.key.row, "--field", note.key.field, "--checkpoint", contextCheckpoint, "--max-bytes", "65536"}
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
				source.Checkpoint != contextCheckpoint || source.Row != note.key.row || source.Field != note.key.field || source.Requests != 0 ||
				source.Bytes.Offset != 0 || source.Bytes.Returned != len([]byte(source.Text)) || source.Bytes.Returned > 65536 ||
				source.Continuation != nil && source.Continuation.ByteOffset != source.Bytes.Returned {
				msg.err = fmt.Errorf("local note source changed during read")
				return msg
			}
			content := source.Text
			if source.Continuation != nil {
				content += "\n\n[More text exists beyond this bounded reader.]"
			}
			msg.values[note.key] = content
		}
		return msg
	}
}

func (m model) finishProposalNotes(msg proposalNotesMsg) (tea.Model, tea.Cmd) {
	review := m.notifications.actionReview
	if !m.notifications.open || review == nil || review.context == nil || msg.root != m.installRoot || msg.repo != m.repo ||
		msg.generation != m.notificationsGeneration || msg.request != m.notifications.notesRequest || msg.kind != review.row.Kind ||
		msg.number != review.row.Number || msg.proposalCheckpoint != review.row.Checkpoint ||
		review.context.ItemContext.Checkpoint == "" || review.context.ItemContext.Checkpoint != msg.contextCheckpoint {
		return m, nil
	}
	review.notesBusy = false
	if msg.err != nil {
		review.problem = "Full local guidance changed or is unavailable; reopen this proposal."
		m.recordError("Proposal notes unavailable", msg.err)
		return m, nil
	}
	review.notes = msg.values
	return m, nil
}
