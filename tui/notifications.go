package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Notifications presents saved action suggestions, proposals, tracked comments and retained activity in two sections.
type notificationsUI struct {
	open, busy       bool
	tracked          *trackedPage
	attention        *attentionPage
	closures         *actionHistoryPage
	actions          actionProposalList
	suggestions      []Item
	suggestionOffset int
	actionReview     *actionReviewUI
	ticked           map[int]bool
	review           *closureReview
	reviewBusy       bool
	reviewAll        bool
	reviewKey        string
	reviewNumbers    []int
	reviewScroll     int
	notesOpen        bool
	notesBusy        bool
	notesText        string
	notesError       string
	notesScroll      int
	notesRequest     uint64
	state            notificationState
	selected         int
	selectAfter      string
	selectItem       Key
	openActionAfter  Key
	problem          string
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
	actions    actionProposalList
	state      notificationState
	unreadKeys []Key
	err        error
}

type notificationChoice struct {
	kind                                                    string
	key                                                     Key
	actionProposal, suggestion, tracked, attention, closure int
}

func itemChoice(key Key) notificationChoice {
	return notificationChoice{kind: "item", key: key, actionProposal: -1, suggestion: -1, tracked: -1, attention: -1, closure: -1}
}

type actionProposalRow struct {
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Target    string `json:"target"`
	Action    string `json:"action"`
	Operation string `json:"operation"`
	Comment   string `json:"comment"`
	Reference *struct {
		Kind   string `json:"kind"`
		Number int    `json:"number"`
	} `json:"reference"`
	UpdatedAt          string                    `json:"updated_at"`
	HeadSHA            string                    `json:"head_sha"`
	Status             string                    `json:"status"`
	Checkpoint         string                    `json:"checkpoint"`
	DecisionQuestion   string                    `json:"decision_question"`
	DecisionResolution *actionDecisionResolution `json:"decision_resolution"`
	Rejection          *proposalRejection        `json:"rejection"`
	Reconsideration    *proposalReconsideration  `json:"reconsideration"`
	Number             int                       `json:"number"`
	Active             bool                      `json:"active"`
	Needs              bool                      `json:"needs_attention"`
	Dismissed          bool                      `json:"dismissed"`
	Inputs             proposalInputs            `json:"inputs"`
	Outcome            *struct {
		Comment struct {
			Status string `json:"status"`
			URL    string `json:"url"`
		} `json:"comment"`
		StateChange struct {
			Status string `json:"status"`
			URL    string `json:"url"`
		} `json:"state_change"`
	} `json:"outcome"`
}

type actionProposalList struct {
	Repository string              `json:"repository"`
	Rows       []actionProposalRow `json:"rows"`
	Requests   int                 `json:"requests"`
}

// closures lists the PR closure proposals, the only actions that can be ticked and reviewed as a batch.
func (l actionProposalList) closures() []actionProposalRow {
	var rows []actionProposalRow
	for _, row := range l.Rows {
		if row.Kind == "pr" && row.Operation == "close" {
			rows = append(rows, row)
		}
	}
	return rows
}

type actionDecisionResolution struct {
	By             string `json:"by"`
	At             string `json:"at"`
	Reason         string `json:"reason"`
	HeldCheckpoint string `json:"held_checkpoint"`
}

type proposalRejection struct {
	ProposalCheckpoint string `json:"proposal_checkpoint"`
	By                 string `json:"by"`
	At                 string `json:"at"`
	Reason             string `json:"reason"`
}

type proposalReconsideration struct {
	By                 string `json:"by"`
	At                 string `json:"at"`
	Reason             string `json:"reason"`
	RejectedCheckpoint string `json:"rejected_checkpoint"`
}

type proposalInputs struct {
	ContextRevision    string `json:"context_revision"`
	FeedbackCheckpoint string `json:"feedback_checkpoint"`
	ContextCheckpoint  string `json:"context_checkpoint"`
	Evidence           []struct {
		Kind       string `json:"kind"`
		Number     int    `json:"number"`
		SnapshotID string `json:"snapshot_id"`
		Components map[string]struct {
			Status string `json:"status"`
		} `json:"components"`
	} `json:"evidence"`
	EvidenceGaps []string `json:"evidence_gaps"`
}

