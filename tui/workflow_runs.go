package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const workflowRunsMaxBytes = 4 * 1024 * 1024
const workflowRunsMaxHunks = 60

type workflowFlag struct {
	Category  string `json:"category"`
	Rule      string `json:"rule"`
	Detail    string `json:"detail"`
	Locations []struct {
		Path string `json:"path"`
		Line int    `json:"line"`
	} `json:"locations"`
	Count int `json:"count"`
}

type workflowRun struct {
	ID             int64          `json:"id"`
	Name           string         `json:"name"`
	Path           string         `json:"path"`
	Event          string         `json:"event"`
	HeadSHA        string         `json:"head_sha"`
	HeadRepository string         `json:"head_repository"`
	State          string         `json:"state"`
	Flags          []workflowFlag `json:"flags"`
}

type workflowHunk struct {
	Path      string   `json:"path"`
	Header    string   `json:"header"`
	Start     int      `json:"start"`
	End       int      `json:"end"`
	Text      string   `json:"text"`
	Truncated bool     `json:"truncated"`
	Rules     []string `json:"rules"`
	Lines     []int    `json:"lines"`
}

type workflowAssessment struct {
	Repository  string `json:"repository"`
	Number      int    `json:"number"`
	Snapshot    string `json:"snapshot_id"`
	Head        string `json:"head_sha"`
	Observation struct {
		ObservedAt string `json:"observed_at"`
	} `json:"observation"`
	PRFlags []workflowFlag `json:"pr_flags"`
	Runs    []workflowRun  `json:"runs"`
	Counts  struct {
		Normal     int `json:"normal"`
		Suspicious int `json:"suspicious"`
	} `json:"counts"`
	Checksum      string         `json:"checksum"`
	Requests      int            `json:"requests"`
	Hunks         []workflowHunk `json:"hunks"`
	HunkOmissions []string       `json:"hunk_omissions"`
	Local         workflowLocal  `json:"local"`
}

// workflowLocal is what this install recorded for the assessed head: holds, agent concerns and approval outcomes. It is not evidence and is outside the checksum.
type workflowLocal struct {
	Notes        []workflowNote    `json:"notes"`
	EarlierHeads []string          `json:"earlier_heads"`
	Outcomes     []workflowOutcome `json:"outcomes"`
}

type workflowNote struct {
	ID       string  `json:"id"`
	Kind     string  `json:"kind"`
	RunID    int64   `json:"run_id"`
	By       string  `json:"by"`
	Reason   *string `json:"reason"`
	At       string  `json:"at"`
	Released *struct {
		By string `json:"by"`
		At string `json:"at"`
	} `json:"released"`
}

type workflowOutcome struct {
	RequestID  string  `json:"request_id"`
	RunID      int64   `json:"run_id"`
	Actor      string  `json:"actor"`
	By         string  `json:"by"`
	Status     string  `json:"status"`
	HTTPStatus *int    `json:"http_status"`
	Detail     *string `json:"detail"`
	StartedAt  string  `json:"started_at"`
}

type workflowApprovalPlan struct {
	Plan struct {
		RequestID  string `json:"request_id"`
		Repository string `json:"repository"`
		Number     int    `json:"number"`
		Head       string `json:"head_sha"`
		Checksum   string `json:"assessment_checksum"`
		Runs       []struct {
			ID    int64  `json:"id"`
			State string `json:"state"`
		} `json:"runs"`
	} `json:"plan"`
	Approval string `json:"approval"`
	Requests int    `json:"requests"`
}

type workflowApprovalResult struct {
	Repository string            `json:"repository"`
	Number     int               `json:"number"`
	RequestID  string            `json:"request_id"`
	Results    []workflowOutcome `json:"results"`
	Stopped    bool              `json:"stopped"`
	Repeated   bool              `json:"repeated"`
}

type workflowListing struct {
	Repository string `json:"repository"`
	Number     int    `json:"number"`
	Snapshot   string `json:"snapshot_id"`
}

// workflowReader shows the runs awaiting approval for the open PR, their flags, holds and outcomes, and the diff hunks behind the flags. Approving suspicious runs needs every flagged hunk scrolled into view first; bin/workflow-runs owns every write.
type workflowReader struct {
	source string
	// fixed is a pinned packet, which only assesses; bound is a notification's freshly read PR, listed live against that snapshot instead of an acquired one.
	fixed, bound bool
	busy         bool
	process      *readProcess
	page         workflowAssessment
	problem      string
	viewport     viewport.Model
	// hunks and heights place each flagged hunk in the content; progress is the first line of each not yet scrolled into view, and read survives a resize.
	hunks, heights, progress []int
	read                     []bool
	// writing is a dry run or write in flight; confirm is the dry-run plan a second a publishes; releasing arms a second u; reason is the open hold-reason input.
	writing   string
	confirm   *workflowApprovalPlan
	releasing bool
	reason    *textinput.Model
}

