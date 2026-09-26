package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var corpusItemLimits = []int{100, 500, 1000, 5000, 10000, 0}

const inventoryRequestBudget = 500
const datasetScope = "open-items"
const datasetProfile = "backlog"

func corpusRequestBudget(itemLimit, members int) int {
	if itemLimit == 0 {
		itemLimit = members
	}
	return 9*((maxInt(itemLimit, 1)+79)/80) + 2
}

func corpusItemLimitLabel(itemLimit int) string {
	if itemLimit == 0 {
		return "full"
	}
	return strconv.Itoa(itemLimit)
}

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
	usage                   *datasetUsage
	preparing               bool
	reuseID                 string
	action, progressProblem string
	previousRun             string
	observing               bool
	open, busy              bool
	snapshot, id            string
	budget                  int
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
			limit := corpusItemLimits[ui.budget]
			args = []string{"corpus-run", msg.id, "--request-budget", strconv.Itoa(corpusRequestBudget(limit, ui.progress.Members)), "--compact", "--bulk"}
			if limit > 0 {
				args = append(args, "--item-limit", strconv.Itoa(limit))
			}
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
	m.corpus.busy = true
	m.corpus.action, m.corpus.progressProblem, m.corpus.observing = action, "", false
	m.corpus.previousRun = ""
	if m.corpus.progress != nil && m.corpus.progress.LastRun != nil {
		m.corpus.previousRun = m.corpus.progress.LastRun.StartedAt
	}
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
		return m, nil
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
	if msg.String() == "d" {
		m.corpus.offset = 0
		if m.refreshing {
			m.status = "Wait for the backlog refresh to finish before downloading the dataset."
			return m, nil
		}
		m.corpus.inventoryNotice = ""
		m.corpus.preparing, m.corpus.reuseID = true, m.corpus.id
		return m.startCorpus("capture")
	}
	switch {
	case key.Matches(msg, keys.CorpusBudget):
		m.corpus.budget = (m.corpus.budget + 1) % len(corpusItemLimits)
	case key.Matches(msg, keys.CorpusRun) && m.corpus.progress != nil && m.corpus.progress.Members > 0:
		m.corpus.offset = 0
		return m.startCorpus("run")
	}
	m.corpus.offset = 0
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
		if msg.action == "capture" {
			m.corpus.inventoryNotice = "Capture failed/cancelled; previous selection retained. Raw publication may have completed: inspect !; recover with bin/cache import-inventory offline. No automatic retry or sync."
		}
		if errors.Is(msg.err, context.Canceled) {
			m.status = "Corpus operation cancelled; checkpoints retained."
		} else {
			m.recordError("Corpus operation failed", msg.err)
			m.status = "Corpus operation failed; checkpoints retained. ! shows details after closing the menu."
		}
		return m, nil
	}
	if msg.action == "usage" {
		m.corpus.usage = msg.usage
		m.status = "Local cache size measured."
		return m, nil
	}
	if msg.action == "restore" {
		if msg.id != "" {
			m.corpus.id, m.corpus.progress = msg.id, &msg.progress
		} else {
			m.corpus.id, m.corpus.progress = "", nil
		}
		m.status = ""
		return m, nil
	}
	if msg.action == "handoff" {
		if msg.id == "" {
			m.status = "No current dataset. Download with d."
			return m, nil
		}
		m.corpus.id, m.corpus.progress = msg.id, &msg.progress
		m.status = "Copying agent prompt…"
		return m, yankCmd(m.installRoot, m.repo, "agent prompt", m.datasetPrompt())
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
	m.status = "Dataset: " + msg.progress.Status + "."
	return m, nil
}

func (m model) corpusView() string {
	return m.datasetView()
}

func (m model) datasetPrompt() string {
	text := ""
	text += fmt.Sprintf("Work from the triage install at %q, targeting %s on %s. Read AGENTS.md and prompts/prepare-analysis.md there.\n\n", m.installRoot, m.repo, evidenceHost)
	text += "Use this saved dataset: " + m.corpus.id + ". Start with bin/cache handoff --corpus " + m.corpus.id + ".\n\n"
	text += "Assess what this saved dataset can support. Use the full inventory for broad title and body exploration, then inspect focused downloaded detail where useful. Explain coverage, gaps, source references and which questions the available data can answer. Do not fetch, save decisions or act on GitHub."
	return text
}

func (m model) datasetScroll(text string) string {
	lines := strings.Split(ansi.Wrap(text, m.menuWidth(), ""), "\n")
	height := m.mainHeight()
	offset := minInt(m.corpus.offset, maxInt(len(lines)-height, 0))
	return inset(strings.Join(lines[offset:minInt(offset+height, len(lines))], "\n"))
}