type closureReview struct {
	Plan struct {
		Repository string              `json:"repo"`
		Operation  string              `json:"operation"`
		Proposals  []actionProposalRow `json:"proposals"`
	} `json:"plan"`
	Approval string                        `json:"approval"`
	Contexts map[int]actionProposalContext `json:"-"`
}

type closureReviewMsg struct {
	root, repo string
	generation uint64
	action     string
	all        bool
	numbers    []int
	review     closureReview
	out        string
	err        error
}

func closureSelectionArgs(all bool, numbers []int) []string {
	if all {
		return []string{"--all", "--kind", "pr", "--operation", "close"}
	}
	args := []string{}
	for _, number := range numbers {
		args = append(args, "--item", fmt.Sprintf("pr:%d", number))
	}
	return args
}

func closureReviewCmd(root, repo string, generation uint64, all bool, numbers []int) tea.Cmd {
	return func() tea.Msg {
		args := append([]string{"--expected-repo", repo, "review"}, closureSelectionArgs(all, numbers)...)
		out, err := runScript(root, "action-proposals", args...)
		msg := closureReviewMsg{root: root, repo: repo, generation: generation, action: "review", all: all, numbers: numbers, err: err}
		if err == nil {
			msg.err = json.Unmarshal([]byte(out), &msg.review)
		}
		if msg.err == nil {
			msg.review.Contexts = make(map[int]actionProposalContext, len(msg.review.Plan.Proposals))
			for _, row := range msg.review.Plan.Proposals {
				context, err := readClosureContext(root, repo, row.Number, row.Checkpoint, 0, "")
				if err != nil {
					msg.err = err
					break
				}
				if !context.Current {
					msg.err = fmt.Errorf("%s", context.Reason)
					break
				}
				msg.review.Contexts[row.Number] = context
			}
		}
		return msg
	}
}

func (m model) beginClosureReview(all bool, numbers []int) (tea.Model, tea.Cmd) {
	m.notifications.reviewBusy = true
	m.status = "Preparing exact PR closure review…"
	return m, closureReviewCmd(m.installRoot, m.repo, m.notificationsGeneration, all, numbers)
}

func closureExecuteCmd(root, repo string, generation uint64, all bool, numbers []int, approval string) tea.Cmd {
	return func() tea.Msg {
		args := append([]string{"--expected-repo", repo, "execute"}, closureSelectionArgs(all, numbers)...)
		args = append(args, "--publish", "--approve", approval)
		out, err := runScript(root, "action-proposals", args...)
		return closureReviewMsg{root: root, repo: repo, generation: generation, action: "execute", out: out, err: err}
	}
}

