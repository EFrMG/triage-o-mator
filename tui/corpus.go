package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const inventoryRequestBudget = 500
const datasetRequestBudget = 100
const datasetScope = "open-items"
const datasetProfile = "backlog"

type corpusProgress struct {
	ID         string `json:"corpus_id"`
	Repository struct {
		Host string `json:"host"`
		Name string `json:"full_name"`
	} `json:"repository"`
	Inventory string         `json:"inventory_snapshot"`
	Scope     string         `json:"scope"`
	Profile   string         `json:"profile"`
	MaxAge    int            `json:"max_age"`
	Members   int            `json:"members"`
	Status    string         `json:"status"`
	UpdatedAt string         `json:"updated_at"`
	Counts    map[string]int `json:"declared_counts"`
	LastRun   *struct {
		StartedAt string `json:"started_at"`
		Budget    int    `json:"request_budget"`
		Requests  int    `json:"requests"`
		Reason    string `json:"reason"`
	} `json:"last_run"`
}

type datasetUsage struct {
	Total int64 `json:"allocated_bytes"`
	Limit int64 `json:"limit_bytes"`
	Files int   `json:"file_count"`
}

type corpusUI struct {
	preparing               bool
	reuseID                 string
	automatic, autoRestore  bool
	retryHard               bool
	autoQueued              bool
	autoBefore, autoStalls  int
	preferenceProblem       string
	action, progressProblem string
	observing               bool
	open, busy              bool
	returnToNotifications   bool
	snapshot, id            string
	inventoryNotice         string
	offset                  int
	operation, observation  uint64
	progress                *corpusProgress
}

type corpusMsg struct {
	usage                         *datasetUsage
	root, action, id              string
	epoch, operation, observation uint64
	progress                      corpusProgress
	inventory                     inventoryResult
	err                           error
}

func validCorpusID(id string) bool {
	return len(id) == 64 && strings.Trim(id, "0123456789abcdef") == ""
}

func corpusCommand(root, repo string, epoch uint64, ui corpusUI, action string, process *readProcess) tea.Cmd {
	return func() tea.Msg {
		msg := corpusMsg{root: root, action: action, id: ui.id, epoch: epoch, operation: ui.operation, observation: ui.observation}
		if action == "capture" {
			out, err := runReadScript(process, root, "fetch", "--full", "--cache-inventory", "--host", evidenceHost, "--expected-repo", repo, "--request-budget", strconv.Itoa(inventoryRequestBudget), "--json")
			if err == nil {
				err = json.Unmarshal([]byte(out), &msg.inventory)
			}
			if err == nil {
				err = msg.inventory.validate(repo)
			}
			msg.err = err
			return msg
		}
		base := []string{"--host", evidenceHost, "--expected-repo", repo}
		call := func(args ...string) (string, error) {
			return runReadScript(process, root, "cache", append(base, args...)...)
		}
		if action == "handoff" || action == "restore" {
			args := []string{"handoff"}
			if action == "handoff" && validCorpusID(ui.id) {
				args = append(args, "--corpus", ui.id)
			}
			out, err := call(args...)
			msg.err = err
			if msg.err == nil {
				msg.err = json.Unmarshal([]byte(out), &msg.progress)
				msg.id = msg.progress.ID
				if msg.err == nil && msg.id != "" && (!validCorpusID(msg.id) || msg.progress.Repository.Host != evidenceHost || msg.progress.Repository.Name != repo) {
					msg.err = fmt.Errorf("selected dataset identity mismatch")
				}
			}
			return msg
		}
		if action == "usage" {
			out, err := call("usage")
			if err == nil {
				msg.usage = &datasetUsage{}
				err = json.Unmarshal([]byte(out), msg.usage)
			}
			msg.err = err
			return msg
		}
		if action == "create" {
			args := []string{"corpus-create", "--snapshot", ui.snapshot, "--scope", datasetScope, "--profile", datasetProfile, "--max-age", cacheMaxAgeArg}
			if ui.preparing && ui.reuseID != "" {
				args = append(args, "--reuse-corpus", ui.reuseID)
			}
			out, err := call(args...)
			var created struct {
				ID string `json:"corpus_id"`
			}
			if err == nil {
				err = json.Unmarshal([]byte(out), &created)
			}
			if err == nil && !validCorpusID(created.ID) {
				err = fmt.Errorf("invalid created corpus ID")
			}
			if err != nil {
				msg.err = err
				return msg
			}
			msg.id = created.ID
		}
		args := []string{"corpus-progress", msg.id}
		if action == "run" {
			args = []string{"corpus-run", msg.id, "--request-budget", strconv.Itoa(datasetRequestBudget), "--compact", "--bulk"}
			if !ui.preparing {
				args = append(args, "--keep-complete")
			}
		}
		out, err := call(args...)
		if err == nil {
			err = json.Unmarshal([]byte(out), &msg.progress)
		}
		if err == nil && (msg.progress.ID != msg.id || !validCorpusID(msg.id) || msg.progress.Repository.Host != evidenceHost || msg.progress.Repository.Name != repo || !validCorpusID(msg.progress.Inventory)) {
			err = fmt.Errorf("corpus response identity mismatch")
		}
		if err == nil && action == "create" {
			_, err = call("select", msg.id)
		}
		msg.err = err
		return msg
	}
}

