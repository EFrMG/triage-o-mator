package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Notifications presents tracked comments and retained PR records in two sections.
type notificationsUI struct {
	open, busy  bool
	tracked     *trackedPage
	attention   *attentionPage
	closures    *actionHistoryPage
	state       notificationState
	selected    int
	selectAfter string
	problem     string
}

type notificationState struct {
	Repository struct {
		Name string `json:"full_name"`
	} `json:"repository"`
	Rows     map[string]notificationStatus `json:"rows"`
	Requests int                           `json:"requests"`
}

type notificationStatus struct {
	ViewedCheckpoint *string `json:"viewed_checkpoint"`
	Dismissed        bool    `json:"dismissed"`
}

type notificationsMsg struct {
	root, repo string
	generation uint64
	tracked    trackedPage
	attention  attentionPage
	closures   actionHistoryPage
	state      notificationState
	err        error
}

type notificationChoice struct {
	kind string
	row  int
}

type notificationDoneMsg struct {
	root, repo, action, source string
	number                     int
	err                        error
}

func notificationChangeCmd(root, repo, action, source string, number int, checkpoint string) tea.Cmd {
	return func() tea.Msg {
		_, err := runScript(root, "cache", "--expected-repo", repo, "notification-"+action, "--source", source, "--number", fmt.Sprint(number), "--checkpoint", checkpoint)
		return notificationDoneMsg{root: root, repo: repo, action: action, source: source, number: number, err: err}
	}
}

func (m model) finishNotificationChange(msg notificationDoneMsg) (tea.Model, tea.Cmd) {
	if msg.root != m.installRoot || msg.repo != m.repo {
		return m, nil
	}
	m.trackingBusy = false
	if msg.err != nil {
		m.recordError("Couldn't update notification", msg.err)
		m.status = "Couldn't update notification. ! shows details."
		return m, nil
	}
	if msg.action == "dismiss" {
		m.status = fmt.Sprintf("Dismissed %s PR #%d from Notifications.", msg.source, msg.number)
	} else {
		m.status = fmt.Sprintf("Viewed %s PR #%d; saved evidence remains.", msg.source, msg.number)
	}
	return m.openNotifications()
}

const notificationsPageSize = 5

func notificationsCommand(root, repo string, generation uint64, trackedAt, attentionAt, closureAt int, attentionCheckpoint, closureCheckpoint string, process *readProcess) tea.Cmd {
	return func() tea.Msg {
		msg := notificationsMsg{root: root, repo: repo, generation: generation}
		for _, read := range []struct {
			command, checkpoint string
			offset              int
			output              any
		}{
			{"track-list", "", trackedAt, &msg.tracked},
			{"attention-list", attentionCheckpoint, attentionAt, &msg.attention},
			{"action-list", closureCheckpoint, closureAt, &msg.closures},
		} {
			args := []string{"--expected-repo", repo, read.command, "--offset", fmt.Sprint(read.offset), "--limit", fmt.Sprint(notificationsPageSize)}
			if read.checkpoint != "" {
				args = append(args, "--checkpoint", read.checkpoint)
			}
			out, err := runReadScript(process, root, "cache", args...)
			if err == nil {
				err = json.Unmarshal([]byte(out), read.output)
			}
			if err != nil {
				msg.err = err
				return msg
			}
		}
		out, err := runReadScript(process, root, "cache", "--expected-repo", repo, "notification-state")
		if err == nil {
			err = json.Unmarshal([]byte(out), &msg.state)
		}
		if err != nil {
			msg.err = err
			return msg
		}
		if msg.tracked.Repository.Name != repo || msg.tracked.Offset != trackedAt || len(msg.tracked.Rows) > notificationsPageSize {
			msg.err = fmt.Errorf("tracked item response identity or bounds mismatch")
		} else if msg.state.Repository.Name != repo || msg.state.Requests != 0 {
			msg.err = fmt.Errorf("notification state identity mismatch")
		} else if err := validateAttentionPage(msg.attention, attentionLocation{section: "list", offset: attentionAt, checkpoint: attentionCheckpoint}, repo); err != nil {
			msg.err = err
		} else {
			msg.err = validateActionHistoryPage(msg.closures, actionHistoryLocation{section: "list", offset: closureAt, checkpoint: closureCheckpoint}, repo)
		}
		return msg
	}
}