func (m model) finishClosureReview(msg closureReviewMsg) (tea.Model, tea.Cmd) {
	if !m.notifications.open || msg.root != m.installRoot || msg.repo != m.repo || msg.generation != m.notificationsGeneration {
		return m, nil
	}
	m.notifications.reviewBusy = false
	if msg.err != nil {
		m.failErr("PR closure proposal operation failed", msg.err)
		m.notifications.review = nil
		if msg.action == "execute" {
			return m.openNotifications()
		}
		return m, nil
	}
	if msg.action == "review" {
		if msg.review.Plan.Repository != m.repo || msg.review.Plan.Operation != "conversation-or-state-action" || msg.review.Approval == "" || len(msg.review.Plan.Proposals) == 0 {
			m.fail("Invalid action review; nothing published.")
			return m, nil
		}
		current := make(map[int]actionProposalRow)
		for _, row := range m.notifications.actions.closures() {
			current[row.Number] = row
		}
		if len(msg.review.Plan.Proposals) != len(msg.numbers) {
			m.fail("Proposal set changed; reopen Notifications before approving.")
			return m, nil
		}
		for i, row := range msg.review.Plan.Proposals {
			saved := current[row.Number]
			if row.Number != msg.numbers[i] || !saved.Active || row.Checkpoint != saved.Checkpoint || row.Target != saved.Target || row.Comment != saved.Comment {
				m.fail("Proposal changed; reopen Notifications before approving.")
				return m, nil
			}
			context, ok := msg.review.Contexts[row.Number]
			if !ok || !context.Current || context.ProposalCheckpoint != row.Checkpoint || context.ItemContext.Checkpoint != row.Inputs.ContextCheckpoint {
				m.fail("Proposal context changed; reopen Notifications before approving.")
				return m, nil
			}
		}
		reviewHost := ""
		for _, row := range msg.review.Plan.Proposals {
			target, err := validateCommentTarget(Item{Kind: "pr", Number: row.Number, URL: row.Target}, m.repo)
			if err != nil || reviewHost != "" && target.host != reviewHost {
				m.fail("PR closure targets must have valid URLs on one GitHub host.")
				return m, nil
			}
			reviewHost = target.host
		}
		m.notifications.review = &msg.review
		m.notifications.reviewAll = msg.all
		m.notifications.reviewKey = "a"
		if msg.all {
			m.notifications.reviewKey = "A"
		}
		m.notifications.reviewNumbers = msg.numbers
		m.notifications.reviewScroll = 0
		m.status = "Review every target and comment; press the same approval key again to execute."
		return m, nil
	}
	if msg.action == "execute" {
		if m.notifications.review == nil || len(m.notifications.review.Plan.Proposals) == 0 {
			m.fail("Approved closure targets are unavailable; refresh the ledger manually.")
			return m.openNotifications()
		}
		target, err := validateCommentTarget(Item{Kind: "pr", Number: m.notifications.review.Plan.Proposals[0].Number, URL: m.notifications.review.Plan.Proposals[0].Target}, m.repo)
		if err != nil {
			m.fail("Approved closure host is unavailable; refresh the ledger manually.")
			return m.openNotifications()
		}
		keys := make([]Key, 0, len(m.notifications.review.Plan.Proposals))
		for _, row := range m.notifications.review.Plan.Proposals {
			keys = append(keys, Key{Kind: "pr", Number: row.Number})
		}
		m.notifications.review = nil
		m.status = "Approved PR closures completed. Refreshing ledger…"
		next, notificationCmd := m.openNotifications()
		updated, syncCmd := next.(model).startRefreshItemsAtHost(false, target.host, keys...)
		return updated, tea.Batch(notificationCmd, syncCmd)
	}
	return m, nil
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
		out, err := runReadScript(process, root, "action-proposals", "--expected-repo", repo, "list")
		if err == nil {
			err = json.Unmarshal([]byte(out), &msg.actions)
		}
		if err != nil {
			msg.err = err
			return msg
		}
		out, err = runReadScript(process, root, "cache", "--expected-repo", repo, "notification-state")
		if err == nil {
			err = json.Unmarshal([]byte(out), &msg.state)
		}
		if err != nil {
			msg.err = err
			return msg
		}
		if msg.tracked.Repository.Name != repo || msg.tracked.Offset != trackedAt || len(msg.tracked.Rows) > notificationsPageSize {
			msg.err = fmt.Errorf("tracked item response identity or bounds mismatch")
		} else if msg.actions.Repository != repo || msg.actions.Requests != 0 {
			msg.err = fmt.Errorf("action proposal response identity mismatch")
		} else if msg.state.Repository.Name != repo || msg.state.Requests != 0 {
			msg.err = fmt.Errorf("notification state identity mismatch")
		} else if err := validateAttentionPage(msg.attention, attentionLocation{section: "list", offset: attentionAt, checkpoint: attentionCheckpoint}, repo); err != nil {
			msg.err = err
		} else {
			msg.err = validateActionHistoryPage(msg.closures, actionHistoryLocation{section: "list", offset: closureAt, checkpoint: closureCheckpoint}, repo)
		}
		if msg.err == nil {
			msg.unreadKeys, msg.err = unreadTrackedKeys(repo, msg.tracked.UnreadTotal, func(args ...string) (string, error) {
				return runReadScript(process, root, "cache", args...)
			})
		}
		return msg
	}
}

func (m model) openNotifications() (tea.Model, tea.Cmd) {
	m.notifications = notificationsUI{open: true, ticked: make(map[int]bool)}
	return m.reloadNotifications()
}

