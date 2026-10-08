package main

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type Focus int

const (
	FocusSidebar Focus = iota
	FocusList
	FocusDetail
)

type untriagedKind int

const (
	untriagedBoth untriagedKind = iota
	untriagedIssue
	untriagedPR
)

type model struct {
	notificationPR          notificationPRUI
	notifications           notificationsUI
	notificationsLifecycle  *readLifecycle
	notificationsGeneration uint64
	trackingBusy            bool
	actionHistory           actionHistoryUI
	actionHistoryLifecycle  *readLifecycle
	actionHistoryGeneration uint64
	attention               attentionUI
	attentionLifecycle      *readLifecycle
	attentionGeneration     uint64
	installRoot             string
	repo                    string
	taxonomy                Taxonomy
	contributor             string

	// pendingApply counts concurrent decision writes and undo commands; batch applies use batches.busy.
	pendingApply int
	// ledgerSeen is the ledgerStamp of the ledger last loaded, so a write from outside the TUI, such as an agent saving decisions or scores, is noticed and read.
	ledgerSeen string

	items            []Item
	groups           groupUI
	batches          batchUI
	briefs           briefsUI
	briefsLifecycle  *readLifecycle
	briefsGeneration uint64
	dups             dupUI
	// similar caches bin/similar's duplicate candidates per item for the session (titles barely change, and each lookup is a subprocess).
	similar map[Key][]dupCandidate
	// notDuplicates are the pairs a human ruled out with bin/not-duplicate, re-read whenever one is added.
	notDuplicates map[dupPairKey]bool
	themePicker   themePicker
	settings      settingsUI
	lastGroupID   string

	sidebar   sidebarModel
	list      list.Model
	listReady bool
	// listAll is every entry of the list on screen; list holds the ones matching the search (search.go), which searchInput holds, and searching is set while it's being typed.
	listAll     []list.Item
	searchInput textinput.Model
	searching   bool
	overview    bool
	// activeTab is which tabs[] entry is actually loaded into list/detail, distinct from sidebar.selected, which is just the sidebar's cursor and can point past the end of tabs (at "Switch Repo") without the displayed list changing.
	// Anything that re-reads "the current tab" (e.g. refreshActiveList after a background sync) must use this, not sidebar.selected, or it can index tabs[] out of range.
	activeTab int
	// untriagedKind and untriagedNewest are the two views of the single Untriaged tab. Both kinds and oldest-first are the defaults used for clearing the backlog.
	untriagedKind   untriagedKind
	untriagedNewest bool
	// activeBatch, when non-empty, is the data/<owner>/<repo>/batches/<id> whose items are loaded into list/detail instead of tabs[activeTab].
	activeBatch string
	// activePairs is set while the Possible Duplicates view is the displayed list; pairs holds bin/similar --pairs output (pairsLoaded once computed).
	activePairs                              bool
	pairs                                    []dupPair
	pairsLoaded                              bool
	detail                                   detailModel
	evidenceLifecycle                        *readLifecycle
	corpus                                   corpusUI
	corpusEpoch                              uint64
	corpusLifecycle, corpusObserverLifecycle *readLifecycle
	evidenceRequest                          uint64
	form                                     decisionForm
	comment                                  commentComposer

	// drafts holds unsaved decision edits per item, so switching between items to compare them before committing with ctrl+s doesn't lose work.
	// Cleared for a key once bin/apply confirms that key was saved.
	drafts map[Key]decisionSnapshot

	// editingRepo/repoInput back the "Switch Repo" sidebar action; repoAll are the repos every install on this machine has data for, repoRecent the ones the typed filter leaves, repoPick the highlighted one (-1: use the typed value), repoLastPick the one Tab returns to.
	editingRepo  bool
	repoInput    textinput.Model
	repoAll      []repoInfo
	repoRecent   []repoInfo
	repoPick     int
	repoLastPick int

	// installing backs the guided install in Switch Repo: what bin/install-to --dry-run says it would change in a repository that has no install yet, shown before anything is written.
	installing installUI

	// codeRoot is the triage-o-mator checkout this binary belongs to, where bin/install-to lives when there is no install to reach it through.
	codeRoot string

	// confirmQuit is set when quitting with unsaved drafts pending, so a second explicit quit is required rather than silently discarding them.
	confirmQuit bool
	// confirmSave arms a second save shortcut after untouched defaults or an empty reason.
	confirmSave bool

	focus Focus

	width, height int
	ready         bool

	status   string
	statusAt time.Time
	// lastError is the last failure in full, for ! (errors.go).
	lastError errorDetails
	// statusError is the last message set through fail, so it shows as an error for as long as it's on screen.
	statusError string
	// statusWarning is the last message set through warn, so it stays yellow while displayed.
	statusWarning string
	// refreshStatus is the running fetch's message, restored when a message shown on top of it expires.
	refreshStatus string
	refreshing    bool
	showHelp      bool

	// confirmSwitch arms a second Enter in Switch Repo that discards unsaved decisions and switches anyway.
	confirmSwitch bool

	// nextSteps are bin/next's suggestions for the overview (nil until loaded).
	nextSteps []nextStep
	nextErr   error

	// ticked holds the items ticked with Space in the list on screen; listConfirm is the list action whose second press will act.
	ticked                 map[Key]bool
	listConfirm            string
	lastMouseX, lastMouseY int
	lastMouseAt            time.Time
	lastMouseTarget        string
	lastMouseTargetAt      time.Time
}