func (m model) openNotifications() (tea.Model, tea.Cmd) {
	if m.notificationsLifecycle == nil {
		m.notificationsLifecycle = &readLifecycle{}
	}
	m.notificationsLifecycle.stop()
	m.notificationsGeneration++
	m.notifications = notificationsUI{open: true, busy: true}
	m.notificationsLifecycle.current = &readProcess{}
	return m, notificationsCommand(m.installRoot, m.repo, m.notificationsGeneration, 0, 0, 0, "", "", m.notificationsLifecycle.current)
}

func (m model) pageNotifications(section string, offset int) (tea.Model, tea.Cmd) {
	n := m.notifications
	if n.tracked == nil {
		n.tracked = &trackedPage{}
	}
	trackedAt, attentionAt, closureAt := n.tracked.Offset, n.attention.Pagination.Offset, n.closures.Pagination.Offset
	m.notifications.selectAfter = section + "-prev"
	if section == "tracked" {
		trackedAt = offset
	} else if section == "attention" {
		attentionAt = offset
	} else {
		closureAt = offset
	}
	if offset == 0 {
		m.notifications.selectAfter = section + "-more"
	}
	m.notificationsLifecycle.stop()
	m.notificationsGeneration++
	m.notifications.busy = true
	m.notificationsLifecycle.current = &readProcess{}
	return m, notificationsCommand(m.installRoot, m.repo, m.notificationsGeneration, trackedAt, attentionAt, closureAt, n.attention.Checkpoint, n.closures.Checkpoint, m.notificationsLifecycle.current)
}

func (m model) finishNotifications(msg notificationsMsg) (tea.Model, tea.Cmd) {
	if !m.notifications.open || msg.root != m.installRoot || msg.repo != m.repo || msg.generation != m.notificationsGeneration {
		return m, nil
	}
	m.notifications.busy = false
	if msg.err != nil {
		m.notifications.problem = "Retained notifications changed or are unavailable. Leave and reopen Notifications to retry. No live read was made."
		m.recordError("Notifications read unavailable", msg.err)
		return m, nil
	}
	m.notifications.attention = &msg.attention
	m.notifications.closures = &msg.closures
	m.notifications.tracked = &msg.tracked
	m.notifications.state = msg.state
	m.sidebar.notificationCount = msg.tracked.UnreadTotal
	if m.notifications.selectAfter != "" {
		for i, choice := range m.notifications.choices() {
			if choice.kind == m.notifications.selectAfter {
				m.notifications.selected = i
				break
			}
		}
		m.notifications.selectAfter = ""
	}
	return m, nil
}

func (n notificationsUI) rowState(source string, number int) (string, bool) {
	row := n.state.Rows[fmt.Sprintf("%s:pr:%d", source, number)]
	if row.ViewedCheckpoint == nil {
		return "", row.Dismissed
	}
	return *row.ViewedCheckpoint, row.Dismissed
}

func (n notificationsUI) watchNeeds(row attentionRow) bool {
	viewed, dismissed := n.rowState("watch", row.Number)
	return !dismissed && row.Attention && (row.WatchCheckpoint == "" || viewed != row.WatchCheckpoint)
}

func (n notificationsUI) actionNeeds(row actionHistoryRow) bool {
	viewed, dismissed := n.rowState("action", row.Number)
	return !dismissed && (row.HistoryCheckpoint == "" || viewed != row.HistoryCheckpoint)
}

func (n notificationsUI) choiceNeeds(choice notificationChoice) bool {
	switch choice.kind {
	case "tracked":
		return n.tracked.Rows[choice.row].NewCount > 0
	case "attention":
		return n.watchNeeds(n.attention.Rows[choice.row])
	case "closure":
		return n.actionNeeds(n.closures.Rows[choice.row])
	case "attention-prev", "attention-more", "closure-prev", "closure-more":
		return true
	default:
		return false
	}
}