func (m model) reloadNotifications() (tea.Model, tea.Cmd) {
	if m.notificationsLifecycle == nil {
		m.notificationsLifecycle = &readLifecycle{}
	}
	m.notificationsLifecycle.stop()
	m.notificationsGeneration++
	m.notifications.busy = true
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
	m.notifications.actions = msg.actions
	m.notifications.suggestions = suggestedActions(m.items, m.taxonomy, msg.actions)
	if m.notifications.suggestionOffset >= len(m.notifications.suggestions) {
		m.notifications.suggestionOffset = maxInt((len(m.notifications.suggestions)-1)/suggestedActionPageSize*suggestedActionPageSize, 0)
	}
	m.notifications.tracked = &msg.tracked
	m.notifications.state = msg.state
	m.sidebar.notificationCount = notificationCount(msg.unreadKeys, msg.actions)
	if m.notifications.selectAfter != "" {
		for i, choice := range m.notifications.choices() {
			if choice.kind == m.notifications.selectAfter {
				m.notifications.selected = i
				break
			}
		}
		m.notifications.selectAfter = ""
	}
	if m.notifications.selectItem.Number > 0 {
		for i, choice := range m.notifications.choices() {
			if choice.kind == "item" && choice.key == m.notifications.selectItem {
				m.notifications.selected = i
				break
			}
		}
		m.notifications.selectItem = Key{}
	}
	if m.notifications.openActionAfter.Number > 0 {
		key := m.notifications.openActionAfter
		m.notifications.openActionAfter = Key{}
		if choice, ok := m.notifications.actionProposalChoice(key); ok {
			return m.openActionReview(choice)
		}
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
	case "item":
		return choice.suggestion >= 0 || choice.actionProposal >= 0 && n.actions.Rows[choice.actionProposal].Needs && n.actions.Rows[choice.actionProposal].Status != "executed" ||
			choice.tracked >= 0 && n.tracked.Rows[choice.tracked].NewCount > 0 ||
			choice.attention >= 0 && n.watchNeeds(n.attention.Rows[choice.attention]) ||
			choice.closure >= 0 && n.actionNeeds(n.closures.Rows[choice.closure])
	case "attention-prev", "attention-more", "closure-prev", "closure-more", "suggestion-prev", "suggestion-more":
		return true
	default:
		return false
	}
}

const suggestedActionPageSize = 20

func suggestedActions(items []Item, taxonomy Taxonomy, actions actionProposalList) []Item {
	covered := make(map[Key]bool)
	for _, row := range actions.Rows {
		covered[Key{Kind: row.Kind, Number: row.Number}] = true
	}

	var suggested []Item
	for _, item := range items {
		operation := taxonomy.OperationFor(item.Action)
		if covered[item.Key()] || operation != "comment" && operation != "close" && operation != "reopen" ||
			operation == "close" && item.State != "open" || operation == "reopen" && item.State != "closed" {
			continue
		}
		suggested = append(suggested, item)
	}
	return byCreatedAtAsc(suggested)
}