type workflowActionMsg struct {
	root, repo, action string
	key                Key
	generation         uint64
	plan               workflowApprovalPlan
	result             workflowApprovalResult
	count              int
	err                error
}

type workflowRunsMsg struct {
	root, repo, source  string
	fixed               bool
	key                 Key
	generation, request uint64
	data                workflowAssessment
	err                 error
}

func (m model) awaitingApprovalRows() int {
	count := 0
	for _, row := range m.checks.page.Rows {
		if awaitingApproval(row) {
			count++
		}
	}

	return count
}

func (m model) canOpenWorkflowRuns() bool {
	return m.checks.loaded && !m.checks.busy && m.awaitingApprovalRows() > 0
}

func (m model) openWorkflowRuns() (tea.Model, tea.Cmd) {
	if !m.canOpenWorkflowRuns() {
		m.status = "No workflow runs are awaiting approval in these checks."
		return m, nil
	}

	source, fixed := m.checksSource()
	evidence := m.detail.enriched.Evidence
	bound := fixed && m.notificationPR.open && evidence != nil && evidence.Mode == "refresh" && source != ""
	m.checks.approval = &workflowReader{source: source, fixed: fixed && !bound, bound: bound, viewport: viewport.New()}
	if fixed && source == "" {
		m.checks.approval.problem = "This item is pinned without diff evidence, so its runs cannot be assessed here. Open the PR as a current item to list them."
		m.resizeWorkflowReader()
		return m, nil
	}

	return m, m.readWorkflowRuns()
}

// readWorkflowRuns lists the runs for a current item, reusing or acquiring its diff snapshot, then assesses them offline; a pinned packet only reads the saved listing bound to its snapshot.
func (m *model) readWorkflowRuns() tea.Cmd {
	return m.rereadWorkflowRuns(!m.checks.approval.fixed)
}

// rereadWorkflowRuns with list false only assesses again, after a local hold or release, bound to the snapshot already shown.
func (m *model) rereadWorkflowRuns(list bool) tea.Cmd {
	reader := m.checks.approval
	if reader.process != nil {
		reader.process.stop()
	}

	// Listing acquires a snapshot for an ordinary current item and binds the notification's own; assessing again binds the one shown.
	snapshot := reader.page.Snapshot
	switch {
	case reader.fixed || reader.bound && (list || snapshot == ""):
		snapshot = reader.source
	case list || snapshot == "":
		snapshot = ""
	}
	reader.process = &readProcess{}
	reader.busy, reader.problem, reader.confirm, reader.releasing = true, "", nil, false
	m.workflowRunsRequest++
	m.resizeWorkflowReader()
	return workflowRunsReadCmd(m.installRoot, m.repo, m.detail.key, reader.source, reader.fixed, !reader.fixed && (list || reader.page.Snapshot == ""), snapshot, m.detail.generation, m.workflowRunsRequest, reader.process)
}

func workflowRunsReadCmd(root, repo string, item Key, source string, fixed, list bool, snapshot string, generation, request uint64, process *readProcess) tea.Cmd {
	return func() tea.Msg {
		msg := workflowRunsMsg{root: root, repo: repo, key: item, source: source, fixed: fixed, generation: generation, request: request}
		base := []string{"--expected-repo", repo, "--number", strconv.Itoa(item.Number), "--host", evidenceHost}
		if list {
			args := []string{"list", "--acquire", "--request-budget", "40"}
			if snapshot != "" {
				args = []string{"list", "--snapshot", snapshot}
			}
			out, err := runReadScript(process, root, "workflow-runs", append(args, base...)...)
			var listing workflowListing
			if err == nil {
				err = json.Unmarshal([]byte(out), &listing)
			}
			if err == nil && (listing.Repository != repo || listing.Number != item.Number || listing.Snapshot == "") {
				err = fmt.Errorf("workflow run listing belongs to another item")
			}
			if err != nil {
				msg.err = err
				return msg
			}
			snapshot = listing.Snapshot
		}

		out, err := runReadScript(process, root, "workflow-runs", append([]string{"assess", "--include-hunks"}, base...)...)
		if err == nil && len(out) > workflowRunsMaxBytes {
			err = fmt.Errorf("workflow run assessment exceeds its size limit")
		}
		if err == nil {
			err = json.Unmarshal([]byte(out), &msg.data)
		}
		if err == nil {
			err = validateWorkflowAssessment(msg.data, repo, item, snapshot)
		}

		msg.err = err
		return msg
	}
}

