package main

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
)

const checksCaptureBudget = 20
const checksCaptureBytes = 262144

type checksCapturePicker struct {
	rows     []checkRow
	selected int
	snapshot string
}

type checksCaptureResponse struct {
	Repository       checksRepository `json:"repository"`
	SourceRepository checksRepository `json:"source_repository"`
	Number           int              `json:"number"`
	Snapshot         string           `json:"snapshot_id"`
	CheckID          int              `json:"check_id"`
	Head             string           `json:"head_sha"`
	Status           string           `json:"status"`
	Stage            string           `json:"capture_stage"`
	LogObject        *checkLogObject  `json:"log_object"`
	Requests         int              `json:"requests"`
	ReservedRequests int              `json:"reserved_requests"`
	RequestBudget    int              `json:"request_budget"`
	MaxLogBytes      int              `json:"max_log_bytes"`
	Omissions        []string         `json:"omissions"`
}

type checksCaptureOutcome struct {
	response checksCaptureResponse
	row      checkRow
	err      error
}

type checksCaptureMsg struct {
	root, repo, source, snapshot string
	key                          Key
	generation, request          uint64
	row                          checkRow
	data                         checksCaptureResponse
	err                          error
}

func (m model) captureRows() []checkRow {
	var rows []checkRow
	for _, row := range m.checks.page.Rows {
		if row.CaptureSupported && row.Result == "failed" && row.Source == "check-run" && row.InternalID > 0 {
			rows = append(rows, row)
		}
	}

	return rows
}

func (m model) canCaptureChecks() bool {
	_, fixed := m.checksSource()
	return !fixed && m.checks.loaded && !m.checks.busy && m.checks.snapshot != "" && len(m.captureRows()) > 0
}

func (m model) openChecksCapture() (tea.Model, tea.Cmd) {
	if _, fixed := m.checksSource(); fixed {
		m.status = "This item packet stays offline. Reopen a current PR to capture a failure log."
		return m, nil
	}

	if m.checks.busy {
		m.status = "Wait for the selected CI read to finish."
		return m, nil
	}

	rows := m.captureRows()
	if !m.checks.loaded || m.checks.snapshot == "" || len(rows) == 0 {
		m.status = "No supported failed Actions check is available on this page."
		return m, nil
	}

	if len(rows) == 1 {
		return m.startChecksCapture(rows[0])
	}

	m.checks.picker = &checksCapturePicker{rows: rows, snapshot: m.checks.snapshot}
	m.status = "Choose one failed check to capture its log."
	return m, nil
}

func (m model) handleChecksCapturePicker(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	picker := m.checks.picker
	if picker == nil {
		return m, nil
	}

	if picker.snapshot != m.checks.snapshot || !m.checksKeyScope() {
		m.checks.picker = nil
		return m, nil
	}

	switch msg.String() {
	case "esc":
		m.checks.picker = nil
		m.status = "Failure log capture cancelled."
	case "j", "down", "tab":
		picker.selected = (picker.selected + 1) % len(picker.rows)
	case "k", "up", "shift+tab":
		picker.selected = (picker.selected + len(picker.rows) - 1) % len(picker.rows)
	case "enter":
		return m.startChecksCapture(picker.rows[picker.selected])
	}

	return m, nil
}

func (m model) checksCaptureOverlay(background string) string {
	picker := m.checks.picker
	var cards [][2]string
	for _, row := range picker.rows {
		cards = append(cards, [2]string{singleLine(row.Name), singleLine(row.App) + " · " + singleLine(row.Repository)})
	}

	header := composerHeader("Capture failure log", "", "Select one failed check", false, m.commentWidth()-4)
	content := cardList(cards, picker.selected, maxInt(m.commentWidth()-4, 1), maxInt(m.commentHeight()-4, 1))
	return m.composerOverlay(background, m.composerPanel(header, content))
}

func (m model) startChecksCapture(row checkRow) (tea.Model, tea.Cmd) {
	if _, fixed := m.checksSource(); fixed || !m.checksKeyScope() || m.checks.snapshot == "" {
		m.checks.picker = nil
		return m, nil
	}
	if !row.CaptureSupported || row.InternalID < 1 || row.Result != "failed" || row.Source != "check-run" {
		return m, nil
	}

	m.stopChecks()
	if m.checksLifecycle == nil {
		m.checksLifecycle = &readLifecycle{}
	}

	m.checksLifecycle.current = &readProcess{}
	m.checks.busy, m.checks.capturing = true, true
	m.checks.pendingSnapshot = m.checks.snapshot
	m.checks.captureRow = &row
	m.status = "Capturing one selected failure log… Esc cancels."
	source, _ := m.checksSource()
	return m, checksCaptureCmd(m.installRoot, m.repo, m.detail.key, source, m.checks.snapshot, row, m.detail.generation, m.checksRequest, m.checksLifecycle.current)
}