func (n notificationsUI) choices() []notificationChoice {
	var items []notificationChoice
	byKey := make(map[Key]int)
	add := func(key Key, source string, row int) {
		index, found := byKey[key]
		if !found {
			index = len(items)
			byKey[key] = index
			items = append(items, itemChoice(key))
		}
		switch source {
		case "action-proposal":
			items[index].actionProposal = row
		case "suggestion":
			items[index].suggestion = row
		case "tracked":
			items[index].tracked = row
		case "attention":
			items[index].attention = row
		case "closure":
			items[index].closure = row
		}
	}
	for i, row := range n.actions.Rows {
		if !row.Dismissed && (row.Status == "pending" || row.Status == "executed" || row.Status == "uncertain") {
			add(Key{Kind: row.Kind, Number: row.Number}, "action-proposal", i)
		}
	}
	end := minInt(n.suggestionOffset+suggestedActionPageSize, len(n.suggestions))
	for i := n.suggestionOffset; i < end; i++ {
		add(n.suggestions[i].Key(), "suggestion", i)
	}
	if n.tracked != nil {
		for i := range n.tracked.Rows {
			add(n.tracked.Rows[i].key(), "tracked", i)
		}
	}
	if n.attention != nil {
		for i := range n.attention.Rows {
			if _, dismissed := n.rowState("watch", n.attention.Rows[i].Number); !dismissed {
				add(Key{Kind: "pr", Number: n.attention.Rows[i].Number}, "attention", i)
			}
		}
	}
	if n.closures != nil {
		for i := range n.closures.Rows {
			if _, dismissed := n.rowState("action", n.closures.Rows[i].Number); !dismissed {
				add(Key{Kind: "pr", Number: n.closures.Rows[i].Number}, "closure", i)
			}
		}
	}
	var choices []notificationChoice
	for _, item := range items {
		if n.choiceNeeds(item) {
			choices = append(choices, item)
		}
	}
	if n.suggestionOffset > 0 {
		choices = append(choices, notificationChoice{kind: "suggestion-prev"})
	}
	if end < len(n.suggestions) {
		choices = append(choices, notificationChoice{kind: "suggestion-more"})
	}
	if n.attention != nil {
		if n.attention.Pagination.Offset > 0 {
			choices = append(choices, notificationChoice{kind: "attention-prev"})
		}
		if n.attention.Pagination.Next != nil {
			choices = append(choices, notificationChoice{kind: "attention-more"})
		}
	}
	if n.closures != nil {
		if n.closures.Pagination.Offset > 0 {
			choices = append(choices, notificationChoice{kind: "closure-prev"})
		}
		if n.closures.Pagination.Next != nil {
			choices = append(choices, notificationChoice{kind: "closure-more"})
		}
	}
	for _, item := range items {
		if !n.choiceNeeds(item) {
			choices = append(choices, item)
		}
	}
	if n.tracked != nil {
		if n.tracked.Offset > 0 {
			choices = append(choices, notificationChoice{kind: "tracked-prev"})
		}
		if n.tracked.Next != nil {
			choices = append(choices, notificationChoice{kind: "tracked-more"})
		}
	}
	return choices
}

func (n notificationsUI) actionProposalChoice(key Key) (notificationChoice, bool) {
	for _, choice := range n.choices() {
		if choice.kind == "item" && choice.key == key && choice.actionProposal >= 0 {
			return choice, true
		}
	}
	return notificationChoice{}, false
}

func (m model) openNotificationChoice(choice notificationChoice) (tea.Model, tea.Cmd) {
	if choice.actionProposal >= 0 {
		return m.openActionReview(choice)
	}

	return m.openNotificationSource(choice, "item")
}

func (m model) pageSuggestedActions(offset int) (tea.Model, tea.Cmd) {
	m.notifications.suggestionOffset = maxInt(offset, 0)
	m.notifications.selected = 0
	for i, choice := range m.notifications.choices() {
		if choice.kind == "item" && choice.suggestion == m.notifications.suggestionOffset {
			m.notifications.selected = i
			break
		}
	}
	return m, nil
}

