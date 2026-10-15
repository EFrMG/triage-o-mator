package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	tea "charm.land/bubbletea/v2"
)

// itemFormEdit pins one confirmed form field. The queue preserves confirmation order while allowing the maintainer to keep reading and navigating.
type itemFormEdit struct {
	id            uint64
	root, repo    string
	key           Key
	field         formField
	snapshot      decisionSnapshot
	proposal      proposal
	expected      map[string]any
	by, batch     string
	host, state   string
	before        []string
	priorProposal []string
}

type itemFormEditMsg struct {
	edit  itemFormEdit
	items []Item
	err   error
}

func (m model) confirmItemField(field formField) (tea.Model, tea.Cmd) {
	it, ok := m.findItem(m.detail.key)
	if !ok {
		return m, nil
	}
	edit := itemFormEdit{root: m.installRoot, repo: m.repo, key: it.Key(), field: field, snapshot: m.form.Snapshot(), by: m.contributor, batch: m.activeBatch}
	if edit.batch == "" {
		edit.batch = "tui"
	}
	edit.proposal = proposal{fields: map[string]bool{}}
	edit.expected = map[string]any{}
	switch field {
	case fieldLabels:
		if m.form.labelOptions() == nil || len(m.form.badLabels) > 0 {
			m.fail("Sync Labels in Settings before confirming this selection.")
			return m, nil
		}
		target, err := validateCommentTarget(it, m.repo)
		if err != nil {
			m.failErr("Can't publish labels for this item", err)
			return m, nil
		}
		edit.host, edit.state = target.host, it.State
		edit.before, edit.priorProposal = slices.Clone(m.form.observedLabels), slices.Clone(it.ProposedLabels)
	case fieldAction:
		if m.form.badAction != "" {
			m.fail("Choose an action from Settings before confirming it.")
			return m, nil
		}
		edit.proposal.Action, edit.proposal.fields["action"] = m.form.Action(), true
		edit.expected["action"] = it.Action
	case fieldReason:
		edit.proposal.Reason, edit.proposal.fields["reason"] = m.form.Reason(), true
		edit.expected["reason"] = it.Reason
	default:
		return m, nil
	}
	if m.form.proposed && m.form.proposalEdit.has("agent_notes") {
		edit.proposal.AgentNotes, edit.proposal.fields["agent_notes"] = m.form.proposalNotes, true
		edit.expected["agent_notes"] = it.AgentNotes
	}

	return m.enqueueItemEdit(edit)
}

func (m model) enqueueItemEdit(edit itemFormEdit) (tea.Model, tea.Cmd) {
	for _, prior := range m.itemEdits {
		if prior.key != edit.key {
			continue
		}
		for _, value := range []struct{ field, text string }{{"action", prior.proposal.Action}, {"reason", prior.proposal.Reason}, {"agent_notes", prior.proposal.AgentNotes}} {
			if prior.proposal.has(value.field) && edit.proposal.has(value.field) {
				edit.expected[value.field] = value.text
			}
		}
		if prior.key == edit.key && prior.field == fieldLabels && edit.field == fieldLabels {
			edit.before, edit.priorProposal = slices.Clone(prior.snapshot.proposedLabels), nil
		}
	}
	m.itemEditSeq++
	edit.id = m.itemEditSeq
	m.itemEdits = append(slices.Clone(m.itemEdits), edit)
	m.pendingApply++
	m.drafts[edit.key] = edit.snapshot
	if m.detail.key == edit.key {
		m.form.touched, m.form.saved = true, false
	}
	m.status = "Saving item changes…"
	if edit.field == fieldLabels {
		m.status = "Publishing labels…"
	}
	if len(m.itemEdits) != 1 {
		return m, nil
	}

	return m, itemFormEditCmd(edit)
}

