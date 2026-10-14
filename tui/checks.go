package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const checksPageSize = 20

type checkRow struct {
	InternalID       int              `json:"internal_id"`
	CaptureSupported bool             `json:"log_capture_supported"`
	Source           string           `json:"source"`
	Repository       string           `json:"repository"`
	App              string           `json:"app"`
	Name             string           `json:"name"`
	Status           string           `json:"status"`
	Conclusion       string           `json:"conclusion"`
	URL              string           `json:"url"`
	StartedAt        string           `json:"started_at"`
	CompletedAt      string           `json:"completed_at"`
	UpdatedAt        string           `json:"updated_at"`
	HeadSHA          string           `json:"head_sha"`
	Result           string           `json:"result"`
	Problems         []string         `json:"problems"`
	FailureLog       *checkFailureLog `json:"failure_log"`
}

type checkLogWindow struct {
	StartLine        int    `json:"start_line"`
	EndLine          int    `json:"end_line"`
	Text             string `json:"text"`
	DisplayTruncated bool   `json:"display_truncated"`
}

type checkLogObject struct {
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
	Format string `json:"format"`
}

type checkFailureLog struct {
	State              string           `json:"state"`
	Problem            string           `json:"problem"`
	LogObject          *checkLogObject  `json:"log_object"`
	WorkflowName       string           `json:"workflow_name"`
	JobName            string           `json:"job_name"`
	FetchedAt          string           `json:"fetched_at"`
	SelectionBasis     string           `json:"selection_basis"`
	DownloadPartial    bool             `json:"download_partial"`
	BindingRevalidated bool             `json:"binding_revalidated"`
	Windows            []checkLogWindow `json:"windows"`
	Omissions          []string         `json:"omissions"`
	DisplayProblems    []string         `json:"display_problems"`
	Continuation       json.RawMessage  `json:"continuation"`
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
	picker          *checksCapturePicker
	capturing       bool
	captureRow      *checkRow
	captureOutcome  *checksCaptureOutcome
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
	return "", m.detail.blockDirect || m.activeBatch != "" && !m.notificationPR.open
}