func (m model) handleNotificationsKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.notifications.actionReview != nil {
		return m.handleActionReviewKey(msg)
	}
	if msg.String() == "f" && !m.notifications.notesOpen {
		if m.notifications.reviewBusy && m.notifications.review != nil && m.notifications.review.Approval != "" {
			return m, nil
		}
		m.notificationsLifecycle.stop()
		m.notificationsGeneration++
		m.notifications = notificationsUI{}
		m.corpus.returnToNotifications = true
		return m.openCorpus()
	}
	if m.notifications.review != nil {
		switch msg.String() {
		case "?":
			m.showHelp = !m.showHelp
			return m, nil
		case "q":
			if m.notifications.reviewBusy {
				return m, nil
			}
			m.notificationsLifecycle.stop()
			m.notificationsGeneration++
			return m.requestQuit()
		case "esc", "x", "h", "left":
			if m.notifications.reviewBusy {
				return m, nil
			}
			m.notifications.review = nil
			return m, nil
		}
		if m.notifications.reviewBusy {
			return m, nil
		}
		switch msg.String() {
		case "a", "A":
			if msg.String() != m.notifications.reviewKey {
				return m, nil
			}
			m.notifications.reviewBusy = true
			m.status = "Publishing approved comments and closing PRs…"
			return m, closureExecuteCmd(m.installRoot, m.repo, m.notificationsGeneration, m.notifications.reviewAll, m.notifications.reviewNumbers, m.notifications.review.Approval)
		case "j", "down":
			m.notifications.reviewScroll++
		case "k", "up":
			m.notifications.reviewScroll = maxInt(0, m.notifications.reviewScroll-1)
		case "ctrl+d":
			m.notifications.reviewScroll += maxInt(m.mainHeight()/2, 1)
		case "ctrl+u":
			m.notifications.reviewScroll = maxInt(0, m.notifications.reviewScroll-maxInt(m.mainHeight()/2, 1))
		}
		vp := m.closureReviewViewport()
		m.notifications.reviewScroll = vp.YOffset()
		return m, nil
	}
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
	if m.notifications.busy || m.notifications.reviewBusy || m.notifications.problem != "" {
		return m, nil
	}
	choices := m.notifications.choices()
	if m.notifications.selected >= len(choices) && len(choices) > 0 {
		m.notifications.selected = len(choices) - 1
	}
	switch msg.String() {
	case "y":
		if len(choices) > 0 && choices[m.notifications.selected].kind == "item" && choices[m.notifications.selected].actionProposal >= 0 {
			row := m.notifications.actions.Rows[choices[m.notifications.selected].actionProposal]
			text, what := m.yankActionProposal(row)
			m.status = "Taking " + what + "…"
			return m, yankCmd(m.installRoot, m.repo, what, text)
		}
		if len(choices) > 0 && choices[m.notifications.selected].kind == "item" && choices[m.notifications.selected].suggestion >= 0 {
			item := m.notifications.suggestions[choices[m.notifications.selected].suggestion]
			text, what := m.yankActionSuggestion(item)
			m.status = "Taking " + what + "…"
			return m, yankCmd(m.installRoot, m.repo, what, text)
		}
	case "e":
		if len(choices) > 0 && !m.trackingBusy {
			return m.openProposalEdit(choices[m.notifications.selected])
		}
	case "t", "i":
		if len(choices) > 0 && choices[m.notifications.selected].kind == "item" {
			choice := choices[m.notifications.selected]
			if msg.String() == "t" {
				return m.openNotificationSource(choice, "watch")
			}
			return m.openNotificationSource(choice, "action")
		}
	case "w":
		if len(choices) > 0 && choices[m.notifications.selected].kind == "item" {
			choice := choices[m.notifications.selected]
			if choice.tracked >= 0 {
				m.status = fmt.Sprintf("Already tracking %s #%d comments.", strings.ToUpper(choice.key.Kind), choice.key.Number)
				return m, nil
			}
			return m.startTracking(choice.key)
		}
	case "space":
		if len(choices) > 0 && choices[m.notifications.selected].kind == "item" && choices[m.notifications.selected].actionProposal >= 0 {
			row := m.notifications.actions.Rows[choices[m.notifications.selected].actionProposal]
			if row.Kind == "pr" && row.Operation == "close" && row.Active {
				m.notifications.ticked[row.Number] = !m.notifications.ticked[row.Number]
			}
		}
	case "a", "A":
		if m.trackingBusy || m.notifications.reviewBusy {
			break
		}
		all := msg.String() == "A"
		var numbers []int
		for _, row := range m.notifications.actions.closures() {
			if row.Active && (all || m.notifications.ticked[row.Number]) {
				numbers = append(numbers, row.Number)
			}
		}
		if len(numbers) == 1 {
			choice, ok := m.notifications.actionProposalChoice(Key{Kind: "pr", Number: numbers[0]})
			if !ok {
				m.fail("Selected PR closure changed; reopen Notifications.")
				return m, nil
			}
			return m.openActionReview(choice)
		}
		if len(numbers) > 1 {
			return m.beginClosureReview(all, numbers)
		}
		if all {
			break
		}
		if len(choices) > 0 {
			choice := choices[m.notifications.selected]
			if choice.kind == "item" && choice.suggestion >= 0 && choice.actionProposal < 0 {
				m.warn("Prepare an exact action proposal before approval; press y to copy this item for an agent.")
				return m, nil
			}
			if choice.actionProposal >= 0 {
				if m.notifications.actions.Rows[choice.actionProposal].Status == "executed" {
					m.status = "This action already ran."
					return m, nil
				}
				return m.openActionReview(choice)
			}
		}
	case "v":
		if len(choices) == 0 || m.trackingBusy {
			break
		}
		return m.changeNotificationItem(choices[m.notifications.selected], "view")
	case "d", "D":
		if len(choices) == 0 || m.trackingBusy {
			break
		}
		choice := choices[m.notifications.selected]
		if choice.kind == "item" && choice.actionProposal >= 0 && m.notifications.actions.Rows[choice.actionProposal].Status == "pending" {
			if msg.String() == "D" {
				return m.openExternalRejectionComposer(choice)
			}
			return m.openRejectionComposer(choice)
		}
		if msg.String() == "D" {
			return m, nil
		}
		return m.changeNotificationItem(choice, "dismiss")
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
		case "item":
			return m.openNotificationChoice(choice)
		case "tracked-more":
			return m.pageNotifications("tracked", *m.notifications.tracked.Next)
		case "tracked-prev":
			return m.pageNotifications("tracked", maxInt(0, m.notifications.tracked.Offset-notificationsPageSize))
		case "attention-more":
			return m.pageNotifications("attention", *m.notifications.attention.Pagination.Next)
		case "attention-prev":
			return m.pageNotifications("attention", maxInt(0, m.notifications.attention.Pagination.Offset-notificationsPageSize))
		case "closure-more":
			return m.pageNotifications("closure", *m.notifications.closures.Pagination.Next)
		case "closure-prev":
			return m.pageNotifications("closure", maxInt(0, m.notifications.closures.Pagination.Offset-notificationsPageSize))
		case "suggestion-more":
			return m.pageSuggestedActions(m.notifications.suggestionOffset + suggestedActionPageSize)
		case "suggestion-prev":
			return m.pageSuggestedActions(m.notifications.suggestionOffset - suggestedActionPageSize)
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
	if n.actionReview != nil {
		return m.actionReviewView()
	}
	if n.review != nil {
		return m.closureReviewView()
	}
	if n.tracked == nil {
		n.tracked = &trackedPage{}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", inset(titleBar("Notifications", "Actions and updates", w)))
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
		fmt.Fprintf(&b, "%s\n", inset(mutedText("No new Notifications.")))
	}
	for _, choice := range choices {
		if n.choiceNeeds(choice) {
			renderNotificationChoice(n, choice, card)
		}
	}
	fmt.Fprintf(&b, "\n%s\n", inset(heading.Render("Past actions")))
	if n.tracked.Total == 0 {
		fmt.Fprintf(&b, "%s\n", inset(mutedText("Track an issue or PR to see its comment activity here.")))
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

func (m model) closureReviewViewport() viewport.Model {
	n := m.notifications
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", inset(titleBar("Review PR closures", "", m.menuWidth())))
	styles := newProposalReviewStyles()
	textWidth := maxInt(m.menuWidth()-4, 1)
	for i, row := range n.review.Plan.Proposals {
		if i > 0 {
			fmt.Fprintln(&b)
		}
		styles.writeProposal(&b, row, proposalReviewState{context: n.proposalContext(row), position: fmt.Sprintf("%d of %d", i+1, len(n.review.Plan.Proposals))}, textWidth)
		fmt.Fprintf(&b, "\n%s\n", inset(styles.muted.Render(proposalRevisionFooter(row, textWidth))))
	}
	vp := viewport.New(viewport.WithWidth(m.cardWidth()), viewport.WithHeight(m.mainHeight()))
	vp.SetContent(b.String())
	vp.SetYOffset(n.reviewScroll)
	return vp
}

func proposalRevisionFooter(row actionProposalRow, width int) string {
	updated := sanitize(row.UpdatedAt)
	if len(updated) >= 20 && updated[10] == 'T' {
		updated = updated[:10] + " " + updated[11:16] + " UTC"
	}
	left := "Updated: " + orPlaceholder(updated, "unknown")
	right := "Head: " + orPlaceholder(sanitize(row.HeadSHA), "unknown")
	if width <= ansi.StringWidth(left)+ansi.StringWidth("Head: ")+1 {
		left = ansi.Truncate(left, maxInt(width/2, 1), "…")
	}
	right = ansi.Truncate(right, maxInt(width-ansi.StringWidth(left)-1, 1), "…")
	return left + strings.Repeat(" ", maxInt(width-ansi.StringWidth(left)-ansi.StringWidth(right), 1)) + right
}

func (m model) closureReviewView() string {
	return m.closureReviewViewport().View()
}

func proposalStatusSummary(operation, status string) string {
	if operation == "" {
		operation = "action"
	}
	name := strings.ToUpper(operation[:1]) + operation[1:]
	switch status {
	case "pending":
		return name + " proposed"
	case "executed":
		return name + " completed"
	case "uncertain":
		return name + " outcome uncertain"
	case "rejected":
		return name + " proposal rejected"
	}
	return name + " proposal"
}

func actionOperationMark(operation string) cardMark {
	switch operation {
	case "reopen":
		return cardMark{text: "Reopen", color: currentTheme.Success}
	case "comment":
		return cardMark{text: "Comment", color: currentTheme.Info}
	case "close":
		return cardMark{text: "Close", color: currentTheme.Error}
	default:
		return cardMark{text: "Action", color: currentTheme.Warning}
	}
}

func renderNotificationChoice(n notificationsUI, choice notificationChoice, card func(string, string, cardMark)) {
	switch choice.kind {
	case "item":
		prefix := fmt.Sprintf("%s #%d", strings.ToUpper(choice.key.Kind), choice.key.Number)
		title := ""
		var parts []string
		if choice.actionProposal >= 0 {
			row := n.actions.Rows[choice.actionProposal]
			if title == "" {
				title = row.Title
			}
			parts = append(parts, proposalStatusSummary(row.Operation, row.Status))
			if row.Action != "" && row.Action != row.Operation {
				parts = append(parts, "Action: "+row.Action)
			}
		}
		if choice.suggestion >= 0 {
			row := n.suggestions[choice.suggestion]
			if title == "" {
				title = row.Title
			}
			parts = append(parts, "Suggested "+row.Action+" · exact action needed")
		}
		if choice.tracked >= 0 {
			row := n.tracked.Rows[choice.tracked]
			if title == "" {
				title = row.Title
			}
			if row.NewCount > 0 {
				parts = append(parts, fmt.Sprintf("%d new comment(s)", row.NewCount))
			} else {
				parts = append(parts, "Tracking comments")
			}
			if row.Error != nil {
				parts = append(parts, "comment check incomplete")
			}
		}
		if choice.attention >= 0 {
			row := n.attention.Rows[choice.attention]
			if title == "" {
				title = strings.TrimPrefix(row.Label, prefix+": ")
				title = strings.TrimPrefix(title, prefix+" · ")
			}
			if row.Selectable {
				parts = append(parts, "Retained activity: "+notificationAttentionSummary(row))
			} else {
				parts = append(parts, "Retained activity unavailable")
			}
		}
		if choice.closure >= 0 {
			row := n.closures.Rows[choice.closure]
			if title == "" {
				title = strings.TrimPrefix(row.Label, prefix+": ")
				title = strings.TrimPrefix(title, prefix+" · ")
			}
			if row.Selectable {
				parts = append(parts, "Imported explanation")
			} else {
				parts = append(parts, "Imported explanation unavailable")
			}
		}
		label := prefix
		if title != "" {
			label += " " + singleLine(title)
		}
		if n.ticked[choice.key.Number] && choice.actionProposal >= 0 && n.actions.Rows[choice.actionProposal].Kind == "pr" && n.actions.Rows[choice.actionProposal].Operation == "close" {
			label = "✓ " + label
		}
		mark := cardMark{}
		if choice.suggestion >= 0 && choice.actionProposal < 0 && (choice.tracked < 0 || n.tracked.Rows[choice.tracked].NewCount == 0) {
			mark = cardMark{text: "SUGGESTED", color: currentTheme.Warning}
		} else if n.choiceNeeds(choice) {
			mark = cardMark{text: "NEW", color: currentTheme.Info}
		} else if choice.actionProposal >= 0 {
			mark = actionOperationMark(n.actions.Rows[choice.actionProposal].Operation)
		}
		card(label, strings.Join(parts, " · "), mark)
	case "tracked-prev", "attention-prev", "closure-prev":
		card("Previous saved items", "Show the preceding saved page", cardMark{})
	case "tracked-more", "attention-more", "closure-more":
		card("More saved items", "Show the next saved page here", cardMark{})
	case "suggestion-prev":
		card("Previous suggested actions", "Show the preceding saved suggestions", cardMark{})
	case "suggestion-more":
		card("More suggested actions", "Show the next saved suggestions", cardMark{})
	}
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