func (n notificationsUI) choices() []notificationChoice {
	var choices []notificationChoice
	if n.tracked != nil {
		for i := range n.tracked.Rows {
			if n.tracked.Rows[i].NewCount > 0 {
				choices = append(choices, notificationChoice{kind: "tracked", row: i})
			}
		}
	}
	if n.attention != nil {
		if n.attention.Pagination.Offset > 0 {
			choices = append(choices, notificationChoice{kind: "attention-prev"})
		}
		for i := range n.attention.Rows {
			if n.watchNeeds(n.attention.Rows[i]) {
				choices = append(choices, notificationChoice{kind: "attention", row: i})
			}
		}
		if n.attention.Pagination.Next != nil {
			choices = append(choices, notificationChoice{kind: "attention-more"})
		}
	}
	if n.closures != nil {
		if n.closures.Pagination.Offset > 0 {
			choices = append(choices, notificationChoice{kind: "closure-prev"})
		}
		for i := range n.closures.Rows {
			if n.actionNeeds(n.closures.Rows[i]) {
				choices = append(choices, notificationChoice{kind: "closure", row: i})
			}
		}
		if n.closures.Pagination.Next != nil {
			choices = append(choices, notificationChoice{kind: "closure-more"})
		}
	}
	if n.tracked != nil {
		if n.tracked.Offset > 0 {
			choices = append(choices, notificationChoice{kind: "tracked-prev"})
		}
		for i := range n.tracked.Rows {
			if n.tracked.Rows[i].NewCount == 0 {
				choices = append(choices, notificationChoice{kind: "tracked", row: i})
			}
		}
		if n.tracked.Next != nil {
			choices = append(choices, notificationChoice{kind: "tracked-more"})
		}
	}
	if n.attention != nil {
		for i := range n.attention.Rows {
			if !n.watchNeeds(n.attention.Rows[i]) {
				_, dismissed := n.rowState("watch", n.attention.Rows[i].Number)
				if !dismissed {
					choices = append(choices, notificationChoice{kind: "attention", row: i})
				}
			}
		}
	}
	if n.closures != nil {
		for i := range n.closures.Rows {
			_, dismissed := n.rowState("action", n.closures.Rows[i].Number)
			if !dismissed && !n.actionNeeds(n.closures.Rows[i]) {
				choices = append(choices, notificationChoice{kind: "closure", row: i})
			}
		}
	}
	return choices
}