func checksReadCmd(root, repo string, item Key, mode, sourceSnapshot, snapshot string, fixed bool, offset int, generation, request uint64, process *readProcess, priority ...checkRow) tea.Cmd {
	return func() tea.Msg {
		msg := checksMsg{root: root, repo: repo, key: item, sourceSnapshot: sourceSnapshot, snapshot: snapshot, fixed: fixed, generation: generation, request: request}
		args := []string{"--include-failure-logs", "--expected-repo", repo, "--number", strconv.Itoa(item.Number), "--mode", mode, "--host", evidenceHost, "--max-age", cacheMaxAgeArg, "--request-budget", "20", "--offset", strconv.Itoa(offset), "--limit", strconv.Itoa(checksPageSize)}
		if snapshot != "" {
			args = append(args, "--snapshot", snapshot)
		}
		if len(priority) == 1 {
			args = append(args, "--failure-log-check-id", strconv.Itoa(priority[0].InternalID), "--failure-log-source-repo", priority[0].Repository)
		}

		out, err := runReadScript(process, root, "pr-checks", args...)
		if err == nil && len(out) > 524288 {
			err = fmt.Errorf("checks response exceeds the display size limit")
		}
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
	if page.Requests < 0 || page.Requests > 20 || mode == "offline" && page.Requests != 0 || len(page.Rows) > checksPageSize || p.Returned != len(page.Rows) || p.Offset != offset || p.Total < 0 || page.HistoryOmissions < 0 || page.RawCount != p.Total+page.HistoryOmissions || p.Before != offset || p.After < 0 || p.Total != p.Before+p.Returned+p.After || p.Next != nil && (*p.Next != offset+len(page.Rows) || p.After == 0 || len(page.Rows) == 0) {
		return fmt.Errorf("checks response budget or pagination mismatch")
	}
	if !slices.Contains([]string{"complete", "partial", "missing", "failed", "unavailable"}, page.Coverage) || !slices.Contains([]string{"within-window", "stale", "unknown"}, page.Freshness) || !slices.Contains([]string{"passed", "failed", "pending", "unknown", "empty"}, page.Result) {
		return fmt.Errorf("checks response coverage, freshness or result is invalid")
	}
	counts := page.ResultCounts
	if counts.Passed < 0 || counts.Failed < 0 || counts.Pending < 0 || counts.Unknown < 0 || counts.Passed+counts.Failed+counts.Pending+counts.Unknown != p.Total || (p.After > 0) != (p.Next != nil) {
		return fmt.Errorf("checks response counts or continuation mismatch")
	}

	logLookups, logBytes := 0, 0
	for _, row := range page.Rows {
		if !slices.ContainsFunc(page.AllowedRepositories, func(repository checksRepository) bool {
			return repository.Host == evidenceHost && repository.FullName == row.Repository
		}) || row.HeadSHA != "" && row.HeadSHA != page.ChecksHead || !slices.Contains([]string{"passed", "failed", "pending", "unknown"}, row.Result) || !slices.Contains([]string{"check-run", "check-suite", "status"}, row.Source) {
			return fmt.Errorf("checks row repository, revision or result mismatch")
		}

		if row.CaptureSupported && (row.InternalID < 1 || row.Source != "check-run" || row.Result != "failed" || row.Status != "completed" || row.HeadSHA == "") {
			return fmt.Errorf("checks capture capability differs from the selected row")
		}
		if log := row.FailureLog; log != nil {
			if row.Result != "failed" || !slices.Contains([]string{"retained", "missing", "unavailable", "invalid", "not-shown"}, log.State) {
				return fmt.Errorf("checks failure log state mismatch")
			}
			if row.Source == "check-run" && log.State != "not-shown" {
				logLookups++
			}
			if log.State == "retained" && (log.LogObject == nil || len(log.Windows) == 0 || !log.BindingRevalidated && !log.DownloadPartial) {
				return fmt.Errorf("checks failure log lacks retained source binding")
			}
			if log.LogObject != nil {
				ref := log.LogObject
				if _, err := hex.DecodeString(ref.SHA256); err != nil || len(ref.SHA256) != 64 || ref.Bytes < 1 || ref.Bytes > 2000000 || ref.Format != "json" {
					return fmt.Errorf("checks failure log object is invalid")
				}
			}
			for _, value := range []string{log.Problem, log.WorkflowName, log.JobName, log.FetchedAt, log.SelectionBasis} {
				if len(value) > 4000 {
					return fmt.Errorf("checks failure log metadata exceeds its display budget")
				}
			}
			for _, value := range append(append([]string{}, log.Omissions...), log.DisplayProblems...) {
				if len(value) > 4000 {
					return fmt.Errorf("checks failure log notice exceeds its display budget")
				}
			}
			lines, bytes, previous := 0, 0, 0
			for _, window := range log.Windows {
				if log.State != "retained" || window.StartLine <= previous || window.EndLine < window.StartLine || window.Text == "" {
					return fmt.Errorf("checks failure log window is invalid")
				}
				actualLines := strings.Count(window.Text, "\n")
				if !strings.HasSuffix(window.Text, "\n") {
					actualLines++
				}
				if actualLines > 20 || actualLines > window.EndLine-window.StartLine+1 || !window.DisplayTruncated && actualLines != window.EndLine-window.StartLine+1 {
					return fmt.Errorf("checks failure log text differs from its source line range")
				}
				lines += window.EndLine - window.StartLine + 1
				bytes += len(window.Text)
				previous = window.EndLine
			}
			if lines > 20 || bytes > 8192 || len(log.Omissions) > 10 || len(log.DisplayProblems) > 10 {
				return fmt.Errorf("checks failure excerpt exceeds its display budget")
			}
			logBytes += bytes
		}
	}
	if logLookups > 8 || logBytes > 65536 {
		return fmt.Errorf("checks failure excerpts exceed the page budget")
	}
	if (len(page.Rows) > 0 || p.Total > 0) && page.Snapshot == "" {
		return fmt.Errorf("checks response has rows without an immutable snapshot")
	}
	return nil
}

func (m *model) stopChecks() {
	m.checksLifecycle.stop()
	m.checksRequest++
	m.checks.busy = false
	m.checks.picker, m.checks.captureRow, m.checks.captureOutcome = nil, nil, nil
	m.checks.capturing = false
}

func (m *model) checksPageText(page checksPage) {
	m.detail.setChecksPage(&page)
}

func (m *model) readChecks(mode string, offset int, priority ...checkRow) tea.Cmd {
	if m.detail.key.Kind != "pr" || m.detail.key.Number < 1 || m.detail.loading {
		return nil
	}
	source, fixed := m.checksSource()
	if fixed && mode != "offline" {
		m.status = "Checks are pinned to this item packet. Reopen a current item to acquire focused checks."
		return nil
	}
	if fixed && source == "" {
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
	m.status = "Reading focused CI…"
	return checksReadCmd(m.installRoot, m.repo, m.detail.key, mode, source, snapshot, fixed, offset, m.detail.generation, m.checksRequest, m.checksLifecycle.current, priority...)
}

// checksIfNeeded runs once after the selected PR's source resolves, independently of its active tab.
func (m *model) checksIfNeeded() tea.Cmd {
	if m.detail.key.Kind != "pr" || m.detail.key.Number < 1 || m.detail.loading || m.checks.loaded || m.checks.busy || m.confirmQuit || (!m.notificationPR.open && m.focus != FocusDetail) {
		return nil
	}
	source, fixed := m.checksSource()
	if fixed && source == "" {
		return nil
	}
	mode := "cache-preferred"
	if fixed {
		mode = "offline"
	}
	return m.readChecks(mode, 0)
}

// checksLiteral keeps captured output inside a fence even when the source contains Markdown fences.
func checksLiteral(text string) string {
	longest, run := 0, 0
	for _, char := range text {
		if char == '`' {
			run++
			longest = maxInt(longest, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", maxInt(3, longest+1))
	return fence + "\n" + text + "\n" + fence + "\n\n"
}

func checksTime(value string) string {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return singleLine(value)
	}
	return parsed.UTC().Format("2006-01-02 15:04 UTC")
}

type checksSpan struct {
	text string
	role string
	bold bool
}

type checksBlock struct {
	spans    []checksSpan
	markdown string
}

// checksBlocks keeps trusted presentation roles separate from source text and captured logs.
func checksBlocks(page checksPage) []checksBlock {
	counts := page.ResultCounts
	blocks := []checksBlock{{spans: []checksSpan{
		{text: strconv.Itoa(counts.Failed), role: currentTheme.Error}, {text: " failed · "},
		{text: strconv.Itoa(counts.Pending), role: currentTheme.Warning}, {text: " pending · "},
		{text: strconv.Itoa(counts.Passed), role: currentTheme.Success}, {text: " passed · "},
		{text: strconv.Itoa(counts.Unknown), role: currentTheme.Muted}, {text: " unknown"},
	}}, {spans: []checksSpan{{text: "Coverage: "}, {text: page.Coverage, role: currentTheme.Muted}, {text: " · Freshness: "}, {text: page.Freshness, role: currentTheme.Muted}}}}
	revision := "Revision match unknown"
	role := currentTheme.Muted
	if page.Head != "" && page.ChecksHead != "" {
		revision, role = "Checks match the selected PR head", ""
		if page.Head != page.ChecksHead {
			revision = "Checks differ from the selected PR head"
		}
	}
	blocks = append(blocks, checksBlock{spans: []checksSpan{{text: revision, role: role}}})
	for _, problem := range page.Problems {
		blocks = append(blocks, checksBlock{spans: []checksSpan{{text: singleLine(problem)}}})
	}
	if page.Pagination.Total == 0 {
		message := "No check rows are available in this observation. Coverage remains incomplete or unknown."
		if page.Coverage == "complete" && page.Result == "empty" {
			message = "No checks or statuses were recorded for this PR head."
		}
		blocks = append(blocks, checksBlock{spans: []checksSpan{{text: message}}})
	}
	var compact []checksSpan
	for _, row := range page.Rows {
		name := singleLine(row.Name)
		if name == "" {
			name = "Unnamed check"
		}
		if row.Result != "failed" {
			if len(compact) > 0 {
				compact = append(compact, checksSpan{text: "\n"})
			}
			compact = append(compact, checksSpan{text: name + " · "}, checksSpan{text: row.Result, role: currentTheme.Muted})
			if row.Conclusion != "" {
				compact = append(compact, checksSpan{text: " · "}, checksSpan{text: singleLine(row.Conclusion), role: currentTheme.Muted})
			}
			if row.Repository != page.Repository.FullName {
				compact = append(compact, checksSpan{text: " · " + singleLine(row.Repository)})
			}
			for _, problem := range row.Problems {
				compact = append(compact, checksSpan{text: "\n" + singleLine(problem)})
			}
			continue
		}
		blocks = append(blocks, checksBlock{spans: []checksSpan{{text: name, role: currentTheme.Accent, bold: true}}})
		meta := []checksSpan{{text: "Failed", role: currentTheme.Muted}}
		if row.App != "" {
			meta = append(meta, checksSpan{text: " · " + singleLine(row.App)})
		}
		if log := row.FailureLog; log != nil && log.State == "retained" {
			if log.WorkflowName != "" {
				meta = append(meta, checksSpan{text: " · " + singleLine(log.WorkflowName)})
			}
			if log.JobName != "" {
				meta = append(meta, checksSpan{text: " / " + singleLine(log.JobName)})
			}
		}
		if row.Repository != page.Repository.FullName {
			meta = append(meta, checksSpan{text: " · " + singleLine(row.Repository)})
		}
		blocks = append(blocks, checksBlock{spans: meta})
		for _, problem := range row.Problems {
			blocks = append(blocks, checksBlock{spans: []checksSpan{{text: singleLine(problem)}}})
		}
		blocks = append(blocks, checksBlock{markdown: checksFailureSource(row)})
	}
	if len(compact) > 0 {
		blocks = append(blocks, checksBlock{spans: compact})
	}
	footer := fmt.Sprintf("%d rows · %d omitted before · %d omitted after", page.Pagination.Total, page.Pagination.Before, page.Pagination.After)
	if page.HistoryOmissions > 0 {
		footer += fmt.Sprintf(" · %d historical or suite observations omitted", page.HistoryOmissions)
	}
	blocks = append(blocks, checksBlock{spans: []checksSpan{{text: footer, role: currentTheme.Muted}}})
	return blocks
}

// renderChecks exports plain source, without terminal styling.
func renderChecks(page checksPage) string {
	var blocks []string
	for _, block := range checksBlocks(page) {
		if block.markdown != "" {
			blocks = append(blocks, strings.TrimSpace(block.markdown))
			continue
		}
		var text strings.Builder
		for _, span := range block.spans {
			text.WriteString(span.text)
		}
		blocks = append(blocks, text.String())
	}
	return strings.Join(blocks, "\n\n") + "\n"
}

func renderChecksView(page checksPage, width int) string {
	var blocks []string
	for _, block := range checksBlocks(page) {
		if block.markdown != "" {
			blocks = append(blocks, renderMarkdown(block.markdown, width))
			continue
		}
		var text strings.Builder
		for _, span := range block.spans {
			style := lipgloss.NewStyle().Bold(span.bold)
			if span.role != "" {
				style = style.Foreground(lipgloss.Color(span.role))
			}
			text.WriteString(style.Render(sanitize(span.text)))
		}
		blocks = append(blocks, ansi.Wrap(text.String(), maxInt(width, 1), ""))
	}
	return strings.Join(blocks, "\n\n")
}

func checksFailureSource(row checkRow) string {
	var b strings.Builder
	log := row.FailureLog
	if log == nil {
		b.WriteString("No captured failure excerpt. Capture requires an explicit request.\n\n")
		return b.String()
	}
	if log.State != "retained" {
		label := map[string]string{"missing": "No captured failure excerpt. Capture requires an explicit request", "unavailable": "Failure excerpt unavailable", "invalid": "Captured failure excerpt could not be verified", "not-shown": "Failure excerpt omitted by this page's display budget"}[log.State]
		fmt.Fprintf(&b, "%s.\n\n", label)
		if log.Problem != "" {
			fmt.Fprintf(&b, "%s\n\n", singleLine(log.Problem))
		}
		if log.DownloadPartial {
			b.WriteString("Partial log capture; complete job log coverage is unknown.\n\n")
		}
		if log.LogObject != nil && !log.BindingRevalidated {
			b.WriteString("Source binding could not be revalidated after capture.\n\n")
		}
		for _, problem := range append(append([]string{}, log.Omissions...), log.DisplayProblems...) {
			fmt.Fprintf(&b, "- %s\n", singleLine(problem))
		}
		return b.String()
	}
	for _, window := range log.Windows {
		fmt.Fprintf(&b, "Captured failure excerpt · Source lines %d–%d", window.StartLine, window.EndLine)
		if log.DownloadPartial {
			b.WriteString(" · partial capture")
		}
		b.WriteString("\n\n")
		b.WriteString(checksLiteral(window.Text))
		if window.DisplayTruncated {
			b.WriteString("This excerpt was truncated for display.\n\n")
		}
	}
	if log.SelectionBasis != "" {
		fmt.Fprintf(&b, "Selection: %s", singleLine(log.SelectionBasis))
	}
	if log.FetchedAt != "" {
		if log.SelectionBasis != "" {
			b.WriteString(" · ")
		}
		fmt.Fprintf(&b, "Captured %s", checksTime(log.FetchedAt))
	}
	if log.SelectionBasis != "" || log.FetchedAt != "" {
		b.WriteString("\n\n")
	}
	if log.DownloadPartial {
		b.WriteString("Partial log capture; complete job log coverage is unknown.\n\n")
	}
	if !log.BindingRevalidated {
		b.WriteString("Source binding could not be revalidated after capture.\n\n")
	}
	for _, problem := range append(append([]string{}, log.Omissions...), log.DisplayProblems...) {
		fmt.Fprintf(&b, "- %s\n", singleLine(problem))
	}
	if len(log.Continuation) > 0 && string(log.Continuation) != "null" {
		b.WriteString("\nMore retained context is available through the pinned offline log reader.\n")
	}
	return b.String()
}

func (m model) finishChecks(msg checksMsg) (tea.Model, tea.Cmd) {
	source, fixed := m.checksSource()
	if !m.checks.busy || msg.root != m.installRoot || msg.repo != m.repo || msg.key != m.detail.key || msg.generation != m.detail.generation || msg.request != m.checksRequest || source != msg.sourceSnapshot || fixed != msg.fixed || !m.notificationPR.open && m.focus != FocusDetail || m.checks.pendingSnapshot != msg.snapshot {
		return m, nil
	}
	m.checks.busy, m.checks.loaded = false, true
	if msg.err != nil {
		m.checks.captureOutcome = nil
		m.checks.page = checksPage{}
		m.detail.setChecksPage(nil)
		m.status = "CI read unavailable. ! shows details; Ctrl-R retries focused CI."
		m.recordError("Checks unavailable", msg.err)
		return m, nil
	}
	m.checks.page, m.checks.snapshot = msg.data, msg.data.Snapshot
	if msg.data.Pagination.Total > 0 {
		m.checksPageText(msg.data)
		m.status = "Focused CI loaded."
		m.finishCaptureOutcome(msg.data)
	} else {
		m.checks.captureOutcome = nil
		m.detail.setChecksPage(nil)
		m.status = "No CI check or status observations. Ctrl-R refreshes focused CI."
		if msg.data.Coverage == "failed" || msg.data.Coverage == "unavailable" || len(msg.data.Problems) > 0 {
			m.status = "CI observations unavailable. ! shows details; Ctrl-R retries focused CI."
			m.recordError("Checks unavailable", fmt.Errorf("%s: %s", msg.data.Coverage, strings.Join(msg.data.Problems, "; ")))
		}
	}
	return m, nil
}

func (m model) prChecksKeyScope() bool {
	if m.detail.key.Kind != "pr" || m.comment.open || m.themePicker.open || m.lastError.open || m.confirmQuit {
		return false
	}
	if m.notificationPR.open {
		return true
	}
	return m.focus == FocusDetail && (m.form.focused == fieldContent || m.detail.AnySectionFull()) &&
		!m.notifications.open && !m.attention.open && !m.actionHistory.open && !m.settings.open && !m.corpus.open && !m.groups.open && !m.batches.open && !m.briefs.open && !m.dups.open
}

func (m model) checksKeyScope() bool {
	return m.detail.checksActive() && m.prChecksKeyScope()
}

func (m model) handleChecksKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	if m.prChecksKeyScope() && (m.checks.capturing || m.checks.captureOutcome != nil && m.checks.busy) {
		if msg.String() == "esc" && m.checks.capturing {
			m.stopChecks()
			m.status = "Failure log capture cancelled; any displayed excerpt is from an earlier capture."
			return m, nil, true
		}
		if key.Matches(msg, keys.ChecksRefresh) || key.Matches(msg, keys.ChecksFocusedRefresh) || key.Matches(msg, keys.ChecksCapture) || key.Matches(msg, keys.ChecksNext) || key.Matches(msg, keys.ChecksPrev) {
			m.status = "Wait for the selected failure log capture or its offline reload to finish."
			return m, nil, true
		}
	}

	if m.prChecksKeyScope() && key.Matches(msg, keys.ChecksFocusedRefresh) {
		return m, m.readChecks("refresh", 0), true
	}
	if !m.checksKeyScope() {
		return m, nil, false
	}
	switch {
	case key.Matches(msg, keys.ChecksCapture):
		next, cmd := m.openChecksCapture()
		return next, cmd, true
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