func newModel(installRoot, repo string, taxonomy Taxonomy, contributor string, items []Item) model {
	repoInput := textinput.New()
	repoInput.Prompt = ""
	repoInput.Placeholder = "filter, owner/repo, or /path/to/an/install"
	repoStyles := repoInput.Styles()
	repoStyles.Focused.Placeholder = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	repoStyles.Blurred.Placeholder = repoStyles.Focused.Placeholder
	repoInput.SetStyles(repoStyles)
	repoInput.CharLimit = 200
	searchInput := textinput.New()
	searchInput.Prompt = ""
	searchInput.Placeholder = "title words or #number"
	searchInput.CharLimit = 200

	// Init starts a fetch straight away, unless there is no install to fetch for, in which case the session starts idle on the picker.
	startingFetch := ""
	if installRoot != "" {
		startingFetch = fetchStatus
	}

	m := model{
		installRoot: installRoot,
		overview:    true,
		repo:        repo,
		taxonomy:    taxonomy,
		contributor: contributor,
		items:       items,
		sidebar:     newSidebar(),
		detail:      newDetailModel(),
		form:        newDecisionForm(taxonomy, repo),
		drafts:      make(map[Key]decisionSnapshot),
		similar:     make(map[Key][]dupCandidate),
		ticked:      map[Key]bool{},
		repoInput:   repoInput,
		searchInput: searchInput,
		status:      startingFetch,
		// r during that fetch must not start a second one.
		refreshing:    startingFetch != "",
		refreshStatus: startingFetch,
		statusAt:      time.Now(),
	}
	var preferenceError error
	m.corpus.automatic, preferenceError = loadCorpusAuto(installRoot, repo)
	if preferenceError != nil {
		m.corpus.preferenceProblem = "Automatic download preference unavailable; defaulting to OFF."
		m.recordError("Automatic download preference unavailable", preferenceError)
	}
	themeTextarea(&m.form.reason)
	themeInput(&m.repoInput)
	themeInput(&m.searchInput)
	m.recomputeSidebarCounts()

	return m
}

const (
	fetchStatus     = "Fetching issues/PRs changed since the last sync…"
	fullFetchStatus = "Re-fetching every open issue/PR (full)…"
)

// installUI is the plan for one repository. A path means the plan screen is up; nothing has been written while it is.
type installUI struct {
	path   string
	solo   bool
	plan   string
	busy   bool
	offset int // the plan can be longer than the panel, and cutting it silently would defeat the point of showing it
}

// installerRoot is where bin/install-to is run from: the checkout this binary belongs to, since that is where the script lives and an install only reaches it through a symlink. It falls back to the current install for a binary that cannot find its checkout.
func (m model) installerRoot() string {
	if m.codeRoot != "" {
		return m.codeRoot
	}

	return m.installRoot
}

// noInstall is a session started outside any install: there is nothing to triage yet, so the TUI is only the Switch Repo picker until one is chosen.
func (m model) noInstall() bool {
	return m.installRoot == ""
}

// openRepoPicker opens Switch Repo on every install's repos, with the filter empty and nothing highlighted.
func (m *model) openRepoPicker() {
	m.editingRepo = true
	m.repoAll = everyKnownRepo(m.installRoot)
	m.repoRecent, m.repoPick, m.repoLastPick = m.repoAll, -1, 0
	m.repoInput.SetValue("")
	m.repoInput.Focus()
}

func (m model) Init() tea.Cmd {
	if m.noInstall() {
		return statusTick()
	}

	return tea.Batch(fetchSyncCmd(m.installRoot, m.repo, false), nextCmd(m.installRoot, m.repo), sidebarCountsCmd(m.installRoot, m.repo), notDuplicatesCmd(m.installRoot, m.repo), statusTick())
}