func validateWorkflowAssessment(data workflowAssessment, repo string, item Key, snapshot string) error {
	if data.Repository != repo || data.Number != item.Number || data.Requests != 0 {
		return fmt.Errorf("workflow run assessment belongs to another item or was not offline")
	}
	if data.Snapshot != snapshot {
		return fmt.Errorf("the saved run listing belongs to another snapshot of this PR; open a current item to list its runs again")
	}

	normal, suspicious := 0, 0
	for _, run := range data.Runs {
		switch run.State {
		case "normal":
			if len(run.Flags) > 0 || len(data.PRFlags) > 0 {
				return fmt.Errorf("workflow run assessment calls a flagged run normal")
			}
			normal++
		case "suspicious":
			suspicious++
		default:
			return fmt.Errorf("workflow run assessment has an unknown run state")
		}
	}

	if normal != data.Counts.Normal || suspicious != data.Counts.Suspicious || len(data.Hunks) > workflowRunsMaxHunks {
		return fmt.Errorf("workflow run assessment counts or hunks exceed their bounds")
	}
	return nil
}

func (m model) finishWorkflowRuns(msg workflowRunsMsg) (tea.Model, tea.Cmd) {
	source, fixed := m.checksSource()
	reader := m.checks.approval
	if reader == nil || !reader.busy || msg.root != m.installRoot || msg.repo != m.repo || msg.key != m.detail.key || msg.generation != m.detail.generation || msg.request != m.workflowRunsRequest || msg.source != source || msg.fixed != reader.fixed || fixed != (reader.fixed || reader.bound) || msg.source != reader.source || !m.checksKeyScope() {
		return m, nil
	}

	reader.busy = false
	if msg.err != nil {
		reader.problem = "Workflow runs could not be listed or assessed.\n\n" + singleLine(msg.err.Error())
		m.recordError("Workflow runs unavailable", msg.err)
	} else {
		// A new assessment may show other hunks, so reading starts again.
		if msg.data.Checksum != reader.page.Checksum || len(msg.data.Hunks) != len(reader.read) {
			reader.read = make([]bool, len(msg.data.Hunks))
		}
		reader.page = msg.data
	}

	reader.viewport.GotoTop()
	m.resizeWorkflowReader()

	return m, nil
}

func workflowFlagLines(flags []workflowFlag) []string {
	var lines []string
	for _, value := range flags {
		rule := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Warning)).Render(sanitize(singleLine(value.Rule)))
		lines = append(lines, rule+": "+sanitize(singleLine(value.Detail)))
		for _, location := range value.Locations {
			place := singleLine(location.Path)
			if location.Line > 0 {
				place += ":" + strconv.Itoa(location.Line)
			}
			lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Muted)).Render("  "+sanitize(place)))
		}
		if more := value.Count - len(value.Locations); more > 0 {
			lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Muted)).Render(fmt.Sprintf("  %d more", more)))
		}
	}

	return lines
}

func workflowHunkText(hunk workflowHunk) string {
	var lines []string
	for _, line := range strings.Split(sanitize(hunk.Text), "\n") {
		role := ""
		switch {
		case strings.HasPrefix(line, "+"):
			role = currentTheme.Success
		case strings.HasPrefix(line, "-"):
			role = currentTheme.Error
		case !strings.HasPrefix(line, " "):
			role = currentTheme.Muted
		}

		style := lipgloss.NewStyle()
		if role != "" {
			style = style.Foreground(lipgloss.Color(role))
		}
		lines = append(lines, style.Render(line))
	}

	return strings.Join(lines, "\n")
}

