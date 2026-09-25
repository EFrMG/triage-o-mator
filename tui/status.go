package main

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Status messages are informational unless something is waiting on them: they expire after a few seconds, and are dropped when you move to another screen, so a message never describes something that's no longer true.
const (
	statusTTL      = 5 * time.Second
	errorStatusTTL = 10 * time.Second
	statusTickRate = time.Second
)

type statusTickMsg struct{}

func statusTick() tea.Cmd {
	return tea.Tick(statusTickRate, func(time.Time) tea.Msg { return statusTickMsg{} })
}

// fail shows msg as an error (red, and it stays longer): for input the TUI refuses, where the wording alone wouldn't mark it as one.
func (m *model) fail(msg string) {
	m.status, m.statusError = msg, msg
}

func (m *model) warn(msg string) {
	m.status, m.statusWarning = msg, msg
}

// statusIsError reports whether the status line shows an error: one set through fail, or one whose wording says so.
func (m model) statusIsError() bool {
	return (m.statusError != "" && m.status == m.statusError) || isErrorStatus(m.status)
}

func isErrorStatus(s string) bool {
	lower := strings.ToLower(s)

	return strings.Contains(lower, "error") || strings.Contains(lower, "failed") || strings.HasPrefix(lower, "couldn't")
}

// statusPinned reports whether the current status is still in force: a confirmation waiting for a second key press, or work still running that it describes.
func (m model) statusPinned() bool {
	return m.confirmQuit || m.confirmSave || m.confirmApprove || m.confirmSwitch || m.batches.confirm != "" || m.listConfirm != "" || m.groups.confirm != "" ||
		m.groups.busy || m.batches.busy || m.dups.busy || m.comment.busy
}

// idleStatus is what the status line falls back to when a message expires: the running fetch, if any, otherwise nothing.
func (m model) idleStatus() string {
	if m.corpus.busy {
		return "Corpus operation running."
	}
	if m.refreshing {
		return m.refreshStatus
	}

	return ""
}

// screenKey identifies what's on screen, so a status that belongs to the previous screen can be dropped on navigation.
func (m model) screenKey() string {
	return fmt.Sprint(m.focus, m.groups.open, m.groups.detail, m.batches.open, m.dups.open, m.editingRepo, m.themePicker.open, m.activeTab, m.activeBatch, m.activePairs, m.detail.key)
}

// trackStatus runs after every update: it timestamps a new message, and clears an unchanged one when the screen changed underneath it.
func (m *model) trackStatus(before model) {
	switch {
	case m.status != before.status:
		m.statusAt = time.Now()
	case m.screenKey() != before.screenKey() && !m.statusPinned():
		m.status = m.idleStatus()
		m.statusAt = time.Now()
	}
}

func (m model) onStatusTick() (tea.Model, tea.Cmd) {
	ttl := statusTTL
	if m.statusIsError() {
		ttl = errorStatusTTL
	}

	if m.status != "" && m.status != m.idleStatus() && !m.statusPinned() && time.Since(m.statusAt) > ttl {
		m.status = m.idleStatus()
		m.statusAt = time.Now()
	}

	if m.corpus.open && m.corpus.busy && m.corpus.action == "run" && !m.corpus.observing && m.corpus.progressProblem == "" {
		m.corpus.observing = true
		if m.corpusObserverLifecycle == nil {
			m.corpusObserverLifecycle = &readLifecycle{}
		}
		m.corpusObserverLifecycle.current = &readProcess{}
		return m, tea.Batch(statusTick(), corpusCommand(m.installRoot, m.repo, m.corpusEpoch, m.corpus, "observe", m.corpusObserverLifecycle.current))
	}
	return m, statusTick()
}