func (m model) handleNotificationsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "?":
		m.showHelp = !m.showHelp
		return m, nil
	case "q":
		m.notificationsLifecycle.stop()
		m.notificationsGeneration++
		return m.requestQuit()
	case "esc", "x", "h":
		m.notificationsLifecycle.stop()
		m.notificationsGeneration++
		m.notifications = notificationsUI{}
		return m, nil
	case "r":
		return m.startRefresh(false)
	}
	if m.notifications.busy || m.notifications.problem != "" {
		return m, nil
	}
	choices := m.notifications.choices()
	switch msg.String() {
	case "v":
		if len(choices) == 0 || m.trackingBusy {
			break
		}
		choice := choices[m.notifications.selected]
		switch choice.kind {
		case "tracked":
			row := m.notifications.tracked.Rows[choice.row]
			if row.NewCount > 0 {
				m.trackingBusy = true
				return m, trackCmd(m.installRoot, m.repo, "read", row.key(), row)
			}
		case "attention":
			row := m.notifications.attention.Rows[choice.row]
			if row.Selectable && m.notifications.watchNeeds(row) {
				m.trackingBusy = true
				return m, notificationChangeCmd(m.installRoot, m.repo, "view", "watch", row.Number, row.WatchCheckpoint)
			}
		case "closure":
			row := m.notifications.closures.Rows[choice.row]
			if row.Selectable && m.notifications.actionNeeds(row) {
				m.trackingBusy = true
				return m, notificationChangeCmd(m.installRoot, m.repo, "view", "action", row.Number, row.HistoryCheckpoint)
			}
		}
	case "d":
		if len(choices) == 0 || m.trackingBusy {
			break
		}
		choice := choices[m.notifications.selected]
		switch choice.kind {
		case "tracked":
			key := m.notifications.tracked.Rows[choice.row].key()
			m.trackingBusy = true
			return m, trackCmd(m.installRoot, m.repo, "remove", key)
		case "attention":
			row := m.notifications.attention.Rows[choice.row]
			checkpoint := row.WatchCheckpoint
			if !row.Selectable {
				checkpoint = m.notifications.attention.Checkpoint
			}
			m.trackingBusy = true
			return m, notificationChangeCmd(m.installRoot, m.repo, "dismiss", "watch", row.Number, checkpoint)
		case "closure":
			row := m.notifications.closures.Rows[choice.row]
			checkpoint := row.HistoryCheckpoint
			if !row.Selectable {
				checkpoint = m.notifications.closures.Checkpoint
			}
			m.trackingBusy = true
			return m, notificationChangeCmd(m.installRoot, m.repo, "dismiss", "action", row.Number, checkpoint)
		}
	case "j", "down", "tab":
		if len(choices) > 0 {
			m.notifications.selected = (m.notifications.selected + 1) % len(choices)
		}
	case "k", "up", "shift+tab":
		if len(choices) > 0 {
			m.notifications.selected = (m.notifications.selected - 1 + len(choices)) % len(choices)
		}
	case "enter", "l", "right":
		if len(choices) == 0 {
			break
		}
		choice := choices[m.notifications.selected]
		switch choice.kind {
		case "tracked":
			return m.openNotificationItem(m.notifications.tracked.Rows[choice.row].key())
		case "tracked-more":
			return m.pageNotifications("tracked", *m.notifications.tracked.Next)
		case "tracked-prev":
			return m.pageNotifications("tracked", maxInt(0, m.notifications.tracked.Offset-notificationsPageSize))
		case "attention":
			row := m.notifications.attention.Rows[choice.row]
			if row.Selectable {
				return m.readAttention(attentionLocation{section: "history", number: row.Number, checkpoint: row.WatchCheckpoint})
			}
		case "closure":
			row := m.notifications.closures.Rows[choice.row]
			if row.Selectable {
				return m.readActionHistory(actionHistoryLocation{section: "entries", number: row.Number, checkpoint: row.HistoryCheckpoint})
			}
		case "attention-more":
			return m.pageNotifications("attention", *m.notifications.attention.Pagination.Next)
		case "attention-prev":
			return m.pageNotifications("attention", maxInt(0, m.notifications.attention.Pagination.Offset-notificationsPageSize))
		case "closure-more":
			return m.pageNotifications("closure", *m.notifications.closures.Pagination.Next)
		case "closure-prev":
			return m.pageNotifications("closure", maxInt(0, m.notifications.closures.Pagination.Offset-notificationsPageSize))
		}
	}
	return m, nil
}

func notificationAttentionSummary(row attentionRow) string {
	var detail struct {
		ResponseComments int `json:"response_comments"`
		Coverage         struct {
			LastSuccessful *string `json:"last_successful_check"`
			LatestComplete bool    `json:"latest_complete"`
			LatestGaps     int     `json:"latest_gap_count"`
		} `json:"coverage"`
	}
	if json.Unmarshal([]byte(row.Preview), &detail) != nil {
		return "Retained activity · open for the saved discussion"
	}
	parts := []string{}
	if detail.ResponseComments > 0 {
		parts = append(parts, fmt.Sprintf("%d response comment(s)", detail.ResponseComments))
	} else {
		parts = append(parts, "No response after closure")
	}
	if detail.Coverage.LatestGaps > 0 || !detail.Coverage.LatestComplete {
		parts = append(parts, "coverage incomplete")
	}
	if detail.Coverage.LastSuccessful == nil {
		parts = append(parts, "no complete check")
	}
	return strings.Join(parts, " · ")
}

