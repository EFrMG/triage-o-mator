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
const checksMaxRows = 1000
const checksMaxBytes = 8 * 1024 * 1024
const checksLoadTimeout = 30 * time.Second

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
	SectionTitle     string `json:"section_title"`
}

type checkLogObject struct {
	SHA256 string `json:"sha256"`
	Bytes  int    `json:"bytes"`
	Format string `json:"format"`
}

type checkFailureLog struct {
	State              string           `json:"state"`
	CaptureSnapshot    string           `json:"capture_snapshot_id"`
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

type checksLogAcquisition struct {
	Requests         int      `json:"requests"`
	ReservedRequests int      `json:"reserved_requests"`
	Attempted        int      `json:"attempted"`
	Reused           int      `json:"reused"`
	Omissions        []string `json:"omissions"`
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
	Repository          checksRepository      `json:"repository"`
	AllowedRepositories []checksRepository    `json:"allowed_repositories"`
	ChecksHead          string                `json:"checks_head_sha"`
	Kind                string                `json:"kind"`
	Number              int                   `json:"number"`
	Mode                string                `json:"mode"`
	Snapshot            string                `json:"snapshot_id"`
	Head                string                `json:"observed_head_sha"`
	ObservedAt          string                `json:"observed_at"`
	Coverage            string                `json:"coverage"`
	Freshness           string                `json:"freshness"`
	FreshnessBasis      string                `json:"freshness_basis"`
	Result              string                `json:"observed_result"`
	Problems            []string              `json:"problems"`
	Rows                []checkRow            `json:"rows"`
	Requests            int                   `json:"requests"`
	ReservedRequests    int                   `json:"reserved_requests"`
	LogAcquisition      *checksLogAcquisition `json:"failure_log_acquisition"`
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
	picker          *checksLogPicker
	reader          *checksLogReader
	approval        *workflowReader
	loaded, busy    bool
	snapshot        string
	pendingSnapshot string
	page            checksPage
	bytes           int
	deadline        time.Time
}

type checksMsg struct {
	root, repo, sourceSnapshot, snapshot string
	fixed                                bool
	key                                  Key
	generation, request                  uint64
	data                                 checksPage
	err                                  error
	offset, bytes                        int
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

func checksReadCmd(root, repo string, item Key, mode, sourceSnapshot, snapshot string, fixed bool, offset int, generation, request uint64, process *readProcess) tea.Cmd {
	return func() tea.Msg {
		msg := checksMsg{root: root, repo: repo, key: item, sourceSnapshot: sourceSnapshot, snapshot: snapshot, fixed: fixed, generation: generation, request: request, offset: offset}
		args := []string{"--include-failure-logs", "--expected-repo", repo, "--number", strconv.Itoa(item.Number), "--mode", mode, "--host", evidenceHost, "--max-age", cacheMaxAgeArg, "--request-budget", "80", "--offset", strconv.Itoa(offset), "--limit", strconv.Itoa(checksPageSize)}
		if mode != "offline" && snapshot == "" {
			args = append(args, "--acquire-failure-logs")
		}
		if snapshot != "" {
			args = append(args, "--snapshot", snapshot)
		}

		out, err := runReadScript(process, root, "pr-checks", args...)
		msg.bytes = len(out)
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
	if page.Requests < 0 || page.Requests > 80 || mode == "offline" && page.Requests != 0 || len(page.Rows) > checksPageSize || p.Returned != len(page.Rows) || p.Offset != offset || p.Total < 0 || page.HistoryOmissions < 0 || page.RawCount != p.Total+page.HistoryOmissions || p.Before != offset || p.After < 0 || p.Total != p.Before+p.Returned+p.After || p.Next != nil && (*p.Next != offset+len(page.Rows) || p.After == 0 || len(page.Rows) == 0) {
		return fmt.Errorf("checks response budget or pagination mismatch")
	}
	if page.ReservedRequests < 0 || page.Requests+page.ReservedRequests > 80 {
		return fmt.Errorf("checks response exceeds its shared request budget")
	}
	if acquisition := page.LogAcquisition; acquisition != nil {
		metadataRequests := page.Requests - acquisition.Requests
		if mode == "offline" || snapshot != "" || acquisition.Requests < 0 || metadataRequests < 0 || metadataRequests > 20 || acquisition.ReservedRequests != page.ReservedRequests || acquisition.ReservedRequests > 33 || acquisition.ReservedRequests%11 != 0 || acquisition.Attempted < 0 || acquisition.Attempted > 3 || acquisition.Reused < 0 || acquisition.Reused > 8 || acquisition.Attempted+acquisition.Reused > 8 || acquisition.ReservedRequests > acquisition.Attempted*11 || acquisition.Attempted == 0 && acquisition.Requests != 0 || len(acquisition.Omissions) > 6 {
			return fmt.Errorf("checks failure log acquisition scope or budget mismatch")
		}
		for _, omission := range acquisition.Omissions {
			if len(omission) > 4000 {
				return fmt.Errorf("checks failure log acquisition notice exceeds its display budget")
			}
		}
	} else if page.ReservedRequests != 0 || page.Requests > 20 {
		return fmt.Errorf("checks metadata response exceeds its request budget")
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
	if m.checks.reader != nil {
		if m.checks.reader.process != nil {
			m.checks.reader.process.stop()
		}
	}
	if m.checks.approval != nil && m.checks.approval.process != nil {
		m.checks.approval.process.stop()
	}
	m.checks.picker, m.checks.reader, m.checks.approval = nil, nil, nil
	m.workflowRunsRequest++
}

func (m *model) checksPageText(page checksPage) {
	m.detail.setChecksPage(&page)
}

func (m *model) readChecks(mode string, offset int) tea.Cmd {
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
	m.checks.bytes = 0
	m.checks.deadline = time.Time{}
	if m.checksLifecycle == nil {
		m.checksLifecycle = &readLifecycle{}
	}
	m.checksLifecycle.current = &readProcess{}
	m.checks.pendingSnapshot, m.checks.busy = snapshot, true
	m.status = "Reading focused CI…"
	return checksReadCmd(m.installRoot, m.repo, m.detail.key, mode, source, snapshot, fixed, offset, m.detail.generation, m.checksRequest, m.checksLifecycle.current)
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

type checksSpan struct {
	text string
	role string
	bold bool
}

type checksBlock struct {
	spans    []checksSpan
	markdown string
	centered bool
}

// checksBlocks keeps trusted presentation roles separate from source text and captured logs.
func checksBlocks(page checksPage) []checksBlock {
	counts := page.ResultCounts
	blocks := []checksBlock{{centered: true, spans: []checksSpan{
		{text: strconv.Itoa(counts.Failed), role: currentTheme.Error}, {text: " failed · "},
		{text: strconv.Itoa(counts.Pending), role: currentTheme.Warning}, {text: " pending · "},
		{text: strconv.Itoa(counts.Passed), role: currentTheme.Success}, {text: " passed · "},
		{text: strconv.Itoa(counts.Unknown), role: currentTheme.Muted}, {text: " unknown"},
	}}}
	data, dataRole := "incomplete", currentTheme.Warning
	if page.Coverage == "complete" {
		data, dataRole = "complete", currentTheme.Success
	} else if page.Coverage == "missing" || page.Coverage == "unavailable" {
		data, dataRole = "missing", currentTheme.Muted
	}
	updated, updatedRole := "unknown", currentTheme.Muted
	if observed, err := time.Parse(time.RFC3339, page.ObservedAt); err == nil {
		age := time.Since(observed)
		if age >= 0 && age <= 24*time.Hour {
			updated, updatedRole = "recently", currentTheme.Success
		} else {
			updated, updatedRole = "stale", currentTheme.Warning
		}
	}
	blocks = append(blocks, checksBlock{spans: []checksSpan{{text: "Data: "}, {text: data, role: dataRole}, {text: " · Updated: "}, {text: updated, role: updatedRole}}})
	if page.Head == "" || page.ChecksHead == "" {
		blocks = append(blocks, checksBlock{spans: []checksSpan{{text: "PR commit match unknown", role: currentTheme.Muted}}})
	} else if page.Head != page.ChecksHead {
		blocks = append(blocks, checksBlock{spans: []checksSpan{{text: "Checks refer to a different PR commit", role: currentTheme.Warning}}})
	}
	for _, problem := range page.Problems {
		blocks = append(blocks, checksBlock{spans: []checksSpan{{text: singleLine(problem), role: currentTheme.Warning}}})
	}
	if acquisition := page.LogAcquisition; acquisition != nil {
		for _, omission := range acquisition.Omissions {
			blocks = append(blocks, checksBlock{spans: []checksSpan{{text: "Failure logs: " + singleLine(omission), role: currentTheme.Warning}}})
		}
	}
	if page.Pagination.Total == 0 {
		message := "No check rows are available in this observation. Coverage remains incomplete or unknown."
		if page.Coverage == "complete" && page.Result == "empty" {
			message = "No checks or statuses were recorded for this PR head."
		}
		blocks = append(blocks, checksBlock{spans: []checksSpan{{text: message}}})
	}
	blocks = append(blocks, awaitingApprovalBlocks(page)...)
	var compact []checksSpan
	for _, row := range page.Rows {
		name := singleLine(row.Name)
		if name == "" {
			name = "Unnamed check"
		}
		if awaitingApproval(row) {
			continue
		}
		if row.Result != "failed" {
			if len(compact) > 0 {
				compact = append(compact, checksSpan{text: "\n"})
			}
			compact = append(compact, checksSpan{text: name + " · "}, checksSpan{text: row.Result, role: checksResultRole(row.Result)})
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
		meta := []checksSpan{{text: "Failed", role: currentTheme.Error}}
		if row.App != "" {
			meta = append(meta, checksSpan{text: " · " + singleLine(row.App)})
		}
		if log := row.FailureLog; log != nil && log.State == "retained" {
			if log.WorkflowName != "" {
				meta = append(meta, checksSpan{text: "\nWorkflow: " + singleLine(log.WorkflowName)})
			}
			if log.JobName != "" {
				meta = append(meta, checksSpan{text: "\nJob: " + singleLine(log.JobName)})
			}
		}
		if row.Repository != page.Repository.FullName {
			meta = append(meta, checksSpan{text: " · " + singleLine(row.Repository)})
		}
		blocks = append(blocks, checksBlock{spans: meta})
		for _, problem := range row.Problems {
			blocks = append(blocks, checksBlock{spans: []checksSpan{{text: singleLine(problem), role: currentTheme.Warning}}})
		}
		blocks = append(blocks, checksBlock{markdown: checksFailureSummary(row)})
	}
	if len(compact) > 0 {
		blocks = append(blocks, checksBlock{spans: compact})
	}
	return blocks
}

// awaitingApproval reports a pending row whose source concluded action_required, such as a fork PR's workflow runs waiting for a maintainer.
func awaitingApproval(row checkRow) bool {
	return row.Result == "pending" && row.Conclusion == "action_required"
}

// awaitingApprovalBlocks groups those rows above failures, since they wait on a person rather than on CI or the contributor.
func awaitingApprovalBlocks(page checksPage) []checksBlock {
	var rows []checkRow
	for _, row := range page.Rows {
		if awaitingApproval(row) {
			rows = append(rows, row)
		}
	}
	if len(rows) == 0 {
		return nil
	}

	blocks := []checksBlock{
		{spans: []checksSpan{{text: fmt.Sprintf("Awaiting approval (%d)", len(rows)), role: currentTheme.Warning, bold: true}}},
		{spans: []checksSpan{{text: "These runs wait for someone to act, usually a maintainer approving workflows for a fork PR. They are not failures, and readiness cannot pass until they run.", role: currentTheme.Muted}}},
	}
	var spans []checksSpan
	for _, row := range rows {
		if len(spans) > 0 {
			spans = append(spans, checksSpan{text: "\n"})
		}
		name := singleLine(row.Name)
		if name == "" {
			name = "Unnamed check"
		}
		spans = append(spans, checksSpan{text: name, role: currentTheme.Accent, bold: true})
		if row.App != "" {
			spans = append(spans, checksSpan{text: " · " + singleLine(row.App)})
		}
		if row.Repository != page.Repository.FullName {
			spans = append(spans, checksSpan{text: " · " + singleLine(row.Repository)})
		}
		for _, problem := range row.Problems {
			// bin/_checks.py explains action_required on every such row; the group heading already says it once.
			if !strings.HasPrefix(problem, "action required:") {
				spans = append(spans, checksSpan{text: "\n" + singleLine(problem), role: currentTheme.Warning})
			}
		}
	}

	return append(blocks, checksBlock{spans: spans})
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
		rendered := ansi.Wrap(text.String(), maxInt(width, 1), "")
		if block.centered {
			rendered = lipgloss.NewStyle().Width(maxInt(width, 1)).Align(lipgloss.Center).Render(rendered)
		}
		blocks = append(blocks, rendered)
	}
	return strings.Join(blocks, "\n\n")
}

// checksLogNotices collapses legacy coverage boilerplate for presentation only; cached evidence stays unchanged.
func checksLogNotices(values ...[]string) []string {
	var notices []string
	for _, group := range values {
		for _, value := range group {
			value = singleLine(value)
			if value == "download is an observed prefix; complete job log coverage is unknown" || value == "retained bytes are only an observed prefix" || value == "Selected capture retained no log bytes; inspect its omissions" || value == "No source lines retained" {
				continue
			}
			value = strings.TrimSuffix(value, "; retained bytes are only an observed prefix")
			if value != "" && !slices.Contains(notices, value) {
				notices = append(notices, value)
			}
		}
	}
	return notices
}

func checksFailureSummary(row checkRow) string {
	log := row.FailureLog
	if log == nil || log.State != "retained" {
		return checksFailureSource(row)
	}
	text := "Failure log retained · e opens the reader.\n\n"
	if log.DownloadPartial {
		text += "Partial retained log; complete job log coverage is unknown.\n"
	}
	return text
}

func checksFailureSource(row checkRow) string {
	var b strings.Builder
	log := row.FailureLog
	if log == nil {
		b.WriteString("No retained failure log. Open a current PR and refresh checks to download supported failure logs.\n\n")
		return b.String()
	}
	if log.State != "retained" {
		label := map[string]string{"missing": "No retained failure log. Open a current PR and refresh checks to download supported failure logs", "unavailable": "Failure log unavailable", "invalid": "Captured failure excerpt could not be verified", "not-shown": "Failure excerpt omitted by the display limit"}[log.State]
		fmt.Fprintf(&b, "%s.\n\n", label)
		for _, problem := range checksLogNotices([]string{log.Problem}, log.Omissions, log.DisplayProblems) {
			fmt.Fprintf(&b, "%s\n\n", problem)
		}
		if log.LogObject != nil && !log.BindingRevalidated {
			b.WriteString("Source binding could not be revalidated after capture.\n\n")
		}
		return b.String()
	}
	b.WriteString(checksFailureSummary(row))
	return b.String()
}

func (m model) finishChecks(msg checksMsg) (tea.Model, tea.Cmd) {
	source, fixed := m.checksSource()
	if !m.checks.busy || msg.root != m.installRoot || msg.repo != m.repo || msg.key != m.detail.key || msg.generation != m.detail.generation || msg.request != m.checksRequest || source != msg.sourceSnapshot || fixed != msg.fixed || !m.notificationPR.open && m.focus != FocusDetail || m.checks.pendingSnapshot != msg.snapshot {
		return m, nil
	}
	m.checks.busy, m.checks.loaded = false, true
	if msg.err != nil {
		if msg.offset > 0 {
			notice := "Some checks could not be loaded; the verified results remain visible."
			if !m.checks.deadline.IsZero() && !time.Now().Before(m.checks.deadline) {
				notice = "Some checks were not displayed: automatic offline loading reached its 30-second limit."
				msg.err = fmt.Errorf("automatic offline loading reached its 30-second limit: %w", msg.err)
			}

			m.checks.page.Problems = append(m.checks.page.Problems, notice)
			m.checksPageText(m.checks.page)
			m.status = "CI continuation unavailable. ! shows details."
			m.recordError("Checks continuation unavailable", msg.err)
			return m, nil
		}
		m.checks.page = checksPage{}
		m.detail.setChecksPage(nil)
		m.status = "CI read unavailable. ! shows details."
		m.recordError("Checks unavailable", msg.err)
		return m, nil
	}
	if msg.offset > 0 {
		first := m.checks.page
		if msg.data.Snapshot != first.Snapshot || msg.data.Head != first.Head || msg.data.ChecksHead != first.ChecksHead || msg.data.Pagination.Total != first.Pagination.Total || msg.data.ResultCounts != first.ResultCounts || msg.data.Coverage != first.Coverage || msg.data.ObservedAt != first.ObservedAt || msg.offset != len(first.Rows) {
			msg.err = fmt.Errorf("checks continuation differs from the selected observation")
			m.checks.busy = true
			return m.finishChecks(msg)
		}
		if m.checks.bytes+msg.bytes > checksMaxBytes || len(first.Rows)+len(msg.data.Rows) > checksMaxRows {
			first.Problems = append(first.Problems, "Some checks were not displayed: the list reached its 1,000-result or 8 MiB display limit.")
			m.checks.page = first
			m.checksPageText(first)
			m.status = "CI display limit reached."
			return m, nil
		}
		first.Rows = append(append([]checkRow{}, first.Rows...), msg.data.Rows...)
		first.Pagination = msg.data.Pagination
		m.checks.page = first
	} else {
		m.checks.page, m.checks.snapshot = msg.data, msg.data.Snapshot
		m.checks.deadline = time.Now().Add(checksLoadTimeout)
	}
	m.checks.bytes += msg.bytes
	if msg.data.Pagination.Total > 0 {
		m.checksPageText(m.checks.page)
		m.status = "Focused CI loaded."
		if m.checksOnLoad == m.detail.key && !m.notificationPR.open && m.detail.JumpToChecks() {
			m.checksOnLoad = Key{}
			m.form.FocusField(fieldContent)
			if m.awaitingApprovalRows() > 0 {
				m.status = "Focused CI loaded, with runs awaiting approval."
			}
		}
	} else {
		m.detail.setChecksPage(nil)
		m.status = "No CI check or status observations."
		if msg.data.Coverage == "failed" || msg.data.Coverage == "unavailable" || len(msg.data.Problems) > 0 {
			m.status = "CI observations unavailable. ! shows details."
			m.recordError("Checks unavailable", fmt.Errorf("%s: %s", msg.data.Coverage, strings.Join(msg.data.Problems, "; ")))
		}
	}
	if next := m.checks.page.Pagination.Next; next != nil {
		if len(m.checks.page.Rows) >= checksMaxRows || m.checks.bytes >= checksMaxBytes || !time.Now().Before(m.checks.deadline) {
			m.checks.page.Problems = append(m.checks.page.Problems, "Some checks were not displayed: automatic offline loading reached its 1,000-result, 8 MiB or 30-second limit.")
			m.checksPageText(m.checks.page)
			m.status = "CI display limit reached."
			return m, nil
		}
		m.checks.busy = true
		m.checks.pendingSnapshot = m.checks.snapshot
		if m.checksLifecycle == nil {
			m.checksLifecycle = &readLifecycle{}
		}
		process := &readProcess{}
		m.checksLifecycle.current = process
		cmd := checksReadCmd(m.installRoot, m.repo, m.detail.key, "offline", source, m.checks.snapshot, fixed, *next, m.detail.generation, m.checksRequest, process)
		deadline := m.checks.deadline
		m.status = "Reading remaining CI results offline…"
		return m, func() tea.Msg {
			timer := time.AfterFunc(time.Until(deadline), process.stop)
			defer timer.Stop()
			return cmd()
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
	if !m.checksKeyScope() {
		return m, nil, false
	}
	switch {
	case key.Matches(msg, keys.ChecksLogs):
		next, cmd := m.openChecksLogs()
		return next, cmd, true
	case key.Matches(msg, keys.ChecksApproval):
		next, cmd := m.openWorkflowRuns()
		return next, cmd, true
	case key.Matches(msg, keys.ChecksRefresh):
		cmd := m.readChecks("refresh", 0)
		return m, cmd, true
	}
	return m, nil, false
}

func checksResultRole(result string) string {
	switch result {
	case "failed":
		return currentTheme.Error
	case "pending":
		return currentTheme.Warning
	case "passed":
		return currentTheme.Success
	default:
		return currentTheme.Muted
	}
}
