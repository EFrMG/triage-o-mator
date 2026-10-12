package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

const checksPageSize = 20

type checkRow struct {
	Source      string   `json:"source"`
	Repository  string   `json:"repository"`
	App         string   `json:"app"`
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	Conclusion  string   `json:"conclusion"`
	URL         string   `json:"url"`
	StartedAt   string   `json:"started_at"`
	CompletedAt string   `json:"completed_at"`
	UpdatedAt   string   `json:"updated_at"`
	HeadSHA     string   `json:"head_sha"`
	Result      string   `json:"result"`
	Problems    []string `json:"problems"`
}

type checksRepository struct {
	Host     string `json:"host"`
	FullName string `json:"full_name"`
}

type checksPage struct {
	RawCount         int `json:"raw_count"`
	HistoryOmissions int `json:"historical_or_suite_omissions"`
	ResultCounts     struct {
		Passed  int `json:"passed"`
		Failed  int `json:"failed"`
		Pending int `json:"pending"`
		Unknown int `json:"unknown"`
	} `json:"result_counts"`
	Repository          checksRepository   `json:"repository"`
	AllowedRepositories []checksRepository `json:"allowed_repositories"`
	ChecksHead          string             `json:"checks_head_sha"`
	Kind                string             `json:"kind"`
	Number              int                `json:"number"`
	Mode                string             `json:"mode"`
	Snapshot            string             `json:"snapshot_id"`
	Head                string             `json:"observed_head_sha"`
	ObservedAt          string             `json:"observed_at"`
	Coverage            string             `json:"coverage"`
	Freshness           string             `json:"freshness"`
	FreshnessBasis      string             `json:"freshness_basis"`
	Result              string             `json:"observed_result"`
	Problems            []string           `json:"problems"`
	Rows                []checkRow         `json:"rows"`
	Requests            int                `json:"requests"`
	Pagination          struct {
		Total    int  `json:"total"`
		Offset   int  `json:"offset"`
		Returned int  `json:"returned"`
		Before   int  `json:"omitted_before"`
		After    int  `json:"omitted_after"`
		Next     *int `json:"next_offset"`
	} `json:"pagination"`
}

type checksUI struct {
	loaded, busy    bool
	snapshot        string
	pendingSnapshot string
	page            checksPage
}

type checksMsg struct {
	root, repo, sourceSnapshot, snapshot string
	fixed                                bool
	key                                  Key
	generation, request                  uint64
	data                                 checksPage
	err                                  error
}

func (d detailModel) checksActive() bool {
	return d.key.Kind == "pr" && d.active >= 0 && d.active < len(d.sections) && d.sections[d.active].name == "Checks"
}

func (m model) checksSource() (string, bool) {
	if evidence := m.detail.enriched.Evidence; evidence != nil {
		return evidence.SnapshotID, true
	}
	return "", m.detail.blockDirect
}

func checksReadCmd(root, repo string, item Key, mode, sourceSnapshot, snapshot string, fixed bool, offset int, generation, request uint64, process *readProcess) tea.Cmd {
	return func() tea.Msg {
		msg := checksMsg{root: root, repo: repo, key: item, sourceSnapshot: sourceSnapshot, snapshot: snapshot, fixed: fixed, generation: generation, request: request}
		args := []string{"--expected-repo", repo, "--number", strconv.Itoa(item.Number), "--mode", mode, "--host", evidenceHost, "--max-age", cacheMaxAgeArg, "--request-budget", "100", "--offset", strconv.Itoa(offset), "--limit", strconv.Itoa(checksPageSize)}
		if snapshot != "" {
			args = append(args, "--snapshot", snapshot)
		}

		out, err := runReadScript(process, root, "pr-checks", args...)
		if err == nil {
			err = json.Unmarshal([]byte(out), &msg.data)
		}
		if err == nil {
			err = validateChecksPage(msg.data, repo, item.Number, mode, snapshot, offset)
		}
		msg.err = err
		return msg
	}
}