func shortSHA(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// resizeWorkflowReader renders the assessment; flags and rule names come from the script, and every source-derived string is sanitized.
func (m *model) resizeWorkflowReader() {
	reader := m.checks.approval
	if reader == nil {
		return
	}

	width := maxInt(m.commentWidth()-4, 1)
	reader.viewport.SetWidth(width)
	reader.viewport.SetHeight(maxInt(m.commentHeight()-6, 1))
	reader.hunks, reader.heights, reader.progress = nil, nil, nil
	defer reader.markViewed()
	if reader.busy || reader.problem != "" {
		reader.viewport.SetContent(ansi.Wrap(reader.problem, width, ""))
		return
	}

	page := reader.page
	bold := func(role, text string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(role)).Bold(true).Render(text)
	}
	muted := func(text string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Muted)).Render(text)
	}

	var blocks []string
	summary := fmt.Sprintf("%d waiting · ", len(page.Runs)) + bold(currentTheme.Success, fmt.Sprintf("%d normal", page.Counts.Normal)) + " · " + bold(currentTheme.Warning, fmt.Sprintf("%d suspicious", page.Counts.Suspicious))
	listed := sanitize(singleLine(page.Observation.ObservedAt))
	if observed, err := time.Parse(time.RFC3339, page.Observation.ObservedAt); err == nil {
		listed = observed.UTC().Format("2006-01-02 15:04 UTC")
	}
	note := "Listed " + listed + " for head " + shortSHA(sanitize(page.Head)) + "."
	if reader.fixed {
		note += " Open the PR as a current item to approve or hold its runs."
	}
	blocks = append(blocks, summary, muted(note))
	if len(page.Local.EarlierHeads) > 0 {
		blocks = append(blocks, muted(fmt.Sprintf("Holds on %d earlier heads stay as history and don't apply to this one.", len(page.Local.EarlierHeads))))
	}
	if len(page.Runs) == 0 {
		blocks = append(blocks, "GitHub listed no runs awaiting approval for this head. The Checks tab may be older than the listing.")
	}

	// Section headings take the accent color, with their counts in the warning color, both in normal weight.
	section := func(title string, count int) string {
		accent := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Accent))
		warning := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Warning))
		return accent.Render(title) + " " + warning.Render(fmt.Sprintf("(%d)", count))
	}

	// The runs come first: they are what a person approves; the PR's flags and the hunks behind them follow.
	if len(page.Runs) > 0 {
		blocks = append(blocks, section("Runs awaiting approval", len(page.Runs)))
	}
	for _, run := range page.Runs {
		name := singleLine(run.Name)
		if name == "" {
			name = "Unnamed workflow"
		}
		state := bold(currentTheme.Success, "normal")
		if run.State != "normal" {
			state = bold(currentTheme.Warning, "suspicious")
		}
		origin := fmt.Sprintf("Run %d for head %s", run.ID, shortSHA(run.HeadSHA))
		if run.HeadRepository != "" && run.HeadRepository != page.Repository {
			origin += " from " + singleLine(run.HeadRepository)
		}
		lines := []string{bold(currentTheme.Accent, sanitize(name)) + ": " + state,
			muted(sanitize(fmt.Sprintf("Workflow %s, on %s", singleLine(run.Path), singleLine(run.Event)))), muted(sanitize(origin))}
		switch {
		case len(run.Flags) > 0:
			lines = append(lines, "", muted("Also flagged for this run:"))
			lines = append(lines, workflowFlagLines(run.Flags)...)
		case run.State == "normal":
			lines = append(lines, "No flags.")
		default:
			lines = append(lines, "Suspicious because of the flags on this PR, below.")
		}
		lines = append(lines, workflowLocalLines(page.Local, run.ID)...)
		blocks = append(blocks, strings.Join(lines, "\n"))
	}

	if len(page.PRFlags) > 0 {
		blocks = append(blocks, section("Flags on this PR", len(page.PRFlags))+"\n"+muted("Every run above is suspicious while any of these hold.")+"\n\n"+strings.Join(workflowFlagLines(page.PRFlags), "\n"))
	}

	if len(page.Hunks) > 0 {
		blocks = append(blocks, section("Flagged changes", len(page.Hunks))+"\n"+muted("The diff comes from the PR snapshot the runs were listed against."))
	}

	offset := 0
	for _, block := range blocks {
		offset += lipgloss.Height(ansi.Wrap(block, width, "")) + 1
	}
	for _, hunk := range page.Hunks {
		// The path reads as plain accent text; the line range after it stays bold.
		place := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Accent)).Render(sanitize(singleLine(hunk.Path)))
		if hunk.End >= hunk.Start && hunk.Start > 0 {
			place += bold(currentTheme.Accent, fmt.Sprintf(" · lines %d–%d", hunk.Start, hunk.End))
		} else if hunk.Header == "" {
			place += bold(currentTheme.Accent, " · no text changes")
		}
		heading := place + "\n" + muted(sanitize(strings.Join(hunk.Rules, ", ")))
		text := heading + "\n" + workflowHunkText(hunk)
		if hunk.Truncated {
			text += "\n" + muted("This hunk was truncated for display.")
		}

		block := ansi.Wrap(text, width, "")
		reader.hunks = append(reader.hunks, offset)
		reader.heights = append(reader.heights, lipgloss.Height(block))
		offset += lipgloss.Height(block) + 1
		blocks = append(blocks, block)
	}

	for _, omission := range page.HunkOmissions {
		blocks = append(blocks, lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Warning)).Render(sanitize(singleLine(omission))))
	}

	var content []string
	for _, block := range blocks {
		content = append(content, ansi.Wrap(block, width, ""))
	}
	reader.viewport.SetContent(strings.Join(content, "\n\n"))
	// Wrapping moves the hunks, so a partly read hunk starts again; a fully read one stays read.
	reader.progress = append([]int(nil), reader.hunks...)
}

