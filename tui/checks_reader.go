package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const checksLogLines = 80

type checksLogPicker struct {
	rows     []checkRow
	selected int
	snapshot string
}

type checksLogPage struct {
	LogObject        checkLogObject `json:"log_object"`
	SelectedSnapshot string         `json:"selected_snapshot_id"`
	CaptureSnapshot  string         `json:"capture_snapshot_id"`
	Packet           struct {
		Repository       checksRepository `json:"repository"`
		SourceRepository checksRepository `json:"source_repository"`
		Number           int              `json:"number"`
		Snapshot         string           `json:"snapshot_id"`
		Head             string           `json:"head_sha"`
		CheckID          int              `json:"check_id"`
		checkFailureLog
	} `json:"packet"`
	Windows         []checkLogWindow       `json:"windows"`
	Continuation    *checksLogContinuation `json:"continuation"`
	DisplayProblems []string               `json:"display_problems"`
}

type checksLogContinuation struct {
	LogObject checkLogObject `json:"log_object"`
	Number    int            `json:"number"`
	Snapshot  string         `json:"snapshot_id"`
	StartLine int            `json:"start_line"`
}

type checksLogReader struct {
	row      checkRow
	snapshot string
	source   string
	fixed    bool
	busy     bool
	process  *readProcess
	page     checksLogPage
	viewport viewport.Model
	starts   []int
	index    int
	problem  string
	footer   string
}

type checksLogMsg struct {
	root, repo, source, snapshot string
	fixed                        bool
	key                          Key
	generation, request          uint64
	row                          checkRow
	start                        int
	data                         checksLogPage
	err                          error
}

func (m model) failureLogRows() []checkRow {
	var rows []checkRow
	for _, row := range m.checks.page.Rows {
		if row.Result == "failed" {
			rows = append(rows, row)
		}
	}

	return rows
}

func (m model) canReadChecksLogs() bool {
	return m.checks.loaded && !m.checks.busy && m.checks.snapshot != "" && len(m.failureLogRows()) > 0
}

func (m model) openChecksLogs() (tea.Model, tea.Cmd) {
	if !m.canReadChecksLogs() {
		m.status = "No failure logs are available on this Checks page."
		return m, nil
	}

	rows := m.failureLogRows()
	if len(rows) == 1 {
		return m.startChecksLogReader(rows[0])
	}

	m.checks.picker = &checksLogPicker{rows: rows, snapshot: m.checks.snapshot}

	return m, nil
}

func (m model) handleChecksLogPicker(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	picker := m.checks.picker
	if picker.snapshot != m.checks.snapshot || !m.checksKeyScope() {
		m.checks.picker = nil
		return m, nil
	}
	switch msg.String() {
	case "esc", "q":
		m.checks.picker = nil
	case "j", "down", "tab":
		picker.selected = (picker.selected + 1) % len(picker.rows)
	case "k", "up", "shift+tab":
		picker.selected = (picker.selected + len(picker.rows) - 1) % len(picker.rows)
	case "enter":
		return m.startChecksLogReader(picker.rows[picker.selected])
	}

	return m, nil
}

func (m model) startChecksLogReader(row checkRow) (tea.Model, tea.Cmd) {
	source, fixed := m.checksSource()
	m.checks.picker = nil
	m.checks.reader = &checksLogReader{row: row, snapshot: m.checks.snapshot, source: source, fixed: fixed, viewport: viewport.New(), starts: []int{0}}
	log := row.FailureLog
	if log == nil || log.State != "retained" || log.LogObject == nil {
		m.checks.reader.problem = checksFailureSource(row)
		m.resizeChecksLogReader()
		return m, nil
	}

	return m, m.readChecksLog(0)
}

func (m *model) readChecksLog(start int) tea.Cmd {
	reader := m.checks.reader
	if reader == nil || reader.row.FailureLog == nil || reader.row.FailureLog.LogObject == nil {
		return nil
	}

	if reader.process != nil {
		reader.process.stop()
	}

	reader.process = &readProcess{}
	reader.busy = true
	reader.problem = ""
	m.checksRequest++
	m.resizeChecksLogReader()
	return checksLogReadCmd(m.installRoot, m.repo, m.detail.key, reader.source, reader.snapshot, reader.fixed, reader.row, start, m.detail.generation, m.checksRequest, reader.process)
}