// enterSidebarSelection handles Enter / l while the sidebar has focus: activate a real tab, open Possible Duplicates, Batches or Groups, or open the Switch Repo prompt.
func (m *model) enterSidebarSelection() tea.Cmd {
	switch m.sidebar.selected {
	case notificationsIndex:
		m.commitDraftIfDirty()
		next, cmd := m.openNotifications()
		*m = next.(model)
		return cmd
	case settingsIndex:
		m.openSettings()
		return nil
	case pairsIndex:
		return m.openPairs()
	case batchesIndex:
		next, cmd := m.openBatches()
		*m = next.(model)

		return cmd
	case briefsIndex:
		next, cmd := m.openBriefs()
		*m = next.(model)

		return cmd
	case groupsIndex:
		next, cmd := m.openGroups()
		*m = next.(model)

		return cmd
	case switchRepoIndex:
		m.openRepoPicker()

		return nil
	}

	m.activateTab(m.sidebar.selected)
	return nil
}

func (m *model) activateTab(idx int) {
	m.ticked, m.listConfirm = map[Key]bool{}, ""
	m.activeBatch = ""
	m.activePairs = false
	m.overview = false
	m.sidebar.selected = idx
	m.activeTab = idx
	listW, _ := m.panelWidths()
	items := m.tabItems(idx)
	m.resetSearch()
	m.list = newItemList(items, m.tabName(idx), listW, m.mainHeight())
	m.listAll = m.list.Items()

	m.listReady = true
	m.focus = FocusList
}

func (m *model) refreshActiveList() {
	if m.pairsLoaded {
		// Pairs are recomputed only when the view opens; as the ledger changes, resolved or closed pairs stay listed but marked handled, and leave the count.
		m.sidebar.pairCount = m.openPairCount()
	}

	if !m.listReady {
		return
	}

	if m.activePairs {
		entries := m.pairItems()
		m.setListEntries(entries)
		m.list.Title = fmt.Sprintf("Possible Duplicates (%d)", m.sidebar.pairCount)
		if !m.pairsLoaded {
			m.list.Title = "Possible Duplicates (finding…)"
		}

		return
	}

	if m.activeBatch != "" {
		if b := m.batchByID(m.activeBatch); b != nil {
			entries, triaged := m.batchItems(*b)
			m.setListEntries(entries)
			m.list.Title = fmt.Sprintf("Batch %s (%d/%d triaged)", b.ID, triaged, len(entries))
			m.applyTicks()
		}

		return
	}

	items := m.tabItems(m.activeTab)
	m.setListEntries(toListItems(items))
	m.list.Title = fmt.Sprintf("%s (%d)", m.tabName(m.activeTab), len(items))
	m.applyTicks()
}

func (m model) tabItems(idx int) []Item {
	if idx != untriagedTab {
		return tabs[idx].Filter(m.items, m.taxonomy)
	}

	items := m.items
	switch m.untriagedKind {
	case untriagedIssue:
		items = filterKind(items, "issue")
	case untriagedPR:
		items = filterKind(items, "pr")
	}

	items = untriagedOpen(items)
	if m.untriagedNewest {
		return byCreatedAtDesc(items)
	}

	return items
}

func (m model) tabName(idx int) string {
	if idx != untriagedTab {
		return tabs[idx].Name
	}

	kind := "Both"
	switch m.untriagedKind {
	case untriagedIssue:
		kind = "Issues"
	case untriagedPR:
		kind = "PRs"
	}

	order := "Oldest"
	if m.untriagedNewest {
		order = "Newest"
	}

	return fmt.Sprintf("Untriaged · %s · %s", kind, order)
}

func (m *model) recomputeSidebarCounts() {
	for i := range tabs {
		m.sidebar.counts[i] = len(m.tabItems(i))
	}
}

func toListItems(items []Item) []list.Item {
	out := make([]list.Item, len(items))
	for i, it := range items {
		out[i] = listItem{Item: it}
	}

	return out
}

// openItem loads it into the detail panel and decision form: the ledger's decision first, then a batch proposal for an untriaged item, then any unsaved draft on top. The returned command fetches whatever the item still needs: its body/comments and its duplicate candidates.
func (m *model) openItem(it Item) tea.Cmd {
	m.confirmSave = false
	if p, ok := m.activeProposal(it.Key()); ok && it.Untriaged() && it.AgentNotes == "" {
		it.AgentNotes = p.AgentNotes
	}

	needsFetch := m.detail.SetItem(it)
	m.loadForm(it)

	var cmds []tea.Cmd
	if needsFetch {
		cmds = append(cmds, m.enrichDetailCmd(false))
	}

	if _, ok := m.similar[it.Key()]; !ok {
		cmds = append(cmds, similarCmd(m.installRoot, m.repo, it.Key()))
	}

	return tea.Batch(cmds...)
}