func workflowLocalLines(local workflowLocal, run int64) []string {
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Muted))
	var lines []string
	for _, note := range local.Notes {
		if note.RunID != run {
			continue
		}

		reason := "no reason given"
		if note.Reason != nil {
			reason = sanitize(singleLine(*note.Reason))
		}
		label, role := "held", currentTheme.Accent
		if note.Kind == "concern" {
			label, role = "agent-concern", currentTheme.Warning
		}
		line := lipgloss.NewStyle().Foreground(lipgloss.Color(role)).Bold(true).Render(label) + ": " + reason + muted.Render(" — "+sanitize(singleLine(note.By)))
		if note.Released != nil {
			line = muted.Render(label + ": " + reason + " — " + sanitize(singleLine(note.By)) + ", released by " + sanitize(singleLine(note.Released.By)))
		}
		lines = append(lines, line)
	}

	for _, outcome := range local.Outcomes {
		if outcome.RunID != run {
			continue
		}

		by := sanitize(singleLine(outcome.By))
		switch outcome.Status {
		case "approved":
			lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Success)).Bold(true).Render("approved")+muted.Render(" by "+by))
		case "refused":
			detail := ""
			if outcome.HTTPStatus != nil {
				detail = fmt.Sprintf(" (HTTP %d)", *outcome.HTTPStatus)
			}
			if outcome.Detail != nil {
				detail += ": " + sanitize(singleLine(*outcome.Detail))
			}
			lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Error)).Bold(true).Render("refused")+muted.Render(detail+" — "+by))
		default:
			lines = append(lines, lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Error)).Bold(true).Render("uncertain")+": inspect the run on GitHub before trying again"+muted.Render(" — "+by))
		}
	}

	return lines
}

// markViewed records the flagged hunk lines now in view; a hunk is read once all of its lines have been, from its first line down.
func (reader *workflowReader) markViewed() {
	if len(reader.read) != len(reader.hunks) || len(reader.progress) != len(reader.hunks) || len(reader.heights) != len(reader.hunks) {
		return
	}

	top := reader.viewport.YOffset()
	bottom := top + reader.viewport.Height()
	for index, start := range reader.hunks {
		end := start + reader.heights[index]
		if top <= reader.progress[index] && reader.progress[index] < bottom {
			reader.progress[index] = minInt(bottom, end)
		}
		if reader.progress[index] >= end {
			reader.read[index] = true
		}
	}
}

func (reader *workflowReader) unread() int {
	count := 0
	for _, read := range reader.read {
		if !read {
			count++
		}
	}

	return count
}

// approvable reports whether a person may preview approving every listed run now, and why not.
func (m model) approvable() (bool, string) {
	reader := m.checks.approval
	switch {
	case reader.busy || reader.writing != "" || reader.problem != "":
		return false, "Wait for the workflow runs to load."
	case reader.fixed:
		return false, "Open the PR as a current item to approve its runs."
	case len(reader.page.Runs) == 0:
		return false, "No runs are awaiting approval for this head."
	case strings.TrimSpace(m.contributor) == "":
		return false, "Set a contributor name before approving runs."
	case reader.page.Counts.Suspicious > 0 && len(reader.page.HunkOmissions) > 0:
		return false, "Some flagged changes are not shown here. Read the full diff and approve on GitHub."
	case reader.page.Counts.Suspicious > 0 && reader.unread() > 0:
		return false, fmt.Sprintf("Read every flagged change first: %d of %d hunks haven't been scrolled into view.", reader.unread(), len(reader.read))
	}

	return true, ""
}

func (m model) workflowRunIDs() []string {
	var ids []string
	for _, run := range m.checks.approval.page.Runs {
		ids = append(ids, strconv.FormatInt(run.ID, 10))
	}

	return ids
}

func workflowRunArgs(repo string, number int, ids []string) []string {
	args := []string{"--expected-repo", repo, "--number", strconv.Itoa(number), "--host", evidenceHost}
	for _, id := range ids {
		args = append(args, "--run", id)
	}

	return args
}

