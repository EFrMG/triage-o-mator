package main

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// runScript is the only way the TUI runs bin/* scripts (callers live here, in groups.go, batches.go, and duplicates.go); it never runs gh itself. It never touches the ledger directly: every mutation goes through bin/apply, matching the "only the scripts mutate the ledger" rule.
// Only stdout is returned, since several scripts print JSON there while progress, notices, and warnings go to stderr; stderr is surfaced in the error when the script fails.

func runScript(installRoot, name string, args ...string) (string, error) {
	return runScriptLines(installRoot, name, nil, args...)
}

// runScriptLines is runScript, calling onLine (when set) with each line the script writes to stderr as it goes, e.g. bin/group export's "fetching 3/12: pr #123".
func runScriptLines(installRoot, name string, onLine func(string), args ...string) (string, error) {
	cmd := exec.Command(filepath.Join(installRoot, "bin", name), args...)
	cmd.Dir = installRoot
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if onLine != nil {
		cmd.Stderr = &lineWriter{all: &stderr, onLine: onLine}
	}

	out, err := cmd.Output()
	if err != nil {
		detail := strings.TrimSpace(stderr.String() + "\n" + string(out))
		return string(out), &scriptError{script: name, args: args, output: detail, err: err}
	}

	return string(out), nil
}

// lineWriter keeps everything written to it, and calls onLine with each complete line as it arrives.
type lineWriter struct {
	all     *strings.Builder
	partial string
	onLine  func(string)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.all.Write(p)
	w.partial += string(p)
	for {
		i := strings.IndexByte(w.partial, '\n')
		if i < 0 {
			return len(p), nil
		}

		w.onLine(strings.TrimRight(w.partial[:i], "\r"))
		w.partial = w.partial[i+1:]
	}
}

// fetchSyncDoneMsg reports the result of a background `bin/fetch && bin/sync`; bin/fetch is incremental unless full is set (or it decides a full fetch is due).
type fetchSyncDoneMsg struct {
	err, trackingErr error
	summary          string
	unreadTotal      int
	repo             string
}

func fetchSyncCmd(installRoot, repo string, full bool) tea.Cmd {
	return func() tea.Msg {
		var args []string
		if full {
			args = append(args, "--full")
		}

		if _, err := runScript(installRoot, "fetch", args...); err != nil {
			return fetchSyncDoneMsg{repo: repo, err: err}
		}

		out, err := runScript(installRoot, "sync")
		if err != nil {
			return fetchSyncDoneMsg{repo: repo, err: err}
		}

		tracked, trackingErr := runScript(installRoot, "cache", "--expected-repo", repo, "track-check", "--request-budget", "100")
		var count struct {
			UnreadTotal int `json:"unread_total"`
		}
		if trackingErr == nil {
			trackingErr = json.Unmarshal([]byte(tracked), &count)
		}
		return fetchSyncDoneMsg{repo: repo, summary: out, trackingErr: trackingErr, unreadTotal: count.UnreadTotal}
	}
}

// ledgerReloadedMsg carries a freshly re-read ledger after fetch / sync or an apply.
type ledgerReloadedMsg struct {
	repo  string // which repo's ledger this is, so a reload that finishes after Switch Repo is dropped
	items []Item
	err   error
}

func reloadLedgerCmd(installRoot, repo string) tea.Cmd {
	return func() tea.Msg {
		items, err := LoadLedger(installRoot, repo)
		return ledgerReloadedMsg{repo: repo, items: items, err: err}
	}
}

// enrichedMsg carries the result of a lazy bin/enrich-one call for one item.
type enrichedMsg struct {
	afterComment, afterClose, afterReopen bool
	root, repo                            string
	generation                            uint64
	key                                   Key
	data                                  EnrichedItem
	err                                   error
}

// EnrichedItem is JSON output from bin/enrich-one (body / comments / diff).
type EnrichedItem struct {
	Number         int            `json:"number"`
	Kind           string         `json:"kind"`
	Title          string         `json:"title"`
	State          string         `json:"state"`
	URL            string         `json:"url"`
	Body           string         `json:"body"`
	CommentBodies  []string       `json:"comment_bodies"`
	CommentAuthors []string       `json:"comment_authors"`
	CommentDates   []string       `json:"comment_dates"`
	Additions      int            `json:"additions"`
	Deletions      int            `json:"deletions"`
	ChangedFiles   int            `json:"changed_files"`
	IsDraft        bool           `json:"is_draft"`
	Mergeable      string         `json:"mergeable"`
	DiffLoaded     bool           `json:"-"`
	DiffText       string         `json:"diff_text"`
	Evidence       *batchEvidence `json:"evidence,omitempty"`
	CachedRead     bool           `json:"-"`
}