func (m model) startCorpus(action string) (tea.Model, tea.Cmd) {
	if action == "run" {
		m.corpus.autoBefore = autoProcessed(m.corpus.progress)
	}
	m.corpus.busy = true
	m.corpus.action, m.corpus.progressProblem, m.corpus.observing = action, "", false
	m.corpus.operation++
	m.corpus.observation++
	m.corpusObserverLifecycle.stop()
	if m.corpusLifecycle == nil {
		m.corpusLifecycle = &readLifecycle{}
	}
	m.corpusLifecycle.current = &readProcess{}
	m.status = "Corpus operation running."
	return m, corpusCommand(m.installRoot, m.repo, m.corpusEpoch, m.corpus, action, m.corpusLifecycle.current)
}

func (m model) handleCorpusKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, keys.Help) {
		m.showHelp = !m.showHelp
		return m, nil
	}

	if key.Matches(msg, keys.Quit) {
		return m.requestQuit()
	}
	if key.Matches(msg, keys.Back, keys.Corpus) {
		m.corpus.open = false
		if m.corpus.returnToNotifications {
			m.corpus.returnToNotifications = false
			return m.openNotifications()
		}
		return m, nil
	}
	if msg.String() == "o" {
		return m.toggleAutomaticCorpus()
	}
	if key.Matches(msg, keys.CorpusStop) && m.corpus.busy {
		m.corpusLifecycle.stop()
		m.status = "Cancelling corpus operation; committed checkpoints remain."
		return m, nil
	}
	if key.Matches(msg, keys.Up, keys.Down) {
		if key.Matches(msg, keys.Down) {
			m.corpus.offset = minInt(m.corpus.offset+1, m.corpusScrollLimit())
		} else {
			m.corpus.offset = maxInt(minInt(m.corpus.offset, m.corpusScrollLimit())-1, 0)
		}
		return m, nil
	}
	if m.corpus.busy {
		return m, nil
	}
	switch msg.String() {
	case "u":
		return m.startCorpus("usage")
	case "y":
		return m.startCorpus("handoff")
	}
	m.corpus.offset = 0
	return m, nil
}

func (m model) openCorpus() (tea.Model, tea.Cmd) {
	if m.noInstall() {
		return m, nil
	}
	m.corpus.open = true
	if !m.corpus.busy {
		m.corpus.operation++
		if m.corpusObserverLifecycle == nil {
			m.corpusObserverLifecycle = &readLifecycle{}
		}
		m.corpusObserverLifecycle.stop()
		m.corpusObserverLifecycle.current = &readProcess{}
		return m, corpusCommand(m.installRoot, m.repo, m.corpusEpoch, m.corpus, "restore", m.corpusObserverLifecycle.current)
	}
	return m, nil
}