// loadForm fills the decision form for it: the ledger's decision, else a batch proposal for an untriaged item, then any unsaved draft on top.
func (m *model) loadForm(it Item) {
	m.form.LoadItem(it)
	if p, ok := m.activeProposal(it.Key()); ok && it.Untriaged() {
		m.form.ApplyProposal(p)
	}

	if draft, ok := m.drafts[it.Key()]; ok {
		m.form.ApplyDraft(draft)
	}
}

// Snapshots the currently-open item's in-progress edits before navigating away from it, so they can be restored later.
func (m *model) commitDraftIfDirty() {
	if m.form.dirty && len(m.detail.sections) > 0 {
		m.drafts[m.detail.key] = m.form.Snapshot()
	}
}

func (m *model) selectCurrentListItem() tea.Cmd {
	if pair, ok := m.list.SelectedItem().(pairListItem); ok {
		return m.openPair(pair)
	}

	sel, ok := m.list.SelectedItem().(listItem)
	if !ok {
		return nil
	}

	m.commitDraftIfDirty()

	cmd := m.openItem(sel.Item)
	m.focus = FocusDetail
	m.layout()

	return cmd
}

const sidebarContentWidth = 30 // fits the longest queue names plus their counts and selection marker, with room for larger counts

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// mainAreaWidth is the space left for list + detail after the sidebar box (content width + 2 border cols + 2 padding cols).
func (m model) mainAreaWidth() int {
	if m.width < 100 || m.noInstall() {
		return maxInt(m.width, 1)
	}

	return m.width - (sidebarContentWidth + 4)
}

func (m model) panelWidths() (int, int) {
	return maxInt(m.mainAreaWidth()-2, 1), maxInt(m.width-4, 1)
}

func (m model) mainHeight() int { return maxInt(m.height-lipgloss.Height(m.footerView())-3, 1) }

// The item view: a two-line header, the tab bar (labels and underline), then the active tab beside the decision form, or full screen.
const detailHeaderLines = 5

// splitMinWidth is the narrowest terminal that still fits the tab and the form side by side; below it the form goes under the content.
const splitMinWidth = 100

func (m model) detailInnerWidth() int { return maxInt(m.width-4, 1) }

func (m model) detailBodyHeight() int { return maxInt(m.mainHeight()-detailHeaderLines, 1) }

func (m model) sideBySide() bool { return !m.detail.full && m.width >= splitMinWidth }

// formPanelWidth is the decision form's share of a side-by-side item view.
func (m model) formPanelWidth() int {
	return maxInt(minInt(m.detailInnerWidth()*2/5, 64), 36)
}

func (m *model) layout() {
	m.layoutComment()
	m.layoutGroupNote()
	m.layoutGroupEdit()
	m.layoutSettingsEditor()
	m.layoutSettingsDefaults()
	m.layoutBriefs()
	listW, _ := m.panelWidths()
	if m.listReady {
		m.list.SetSize(listW, maxInt(m.mainHeight()-listHeaderHeight, 1))
	}

	m.repoInput.SetWidth(maxInt(m.mainAreaWidth()-8, 1))
	w, h := m.detailInnerWidth(), m.detailBodyHeight()
	switch {
	case m.detail.full:
		m.detail.Resize(w, h)
	case m.sideBySide():
		fw := m.formPanelWidth()
		m.form.SetWidth(fw)
		m.detail.Resize(w-fw-3, h)
	default:
		m.form.SetWidth(w)
		m.detail.Resize(w, maxInt(h-lipgloss.Height(m.formPanel(w))-1, 1))
	}
}

type scoreDimensionSpec struct {
	key, label string
	maximum    int
}

func scoreDimensions(kind string) []scoreDimensionSpec {
	if kind == "pr" {
		return []scoreDimensionSpec{{"correctness", "Correctness", 2}, {"safeguards", "Safeguards", 2}, {"reviewability", "Reviewability", 1}}
	}

	return []scoreDimensionSpec{{"clarity", "Clarity", 2}, {"support", "Support", 2}, {"actionability", "Actionability", 1}}
}

func scoreDimensionText(score *ItemScore, spec scoreDimensionSpec, next *scoreDimensionSpec) string {
	value, found := score.Dimensions[spec.key]
	if !found {
		return "No separate assessment recorded"
	}

	result := fmt.Sprintf("%d/%d", value, spec.maximum)
	prefix := fmt.Sprintf("%s %d:", spec.label, value)
	reason := singleLine(score.Reason)
	start := strings.Index(strings.ToLower(reason), strings.ToLower(prefix))
	if start < 0 {
		return result
	}
	start += len(prefix)
	end := len(reason)
	if next != nil {
		if nextValue, ok := score.Dimensions[next.key]; ok {
			nextPrefix := fmt.Sprintf("%s %d:", next.label, nextValue)
			if offset := strings.Index(strings.ToLower(reason[start:]), strings.ToLower(nextPrefix)); offset >= 0 {
				end = start + offset
			}
		}
	}

	detail := strings.TrimSpace(reason[start:end])
	// The saved reason may append a separate coverage note after the three dimension sentences; the form shows only the dimension sentence.
	if stop := strings.Index(detail, ". "); stop >= 0 {
		detail = detail[:stop+1]
	}
	if detail != "" {
		result += " · " + detail
	}

	return result
}