func checksLogReadCmd(root, repo string, item Key, source, snapshot string, fixed bool, row checkRow, start int, generation, request uint64, process *readProcess) tea.Cmd {
	return func() tea.Msg {
		msg := checksLogMsg{root: root, repo: repo, key: item, source: source, snapshot: snapshot, fixed: fixed, row: row, start: start, generation: generation, request: request}
		ref := row.FailureLog.LogObject
		args := []string{"read", "--expected-repo", repo, "--number", strconv.Itoa(item.Number), "--snapshot", snapshot, "--source-repo", row.Repository, "--check-id", strconv.Itoa(row.InternalID), "--host", evidenceHost, "--log-object", ref.SHA256, "--log-bytes", strconv.Itoa(ref.Bytes), "--max-lines", strconv.Itoa(checksLogLines)}
		if start > 0 {
			args = append(args, "--start-line", strconv.Itoa(start))
		}

		out, err := runReadScript(process, root, "pr-check-log", args...)
		if err == nil && len(out) > 524288 {
			err = fmt.Errorf("failure log response exceeds its size limit")
		}
		if err == nil {
			err = json.Unmarshal([]byte(out), &msg.data)
		}
		if err == nil {
			err = validateChecksLogPage(msg.data, repo, item, snapshot, row, start)
		}

		msg.err = err
		return msg
	}
}

func validateChecksLogPage(data checksLogPage, repo string, item Key, snapshot string, row checkRow, start int) error {
	packet := data.Packet
	captureSnapshot := row.FailureLog.CaptureSnapshot
	if captureSnapshot == "" {
		captureSnapshot = snapshot
	}
	if data.LogObject != *row.FailureLog.LogObject || packet.Repository.Host != evidenceHost || packet.Repository.FullName != repo || packet.SourceRepository.Host != evidenceHost || packet.SourceRepository.FullName != row.Repository || packet.Number != item.Number || packet.CheckID != row.InternalID || packet.Head != row.HeadSHA || data.SelectedSnapshot != snapshot || data.CaptureSnapshot != captureSnapshot || packet.Snapshot != captureSnapshot {
		return fmt.Errorf("failure log differs from the selected check, object or snapshot")
	}

	lines, bytes, previous := 0, 0, 0
	for _, window := range data.Windows {
		count := strings.Count(window.Text, "\n")
		if !strings.HasSuffix(window.Text, "\n") {
			count++
		}
		if window.StartLine <= previous || window.EndLine < window.StartLine || window.Text == "" || count < 1 || count != window.EndLine-window.StartLine+1 || len(window.SectionTitle) > 4000 {
			return fmt.Errorf("failure log window differs from its source lines")
		}
		if previous == 0 && start > 0 && window.StartLine != start {
			return fmt.Errorf("failure log starts at another source line")
		}
		lines += count
		bytes += len(window.Text)
		previous = window.EndLine
	}

	if lines > checksLogLines || bytes > 196608 || len(data.Windows) > checksLogLines || len(data.DisplayProblems) > 10 || len(packet.Omissions) > 10 {
		return fmt.Errorf("failure log exceeds its display budget")
	}
	for _, value := range append(append([]string{}, packet.Omissions...), data.DisplayProblems...) {
		if len(value) > 4000 {
			return fmt.Errorf("failure log notice exceeds its display budget")
		}
	}
	if next := data.Continuation; next != nil && (next.LogObject != data.LogObject || next.Snapshot != snapshot || next.Number != item.Number || next.StartLine <= previous || previous == 0) {
		return fmt.Errorf("failure log continuation changed its source")
	}
	return nil
}

func (m model) finishChecksLog(msg checksLogMsg) (tea.Model, tea.Cmd) {
	source, fixed := m.checksSource()
	reader := m.checks.reader
	if reader == nil || !reader.busy || msg.root != m.installRoot || msg.repo != m.repo || msg.key != m.detail.key || msg.generation != m.detail.generation || msg.request != m.checksRequest || msg.source != source || msg.fixed != fixed || msg.snapshot != m.checks.snapshot || msg.snapshot != reader.snapshot || msg.row.InternalID != reader.row.InternalID || msg.row.Repository != reader.row.Repository || msg.row.HeadSHA != reader.row.HeadSHA || *msg.row.FailureLog.LogObject != *reader.row.FailureLog.LogObject || !m.checksKeyScope() {
		return m, nil
	}

	reader.busy = false
	if msg.err != nil {
		reader.problem = "Retained failure log could not be verified.\n\n" + singleLine(msg.err.Error())
	} else {
		reader.page = msg.data
	}

	m.resizeChecksLogReader()
	reader.viewport.GotoTop()

	return m, nil
}