func (m model) finishCorpus(msg corpusMsg) (tea.Model, tea.Cmd) {
	if msg.root != m.installRoot || msg.epoch != m.corpusEpoch || msg.operation != m.corpus.operation {
		return m, nil
	}
	if msg.action == "restore" && msg.observation != m.corpus.observation {
		return m, nil
	}
	if msg.action == "observe" {
		if !m.corpus.busy || m.corpus.action != "run" || msg.id != m.corpus.id {
			return m, nil
		}
		m.corpus.observing = false
		if msg.err != nil {
			m.corpus.progressProblem = "Progress unavailable; download continues."
		} else {
			m.corpus.progress = &msg.progress
		}
		return m, nil
	}
	m.corpus.busy, m.corpus.observing = false, false
	m.corpus.observation++
	m.corpusObserverLifecycle.stop()
	if msg.action != "restore" && m.corpusLifecycle != nil && m.corpusLifecycle.current != nil {
		p := m.corpusLifecycle.current
		p.mu.Lock()
		cancelled := p.cancelled
		p.mu.Unlock()
		if cancelled {
			msg.err = context.Canceled
		}
	}
	if msg.err != nil {
		m.corpus.preparing = false
		if msg.action == "create" || msg.action == "run" {
			m.corpus.inventoryNotice = ""
		}
		m.corpus.autoQueued, m.corpus.autoRestore = false, false
		m.corpus.retryHard = false
		if msg.action == "capture" {
			m.corpus.inventoryNotice = "Capture failed/cancelled; previous selection retained. Raw publication may have completed: inspect the error details; recover with bin/cache import-inventory offline. No automatic retry or sync."
		}
		if errors.Is(msg.err, context.Canceled) {
			m.status = "Corpus operation cancelled; checkpoints retained."
		} else {
			m.recordError("Corpus operation failed", msg.err)
			m.status = "Corpus operation failed; checkpoints retained. Details are available after closing the menu."
		}
		return m, nil
	}
	if msg.action == "usage" {
		m.status = fmt.Sprintf("Local storage: %.2f / %.2f GB (%d files)", float64(msg.usage.Total)/1e9, float64(msg.usage.Limit)/1e9, msg.usage.Files)
		if m.corpus.autoQueued {
			return m.requestAutomaticCorpus()
		}
		return m, nil
	}
	if msg.action == "restore" {
		if msg.id != "" {
			m.corpus.id, m.corpus.progress = msg.id, &msg.progress
		} else {
			m.corpus.id, m.corpus.progress = "", nil
		}
		m.status = ""
		if m.corpus.autoRestore && m.corpus.automatic {
			m.corpus.autoRestore, m.corpus.autoQueued = false, false
			retryHard := m.corpus.retryHard
			m.corpus.retryHard = false
			if m.corpus.progress != nil && m.corpus.progress.Members > 0 && m.corpus.progress.Status != "finished" {
				if m.corpus.progress.Status == "pending" || m.corpus.progress.Status == "running" ||
					m.corpus.progress.Status == "stopped" && m.corpus.progress.LastRun != nil && (autoResumeReason(m.corpus.progress.LastRun.Reason) || retryHard) {
					return m.startCorpus("run")
				}
				m.status = "Download paused after the previous error; inspect it, then turn OFF and ON to retry."
				return m, nil
			}
			m.corpus.preparing, m.corpus.reuseID = true, m.corpus.id
			return m.startCorpus("capture")
		}
		return m, nil
	}
	if msg.action == "handoff" {
		if msg.id == "" {
			m.status = "No current dataset. Download one first."
			return m, nil
		}
		m.corpus.id, m.corpus.progress = msg.id, &msg.progress
		m.status = "Copying agent prompt…"
		copyCmd := yankCmd(m.installRoot, m.repo, "agent prompt", m.datasetPrompt())
		if m.corpus.autoQueued {
			next, autoCmd := m.requestAutomaticCorpus()
			return next, tea.Batch(copyCmd, autoCmd)
		}
		return m, copyCmd
	}
	if msg.action == "capture" {
		m.corpus.snapshot = msg.inventory.Snapshot
		m.corpus.inventoryNotice = fmt.Sprintf("Listed %d open items; preparing the download.", msg.inventory.Items)
		if msg.inventory.Status == "empty" {
			m.corpus.inventoryNotice = "Full inventory is empty; raw observation saved, no snapshot or corpus created. Previous download retained."
		}
		m.status = m.corpus.inventoryNotice
		if m.corpus.preparing && msg.inventory.Status != "empty" {
			return m.startCorpus("create")
		}
		m.corpus.preparing = false
		return m, nil
	}
	m.corpus.id, m.corpus.progress = msg.id, &msg.progress
	if m.corpus.preparing && msg.action == "create" && msg.progress.Members > 0 {
		return m.startCorpus("run")
	}
	m.corpus.preparing = false
	if msg.action == "create" || msg.action == "run" {
		m.corpus.inventoryNotice = ""
	}
	m.status = "Dataset: " + msg.progress.Status + "."
	if msg.action == "run" && m.corpus.automatic && msg.progress.Status == "stopped" && msg.progress.LastRun != nil && autoResumeReason(msg.progress.LastRun.Reason) {
		if autoProcessed(&msg.progress) <= m.corpus.autoBefore {
			m.corpus.autoStalls++
		} else {
			m.corpus.autoStalls = 0
		}
		if m.corpus.autoStalls < 2 {
			m.corpus.autoQueued = false
			return m.startCorpus("run")
		}
		m.status = "Automatic download paused because two runs made no item progress. Inspect the saved reason."
	} else if msg.action == "run" && m.corpus.automatic && msg.progress.Status == "stopped" && msg.progress.LastRun != nil {
		m.warn("Automatic download paused: " + truncateSentence(msg.progress.LastRun.Reason, 110))
	}
	m.corpus.autoQueued = false
	return m, nil
}