// formPanel is the decision form, its attribution, the current contributor and any separate score breakdown.
func (m model) formPanel(width int) string {
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Muted))
	lines := append(strings.Split(m.form.View(width), "\n"), "")
	appendMuted := func(text string) {
		for _, line := range strings.Split(ansi.Wrap(text, maxInt(width, 1), ""), "\n") {
			lines = append(lines, muted.Render(line))
		}
	}
	if it, ok := m.findItem(m.detail.key); ok && !it.Untriaged() {
		triaged := "triaged by " + orPlaceholder(it.TriagedBy, "?") + " · " + shortDate(it.TriagedAt)
		if it.BatchID != "" && it.BatchID != "tui" {
			triaged += " · " + it.BatchID
		}

		appendMuted(triaged)
	}
	if it, ok := m.findItem(m.detail.key); ok && it.ReviewRequest != nil {
		appendMuted("Pending review by " + it.ReviewRequest.By + " · " + shortDate(it.ReviewRequest.At))
		appendMuted(it.ReviewRequest.Reason)
	}
	appendMuted("you: " + m.contributor)
	if it, ok := m.findItem(m.detail.key); ok && it.ItemScore != nil {
		lines = append(lines, "")
		lines = append(lines, muted.Bold(true).Render("Score by "+singleLine(orPlaceholder(it.ItemScore.AssessedBy, "?"))), "")
		label := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Foreground)).Bold(true)
		body := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Foreground)).Bold(false)
		makeDimension := func(name, detail string) []string {
			text := label.Render(name+":") + " " + body.Render(singleLine(detail))
			return strings.Split(ansi.Wrap(text, maxInt(width, 1), ""), "\n")
		}
		var blocks [][]string
		if it.ItemScore.Value == nil {
			blocks = append(blocks, makeDimension("Unassessed", it.ItemScore.Reason))
		} else {
			dimensions := scoreDimensions(it.Kind)
			for i, dimension := range dimensions {
				var next *scoreDimensionSpec
				if i+1 < len(dimensions) {
					next = &dimensions[i+1]
				}
				blocks = append(blocks, makeDimension(dimension.label, scoreDimensionText(it.ItemScore, dimension, next)))
			}
		}
		budget := m.detailBodyHeight() - len(lines) - maxInt(len(blocks)-1, 0)
		if !m.sideBySide() {
			budget -= 2 // leave one content row and its divider above the stacked form
		}
		limits := make([]int, len(blocks))
		for i := range limits {
			limits[i] = 1
		}
		remaining := maxInt(budget-len(blocks), 0)
		for remaining > 0 {
			grew := false
			for i, block := range blocks {
				if limits[i] < len(block) {
					limits[i]++
					remaining--
					grew = true
					if remaining == 0 {
						break
					}
				}
			}
			if !grew {
				break
			}
		}
		for i, block := range blocks {
			if i > 0 {
				lines = append(lines, "")
			}
			count := minInt(limits[i], len(block))
			lines = append(lines, block[:count]...)
			if count < len(block) {
				last := len(lines) - 1
				lines[last] = ansi.Truncate(lines[last], maxInt(width-1, 1), "") + "…"
			}
		}
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "…")
	}

	return strings.Join(lines, "\n")
}

func orPlaceholder(s, placeholder string) string {
	if strings.TrimSpace(s) == "" {
		return placeholder
	}

	return s
}