func (m model) datasetView() string {
	return m.datasetScroll(m.datasetText())
}

func (m model) corpusScrollLimit() int {
	return maxInt(len(strings.Split(ansi.Wrap(m.datasetText(), m.menuWidth(), ""), "\n"))-m.mainHeight(), 0)
}

func (m model) datasetText() string {
	c := m.corpus
	heading := lipgloss.NewStyle().Bold(true).Foreground(focusedBorderColor)
	text := titleBar(singleLine(m.repo), "· Full cache management for local search & comparison", m.menuWidth()) + "\n\n"
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
		text += mutedText(fmt.Sprintf("%s · %s · update reuse within %g hours", singleLine(p.Scope), singleLine(p.Profile), float64(p.MaxAge)/3600)) + "\n"
		if p.UpdatedAt != "" {
			text += mutedText("Last checkpoint: "+singleLine(p.UpdatedAt)) + "\n"
		}
		label, requests, budget := "Next run", 0, corpusRequestBudget(corpusItemLimits[c.budget], p.Members)
		if p.LastRun != nil {
			label, requests, budget = "Last run", p.LastRun.Requests, p.LastRun.Budget
		}
		if c.busy && c.action == "run" {
			label = "This run"
			if p.LastRun == nil || p.LastRun.StartedAt == c.previousRun {
				requests, budget = 0, corpusRequestBudget(corpusItemLimits[c.budget], p.Members)
			}
		}
		text += "\n" + heading.Render(label+" · request allowance") + "\n\n"
		text += progressBar(minInt(maxInt(requests, 0), budget), budget, minInt(m.menuWidth()-2, 28)) + fmt.Sprintf(" %d/%d requests\n\n", requests, budget)
		text += mutedText("Saved at item checkpoints, not a live request counter.") + "\n"
		if p.LastRun != nil && p.LastRun.Reason != "" && !(c.busy && c.action == "run") {
			text += lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Warning)).Render("Stopped: "+singleLine(p.LastRun.Reason)) + "\n"
		}
	} else {
		if activity != "" {
			text += heading.Render("Download · "+activity) + "\n"
			text += "Item counts will appear after the open-item list is ready.\n"
		} else {
			text += heading.Render("No download selected") + "\n"
			text += "Download the backlog to begin.\n"
		}
		if c.inventoryNotice != "" {
			text += singleLine(c.inventoryNotice) + "\n"
		}
	}
	if c.progress != nil {
		text += mutedText("Updates at saved item checkpoints; processed includes gaps and failures.") + "\n"
		if c.inventoryNotice != "" {
			text += singleLine(c.inventoryNotice) + "\n"
		}
	}
	if c.progressProblem != "" {
		text += c.progressProblem + "\n"
	}
	text += "\n\n" + heading.Render("Analyze with an agent") + "\n"
	text += "Copy the agent prompt, then paste it into a session with access to this checkout.\n"
	text += "\n\n"
	text += heading.Render("Download limits") + "\n\n"
	text += fmt.Sprintf("Listing  %d GitHub requests\n", inventoryRequestBudget) + mutedText("Fixed ceiling to discover open items.") + "\n\n"
	text += datasetBudget("Item data", c.budget) + "\n" + mutedText("Items per run; shared allowance covers batched API reads and Git transfers.") + "\n\n"
	text += mutedText("Limits apply per run. A limit stop saves progress; resume to continue.") + "\n"
	text += "Reuse eligible data within one day · storage ceiling 5 GB\n"
	if c.usage != nil {
		text += fmt.Sprintf("Local storage: %.2f / %.2f GB (%d files)\n", float64(c.usage.Total)/1e9, float64(c.usage.Limit)/1e9, c.usage.Files)
	}
	text += mutedText("Updates include newly opened items. Full downloads start with PRs; resume tries pending items before gaps.") + "\n"
	text += mutedText("Local data: "+filepath.Join(m.installRoot, "data", m.repo, "cache")) + "\n"
	return text
}

func datasetBudget(label string, selected int) string {
	choices := make([]string, len(corpusItemLimits))
	for i, limit := range corpusItemLimits {
		choices[i] = corpusItemLimitLabel(limit)
		if i == selected {
			choices[i] = lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Background)).Background(focusedBorderColor).Bold(true).Render(" " + choices[i] + " ")
		} else {
			choices[i] = mutedText(choices[i])
		}
	}
	return label + "  " + strings.Join(choices, " · ")
}