// approveWorkflowRuns previews on the first press and publishes the exact previewed plan on the second.
func (m model) approveWorkflowRuns() (tea.Model, tea.Cmd) {
	reader := m.checks.approval
	if ok, why := m.approvable(); !ok {
		reader.confirm = nil
		m.warn(why)
		return m, nil
	}

	root, repo, key, generation, by, ids := m.installRoot, m.repo, m.detail.key, m.detail.generation, m.contributor, m.workflowRunIDs()
	if plan := reader.confirm; plan != nil {
		reader.confirm, reader.writing = nil, "Approving runs on GitHub…"
		return m, func() tea.Msg {
			msg := workflowActionMsg{root: root, repo: repo, action: "approve", key: key, generation: generation, plan: *plan}
			args := append([]string{"approve"}, workflowRunArgs(repo, key.Number, ids)...)
			out, err := runScript(root, "workflow-runs", append(args, "--by", by, "--publish", "--request-id", plan.Plan.RequestID, "--approve", plan.Approval)...)
			// A stopped write still prints its recorded outcomes, so they are read even when the script reports an error.
			if parseErr := json.Unmarshal([]byte(out), &msg.result); parseErr == nil && (msg.result.Repository != repo || msg.result.Number != key.Number || msg.result.RequestID != plan.Plan.RequestID) {
				msg.result = workflowApprovalResult{}
				if err == nil {
					err = fmt.Errorf("approval outcome belongs to another request")
				}
			}
			msg.err = err
			return msg
		}
	}

	page := reader.page
	reader.writing = "Preparing the exact approval…"
	return m, func() tea.Msg {
		msg := workflowActionMsg{root: root, repo: repo, action: "preview", key: key, generation: generation}
		out, err := runScript(root, "workflow-runs", append(append([]string{"approve"}, workflowRunArgs(repo, key.Number, ids)...), "--by", by)...)
		if err == nil {
			err = json.Unmarshal([]byte(out), &msg.plan)
		}
		if err == nil {
			err = validateApprovalPlan(msg.plan, page, repo, key, ids)
		}
		msg.err = err
		return msg
	}
}

// validateApprovalPlan binds the dry run to what is on screen: the same head, the same runs and the same assessment checksum.
func validateApprovalPlan(plan workflowApprovalPlan, page workflowAssessment, repo string, key Key, ids []string) error {
	p := plan.Plan
	if p.Repository != repo || p.Number != key.Number || plan.Requests != 0 || plan.Approval == "" || p.RequestID == "" {
		return fmt.Errorf("approval plan belongs to another item or was not offline")
	}
	if p.Head != page.Head || p.Checksum != page.Checksum || len(p.Runs) != len(ids) {
		return fmt.Errorf("the assessment changed since it was shown; press r to list the runs again")
	}

	shown := map[string]bool{}
	for _, id := range ids {
		shown[id] = true
	}
	for _, run := range p.Runs {
		if !shown[strconv.FormatInt(run.ID, 10)] {
			return fmt.Errorf("the approval plan names a run that isn't shown")
		}
	}

	return nil
}

func (m model) openWorkflowHold() (tea.Model, tea.Cmd) {
	reader := m.checks.approval
	if reader.busy || reader.writing != "" || reader.problem != "" || len(reader.page.Runs) == 0 {
		m.warn("No listed runs to hold.")
		return m, nil
	}
	if reader.fixed {
		m.warn("Open the PR as a current item to hold its runs.")
		return m, nil
	}
	if strings.TrimSpace(m.contributor) == "" {
		m.warn("Set a contributor name before holding runs.")
		return m, nil
	}

	input := textinput.New()
	input.Prompt = ""
	input.Placeholder = "optional; never posted"
	input.CharLimit = 160
	input.SetWidth(maxInt(m.commentWidth()-30, 10))
	themeInput(&input)
	reader.reason = &input
	return m, reader.reason.Focus()
}

func (m model) handleWorkflowHoldInput(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	reader := m.checks.approval
	switch msg.String() {
	case "esc":
		reader.reason = nil
		return m, nil
	case "enter":
		reason := strings.TrimSpace(reader.reason.Value())
		reader.reason, reader.writing = nil, "Saving the hold…"
		root, repo, key, generation, by, head, ids := m.installRoot, m.repo, m.detail.key, m.detail.generation, m.contributor, reader.page.Head, m.workflowRunIDs()
		return m, func() tea.Msg {
			msg := workflowActionMsg{root: root, repo: repo, action: "hold", key: key, generation: generation}
			for _, id := range ids {
				args := []string{"hold", "--expected-repo", repo, "--number", strconv.Itoa(key.Number), "--host", evidenceHost, "--run", id, "--head", head, "--by", by}
				if reason != "" {
					args = append(args, "--reason", reason)
				}
				if _, msg.err = runScript(root, "workflow-runs", args...); msg.err != nil {
					break
				}
				msg.count++
			}
			return msg
		}
	}

	input, cmd := reader.reason.Update(msg)
	reader.reason = &input
	return m, cmd
}