// itemView is the open item: header, tabs, and the active tab beside the form (or full screen, or with the form below on narrow terminals).
func (m model) itemView() string {
	w, h := m.detailInnerWidth(), m.detailBodyHeight()
	title := fmt.Sprintf("%s #%d", m.detail.key.Kind, m.detail.key.Number)
	meta := []string{}
	scoreMark := cardMark{}
	if m.notificationPR.open {
		it := m.detail.item
		if it.Title != "" {
			title += " " + it.Title
		}
		if it.State != "" {
			stateColor := currentTheme.Muted
			if strings.EqualFold(it.State, "closed") {
				stateColor = currentTheme.Error
			}
			meta = append(meta, lipgloss.NewStyle().Foreground(lipgloss.Color(stateColor)).Render(singleLine(it.State)))
		}
	} else if it, ok := m.findItem(m.detail.key); ok {
		title += " · " + it.Title
		scoreMark = itemScoreMark(it)
		labels := "no labels"
		if len(it.Labels) > 0 {
			labels = strings.Join(it.Labels, ", ")
		}

		author := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Info)).Render(singleLine(orPlaceholder(it.Author, "?")))
		stateValue := it.State
		if !m.detail.loading && m.detail.loadErr == nil && !m.detail.blockDirect && m.detail.enriched.Evidence == nil && m.detail.enriched.State != "" {
			stateValue = m.detail.enriched.State
		}
		stateColor := currentTheme.Muted
		switch strings.ToLower(stateValue) {
		case "open":
			stateColor = currentTheme.Success
		case "closed":
			stateColor = currentTheme.Error
		}

		state := lipgloss.NewStyle().Foreground(lipgloss.Color(stateColor)).Render(singleLine(stateValue))
		meta = append(meta, author, state, mutedText(singleLine(labels)), mutedText("updated "+singleLine(shortDate(it.UpdatedAt))))
	}

	if !m.notificationPR.open {
		meta = append(meta, mutedText(singleLine(m.similarLabel())))
		if g := m.lastGroup(); g != nil {
			meta = append(meta, mutedText(singleLine(m.lastGroupLabel())))
		}
	}

	titleWidth := w
	scoreWidth := lipgloss.Width(scoreMark.text)
	showScore := scoreMark.text != "" && scoreWidth+2 < w
	if showScore {
		titleWidth -= scoreWidth + 1
	}
	itemTitle := ansi.Truncate(singleLine(title), titleWidth, "…")
	heading := lipgloss.NewStyle().Bold(true).Render(itemTitle)
	if styled, ok := styledItemHeading(itemTitle, lipgloss.Color(currentTheme.Foreground), nil); ok {
		heading = styled
	}
	if showScore {
		badge := lipgloss.NewStyle().Foreground(lipgloss.Color(scoreMark.color)).Bold(true).Render(scoreMark.text)
		heading += strings.Repeat(" ", maxInt(w-ansi.StringWidth(heading)-scoreWidth, 1)) + badge
	}
	header := heading + "\n" + ansi.Truncate(strings.Join(meta, mutedText(" · ")), w, "…")
	tabs := m.detail.TabBar(w, m.form.focused == fieldContent || m.detail.full)
	content := lipgloss.NewStyle().Width(m.detail.width).Height(m.detail.height).MaxHeight(m.detail.height).Render(m.detail.View())

	var body string
	switch {
	case m.detail.full:
		body = content
	case m.sideBySide():
		divider := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Border)).Render(strings.TrimRight(strings.Repeat("│\n", h), "\n"))
		form := lipgloss.NewStyle().Width(m.formPanelWidth()).Height(h).MaxHeight(h).Render(m.formPanel(m.formPanelWidth()))
		body = lipgloss.JoinHorizontal(lipgloss.Top, content, " ", divider, " ", form)
	default:
		rule := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Border)).Render(strings.Repeat("─", w))
		body = content + "\n" + rule + "\n" + m.formPanel(w)
	}

	return header + "\n\n" + tabs + "\n" + body
}

func (m *model) findItem(key Key) (Item, bool) {
	for _, it := range m.items {
		if it.Key() == key {
			return it, true
		}
	}

	return Item{}, false
}

var (
	focusedBorderColor = lipgloss.Color("212")
	blurredBorderColor = lipgloss.Color("240")
)

// Lip Gloss v2 includes the border in Width and Height, so panel callers pass the full outer size.
func panelStyle(focused bool) lipgloss.Style {
	color := blurredBorderColor
	if focused {
		color = focusedBorderColor
	}

	return screenStyle().Border(lipgloss.RoundedBorder()).BorderForeground(color)
}

// fitScreen is a final boundary for status/error text and tiny terminals. Reading content uses wrapped, scrollable viewports instead of relying on this boundary.
func fitScreen(s string, width, height int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > height {
		lines = lines[:maxInt(height, 0)]
	}

	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], maxInt(width, 0), "")
	}

	return strings.Join(lines, "\n")
}

func (m model) View() tea.View {
	v := tea.NewView(m.viewContent())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.ForegroundColor = lipgloss.Color(currentTheme.Foreground)
	v.BackgroundColor = lipgloss.Color(currentTheme.Background)
	return v
}