type batchEvidence struct {
	SnapshotID string              `json:"snapshot_id"`
	Problems   map[string][]string `json:"problems"`
	Mode       string              `json:"mode"`
	Stats      struct {
		Requests int `json:"requests"`
	} `json:"stats"`
	Components map[string]*evidenceComponent `json:"components"`
}

type evidenceComponent struct {
	Status    string          `json:"status"`
	FetchedAt string          `json:"fetched_at"`
	Object    json.RawMessage `json:"object"`
}

func enrichItemCmd(installRoot, repo string, key Key, generation uint64, withDiff bool) tea.Cmd {
	return func() tea.Msg {
		args := []string{"--expected-repo", repo, "--kind", key.Kind, "--number", strconv.Itoa(key.Number)}
		if withDiff {
			args = append(args, "--diff")
		}

		out, err := runScript(installRoot, "enrich-one", args...)
		if err != nil {
			return enrichedMsg{root: installRoot, repo: repo, generation: generation, key: key, err: err}
		}

		var data EnrichedItem
		if err := json.Unmarshal([]byte(out), &data); err != nil {
			return enrichedMsg{root: installRoot, repo: repo, generation: generation, key: key, err: fmt.Errorf("parsing enrich-one output: %w", err)}
		}

		data.DiffLoaded = withDiff
		return enrichedMsg{root: installRoot, repo: repo, generation: generation, key: key, data: data}
	}
}

// applyDoneMsg reports the result of an apply (decision save or approve).
type applyDoneMsg struct {
	snapshot *decisionSnapshot
	approval bool
	key      Key
	// count is set for a bulk approval of ticked items (key is then unset); approved holds the approved items either way.
	count    int
	approved []Key
	err      error
}

// applyDecisionCmd saves one decision; batchID, when set, stamps it with the batch it was made in (bin/apply defaults to "tui").
// agentNotes, when non-empty, carries a batch proposal's notes into the ledger along with the decision saved from it; empty leaves the item's existing notes alone.
// reviewedBy records an explicit human confirmation with the save; by remains the decision author, which can be the author of an unchanged batch proposal.
func applyDecisionCmd(installRoot string, key Key, category, action, confidence, reason, agentNotes, by, batchID, reviewedBy string) tea.Cmd {
	return func() tea.Msg {
		args := []string{
			"--number", strconv.Itoa(key.Number), "--kind", key.Kind,
			"--category", category, "--action", action,
			"--reason", reason, "--by", by,
		}
		// bin/apply restricts --confidence to low/medium/high via argparse choices; omit the flag entirely rather than passing "" when unset.
		if confidence != "" {
			args = append(args, "--confidence", confidence)
		}

		if batchID != "" {
			args = append(args, "--batch-id", batchID)
		}

		if agentNotes != "" {
			args = append(args, "--agent-notes", agentNotes)
		}

		if reviewedBy != "" {
			args = append(args, "--reviewed", "--reviewed-by", reviewedBy)
		}

		_, err := runScript(installRoot, "apply", args...)

		return applyDoneMsg{key: key, err: err, approval: reviewedBy != "", approved: []Key{key}}
	}
}

func approveCmd(installRoot string, key Key, by string) tea.Cmd {
	return func() tea.Msg {
		_, err := runScript(installRoot, "apply", "--number", strconv.Itoa(key.Number), "--kind", key.Kind, "--approve", "--by", by)

		return applyDoneMsg{key: key, err: err, approval: true, approved: []Key{key}}
	}
}

// installPlannedMsg carries what bin/install-to --dry-run would do to a repository, and installedMsg what it did once the plan was accepted.
type installPlannedMsg struct {
	path string
	solo bool
	plan string
	err  error
}

type installedMsg struct {
	path string
	solo bool
	err  error
}

// installArgs is one place for what the TUI passes bin/install-to, so the plan it shows and the run it makes can never disagree about anything but --dry-run.
func installArgs(path string, solo bool, dryRun bool) []string {
	args := []string{path}
	if solo {
		args = append(args, "--solo")
	}

	if dryRun {
		return append(args, "--dry-run")
	}

	// The plan the person just read is the confirmation; --yes is what stops the script asking again for the files it already listed.
	return append(args, "--yes")
}

// installPlanCmd asks bin/install-to what it would change, writing nothing.
func installPlanCmd(scriptRoot, path string, solo bool) tea.Cmd {
	return func() tea.Msg {
		out, err := runScript(scriptRoot, "install-to", installArgs(path, solo, true)...)

		return installPlannedMsg{path: path, solo: solo, plan: out, err: err}
	}
}

// installCmd makes the change the plan described.
func installCmd(scriptRoot, path string, solo bool) tea.Cmd {
	return func() tea.Msg {
		_, err := runScript(scriptRoot, "install-to", installArgs(path, solo, false)...)

		return installedMsg{path: path, solo: solo, err: err}
	}
}
