package main

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type groupHandoffPacket struct {
	Repo          string             `json:"repo"`
	ExportedAt    string             `json:"exported_at"`
	Group         Group              `json:"group"`
	RelatedGroups []Group            `json:"related_groups"`
	Items         []groupHandoffItem `json:"items"`
}

type groupHandoffItem struct {
	Kind              string         `json:"kind"`
	Number            int            `json:"number"`
	Title             string         `json:"title"`
	State             string         `json:"state"`
	ProposedLabels    []string       `json:"proposed_labels"`
	Action            string         `json:"action"`
	Confidence        string         `json:"confidence"`
	Reason            string         `json:"reason"`
	TriagedBy         string         `json:"triaged_by"`
	AgentNotes        string         `json:"agent_notes"`
	ReviewRequest     *ReviewRequest `json:"review_request"`
	MaintainerNotes   string         `json:"maintainer_notes"`
	MissingFromLedger bool           `json:"missing_from_ledger"`
	LocalContext      struct {
		Checkpoint       string   `json:"checkpoint"`
		RelevantGroupIDs []string `json:"relevant_group_ids"`
		Feedback         []struct {
			Kind          string `json:"kind"`
			By            string `json:"by"`
			At            string `json:"at"`
			Reason        string `json:"reason"`
			Status        string `json:"status"`
			CommentStatus string `json:"comment_status"`
			StateStatus   string `json:"state_status"`
			RequestID     string `json:"request_id"`
		} `json:"feedback"`
	} `json:"local_context"`
}

type groupHandoffMsg struct {
	root, repo, id string
	revision       int
	request        uint64
	selected       []Key
	packet         groupHandoffPacket
	err            error
}

func (m model) startGroupHandoff(all bool) (tea.Model, tea.Cmd) {
	g := m.selectedGroup()
	if !m.groups.detail || g == nil || len(g.Members) == 0 {
		m.warn("Open a group with members before copying a proposal handoff.")
		return m, nil
	}
	if g.Status == "archived" {
		m.warn("Archived groups cannot start a new proposal handoff.")
		return m, nil
	}

	selected := m.tickedMembers(*g)
	if all {
		selected = make([]Key, 0, len(g.Members))
		for _, member := range g.Members {
			selected = append(selected, member.Key())
		}
	} else if len(selected) == 0 && m.groups.member >= 0 && m.groups.member < len(g.Members) {
		selected = []Key{g.Members[m.groups.member].Key()}
	}
	if len(selected) == 0 {
		m.warn("Select a group member before copying a proposal handoff.")
		return m, nil
	}

	m.groups.handoffRequest++
	m.groups.busy = true
	m.status = "Reading current group context for handoff…"
	return m, groupHandoffCmd(m.installRoot, m.repo, m.groups.handoffRequest, *g, selected)
}

func groupHandoffCmd(root, repo string, request uint64, displayed Group, selected []Key) tea.Cmd {
	return func() tea.Msg {
		msg := groupHandoffMsg{root: root, repo: repo, id: displayed.ID, revision: displayed.Revision, request: request, selected: selected}
		out, err := runScript(root, "group", "--expected-repo", repo, "export", displayed.ID, "--format", "json")
		if err != nil {
			msg.err = err
			return msg
		}

		var packet groupHandoffPacket
		if err := json.Unmarshal([]byte(out), &packet); err != nil {
			msg.err = fmt.Errorf("invalid group handoff packet: %w", err)
			return msg
		}
		if packet.Repo != repo || packet.Group.Repo != repo || packet.Group.ID != displayed.ID || packet.Group.Revision != displayed.Revision || packet.Group.Title != displayed.Title || packet.Group.Description != displayed.Description || packet.Group.Status != displayed.Status || len(packet.Group.Members) != len(displayed.Members) || len(packet.Items) != len(displayed.Members) {
			msg.err = fmt.Errorf("group changed since it was displayed; reopen Groups before copying")
			return msg
		}
		members := make(map[Key]bool, len(displayed.Members))
		for i, member := range displayed.Members {
			current := packet.Group.Members[i]
			item := packet.Items[i]
			if current.Key() != member.Key() || current.Notes != member.Notes || item.Kind != member.Kind || item.Number != member.Number || item.LocalContext.Checkpoint == "" {
				msg.err = fmt.Errorf("group membership or context changed; reopen Groups before copying")
				return msg
			}
			members[member.Key()] = true
		}
		for _, key := range selected {
			if !members[key] {
				msg.err = fmt.Errorf("selected member left the group; reopen Groups before copying")
				return msg
			}
		}
		msg.packet = packet
		return msg
	}
}