func (m model) viewContent() string {
	if !m.ready {
		return "starting…"
	}
	if m.needsResize() {
		return m.resizeView()
	}

	body := m.bodyView()
	if m.notifications.open && m.notifications.actionReview != nil && m.notifications.notesOpen {
		body = m.proposalNotesOverlay(body)
	}
	if m.groups.open && m.groups.editing == "notes" {
		body = m.groupNoteOverlay(body)
	}
	if m.groups.open && (m.groups.editing == "edit" || m.groups.editing == "new") {
		body = m.groupEditOverlay(body)
	}
	if m.settings.open && m.settings.editor != nil {
		body = m.settingsOverlay(body)
	}
	if m.settings.open && m.settings.defaults != nil {
		body = m.settingsDefaultsOverlay(body)
	}
	if m.comment.open {
		body = m.commentOverlay(body)
	}

	status := m.statusRow()
	footer := m.footerView()
	bodyHeight := maxInt(m.height-lipgloss.Height(footer)-1, 0)
	body = screenStyle().Width(m.width).Height(bodyHeight).Render(fitScreen(body, m.width, bodyHeight))

	return repaint(screenStyle().Width(m.width).Height(m.height).Render(fitScreen(body+"\n"+status+"\n"+footer, m.width, m.height)))
}

func (m model) resizeView() string {
	width, height := maxInt(m.width, 1), maxInt(m.height, 1)
	message := fmt.Sprintf("Please resize to at least 60 × %d.", m.minimumHeight())
	lines := strings.Split(ansi.Wrap(message, maxInt(width-4, 1), ""), "\n")
	style := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Muted))
	for i, line := range lines {
		lines[i] = lipgloss.PlaceHorizontal(width, lipgloss.Center, style.Render(line))
	}
	frame := lipgloss.PlaceVertical(height, lipgloss.Center, strings.Join(lines, "\n"))

	footer := m.footerView()
	footerHeight := lipgloss.Height(footer)
	if footerHeight > 0 && height-footerHeight > (height+len(lines))/2 {
		frame = lipgloss.NewCompositor(lipgloss.NewLayer(frame), lipgloss.NewLayer(footer).Y(height-footerHeight).Z(1)).Render()
	}

	return repaint(screenStyle().Width(width).Height(height).Render(fitScreen(frame, width, height)))
}

func (m model) bodyView() string {
	var body string
	switch {
	case m.lastError.open:
		body = m.errorView()
	case m.themePicker.open:
		body = m.themePickerView()
	case m.settings.open:
		body = m.withSidebar(m.settingsView(), true)
	case m.notificationPR.open:
		body = m.titled(panelStyle(true).Width(m.width).Height(m.mainHeight()+2).Padding(0, 1).Render(m.itemView()), true)
	case m.attention.open:
		body = m.withSidebar(m.attentionView(), true)
	case m.actionHistory.open:
		body = m.withSidebar(m.actionHistoryView(), true)
	case m.notifications.open:
		body = m.withSidebar(m.notificationsView(), true)
	case m.groups.open:
		body = m.groupsView()
	case m.batches.open:
		body = m.batchesView()
	case m.briefs.open:
		body = m.briefsView()
	case m.dups.open:
		body = m.dupsView()
	case m.corpus.open:
		body = m.withSidebar(m.corpusView(), true)
	case m.focus == FocusDetail:
		body = m.titled(panelStyle(true).Width(m.width).Height(m.mainHeight()+2).Padding(0, 1).Render(m.itemView()), true)
	default:
		sidebarBox := m.sidebarBox(m.focus == FocusSidebar)
		var content string
		if m.editingRepo {
			content = m.repoPromptView()
		} else if !m.listReady {
			content = lipgloss.NewStyle().Padding(0, 1).Render(m.overviewView())
		} else if len(m.list.Items()) == 0 {
			content = m.emptyListView()
		} else {
			content = m.listHeader(m.mainAreaWidth()-2) + "\n" + m.list.View()
		}

		mainView := m.titled(panelStyle(m.focus == FocusList).Width(m.mainAreaWidth()).Height(m.mainHeight()+2).Render(ansi.Wrap(content, m.mainAreaWidth()-2, "")), m.focus == FocusList)
		if m.width < 100 || m.noInstall() {
			if m.focus == FocusSidebar && !m.editingRepo && !m.overview {
				body = sidebarBox
			} else {
				body = mainView
			}
		} else {
			body = lipgloss.JoinHorizontal(lipgloss.Top, sidebarBox, mainView)
		}
	}

	return body
}

func (m model) needsResize() bool {
	return m.width < 60 || m.height < m.minimumHeight()
}

func (m model) minimumHeight() int {
	if m.comment.open || m.groups.editing == "notes" || m.groups.editing == "edit" || m.groups.editing == "new" {
		return maxInt(24, lipgloss.Height(m.footerView())+17)
	}

	return 24
}