func (m model) activeWorkflowNotes() []workflowNote {
	var notes []workflowNote
	for _, note := range m.checks.approval.page.Local.Notes {
		if note.Released == nil {
			notes = append(notes, note)
		}
	}

	return notes
}

// releaseWorkflowNotes releases every active hold and agent concern on this head after a second u.
func (m model) releaseWorkflowNotes() (tea.Model, tea.Cmd) {
	reader := m.checks.approval
	notes := m.activeWorkflowNotes()
	if reader.busy || reader.writing != "" || reader.fixed || len(notes) == 0 || strings.TrimSpace(m.contributor) == "" {
		reader.releasing = false
		m.warn("No holds or concerns to release here.")
		return m, nil
	}
	if !reader.releasing {
		reader.releasing = true
		return m, nil
	}

	reader.releasing, reader.writing = false, "Releasing holds…"
	root, repo, key, generation, by := m.installRoot, m.repo, m.detail.key, m.detail.generation, m.contributor
	return m, func() tea.Msg {
		msg := workflowActionMsg{root: root, repo: repo, action: "release", key: key, generation: generation}
		for _, note := range notes {
			if _, msg.err = runScript(root, "workflow-runs", "release", "--expected-repo", repo, "--number", strconv.Itoa(key.Number), "--note", note.ID, "--by", by); msg.err != nil {
				break
			}
			msg.count++
		}
		return msg
	}
}

// commentOnWorkflowRuns opens the usual composer, prefilled with this head's latest hold reason for the person to rewrite; a hold itself is never posted.
func (m model) commentOnWorkflowRuns() (tea.Model, tea.Cmd) {
	reason := ""
	for _, note := range m.activeWorkflowNotes() {
		if note.Kind == "hold" && note.Reason != nil {
			reason = *note.Reason
		}
	}

	if reader := m.checks.approval; reader.process != nil {
		reader.process.stop()
	}
	m.checks.approval = nil
	m.workflowRunsRequest++
	// A notification's PR may be absent from the ledger, so its comment target comes from the fresh read it shows.
	next, cmd := m.openComment()
	if m.notificationPR.open {
		next, cmd = m.openNotificationCommentAction("c")
	}
	m = next.(model)
	if m.comment.open && reason != "" {
		m.comment.text.SetValue(reason)
	}
	return m, cmd
}

// finishWorkflowAction reports every write's outcome while the same install and repository are selected, even after the window closed; only an open window for the same item rereads.
func (m model) finishWorkflowAction(msg workflowActionMsg) (tea.Model, tea.Cmd) {
	if msg.root != m.installRoot || msg.repo != m.repo {
		return m, nil
	}

	reader := m.checks.approval
	current := reader != nil && msg.key == m.detail.key && msg.generation == m.detail.generation && m.checksKeyScope()
	if current {
		reader.writing = ""
	}

	switch msg.action {
	case "preview":
		if !current {
			return m, nil
		}
		if msg.err != nil {
			m.failErr("Couldn't prepare the approval", msg.err)
			return m, nil
		}
		// A reopened or reread window may show another assessment; the plan arms only the one it was made for.
		if reader.busy || msg.plan.Plan.Checksum != reader.page.Checksum {
			return m, nil
		}
		plan := msg.plan
		reader.confirm = &plan
		return m, nil
	case "approve":
		counts := map[string]int{}
		for _, row := range msg.result.Results {
			counts[row.Status]++
		}
		status := fmt.Sprintf("PR #%d: %d approved, %d refused by GitHub", msg.key.Number, counts["approved"], counts["refused"])
		if counts["changed"] > 0 {
			status += fmt.Sprintf(", %d skipped because they no longer await approval", counts["changed"])
		}
		if counts["unknown"] > 0 {
			m.failErr(status+"; an approval is uncertain, so inspect the run on GitHub before trying again", msg.err)
		} else if msg.err != nil {
			m.failErr("Nothing was approved", msg.err)
		} else {
			m.status = status + "."
		}
	case "hold", "release":
		done := map[string]string{"hold": "Held %d run(s)", "release": "Released %d hold(s) or concern(s)"}[msg.action]
		if msg.err != nil {
			m.failErr(fmt.Sprintf(done+" before stopping", msg.count), msg.err)
		} else {
			m.status = fmt.Sprintf(done+" on PR #%d. Holds are local and never posted.", msg.count, msg.key.Number)
		}
	}

	if !current {
		return m, nil
	}

	return m, m.rereadWorkflowRuns(msg.action == "approve")
}