func itemFormEditCmd(edit itemFormEdit) tea.Cmd {
	return func() tea.Msg {
		msg := itemFormEditMsg{edit: edit}
		if edit.field == fieldLabels {
			args := []string{"edit", "--expected-repo", edit.repo, "--host", edit.host, "--kind", edit.key.Kind, "--number", strconv.Itoa(edit.key.Number),
				"--expected-state", edit.state, "--labels", labelNamesJSON(edit.snapshot.proposedLabels), "--expected-labels", labelNamesJSON(edit.before),
				"--expected-proposals", labelNamesJSON(edit.priorProposal), "--by", edit.by, "--batch-id", edit.batch}
			var preview struct {
				Approval string `json:"approval"`
				Plan     struct {
					RequestID string `json:"request_id"`
				} `json:"plan"`
			}
			var out string
			out, msg.err = runScript(edit.root, "item-labels", args...)
			if msg.err == nil {
				msg.err = json.Unmarshal([]byte(out), &preview)
			}
			if msg.err == nil && (preview.Approval == "" || preview.Plan.RequestID == "") {
				msg.err = fmt.Errorf("label edit returned no exact publication plan")
			}
			if msg.err == nil {
				_, msg.err = runScript(edit.root, "item-labels", append(args, "--publish", "--request-id", preview.Plan.RequestID, "--approve", preview.Approval)...)
			}
		}
		if msg.err == nil && edit.proposal.hasEdits() {
			msg.err = applyDecisionCmd(edit.root, edit.repo, edit.key, edit.proposal, edit.by, edit.batch, false, edit.expected)().(applyDoneMsg).err
		}
		if msg.err == nil {
			msg.items, msg.err = LoadLedger(edit.root, edit.repo)
		}

		return msg
	}
}

func labelNamesJSON(names []string) string {
	if names == nil {
		names = []string{}
	}
	data, _ := json.Marshal(names)
	return string(data)
}

func (m model) finishItemEdit(msg itemFormEditMsg) (tea.Model, tea.Cmd) {
	edit := msg.edit
	if edit.root != m.installRoot || edit.repo != m.repo || len(m.itemEdits) == 0 || m.itemEdits[0].id != edit.id {
		return m, nil
	}
	m.itemEdits = slices.Clone(m.itemEdits[1:])
	m.pendingApply--
	if msg.err != nil {
		// Later edits to this item depend on the failed selection. Keep their drafts, and continue only independent items.
		pending := m.itemEdits[:0]
		for _, queued := range m.itemEdits {
			if queued.key == edit.key {
				m.pendingApply--
				continue
			}
			pending = append(pending, queued)
		}
		m.itemEdits = pending
		if m.detail.key == edit.key {
			m.form.dirty, m.form.saved = true, false
		}
		m.failErr(fmt.Sprintf("Couldn't apply changes to %s #%d; draft kept", edit.key.Kind, edit.key.Number), msg.err)
	} else {
		if draft, ok := m.drafts[edit.key]; ok {
			updateDraftBaseline(&draft, edit)
			if m.itemDraftDirty(draft) {
				m.drafts[edit.key] = draft
			} else {
				delete(m.drafts, edit.key)
			}
		}
		if m.detail.key == edit.key {
			current := m.form.Snapshot()
			updateDraftBaseline(&current, edit)
			m.form.baseLabels, m.form.baseAction, m.form.baseReason = current.baseLabels, current.baseAction, current.baseReason
			m.form.observedLabels = current.observedLabels
			m.form.updateDirty()
			if !m.form.dirty {
				delete(m.drafts, edit.key)
			}
		}
		if msg.items != nil {
			m.items = msg.items
			m.recomputeSidebarCounts()
			m.refreshActiveList()
		}
		m.status = "Item updated."
		if edit.field == fieldLabels {
			m.status = "Labels published."
		}
	}
	if len(m.itemEdits) > 0 {
		return m, itemFormEditCmd(m.itemEdits[0])
	}

	return m, reloadLedgerCmd(m.installRoot, m.repo)
}

func updateDraftBaseline(draft *decisionSnapshot, edit itemFormEdit) {
	switch edit.field {
	case fieldLabels:
		draft.baseLabels = slices.Clone(edit.snapshot.proposedLabels)
		draft.observedLabels = slices.Clone(edit.snapshot.proposedLabels)
	case fieldAction:
		draft.baseAction = edit.proposal.Action
	case fieldReason:
		draft.baseReason = edit.proposal.Reason
	}
}

func (m model) itemDraftDirty(draft decisionSnapshot) bool {
	action := draft.badAction
	if action == "" && draft.actionIdx >= 0 {
		options := m.taxonomy.SelectableActions()
		if len(options) > 0 {
			action = options[draft.actionIdx%len(options)]
		}
	}

	return !slices.Equal(draft.proposedLabels, draft.baseLabels) || action != draft.baseAction || draft.reason != draft.baseReason
}