func (m model) corpusView() string {
	return m.datasetView()
}

func (m model) datasetPrompt() string {
	text := ""
	text += fmt.Sprintf("Work from the triage install at %q, targeting %s on %s. Read AGENTS.md and prompts/prepare-analysis.md there.\n\n", m.installRoot, m.repo, evidenceHost)
	text += "Use this saved dataset: " + m.corpus.id + ". Start with bin/cache handoff --corpus " + m.corpus.id + ".\n\n"
	if p := m.corpus.progress; p != nil {
		text += fmt.Sprintf("Saved coverage: %d complete, %d incomplete, %d failed, %d pending of %d items; pass status %s.\n",
			p.Counts["complete"], p.Counts["gaps"], p.Counts["error"], p.Counts["pending"]+p.Counts["running"], p.Members, singleLine(p.Status))
		if p.LastRun != nil && p.LastRun.Reason != "" {
			text += "Last stop: " + truncateSentence(singleLine(p.LastRun.Reason), 300) + "\n"
		}
		text += "A finished pass does not prove complete or current coverage.\n\n"
	}
	text += "Assess what this saved dataset can support. Use the full inventory for broad title and body exploration, then inspect focused downloaded detail where useful. Explain coverage, gaps, source references and which questions the available data can answer. Do not fetch, save decisions or act on GitHub."
	return text
}

func (m model) datasetScroll(text string) string {
	lines := strings.Split(ansi.Wrap(text, m.menuWidth(), ""), "\n")
	height := m.mainHeight()
	bodyHeight := maxInt(height-1, 0)
	offset := minInt(m.corpus.offset, maxInt(len(lines)-bodyHeight, 0))
	visible := append([]string{}, lines[offset:minInt(offset+bodyHeight, len(lines))]...)
	for len(visible) < bodyHeight {
		visible = append(visible, "")
	}
	return inset(strings.Join(append(visible, mutedText(datasetFooter(m.menuWidth()))), "\n"))
}

func (m model) datasetView() string {
	return m.datasetScroll(m.datasetText())
}

func (m model) corpusScrollLimit() int {
	return maxInt(len(strings.Split(ansi.Wrap(m.datasetText(), m.menuWidth(), ""), "\n"))-maxInt(m.mainHeight()-1, 0), 0)
}