func checksCaptureCmd(root, repo string, item Key, source, snapshot string, row checkRow, generation, request uint64, process *readProcess) tea.Cmd {
	return func() tea.Msg {
		msg := checksCaptureMsg{root: root, repo: repo, key: item, source: source, snapshot: snapshot, row: row, generation: generation, request: request}
		args := []string{"capture", "--expected-repo", repo, "--number", strconv.Itoa(item.Number), "--snapshot", snapshot, "--source-repo", row.Repository, "--check-id", strconv.Itoa(row.InternalID), "--host", evidenceHost, "--request-budget", strconv.Itoa(checksCaptureBudget), "--max-log-bytes", strconv.Itoa(checksCaptureBytes)}
		out, err := runReadScript(process, root, "pr-check-log", args...)
		if err == nil && len(out) > 524288 {
			err = fmt.Errorf("failure log capture response exceeds its size limit")
		}
		if err == nil {
			err = json.Unmarshal([]byte(out), &msg.data)
		}
		if err == nil {
			err = validateChecksCapture(msg.data, repo, item, snapshot, row)
		}
		msg.err = err
		return msg
	}
}

func validateChecksCapture(data checksCaptureResponse, repo string, item Key, snapshot string, row checkRow) error {
	if data.Repository.Host != evidenceHost || data.Repository.FullName != repo || data.SourceRepository.Host != evidenceHost || data.SourceRepository.FullName != row.Repository || data.Number != item.Number || data.Snapshot != snapshot || data.CheckID != row.InternalID || data.Head != row.HeadSHA {
		return fmt.Errorf("failure log capture differs from the selected check and snapshot")
	}
	if data.RequestBudget != checksCaptureBudget || data.MaxLogBytes != checksCaptureBytes || data.Requests < 0 || data.ReservedRequests != 0 && data.ReservedRequests != 11 || data.Requests+data.ReservedRequests > checksCaptureBudget || data.Status != "retained" && data.Status != "unavailable" || data.Stage != "locate" && data.Stage != "fetch" || len(data.Omissions) > 10 {
		return fmt.Errorf("failure log capture status or budget is invalid")
	}
	if data.Status == "retained" && data.LogObject == nil {
		return fmt.Errorf("failure log capture has no immutable retained object")
	}
	if ref := data.LogObject; ref != nil {
		if _, err := hex.DecodeString(ref.SHA256); err != nil || len(ref.SHA256) != 64 || ref.Bytes < 1 || ref.Bytes > 2000000 || ref.Format != "json" {
			return fmt.Errorf("failure log capture object reference is invalid")
		}
	}
	for _, omission := range data.Omissions {
		if len(omission) > 4000 {
			return fmt.Errorf("failure log capture notice exceeds its display limit")
		}
	}
	return nil
}

func (m model) finishChecksCapture(msg checksCaptureMsg) (tea.Model, tea.Cmd) {
	source, fixed := m.checksSource()
	row := m.checks.captureRow
	if !m.checks.busy || !m.checks.capturing || row == nil || fixed || msg.root != m.installRoot || msg.repo != m.repo || msg.key != m.detail.key || msg.generation != m.detail.generation || msg.request != m.checksRequest || msg.source != source || msg.snapshot != m.checks.snapshot || msg.snapshot != m.checks.pendingSnapshot || row.InternalID != msg.row.InternalID || row.Repository != msg.row.Repository || row.HeadSHA != msg.row.HeadSHA || !m.notificationPR.open && m.focus != FocusDetail {
		return m, nil
	}

	outcome := &checksCaptureOutcome{response: msg.data, row: msg.row, err: msg.err}
	if msg.err != nil {
		m.recordError("Failure log capture unavailable", msg.err)
	} else if msg.data.Status != "retained" {
		m.recordError("Failure log capture unavailable", fmt.Errorf("Failure log unavailable.\n%s", strings.Join(checksLogNotices(msg.data.Omissions), "\n")))
	}

	cmd := m.readChecks("offline", m.checks.page.Pagination.Offset, msg.row)
	m.checks.captureOutcome = outcome
	return m, cmd
}

func (m *model) finishCaptureOutcome(page checksPage) {
	outcome := m.checks.captureOutcome
	if outcome == nil {
		return
	}

	m.checks.captureOutcome = nil
	if outcome.err != nil || outcome.response.Status != "retained" {
		m.status = "Failure log unavailable. ! shows details."
		if checksDisplayedExcerpt(page, &outcome.row) {
			m.status += " Previous excerpt still displayed."
		}
		return
	}

	for _, row := range page.Rows {
		log := row.FailureLog
		if row.InternalID == outcome.response.CheckID && row.Repository == outcome.response.SourceRepository.FullName && log != nil && log.State == "retained" && log.LogObject != nil && log.LogObject.SHA256 == outcome.response.LogObject.SHA256 {
			m.status = "Selected failure excerpt captured and verified offline."
			return
		}
	}
	m.status = "Capture completed, but its verified excerpt is unavailable in this page."
}

// checksDisplayedExcerpt only identifies retained text for the check selected by this attempt.
func checksDisplayedExcerpt(page checksPage, selected *checkRow) bool {
	if selected == nil {
		return false
	}

	for _, row := range page.Rows {
		if row.InternalID == selected.InternalID && row.Repository == selected.Repository && row.HeadSHA == selected.HeadSHA && row.FailureLog != nil && row.FailureLog.State == "retained" && len(row.FailureLog.Windows) > 0 {
			return true
		}
	}
	return false
}
