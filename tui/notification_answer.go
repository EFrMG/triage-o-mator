package main

import (
	"encoding/json"
	"fmt"
	"strconv"

	tea "charm.land/bubbletea/v2"
)

type proposalAnswerDoneMsg struct {
	root, repo, checkpoint, answer, by, target, comment string
	generation                                          uint64
	key                                                 Key
	saved                                               bool
	row                                                 actionProposalRow
	err                                                 error
}

func (m model) answerProposalCmd() tea.Cmd {
	root, repo, generation := m.installRoot, m.repo, m.notificationsGeneration
	choice, checkpoint := m.comment.answerChoice, m.comment.answerCheckpoint
	answer, by := m.comment.text.Value(), m.reviewer
	args := []string{"--expected-repo", repo, "answer", "--kind", choice.key.Kind, "--number", strconv.Itoa(choice.key.Number), "--checkpoint", checkpoint, "--by", by, "--answer", answer}
	row := m.notifications.actions.Rows[choice.actionProposal]
	target, comment := row.Target, row.Comment

	return func() tea.Msg {
		msg := proposalAnswerDoneMsg{root: root, repo: repo, generation: generation, key: choice.key, checkpoint: checkpoint,
			answer: answer, by: by, target: target, comment: comment}
		out, err := runScript(root, "action-proposals", args...)
		if err != nil {
			msg.err = err
			return msg
		}
		msg.saved = true
		if err := json.Unmarshal([]byte(out), &msg.row); err != nil {
			msg.err = fmt.Errorf("could not read saved action answer: %w", err)
			return msg
		}
		if msg.row.Number != choice.key.Number || msg.row.Status != "pending" || msg.row.Checkpoint == "" || msg.row.Checkpoint == checkpoint ||
			msg.row.Target != target || msg.row.Comment != comment || msg.row.DecisionQuestion == "" || msg.row.DecisionResolution == nil ||
			msg.row.DecisionResolution.By != by || msg.row.DecisionResolution.Reason != answer || msg.row.DecisionResolution.HeldCheckpoint != checkpoint ||
			msg.row.Kind != choice.key.Kind {
			msg.err = fmt.Errorf("saved answer differs from the displayed action proposal")
		}
		return msg
	}
}

func (m model) finishProposalAnswer(msg proposalAnswerDoneMsg) (tea.Model, tea.Cmd) {
	if !m.comment.open || m.comment.answerCheckpoint != msg.checkpoint || msg.root != m.installRoot || msg.repo != m.repo ||
		msg.generation != m.notificationsGeneration || msg.key != m.comment.key {
		return m, nil
	}
	m.comment.busy = false
	if !msg.saved {
		m.failErr("Couldn't save action answer; draft retained", msg.err)
		return m, nil
	}

	m.comment.open = false
	if msg.err != nil {
		m.recordError("Answer saved; inspect its new proposal version", msg.err)
		m.status = "Answer was saved but its response could not be checked. ! shows details."
	} else {
		m.status = fmt.Sprintf("Answered %s #%d action question; review the new exact proposal.", msg.key.Kind, msg.key.Number)
	}
	next, cmd := m.openNotifications()
	updated := next.(model)
	updated.notifications.selectItem = msg.key
	if msg.err == nil {
		updated.notifications.openActionAfter = msg.key
	}
	return updated, cmd
}