// switchBusy gates direct reads and writes without cancellation/reply identities. Explicit evidence/corpus processes are stopped on switch and their stale replies are rejected. Unsaved drafts still require discard confirmation.
func (m model) switchBusy() string {
	if m.settings.busy || m.settingsEditorChanged() {
		return "save or discard the Settings draft first."
	}
	if m.refreshing || m.groups.busy || m.batches.busy || m.dups.busy || m.detail.loading || m.pendingApply > 0 {
		return "wait for the current fetch or save to finish."
	}

	return ""
}

// switchInstall moves the session to another install: another repository's triage-o-mator/ directory, with its own taxonomy, theme choice, ledgers and groups.
// The install's config/repo is what its scripts read, so it is pointed at the repo being opened before anything reads from it.
func (m *model) switchInstall(root, repo string) tea.Cmd {
	taxonomy, err := LoadTaxonomy(root)
	if err != nil {
		m.failErr("Couldn't read that install's taxonomy", err)

		return nil
	}

	m.installRoot, m.taxonomy = root, taxonomy
	m.form = newDecisionForm(taxonomy, repo)

	// A palette that can't be read there (a checkout that moved, say) is no reason to refuse the switch: the one already loaded stays.
	_ = loadTheme(root)

	return m.switchRepo(repo)
}

// switchRepo points the TUI at repo's own data folder, dropping every per-repo cache (ledger, content, duplicates, groups, batches), and fetches it.
func (m *model) switchRepo(repo string) tea.Cmd {
	m.briefsLifecycle.stop()
	m.briefsGeneration++
	m.briefs = briefsUI{}
	m.notificationsLifecycle.stop()
	m.notificationsGeneration++
	m.notifications = notificationsUI{}
	m.actionHistoryLifecycle.stop()
	m.actionHistoryGeneration++
	m.actionHistory = actionHistoryUI{}
	m.attentionLifecycle.stop()
	m.attentionGeneration++
	m.attention = attentionUI{}
	m.evidenceLifecycle.stop()
	m.corpusLifecycle.stop()
	m.corpusObserverLifecycle.stop()
	m.corpusEpoch++
	m.corpus = corpusUI{}
	automatic, preferenceError := loadCorpusAuto(m.installRoot, repo)
	m.corpus.automatic = automatic
	if preferenceError != nil {
		m.corpus.preferenceProblem = "Automatic download preference unavailable; defaulting to OFF."
		m.recordError("Automatic download preference unavailable", preferenceError)
	}
	items, err := LoadLedger(m.installRoot, repo)
	if err != nil {
		m.failErr("Couldn't read the ledger", err)
	}

	m.repo, m.items = repo, items
	m.drafts = make(map[Key]decisionSnapshot)
	m.ticked, m.listConfirm = map[Key]bool{}, ""
	m.nextSteps, m.nextErr = nil, nil
	m.similar = make(map[Key][]dupCandidate)
	m.notDuplicates = nil
	m.detail = newDetailModel()
	m.settings = settingsUI{}
	m.trackingBusy = false
	m.evidenceRequest++
	m.groups, m.batches, m.dups = groupUI{}, batchUI{}, dupUI{}
	m.lastGroupID, m.activeBatch = "", ""
	m.lastMouseTarget = ""
	m.activePairs, m.pairs, m.pairsLoaded = false, nil, false

	m.sidebar.pairCount, m.sidebar.batchCount, m.sidebar.groupCount, m.sidebar.notificationCount = -1, -1, -1, -1
	m.recomputeSidebarCounts()
	m.listReady, m.overview, m.focus = false, true, FocusSidebar

	m.refreshing = true
	m.status = fmt.Sprintf("Switched to %s. Fetching…", repo)
	m.refreshStatus = m.status

	return tea.Batch(fetchSyncCmd(m.installRoot, repo, false), sidebarCountsCmd(m.installRoot, repo), notDuplicatesCmd(m.installRoot, repo))
}

// emptyListView replaces bubbles/list's own empty state, which says "No items" twice (status bar and body), with the list title and one centered message.
func (m model) emptyListView() string {
	message := "Nothing here yet."
	switch {
	case m.activePairs && !m.pairsLoaded:
		message = "Finding pairs…"
	case m.searchQuery() != "" && len(m.listAll) > 0:
		message = "No titles match the search."
	}

	header := m.listHeader(m.mainAreaWidth() - 2)
	height := maxInt(m.mainHeight()-lipgloss.Height(header), 1)
	body := lipgloss.Place(maxInt(m.mainAreaWidth()-2, 1), height, lipgloss.Center, lipgloss.Center, lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Muted)).Render(message))

	return header + "\n" + body
}

func singleLine(s string) string { return strings.Join(strings.Fields(ansi.Strip(s)), " ") }