func validateChecksPage(page checksPage, repo string, number int, mode, snapshot string, offset int) error {
	p := page.Pagination
	if page.Repository.Host != evidenceHost || page.Repository.FullName != repo || page.Kind != "pr" || page.Number != number || page.Mode != mode || snapshot != "" && page.Snapshot != snapshot {
		return fmt.Errorf("checks response identity, mode or selected snapshot mismatch")
	}
	if page.Requests < 0 || page.Requests > 100 || mode == "offline" && page.Requests != 0 || len(page.Rows) > checksPageSize || p.Returned != len(page.Rows) || p.Offset != offset || p.Total < 0 || page.HistoryOmissions < 0 || page.RawCount != p.Total+page.HistoryOmissions || p.Before != offset || p.After < 0 || p.Total != p.Before+p.Returned+p.After || p.Next != nil && (*p.Next != offset+len(page.Rows) || p.After == 0 || len(page.Rows) == 0) {
		return fmt.Errorf("checks response budget or pagination mismatch")
	}
	if !slices.Contains([]string{"complete", "partial", "missing", "failed", "unavailable"}, page.Coverage) || !slices.Contains([]string{"within-window", "stale", "unknown"}, page.Freshness) || !slices.Contains([]string{"passed", "failed", "pending", "unknown", "empty"}, page.Result) {
		return fmt.Errorf("checks response coverage, freshness or result is invalid")
	}
	counts := page.ResultCounts
	if counts.Passed < 0 || counts.Failed < 0 || counts.Pending < 0 || counts.Unknown < 0 || counts.Passed+counts.Failed+counts.Pending+counts.Unknown != p.Total || (p.After > 0) != (p.Next != nil) {
		return fmt.Errorf("checks response counts or continuation mismatch")
	}

	for _, row := range page.Rows {
		if !slices.ContainsFunc(page.AllowedRepositories, func(repository checksRepository) bool {
			return repository.Host == evidenceHost && repository.FullName == row.Repository
		}) || row.HeadSHA != "" && row.HeadSHA != page.ChecksHead || !slices.Contains([]string{"passed", "failed", "pending", "unknown"}, row.Result) || !slices.Contains([]string{"check-run", "check-suite", "status"}, row.Source) {
			return fmt.Errorf("checks row repository, revision or result mismatch")
		}
	}
	if (len(page.Rows) > 0 || p.Total > 0) && page.Snapshot == "" {
		return fmt.Errorf("checks response has rows without an immutable snapshot")
	}
	return nil
}

func (m *model) stopChecks() {
	m.checksLifecycle.stop()
	m.checksRequest++
	if m.checks.busy && m.checks.loaded && m.checks.page.Coverage != "" {
		m.checksText(renderChecks(m.checks.page))
	}
	m.checks.busy = false
}

func (m *model) checksText(text string) {
	for i := range m.detail.sections {
		s := &m.detail.sections[i]
		if s.name == "Checks" {
			s.source, s.loaded, s.renderedFor = text, true, ""
		}
	}
	m.detail.renderActive()
}

func (m *model) readChecks(mode string, offset int) tea.Cmd {
	if !m.detail.checksActive() {
		return nil
	}
	source, fixed := m.checksSource()
	if fixed && mode != "offline" {
		m.status = "Checks are pinned to this item packet. Reopen a current item to acquire focused checks."
		return nil
	}
	if fixed && source == "" {
		m.checksText("No selected checks snapshot is available in this item packet. No automatic fetch.")
		m.checks.loaded = !m.detail.loading
		return nil
	}
	snapshot := m.checks.snapshot
	if fixed {
		snapshot = source
	}
	if mode == "refresh" {
		snapshot = ""
	}
	if offset > 0 && snapshot == "" {
		return nil
	}

	m.stopChecks()
	if m.checksLifecycle == nil {
		m.checksLifecycle = &readLifecycle{}
	}
	m.checksLifecycle.current = &readProcess{}
	m.checks.pendingSnapshot, m.checks.busy = snapshot, true
	m.checksText("Reading checks " + mode + "…")
	return checksReadCmd(m.installRoot, m.repo, m.detail.key, mode, source, snapshot, fixed, offset, m.detail.generation, m.checksRequest, m.checksLifecycle.current)
}

func (m *model) checksIfNeeded() tea.Cmd {
	if !m.detail.checksActive() {
		return nil
	}
	if !m.checks.loaded && !m.checks.busy {
		return m.readChecks("offline", 0)
	}
	return nil
}

