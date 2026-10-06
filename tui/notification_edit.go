package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	tea "charm.land/bubbletea/v2"
)

type proposalEditDoneMsg struct {
	root, repo, checkpoint string
	generation             uint64
	key                    Key
	comment                string
	saved                  bool
	row                    actionProposalRow
	err                    error
}

func (m model) editProposalCmd() tea.Cmd {
	root, repo, generation := m.installRoot, m.repo, m.notificationsGeneration
	checkpoint, key := m.comment.proposalEditCheckpoint, m.comment.key
	comment, by := m.comment.text.Value(), m.reviewer

	return func() tea.Msg {
		msg := proposalEditDoneMsg{root: root, repo: repo, generation: generation, checkpoint: checkpoint, key: key, comment: comment}
		draft, err := os.CreateTemp("", "triage-proposal-edit-*.md")
		if err != nil {
			msg.err = err
			return msg
		}
		defer os.Remove(draft.Name())
		if _, err := draft.WriteString(comment); err != nil {
			draft.Close()
			msg.err = err
			return msg
		}
		if err := draft.Close(); err != nil {
			msg.err = err
			return msg
		}

		args := []string{"--expected-repo", repo, "edit", "--kind", key.Kind, "--number", strconv.Itoa(key.Number), "--checkpoint", checkpoint, "--comment-file", draft.Name(), "--by", by}
		out, err := runScript(root, "action-proposals", args...)
		if err != nil {
			msg.err = err
			return msg
		}
		msg.saved = true
		if err := json.Unmarshal([]byte(out), &msg.row); err != nil {
			msg.err = fmt.Errorf("could not read edited proposal: %w", err)
		} else if msg.row.Number != key.Number || msg.row.Status != "pending" || msg.row.Checkpoint == "" || msg.row.Checkpoint == checkpoint || msg.row.Comment != comment {
			msg.err = fmt.Errorf("saved proposal did not match the edited text")
		}
		return msg
	}
}

func (m model) finishProposalEdit(msg proposalEditDoneMsg) (tea.Model, tea.Cmd) {
	if !m.comment.open || m.comment.proposalEditCheckpoint != msg.checkpoint || msg.root != m.installRoot || msg.repo != m.repo || msg.generation != m.notificationsGeneration || msg.key != m.comment.key {
		return m, nil
	}
	m.comment.busy = false
	if !msg.saved {
		m.failErr("Couldn't save proposal edit; draft retained", msg.err)
		return m, nil
	}

	m.comment.open = false
	if msg.err != nil {
		m.recordError("Proposal changed; inspect the saved version", msg.err)
		m.status = "Proposal was saved but its response could not be checked. ! shows details."
	} else {
		m.status = fmt.Sprintf("Edited %s #%d proposal; review its new version before approval.", msg.key.Kind, msg.key.Number)
	}
	next, cmd := m.openNotifications()
	updated := next.(model)
	updated.notifications.selectItem = msg.key
	if msg.err == nil {
		updated.notifications.openActionAfter = msg.key
	}
	return updated, cmd
}