func handoffValue(b *strings.Builder, label, value string) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\r\n", "\n"))
	if value == "" {
		return
	}
	if !strings.Contains(value, "\n") {
		fmt.Fprintf(b, "- %s: %s\n", label, value)
		return
	}
	fmt.Fprintf(b, "- %s:\n", label)
	for _, line := range strings.Split(value, "\n") {
		fmt.Fprintf(b, "  > %s\n", line)
	}
}

func groupHandoffText(header string, msg groupHandoffMsg) string {
	packet := msg.packet
	selected := make(map[Key]bool, len(msg.selected))
	for _, key := range msg.selected {
		selected[key] = true
	}

	var b strings.Builder
	b.WriteString(header)
	fmt.Fprintf(&b, "\n## Proposal handoff · group %s, revision %d\n\nSelected for this pass:\n", msg.id, msg.revision)
	for _, key := range msg.selected {
		fmt.Fprintf(&b, "- %s #%d\n", key.Kind, key.Number)
	}
	b.WriteString("\nAssess these members even if already triaged. Use the current human guidance and prior feedback below; hold conflicting guidance for maintainer resolution. For each selected PR, inspect immutable evidence and gaps, then prepare an explained closure proposal only if justified; otherwise report why it should stay open. Selected issues can inform the decision or receive a no-action report. Do not publish or grant approval.\n")
	fmt.Fprintf(&b, "\n### Group · %s\n\n", strings.Join(strings.Fields(packet.Group.Title), " "))
	fmt.Fprintf(&b, "- Status: %s · revision %d (readiness is not approval)\n", packet.Group.Status, packet.Group.Revision)
	handoffValue(&b, "Description", packet.Group.Description)
	handoffValue(&b, "Assignee", packet.Group.Assignee)
	if len(packet.Group.CandidateOrigin) > 0 {
		b.WriteString("- Pinned candidate origin exists; inspect the full packet for its frozen provenance, not a semantic verdict.\n")
	}

	b.WriteString("\n### Members\n")
	for i, member := range packet.Group.Members {
		item := packet.Items[i]
		title := strings.Join(strings.Fields(item.Title), " ")
		if title == "" {
			title = "(missing from ledger)"
		}
		role := "context"
		if selected[member.Key()] {
			role = "selected"
		}
		fmt.Fprintf(&b, "\n#### %s #%d · %s [%s]\n\n", member.Kind, member.Number, title, role)
		if item.MissingFromLedger {
			b.WriteString("- Missing from the ledger; no local decision is available.\n")
		} else {
			handoffValue(&b, "State", item.State)
			var decision []string
			if len(item.ProposedLabels) > 0 {
				decision = append(decision, "labels "+strings.Join(item.ProposedLabels, ", "))
			}
			for _, part := range []struct{ label, value string }{{"action", item.Action}, {"confidence", item.Confidence}} {
				if part.value != "" {
					decision = append(decision, part.label+" "+part.value)
				}
			}
			if len(decision) > 0 {
				fmt.Fprintf(&b, "- Local decision: %s\n", strings.Join(decision, " · "))
				handoffValue(&b, "Proposed by", item.TriagedBy)
				handoffValue(&b, "Decision reason", item.Reason)
			} else {
				b.WriteString("- No local triage decision recorded.\n")
			}
			if item.ReviewRequest != nil {
				fmt.Fprintf(&b, "- Pending review requested by %s: %s\n", item.ReviewRequest.By, item.ReviewRequest.Reason)
			}
			handoffValue(&b, "Maintainer note", item.MaintainerNotes)
			handoffValue(&b, "Agent note", item.AgentNotes)
		}
		by := member.UpdatedBy
		if by == "" {
			by = member.AddedBy
		}
		noteLabel := "Group member note"
		if by != "" {
			noteLabel += " by " + by
		}
		if member.UpdatedAt != "" {
			noteLabel += " at " + member.UpdatedAt
		}
		handoffValue(&b, noteLabel, member.Notes)
		for _, event := range item.LocalContext.Feedback {
			switch event.Kind {
			case "rejection":
				label := "Earlier proposal rejected by " + event.By
				if event.At != "" {
					label += " at " + event.At
				}
				if strings.TrimSpace(event.Reason) == "" {
					fmt.Fprintf(&b, "- %s (no reason given)\n", label)
				} else {
					handoffValue(&b, label, event.Reason)
				}
			case "write_outcome":
				fmt.Fprintf(&b, "- Recorded %s write outcome: comment %s; state %s; request %s\n", event.Status, event.CommentStatus, event.StateStatus, event.RequestID)
			case "unreconciled_attempt":
				fmt.Fprintf(&b, "- Unreconciled write attempt: request %s\n", event.RequestID)
			default:
				fmt.Fprintf(&b, "- Retained proposal feedback: %s\n", event.Kind)
			}
		}
	}

	b.WriteString("\n### Current member checkpoints\n\nUse every member checkpoint with --member-context when preparing a group-first proposal:\n")
	for i, member := range packet.Group.Members {
		fmt.Fprintf(&b, "- %s:%d:%s\n", member.Kind, member.Number, packet.Items[i].LocalContext.Checkpoint)
	}

	relevant := make(map[string]bool)
	for i, member := range packet.Group.Members {
		if selected[member.Key()] {
			for _, id := range packet.Items[i].LocalContext.RelevantGroupIDs {
				relevant[id] = true
			}
		}
	}
	for _, related := range packet.RelatedGroups {
		if !relevant[related.ID] {
			continue
		}
		fmt.Fprintf(&b, "\n### Other relevant group · %s\n\n- ID: %s · revision %d · status %s\n", strings.Join(strings.Fields(related.Title), " "), related.ID, related.Revision, related.Status)
		handoffValue(&b, "Description", related.Description)
		for _, member := range related.Members {
			if selected[member.Key()] {
				label := fmt.Sprintf("Note on %s #%d", member.Kind, member.Number)
				if member.UpdatedBy != "" {
					label += " by " + member.UpdatedBy
				}
				handoffValue(&b, label, member.Notes)
			}
		}
	}

	fmt.Fprintf(&b, "\nRefresh guidance and omitted details before proposing: bin/group export %s --format json. Use --group-id %s and every checkpoint above as --member-context. No evidence was acquired for this copy; inspect selected snapshots and gaps separately. Do not use bin/batch --group here because it omits already-triaged members.\n", msg.id, msg.id)
	return b.String()
}

func (m model) finishGroupHandoff(msg groupHandoffMsg) (tea.Model, tea.Cmd) {
	g := m.selectedGroup()
	if !m.groups.open || !m.groups.detail || msg.root != m.installRoot || msg.repo != m.repo || msg.request != m.groups.handoffRequest || g == nil || g.ID != msg.id || g.Revision != msg.revision {
		return m, nil
	}
	m.groups.busy = false
	if msg.err != nil {
		m.failErr("Couldn't copy group handoff", msg.err)
		return m, nil
	}

	what := fmt.Sprintf("proposal handoff for %d group members", len(msg.selected))
	text := groupHandoffText(m.yankHeader(what), msg)
	m.status = "Taking " + what + "…"
	return m, yankCmd(m.installRoot, m.repo, what, text)
}