func (m model) handleWorkflowReader(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	reader := m.checks.approval
	if reader.reason != nil {
		return m.handleWorkflowHoldInput(msg)
	}

	// The second press of a or u acts; any other key cancels it.
	if msg.String() != "a" {
		reader.confirm = nil
	}
	if msg.String() != "u" {
		reader.releasing = false
	}
	defer reader.markViewed()

	switch msg.String() {
	case "a":
		return m.approveWorkflowRuns()
	case "d":
		return m.openWorkflowHold()
	case "u":
		return m.releaseWorkflowNotes()
	case "c":
		if reader.fixed {
			m.warn("Open the PR as a current item to comment on it.")
			return m, nil
		}
		return m.commentOnWorkflowRuns()
	case "esc", "q":
		if reader.process != nil {
			reader.process.stop()
		}
		m.checks.approval = nil
		m.workflowRunsRequest++
		return m, nil
	case "r":
		if !reader.busy && !reader.fixed {
			return m, m.readWorkflowRuns()
		}
	case "n", "]":
		for _, offset := range reader.hunks {
			if offset > reader.viewport.YOffset() {
				reader.viewport.SetYOffset(offset)
				break
			}
		}
	case "p", "[":
		for index := len(reader.hunks) - 1; index >= 0; index-- {
			if reader.hunks[index] < reader.viewport.YOffset() {
				reader.viewport.SetYOffset(reader.hunks[index])
				break
			}
		}
	case "g", "home":
		reader.viewport.GotoTop()
	case "G", "end":
		reader.viewport.GotoBottom()
	default:
		var cmd tea.Cmd
		reader.viewport, cmd = reader.viewport.Update(msg)
		return m, cmd
	}

	return m, nil
}

func (m model) workflowRunsOverlay(background string) string {
	reader := m.checks.approval
	width := maxInt(m.commentWidth()-4, 1)
	content := reader.viewport.View()
	if reader.busy {
		content = "Listing workflow runs awaiting approval…"
		if reader.fixed {
			content = "Assessing the saved workflow run listing…"
		}
	}

	header := composerHeader("Workflow approval", "", fmt.Sprintf("PR #%d", m.detail.key.Number), false, width)
	if line := m.workflowStatusLine(width); line != "" {
		content += "\n\n" + line
	}
	return m.composerOverlay(background, m.composerPanel(header, content))
}

// workflowStatusLine sits below the scrolled content: the hold-reason input, a pending confirmation, a write in flight, or how much remains to read. Keys stay in the footer.
func (m model) workflowStatusLine(width int) string {
	reader := m.checks.approval
	warning := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Warning)).Bold(true)
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Muted))
	var line string
	switch {
	case reader.reason != nil:
		line = fmt.Sprintf("Hold %d listed run(s). Reason: ", len(reader.page.Runs)) + reader.reason.View()
	case reader.writing != "":
		line = muted.Render(reader.writing)
	case reader.confirm != nil:
		var ids []string
		suspicious := 0
		for _, run := range reader.confirm.Plan.Runs {
			ids = append(ids, strconv.FormatInt(run.ID, 10))
			if run.State != "normal" {
				suspicious++
			}
		}
		line = warning.Render(fmt.Sprintf("Ready to approve runs %s at head %s (%d suspicious).", strings.Join(ids, ", "), shortSHA(reader.confirm.Plan.Head), suspicious))
	case reader.releasing:
		line = warning.Render(fmt.Sprintf("Ready to release %d hold(s) or concern(s) on this head.", len(m.activeWorkflowNotes())))
	case reader.busy || reader.problem != "" || len(reader.page.Runs) == 0:
		return ""
	case reader.fixed:
		line = muted.Render("Pinned packet: reading only.")
	case reader.page.Counts.Suspicious > 0 && len(reader.read) > 0:
		read := len(reader.read) - reader.unread()
		line = muted.Render(fmt.Sprintf("Read %d of %d flagged hunks.", read, len(reader.read)))
		if read == len(reader.read) {
			line = muted.Render("Every flagged hunk has been read.")
		}
	default:
		return ""
	}

	return ansi.Truncate(line, width, "…")
}