func datasetFooter(width int) string {
	left := "Reuse eligible data within one day"
	right := "storage ceiling 5 GB"
	if width <= ansi.StringWidth(left)+ansi.StringWidth(right) {
		left = ansi.Truncate(left, maxInt(width-ansi.StringWidth(right)-1, 1), "…")
	}
	right = ansi.Truncate(right, maxInt(width-ansi.StringWidth(left)-1, 1), "…")
	return ansi.Truncate(left+strings.Repeat(" ", maxInt(width-ansi.StringWidth(left)-ansi.StringWidth(right), 1))+right, width, "…")
}

func (m model) datasetText() string {
	c := m.corpus
	heading := lipgloss.NewStyle().Bold(true).Foreground(focusedBorderColor)
	subtitle := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Foreground)).Render("Full cache management for local search & comparison")
	text := ansi.Truncate(titleBar(singleLine(m.repo), "", m.menuWidth())+"  "+subtitle, m.menuWidth(), "…") + "\n\n"
	text += lipgloss.PlaceHorizontal(m.menuWidth(), lipgloss.Center, heading.Render("Automatic download")) + "\n"
	text += "\n" + datasetAutoToggle(c.automatic, m.menuWidth()) + "\n\n"
	text += lipgloss.PlaceHorizontal(m.menuWidth(), lipgloss.Center, mutedText("Starts after each backlog refresh.")) + "\n\n\n"
	if c.preferenceProblem != "" {
		text += lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Warning)).Render(c.preferenceProblem) + "\n\n"
	}
	activity := ""
	if c.busy {
		switch c.action {
		case "capture":
			activity = "listing open items"
		case "create":
			activity = "preparing download"
		case "run":
			activity = "downloading"
		}
	}

	if p := c.progress; p != nil {
		status := singleLine(p.Status)
		if activity != "" {
			status = activity
		}
		text += heading.Render(fmt.Sprintf("Selected download · %d items · %s", p.Members, status)) + "\n\n"
		done := p.Counts["complete"] + p.Counts["gaps"] + p.Counts["error"]
		text += progressBar(minInt(done, p.Members), p.Members, minInt(m.menuWidth()-2, 28)) + fmt.Sprintf(" %d/%d items processed\n\n", done, p.Members)
		text += fmt.Sprintf("Available at last check %d · incomplete %d · pending %d · failed %d\n", p.Counts["complete"], p.Counts["gaps"], p.Counts["pending"]+p.Counts["running"], p.Counts["error"])
		if p.UpdatedAt != "" {
			text += mutedText("Last checkpoint: "+singleLine(p.UpdatedAt)) + "\n"
		}
		if p.LastRun != nil && p.LastRun.Reason != "" && !(c.busy && c.action == "run") {
			text += lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Warning)).Render("Stopped: "+singleLine(p.LastRun.Reason)) + "\n"
		}
	} else {
		if activity != "" {
			text += heading.Render("Download · "+activity) + "\n"
			text += "Item counts will appear after the open-item list is ready.\n"
		} else {
			text += heading.Render("No download selected") + "\n"
			text += "Turn automatic download ON to begin.\n"
		}
		if c.inventoryNotice != "" {
			text += singleLine(c.inventoryNotice) + "\n"
		}
	}
	if c.progress != nil {
		if c.inventoryNotice != "" {
			text += singleLine(c.inventoryNotice) + "\n"
		}
	}
	if c.progressProblem != "" {
		text += c.progressProblem + "\n"
	}
	return strings.TrimRight(text, "\n")
}

func datasetAutoToggle(automatic bool, width int) string {
	selected := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Background)).Background(lipgloss.Color(currentTheme.Accent)).Bold(true).Padding(1, 1)
	plain := lipgloss.NewStyle().Padding(1, 1)
	on, off := plain.Render("ON"), plain.Render("OFF")
	if automatic {
		on = selected.Render("ON")
	} else {
		off = selected.Render("OFF")
	}
	return lipgloss.PlaceHorizontal(maxInt(width, 1), lipgloss.Center, lipgloss.JoinHorizontal(lipgloss.Top, on, " ", off))
}
