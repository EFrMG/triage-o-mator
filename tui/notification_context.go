package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type proposalItemContext struct {
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

func readClosureContext(root, repo string, number int, checkpoint string, offset int, contextCheckpoint string) (actionProposalContext, error) {
	args := []string{"--expected-repo", repo, "context", "--kind", "pr", "--number", strconv.Itoa(number), "--checkpoint", checkpoint, "--offset", strconv.Itoa(offset)}
	if contextCheckpoint != "" {
		args = append(args, "--context-checkpoint", contextCheckpoint)
	}
	out, err := runScript(root, "action-proposals", args...)
	if err != nil {
		return actionProposalContext{}, err
	}
	var context actionProposalContext
	if err := json.Unmarshal([]byte(out), &context); err != nil {
		return actionProposalContext{}, err
	}
	if context.Repository != repo || context.Kind != "pr" || context.Number != number || context.ProposalCheckpoint != checkpoint || context.Requests != 0 ||
		context.ItemContext.Repository != repo || context.ItemContext.Item.Kind != "pr" || context.ItemContext.Item.Number != number ||
		context.ItemContext.Requests != 0 || context.ItemContext.Checkpoint == "" || context.ItemContext.Pagination.Offset != offset || len(context.ItemContext.Rows) > 10 ||
		context.Current && context.Reason != "" || !context.Current && context.Reason == "" {
		return actionProposalContext{}, fmt.Errorf("proposal context identity or status mismatch")
	}
	return context, nil
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

func (c actionProposalContext) guidanceBlocks() []guidanceBlock {
	var blocks []guidanceBlock
	lastGroup := -1
	for _, row := range c.ItemContext.Rows {
		switch row.Kind {
		case "ledger":
			block := guidanceBlock{title: "Local decision"}
			if len(row.Fields) == 0 {
				block.lines = append(block.lines, "No local ledger row for this item")
			} else {
				call := strings.Trim(strings.Join([]string{contextField(row.Fields, "action"), contextField(row.Fields, "confidence")}, " · "), " ·")
				if call == "" {
					block.lines = append(block.lines, "No local triage decision yet")
				} else {
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
				}
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

func (n notificationsUI) proposalContext(row actionProposalRow) *actionProposalContext {
	if n.review != nil {
		if context, ok := n.review.Contexts[row.Number]; ok && context.ProposalCheckpoint == row.Checkpoint {
			return &context
		}
	}
	return nil
}

type proposalEvidenceLine struct {
	item, missing string
	complete      bool
}

func proposalEvidenceLines(inputs proposalInputs) []proposalEvidenceLine {
	var lines []proposalEvidenceLine
	for _, evidence := range inputs.Evidence {
		missing := make([]string, 0, len(evidence.Components))
		for name, component := range evidence.Components {
			if component.Status != "complete" && component.Status != "not_applicable" {
				missing = append(missing, strings.ReplaceAll(name, "_", " "))
			}
		}
		sort.Strings(missing)
		kind := "Issue"
		if evidence.Kind == "pr" {
			kind = "PR"
		}
		line := proposalEvidenceLine{item: fmt.Sprintf("%s #%d", kind, evidence.Number)}
		if len(evidence.Components) == 0 {
			line.missing = "component details"
		} else if len(missing) > 0 {
			line.missing = strings.Join(missing, ", ")
		} else {
			line.complete = true
		}
		lines = append(lines, line)
	}
	for _, gap := range inputs.EvidenceGaps {
		lines = append(lines, proposalEvidenceLine{item: "Gap", missing: gap})
	}
	if len(lines) == 0 {
		lines = append(lines, proposalEvidenceLine{item: "No selected evidence or declared gap"})
	}
	return lines
}