// checksLogText changes only the reading presentation; source line ranges and cached bytes remain intact.
func checksLogText(text, sectionTitle string) string {
	var lines []string
	for _, line := range strings.Split(strings.TrimSuffix(ansi.Strip(text), "\n"), "\n") {
		if prefix, rest, ok := strings.Cut(line, " "); ok {
			if _, err := time.Parse(time.RFC3339Nano, prefix); err == nil {
				line = rest
			}
		}
		if strings.HasPrefix(line, "##[group]") {
			title := singleLine(strings.TrimPrefix(line, "##[group]"))
			if title == sectionTitle {
				continue
			}
			line = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(currentTheme.Accent)).Render(title)
		} else if strings.HasPrefix(line, "##[endgroup]") {
			line = ""
		} else {
			line = sanitize(strings.ReplaceAll(line, "##[error]", "Error: "))
		}
		lines = append(lines, line)
	}

	return strings.Join(lines, "\n")
}

func (m *model) resizeChecksLogReader() {
	reader := m.checks.reader
	if reader == nil {
		return
	}

	width := maxInt(m.commentWidth()-4, 1)
	reader.viewport.SetWidth(width)
	reader.footer = ""
	content := ansi.Wrap(reader.problem, width, "")
	if !reader.busy && reader.problem == "" {
		var b strings.Builder
		log := reader.page.Packet.checkFailureLog
		if log.DownloadPartial {
			b.WriteString("Partial retained log; complete job log coverage is unknown.\n\n")
		}
		if !log.BindingRevalidated && len(reader.page.Windows) > 0 {
			b.WriteString("Source binding could not be revalidated after capture.\n\n")
		}
		for _, window := range reader.page.Windows {
			title := singleLine(window.SectionTitle)
			if title == "" {
				title = "Retained context"
			}
			heading := fmt.Sprintf("%s · Source lines %d–%d", title, window.StartLine, window.EndLine)
			b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(currentTheme.Accent)).Render(heading))
			b.WriteString("\n")
			b.WriteString(checksLogText(window.Text, title))
			b.WriteString("\n")
			if window.DisplayTruncated {
				b.WriteString("This source window was truncated for display.\n")
			}
			b.WriteString("\n")
		}
		content = ansi.Wrap(b.String(), width, "")
		for _, notice := range checksLogNotices(log.Omissions, reader.page.DisplayProblems) {
			notice = singleLine(notice)
			if notice == "only selected source windows are retained; intervening lines are omitted" {
				notice = "Only" + strings.TrimPrefix(notice, "only")
				if height := maxInt(m.commentHeight()-5, 0); height > 0 {
					reader.footer = lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Muted)).Width(width).Align(lipgloss.Center).Render(fitScreen(ansi.Wrap(notice, width, ""), width, height))
				}
				continue
			}
			content += ansi.Wrap(notice, width, "") + "\n"
		}
	}

	footerHeight := 0
	if reader.footer != "" {
		footerHeight = lipgloss.Height(reader.footer)
	}
	reader.viewport.SetHeight(maxInt(m.commentHeight()-4-footerHeight, 1))
	reader.viewport.SetContent(content)
}

func (m model) handleChecksLogReader(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	reader := m.checks.reader
	switch msg.String() {
	case "esc", "q":
		if reader.process != nil {
			reader.process.stop()
		}
		m.checks.reader = nil
		m.checksRequest++
		return m, nil
	case "n", "]":
		if !reader.busy && reader.page.Continuation != nil {
			start := reader.page.Continuation.StartLine
			reader.starts = append(reader.starts[:reader.index+1], start)
			reader.index++
			return m, m.readChecksLog(start)
		}
	case "p", "[":
		if !reader.busy && reader.index > 0 {
			reader.index--
			return m, m.readChecksLog(reader.starts[reader.index])
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

func (m model) checksLogsOverlay(background string) string {

	width := maxInt(m.commentWidth()-4, 1)
	if picker := m.checks.picker; picker != nil {
		var cards [][2]string
		for _, row := range picker.rows {
			state := "No retained log"
			if row.FailureLog != nil {
				state = "Log " + row.FailureLog.State
			}
			cards = append(cards, [2]string{singleLine(row.Name), state + " · " + singleLine(row.Repository)})
		}
		header := composerHeader("Failure logs", "", "Select a check", false, width)
		return m.composerOverlay(background, m.composerPanel(header, cardList(cards, picker.selected, width, maxInt(m.commentHeight()-4, 1))))
	}
	reader := m.checks.reader
	content := reader.viewport.View()
	if reader.busy {
		content = "Reading retained log…"
	} else if reader.footer != "" {
		content += "\n" + reader.footer
	}
	header := composerHeader("Failure log", "", singleLine(reader.row.Name), false, width)
	return m.composerOverlay(background, m.composerPanel(header, content))
}