func renderChecks(page checksPage) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Coverage: %s · Freshness: %s\n\nObserved result: %s\n\n", page.Coverage, page.Freshness, page.Result)
	counts := page.ResultCounts
	fmt.Fprintf(&b, "Check/status observations: %d passed · %d failed · %d pending · %d unknown\n\n", counts.Passed, counts.Failed, counts.Pending, counts.Unknown)
	categories := 0
	for _, count := range []int{counts.Passed, counts.Failed, counts.Pending, counts.Unknown} {
		if count > 0 {
			categories++
		}
	}
	if categories > 1 {
		b.WriteString("Mixed observations.\n\n")
	}
	head, observed := page.Head, page.ObservedAt
	if head == "" {
		head = "unknown"
	}
	if observed == "" {
		observed = "unknown"
	}
	fmt.Fprintf(&b, "Observed PR head: %s\n\nObserved at: %s\n\n", head, observed)
	if page.ChecksHead != "" {
		fmt.Fprintf(&b, "Checks head: %s\n\n", page.ChecksHead)
	}
	if page.FreshnessBasis != "" {
		fmt.Fprintf(&b, "Freshness basis: %s\n\n", singleLine(page.FreshnessBasis))
	}
	b.WriteString("This observation does not establish required checks or merge readiness.\n\n")
	fmt.Fprintf(&b, "%d source observations · %d historical or suite observations omitted from this view\n\n", page.RawCount, page.HistoryOmissions)
	for _, problem := range page.Problems {
		fmt.Fprintf(&b, "- %s\n", singleLine(problem))
	}
	if len(page.Rows) == 0 {
		b.WriteString("\nNo check rows in this observation. Empty, missing and unknown coverage remain separate.\n")
	}
	for _, row := range page.Rows {
		status, conclusion := row.Status, row.Conclusion
		if status == "" {
			status = "unknown"
		}
		if conclusion == "" {
			conclusion = "unknown"
		}
		fmt.Fprintf(&b, "\n### %s\n\n%s · status: %s · conclusion: %s\n\n", singleLine(row.Name), row.Result, singleLine(status), singleLine(conclusion))
		fmt.Fprintf(&b, "%s · %s\n\n", row.Repository, row.Source)
		if row.App != "" {
			fmt.Fprintf(&b, "App: %s\n\n", singleLine(row.App))
		}
		if row.URL != "" {
			fmt.Fprintf(&b, "%s\n\n", row.URL)
		}
		if row.StartedAt != "" {
			fmt.Fprintf(&b, "Started: %s\n\n", row.StartedAt)
		}
		if row.CompletedAt != "" {
			fmt.Fprintf(&b, "Completed: %s\n\n", row.CompletedAt)
		}
		if row.UpdatedAt != "" {
			fmt.Fprintf(&b, "Updated: %s\n\n", row.UpdatedAt)
		}
		for _, problem := range row.Problems {
			fmt.Fprintf(&b, "- %s\n", singleLine(problem))
		}
	}
	p := page.Pagination
	fmt.Fprintf(&b, "\n%d rows · %d omitted before · %d omitted after\n", p.Total, p.Before, p.After)
	return b.String()
}

func (m model) finishChecks(msg checksMsg) (tea.Model, tea.Cmd) {
	source, fixed := m.checksSource()
	if msg.root != m.installRoot || msg.repo != m.repo || msg.key != m.detail.key || msg.generation != m.detail.generation || msg.request != m.checksRequest || !m.detail.checksActive() || source != msg.sourceSnapshot || fixed != msg.fixed || m.checks.pendingSnapshot != msg.snapshot {
		return m, nil
	}
	m.checks.busy, m.checks.loaded = false, true
	if msg.err != nil {
		m.checksText("Checks unavailable: " + msg.err.Error() + "\n\nNo automatic fetch. Press r to request focused CI for a current item.")
		m.recordError("Checks unavailable", msg.err)
		return m, nil
	}
	m.checks.page, m.checks.snapshot = msg.data, msg.data.Snapshot
	m.checksText(renderChecks(msg.data))
	return m, nil
}

func (m model) checksKeyScope() bool {
	if !m.detail.checksActive() || m.comment.open || m.themePicker.open || m.lastError.open || m.confirmQuit {
		return false
	}
	if m.notificationPR.open {
		return true
	}
	return m.focus == FocusDetail && (m.form.focused == fieldContent || m.detail.AnySectionFull()) &&
		!m.notifications.open && !m.attention.open && !m.actionHistory.open && !m.settings.open && !m.corpus.open && !m.groups.open && !m.batches.open && !m.briefs.open && !m.dups.open
}

func (m model) handleChecksKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	if !m.checksKeyScope() {
		return m, nil, false
	}
	switch {
	case key.Matches(msg, keys.ChecksRefresh):
		cmd := m.readChecks("refresh", 0)
		return m, cmd, true
	case key.Matches(msg, keys.ChecksNext):
		if !m.checks.busy && m.checks.page.Pagination.Next != nil {
			cmd := m.readChecks("offline", *m.checks.page.Pagination.Next)
			return m, cmd, true
		}
		return m, nil, true
	case key.Matches(msg, keys.ChecksPrev):
		if !m.checks.busy && m.checks.page.Pagination.Offset > 0 {
			cmd := m.readChecks("offline", maxInt(m.checks.page.Pagination.Offset-checksPageSize, 0))
			return m, cmd, true
		}
		return m, nil, true
	}
	return m, nil, false
}
