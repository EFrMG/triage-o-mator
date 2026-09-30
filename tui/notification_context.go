package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type autoCloseContext struct {
	Repository         string               `json:"repository"`
	Number             int                  `json:"number"`
	ProposalCheckpoint string               `json:"proposal_checkpoint"`
	Current            bool                 `json:"current"`
	Reason             string               `json:"reason"`
	ItemContext        autoCloseItemContext `json:"item_context"`
	LatestRejection    *autoCloseRejection  `json:"latest_rejection"`
	Requests           int                  `json:"requests"`
}

type autoCloseItemContext struct {
	Repository string `json:"repository"`
	Item       struct {
		Kind   string `json:"kind"`
		Number int    `json:"number"`
	} `json:"item"`
	Checkpoint    string `json:"checkpoint"`
	GroupCount    int    `json:"group_count"`
	FeedbackCount int    `json:"feedback_count"`
	Rows          []struct {
		Kind     string                     `json:"kind"`
		ID       string                     `json:"id"`
		Selected bool                       `json:"selected"`
		Fields   map[string]json.RawMessage `json:"fields"`
	} `json:"rows"`
	Pagination struct {
		Offset int  `json:"offset"`
		Next   *int `json:"next_offset"`
	} `json:"pagination"`
	Requests int `json:"requests"`
}

type autoCloseContextMsg struct {
	root, repo, checkpoint string
	generation             uint64
	number                 int
	context                autoCloseContext
	err                    error
}

func readAutoCloseContext(root, repo string, number int, checkpoint string, offset int, contextCheckpoint string) (autoCloseContext, error) {
	args := []string{"--expected-repo", repo, "context", "--number", strconv.Itoa(number), "--checkpoint", checkpoint, "--offset", strconv.Itoa(offset)}
	if contextCheckpoint != "" {
		args = append(args, "--context-checkpoint", contextCheckpoint)
	}
	out, err := runScript(root, "auto-close", args...)
	if err != nil {
		return autoCloseContext{}, err
	}
	var context autoCloseContext
	if err := json.Unmarshal([]byte(out), &context); err != nil {
		return autoCloseContext{}, err
	}
	if context.Repository != repo || context.Number != number || context.ProposalCheckpoint != checkpoint || context.Requests != 0 ||
		context.ItemContext.Repository != repo || context.ItemContext.Item.Kind != "pr" || context.ItemContext.Item.Number != number ||
		context.ItemContext.Requests != 0 || context.ItemContext.Checkpoint == "" || context.ItemContext.Pagination.Offset != offset || len(context.ItemContext.Rows) > 10 ||
		context.Current && context.Reason != "" || !context.Current && context.Reason == "" {
		return autoCloseContext{}, fmt.Errorf("proposal context identity or status mismatch")
	}
	return context, nil
}

func autoCloseContextCmd(root, repo string, generation uint64, number int, checkpoint string, offset int, contextCheckpoint string) tea.Cmd {
	return func() tea.Msg {
		context, err := readAutoCloseContext(root, repo, number, checkpoint, offset, contextCheckpoint)
		return autoCloseContextMsg{root: root, repo: repo, generation: generation, number: number, checkpoint: checkpoint, context: context, err: err}
	}
}

func (m model) beginAutoCloseContext(number int, checkpoint string, offset int, contextCheckpoint string) (tea.Model, tea.Cmd) {
	m.notifications.contextBusy = true
	m.notifications.contextError = ""
	m.notifications.context = nil
	m.notifications.notesOpen = false
	m.notifications.notesBusy = false
	m.notifications.notesText = ""
	m.notifications.notesError = ""
	m.notifications.notesScroll = 0
	m.notifications.notesRequest++
	return m, autoCloseContextCmd(m.installRoot, m.repo, m.notificationsGeneration, number, checkpoint, offset, contextCheckpoint)
}

func (m model) finishAutoCloseContext(msg autoCloseContextMsg) (tea.Model, tea.Cmd) {
	if !m.notifications.open || m.notifications.review == nil || msg.root != m.installRoot || msg.repo != m.repo || msg.generation != m.notificationsGeneration ||
		len(m.notifications.review.Plan.Proposals) != 1 || m.notifications.review.Plan.Proposals[0].Number != msg.number ||
		m.notifications.review.Plan.Proposals[0].Checkpoint != msg.checkpoint {
		return m, nil
	}
	m.notifications.contextBusy = false
	if msg.err != nil {
		m.notifications.contextError = "Local proposal context changed or is unavailable. Reopen Notifications to retry."
		m.recordError("Proposal context read unavailable", msg.err)
		return m, nil
	}
	row := m.notifications.review.Plan.Proposals[0]
	if msg.context.Current && (row.Inputs == nil || msg.context.ItemContext.Checkpoint != row.Inputs.ContextCheckpoint) {
		m.notifications.contextError = "Local context response differs from the saved proposal. Reopen Notifications."
		return m, nil
	}
	m.notifications.context = &msg.context
	m.notifications.reviewScroll = 0
	return m, nil
}