func (m model) notificationsView() string {
	w := m.menuWidth()
	n := m.notifications
	if n.tracked == nil {
		n.tracked = &trackedPage{}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", inset(titleBar("Notifications", m.repo+" · retained offline records", w)))
	fmt.Fprintf(&b, "%s\n\n", inset(mutedText("Tracked comments update on ledger refresh. v marks viewed; d dismisses. Opening an item fetches current details.")))
	if n.busy {
		return b.String() + inset("Reading retained notifications…")
	}
	if n.problem != "" {
		return b.String() + inset(n.problem)
	}
	choices := n.choices()
	selected := n.selected
	if selected >= len(choices) {
		selected = maxInt(len(choices)-1, 0)
	}
	heading := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(currentTheme.Accent))
	fmt.Fprintf(&b, "%s\n", inset(heading.Render("Needs attention")))
	choiceIndex, selectedLine := 0, 0
	card := func(label, summary string, mark cardMark) {
		if selected == choiceIndex {
			selectedLine = strings.Count(b.String(), "\n")
		}
		fmt.Fprintf(&b, "%s\n", markedCard(label, summary, mark, selected == choiceIndex, m.cardWidth()))
		choiceIndex++
	}
	hasNeeds := false
	for _, choice := range choices {
		if n.choiceNeeds(choice) {
			hasNeeds = true
			break
		}
	}
	if !hasNeeds {
		fmt.Fprintf(&b, "%s\n", inset(mutedText("Nothing needs attention.")))
	}
	for _, choice := range choices {
		if n.choiceNeeds(choice) {
			renderNotificationChoice(n, choice, card)
		}
	}
	fmt.Fprintf(&b, "\n%s\n", inset(heading.Render("Past actions")))
	if n.tracked.Total == 0 {
		fmt.Fprintf(&b, "%s\n", inset(mutedText("Press w on an issue or PR to track its comments.")))
	}
	hasPast := false
	for _, choice := range choices {
		if !n.choiceNeeds(choice) {
			hasPast = true
			renderNotificationChoice(n, choice, card)
		}
	}
	if !hasPast {
		fmt.Fprintf(&b, "%s\n", inset(mutedText("No past actions yet.")))
	}
	vp := viewport.New(viewport.WithWidth(m.cardWidth()), viewport.WithHeight(m.mainHeight()))
	vp.SetContent(b.String())
	vp.SetYOffset(maxInt(0, selectedLine-m.mainHeight()/2))
	return vp.View()
}

func renderNotificationChoice(n notificationsUI, choice notificationChoice, card func(string, string, cardMark)) {
	switch choice.kind {
	case "tracked":
		row := n.tracked.Rows[choice.row]
		key := row.key()
		summary := fmt.Sprintf("last checked %s", row.CheckedAt)
		mark := cardMark{}
		if row.NewCount > 0 {
			summary = fmt.Sprintf("%d new comment(s) · %s", row.NewCount, summary)
			mark = cardMark{text: "NEW", color: currentTheme.Info}
		}
		if row.Error != nil {
			summary += " · check incomplete"
		}
		card(fmt.Sprintf("%s #%d: %s", strings.ToUpper(key.Kind), key.Number, singleLine(row.Title)), summary, mark)
	case "attention":
		row := n.attention.Rows[choice.row]
		label := row.Label
		if !row.Selectable {
			label += " · unavailable"
		}
		card(label, notificationAttentionSummary(row), cardMark{text: "PR", color: currentTheme.Warning})
	case "closure":
		row := n.closures.Rows[choice.row]
		label := row.Label
		if !row.Selectable {
			label += " · unavailable"
		}
		card(label, "Imported explanation · operation and provenance unverified", cardMark{text: "PR", color: currentTheme.Info})
	case "tracked-prev", "attention-prev", "closure-prev":
		card("Previous saved items", "Show the preceding saved page", cardMark{})
	case "tracked-more", "attention-more", "closure-more":
		card("More saved items", "Show the next saved page here", cardMark{})
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