func contextField(fields map[string]json.RawMessage, name string) string {
	raw, ok := fields[name]
	if !ok || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var preview struct {
		Text    string `json:"preview"`
		Omitted int    `json:"omitted_bytes"`
	}
	var fieldsInObject map[string]json.RawMessage
	if json.Unmarshal(raw, &fieldsInObject) == nil && fieldsInObject["preview"] != nil && json.Unmarshal(raw, &preview) == nil {
		if preview.Omitted > 0 {
			return fmt.Sprintf("%s… (%d more bytes)", preview.Text, preview.Omitted)
		}
		return preview.Text
	}
	return string(raw)
}

type guidanceBlock struct {
	title string
	lines []string
}

func contextExcerpt(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > 180 {
		return string(runes[:180]) + "…"
	}
	return value
}

func (c autoCloseContext) guidanceBlocks() []guidanceBlock {
	var blocks []guidanceBlock
	lastGroup := -1
	for _, row := range c.ItemContext.Rows {
		switch row.Kind {
		case "ledger":
			block := guidanceBlock{title: "Local decision"}
			if len(row.Fields) == 0 {
				block.lines = append(block.lines, "No ledger decision recorded")
			} else {
				call := strings.Trim(strings.Join([]string{contextField(row.Fields, "category"), contextField(row.Fields, "action"), contextField(row.Fields, "confidence")}, " · "), " ·")
				if call == "" {
					call = "No decision recorded"
				}
				block.lines = append(block.lines, "Call: "+call)
				review := "Unreviewed"
				if contextField(row.Fields, "reviewed") == "true" {
					review = "Reviewed"
					if by := contextField(row.Fields, "reviewed_by"); by != "" {
						review += " by " + by
					}
				}
				if by := contextField(row.Fields, "triaged_by"); by != "" {
					review += " · proposed by " + by
				}
				block.lines = append(block.lines, review)
				for _, field := range []struct{ key, label string }{{"reason", "Reason"}, {"reviewer_notes", "Reviewer note"}, {"agent_notes", "Agent note"}} {
					if value := contextField(row.Fields, field.key); value != "" {
						block.lines = append(block.lines, field.label+": "+contextExcerpt(value))
					}
				}
			}
			blocks = append(blocks, block)
		case "group":
			block := guidanceBlock{title: "Group · " + contextField(row.Fields, "title")}
			status := contextField(row.Fields, "status")
			if by := contextField(row.Fields, "updated_by"); by != "" {
				status += " · edited by " + by
			}
			block.lines = append(block.lines, status)
			if value := contextField(row.Fields, "description"); value != "" {
				block.lines = append(block.lines, "Guidance: "+contextExcerpt(value))
			}
			blocks = append(blocks, block)
			lastGroup = len(blocks) - 1
		case "member":
			if row.Selected {
				if value := contextField(row.Fields, "notes"); value != "" {
					by := contextField(row.Fields, "updated_by")
					if by == "" {
						by = contextField(row.Fields, "added_by")
					}
					line := "Member note"
					if by != "" {
						line += " (" + by + ")"
					}
					line += ": " + contextExcerpt(value)
					if lastGroup >= 0 {
						blocks[lastGroup].lines = append(blocks[lastGroup].lines, line)
					} else {
						blocks = append(blocks, guidanceBlock{title: "Member guidance", lines: []string{line}})
					}
				}
			}
		}
	}
	return blocks
}

func (n notificationsUI) proposalContext(row autoCloseRow) *autoCloseContext {
	if n.context != nil && n.context.Number == row.Number && n.context.ProposalCheckpoint == row.Checkpoint {
		return n.context
	}
	if n.review != nil {
		if context, ok := n.review.Contexts[row.Number]; ok && context.ProposalCheckpoint == row.Checkpoint {
			return &context
		}
	}
	return nil
}

func proposalEvidenceLines(row autoCloseRow) []string {
	if row.Inputs == nil {
		return []string{"This older proposal has no declared evidence or local context."}
	}
	var lines []string
	for _, evidence := range row.Inputs.Evidence {
		components := make([]string, 0, len(evidence.Components))
		for name, component := range evidence.Components {
			components = append(components, name+": "+component.Status)
		}
		sort.Strings(components)
		lines = append(lines, fmt.Sprintf("%s #%d · snapshot %s · %s", evidence.Kind, evidence.Number, evidence.SnapshotID, strings.Join(components, ", ")))
	}
	for _, gap := range row.Inputs.EvidenceGaps {
		lines = append(lines, "Gap: "+gap)
	}
	if len(lines) == 0 {
		lines = append(lines, "No selected evidence or declared gap")
	}
	return lines
}
