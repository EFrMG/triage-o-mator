package main

import (
	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Duplicate candidates come from bin/similar, loaded once per item when it is opened and cached for the session.
// The Duplicates screen compares one item, the top card, with its candidates: the top one is the original that stays, and m marks the selected candidate below as a duplicate of it. Both cards carry their dates, since the older item is usually the one to keep, and marking a candidate older than the original says so.
// Like Groups, the screen never writes anything itself: marking a duplicate only prefills the candidate's decision form, and grouping goes through bin/group.

type dupCandidate struct {
	Number int     `json:"number"`
	Kind   string  `json:"kind"`
	Title  string  `json:"title"`
	Score  float64 `json:"score"`
}

func (c dupCandidate) Key() Key { return Key{Kind: c.Kind, Number: c.Number} }

// sourceRow is the cursor position of the compared item's own card, above the candidates.
const sourceRow = -1

type dupUI struct {
	open, busy bool
	source     Key
	// selected is the candidate under the cursor, or sourceRow (-1) for the card of the item being compared, which sits above them.
	selected     int
	checked      map[Key]bool
	returnToDups bool
	// fromList is set when the screen was opened from the Possible Duplicates view, so Esc goes back there; want is the pair's other item to preselect.
	fromList bool
	want     dupCandidate
	// confirm is the key ("d") whose second press will act; any other key disarms it.
	confirm string
}

type similarLoadedMsg struct {
	repo       string // dropped if it finishes after Switch Repo
	key        Key
	candidates []dupCandidate
	err        error
}

func similarCmd(root, repo string, key Key) tea.Cmd {
	return func() tea.Msg {
		out, err := runScript(root, "similar", "--kind", key.Kind, "--number", strconv.Itoa(key.Number))
		if err != nil {
			return similarLoadedMsg{repo: repo, key: key, err: err}
		}
		var result struct {
			Candidates []dupCandidate `json:"candidates"`
		}
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			return similarLoadedMsg{repo: repo, key: key, err: fmt.Errorf("parsing similar output: %w", err)}
		}
		return similarLoadedMsg{repo: repo, key: key, candidates: result.Candidates}
	}
}

func (m model) onSimilarLoaded(msg similarLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.repo != m.repo {
		return m, nil
	}
	if msg.err != nil {
		if m.dups.open && m.dups.source == msg.key {
			m.dups.busy = false
			m.failErr("Couldn't find duplicates", msg.err)
		}

		return m, nil
	}

	m.similar[msg.key] = msg.candidates
	if m.dups.source == msg.key {
		m.dups.busy = false
		m.selectWantedDup()
	}

	return m, nil
}

// similarLabel summarizes the current item's candidates for the item header.
func (m model) similarLabel() string {
	candidates, ok := m.similar[m.detail.key]
	switch {
	case !ok:
		return "Possible duplicates: checking…"
	case len(candidates) == 0:
		return "Possible duplicates: none by title"
	}

	parts := make([]string, 0, 3)
	for _, c := range candidates {
		if c.Kind == m.detail.key.Kind && m.ruledOut(c.Kind, c.Number, m.detail.key.Number) {
			continue
		}

		if len(parts) == 3 {
			break
		}

		parts = append(parts, fmt.Sprintf("#%d %.0f%%", c.Number, c.Score*100))
	}

	if len(parts) == 0 {
		return "Possible duplicates: none by title after recorded exclusions"
	}

	return "Possible duplicates: " + strings.Join(parts, ", ") + " (m to compare)"
}

func (m model) openDuplicates() (tea.Model, tea.Cmd) {
	m.commitDraftIfDirty()

	return m.openDuplicatesFor(m.detail.key, false)
}

// openDuplicatesFor opens the Duplicates screen for key; fromList (m on a hovered list item) makes Esc return to that list.
func (m model) openDuplicatesFor(key Key, fromList bool) (tea.Model, tea.Cmd) {
	m.dups = dupUI{open: true, source: key, checked: map[Key]bool{}, fromList: fromList}
	if _, ok := m.similar[m.dups.source]; !ok {
		m.dups.busy = true
		return m, similarCmd(m.installRoot, m.repo, m.dups.source)
	}

	return m, nil
}

func (m model) dupCandidates() []dupCandidate { return m.similar[m.dups.source] }

func (m model) selectedDup() *dupCandidate {
	candidates := m.dupCandidates()
	if m.dups.selected < 0 || m.dups.selected >= len(candidates) {
		return nil
	}

	return &candidates[m.dups.selected]
}

// closeDuplicates leaves the screen. Opened from the Possible Duplicates view, Esc (toDetail false) goes back to that list; otherwise, and always when marking, the item being compared is put back into the detail panel, in case a candidate was opened from here. Marking then sets returnToDups, so going back from the item returns here.
func (m *model) closeDuplicates(toDetail bool) tea.Cmd {
	m.dups.open, m.dups.returnToDups = false, false
	if !toDetail && m.dups.fromList {
		m.focus = FocusList
		// A candidate opened from here may have left a draft, shown as unsaved in the list.
		m.showList()

		return nil
	}

	m.focus = FocusDetail
	if m.detail.key == m.dups.source {
		return nil
	}

	m.commitDraftIfDirty()
	if it, ok := m.findItem(m.dups.source); ok {
		return m.openItem(it)
	}

	return nil
}

func (m model) handleDupKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Saving can warn here as it does in the item (untouched defaults, no reason); any other key takes the warning back.
	if m.confirmSave && !key.Matches(msg, keys.Save, keys.SaveApprove) {
		m.confirmSave = false
		m.status = ""
	}

	if m.dups.confirm != "" && !key.Matches(msg, keys.Delete) {
		m.dups.confirm = ""
		m.status = ""
	}

	switch {
	case key.Matches(msg, keys.Back):
		cmd := m.closeDuplicates(false)

		return m, cmd
	case key.Matches(msg, keys.Quit):
		return m.requestQuit()
	case key.Matches(msg, keys.Help):
		m.showHelp = !m.showHelp

		return m, nil
	case key.Matches(msg, keys.Theme):
		m.openThemePicker()

		return m, nil
	}

	if m.dups.busy {
		return m, nil
	}

	c := m.selectedDup()
	last := len(m.dupCandidates()) - 1
	switch {
	case key.Matches(msg, keys.Down):
		m.dups.selected = minInt(m.dups.selected+1, last)
	case key.Matches(msg, keys.Up):
		m.dups.selected = maxInt(m.dups.selected-1, sourceRow)
	case key.Matches(msg, keys.Top):
		m.dups.selected = sourceRow
	case key.Matches(msg, keys.Bottom):
		m.dups.selected = maxInt(last, sourceRow)
	case key.Matches(msg, keys.Tick):
		if c == nil {
			m.status = "This is the item being compared; it's always part of the group."

			return m, nil
		}

		m.dups.checked[c.Key()] = !m.dups.checked[c.Key()]
	case key.Matches(msg, keys.Enter), key.Matches(msg, keys.Forward):
		// Enter reads whichever card is under the cursor, the compared item included, and Esc comes back here.
		return m.openFromDuplicates(m.selectedDupKey())
	case key.Matches(msg, keys.Save):
		return m.saveFromDuplicates()
	case key.Matches(msg, keys.SaveApprove):
		return m.saveDuplicateDecision(true)
	case key.Matches(msg, keys.Open):
		if it, ok := m.findItem(m.selectedDupKey()); ok {
			m.status = "Opening " + it.URL + "…"

			return m, openURLCmd(it.URL)
		}
	case key.Matches(msg, keys.Reopen), key.Matches(msg, keys.ReopenEditor):
		var selectedKeys []Key
		for _, candidate := range m.dupCandidates() {
			if m.dups.checked[candidate.Key()] {
				selectedKeys = append(selectedKeys, candidate.Key())
			}
		}
		if len(selectedKeys) == 0 {
			selectedKeys = []Key{m.selectedDupKey()}
		}
		var items []Item
		for _, k := range selectedKeys {
			it, ok := m.findItem(k)
			if !ok {
				m.warn("A selected duplicate candidate is missing from the ledger; refresh first.")
				return m, nil
			}
			items = append(items, it)
		}
		if key.Matches(msg, keys.ReopenEditor) {
			return m.openExternalReopen(items)
		}
		return m.openReopen(items)
	case key.Matches(msg, keys.MarkDup):
		if c == nil {
			m.status = "This is the original: pick a candidate below to mark it a duplicate of this one."

			return m, nil
		}

		return m.markCandidateDuplicate(*c)
	case key.Matches(msg, keys.Delete):
		if c == nil {
			m.status = "This is the original: d rules out a candidate below as a duplicate of it."

			return m, nil
		}

		return m.requestRuleOutCandidate(*c)
	case key.Matches(msg, keys.SwapDup):
		if c == nil {
			m.status = "This is already the original: move to a candidate to compare the other way round."

			return m, nil
		}

		return m.swapDupOriginal(*c)
	case key.Matches(msg, keys.Group):
		// b means "put into a group": here, the item and the ticked candidates (or the selected one) into a new group.
		members := m.checkedDups()
		if len(members) == 0 && c != nil {
			members = []dupCandidate{*c}
		}

		if len(members) == 0 {
			m.status = "Nothing to group yet: tick candidates with Space, or move to the one to group."

			return m, nil
		}

		title := ""
		if it, ok := m.findItem(m.dups.source); ok {
			title = it.Title
		}

		m.dups.busy = true
		m.groups.busy = true
		m.status = "Creating duplicate-review group…"

		return m, dupGroupCmd(m.installRoot, m.dups.source, title, members, m.reviewer)
	}

	return m, nil
}

// markCandidateDuplicate prefills the candidate's own decision as a duplicate of the item on the top card, and opens it so the confidence and reason can be checked. Nothing is written until s or S, here or on the item.
func (m model) markCandidateDuplicate(c dupCandidate) (tea.Model, tea.Cmd) {
	original, ok := m.findItem(m.dups.source)
	if !ok {
		m.status = "The item being compared is missing from the ledger; r fetches."

		return m, nil
	}

	next, cmd := m.openFromDuplicates(c.Key())
	m = next.(model)
	if m.detail.key != c.Key() {
		return m, cmd
	}

	if err := m.form.MarkDuplicate(original.Number, original.Title); err != nil {
		m.fail("Can't mark it as a duplicate: " + err.Error())

		return m, cmd
	}

	m.status = fmt.Sprintf("Prefilled #%d as a duplicate of #%d. Check confidence and reason, then s to save or S to save and approve.", c.Number, original.Number)
	// The older report is usually the one to keep, so say when this closes it in favour of a later one.
	if it, found := m.findItem(c.Key()); found && it.CreatedAt != "" && original.CreatedAt != "" && it.CreatedAt < original.CreatedAt {
		m.status = fmt.Sprintf("#%d (%s) is older than #%d (%s): check which one should stay. Prefilled as a duplicate anyway; Esc leaves it unsaved.", c.Number, shortDate(it.CreatedAt), original.Number, shortDate(original.CreatedAt))
	}

	return m, cmd
}

// swapDupOriginal turns the hovered candidate into the original on top, so the comparison runs the other way and m closes the item that was on top. The new original brings its own candidates (their similarity is always to the card above), with the old one selected among them.
func (m model) swapDupOriginal(c dupCandidate) (tea.Model, tea.Cmd) {
	was := m.dups.source
	old, ok := m.findItem(was)
	if !ok {
		m.status = "The item on top is missing from the ledger; r fetches."

		return m, nil
	}

	m.dups = dupUI{open: true, source: c.Key(), checked: map[Key]bool{}, fromList: m.dups.fromList, want: dupCandidate{Number: was.Number, Kind: was.Kind, Title: old.Title, Score: c.Score}}
	m.status = fmt.Sprintf("#%d is the original now: m marks a candidate, #%d included, as a duplicate of it. M swaps back.", c.Number, was.Number)
	if _, cached := m.similar[m.dups.source]; cached {
		m.selectWantedDup()

		return m, nil
	}

	m.dups.busy = true

	return m, similarCmd(m.installRoot, m.repo, m.dups.source)
}

// selectedDupKey is the item the cursor is on: a candidate, or the compared item itself on the top card.
func (m model) selectedDupKey() Key {
	if c := m.selectedDup(); c != nil {
		return c.Key()
	}

	return m.dups.source
}

// openFromDuplicates opens an item from this screen to read in full; Esc comes back to the comparison, cursor and all.
func (m model) openFromDuplicates(key Key) (tea.Model, tea.Cmd) {
	it, ok := m.findItem(key)
	if !ok {
		m.status = "Missing from the ledger; r fetches."

		return m, nil
	}

	if m.detail.key != key {
		m.commitDraftIfDirty()
	}

	cmd := m.openItem(it)
	m.focus = FocusDetail
	m.dups.open, m.dups.returnToDups = false, true

	return m, cmd
}

// saveFromDuplicates saves the decision in the form without leaving the screen, as long as it belongs to an item on it: m prefills one here, and the comparison is where you are when it's ready to save.
func (m model) saveFromDuplicates() (tea.Model, tea.Cmd) {
	return m.saveDuplicateDecision(false)
}

func (m model) saveDuplicateDecision(approve bool) (tea.Model, tea.Cmd) {
	if !m.onDupScreen(m.detail.key) {
		m.status = "Nothing to save here: m marks the selected candidate, or Enter opens an item to decide on it."

		return m, nil
	}

	return m.requestDecisionSave(approve)
}

// onDupScreen reports whether key is one of the items on this screen: the original on top, or one of its candidates.
func (m model) onDupScreen(key Key) bool {
	if key == m.dups.source {
		return true
	}

	for _, c := range m.dupCandidates() {
		if c.Key() == key {
			return true
		}
	}

	return false
}

func (m model) checkedDups() []dupCandidate {
	var out []dupCandidate
	for _, c := range m.dupCandidates() {
		if m.dups.checked[c.Key()] {
			out = append(out, c)
		}
	}

	return out
}

// dupGroupCmd creates a group holding the compared item and the chosen candidates, so the comparison can be handed off, exported, and extended with B like any other group.
func dupGroupCmd(root string, source Key, sourceTitle string, members []dupCandidate, by string) tea.Cmd {
	return func() tea.Msg {
		title := fmt.Sprintf("Possible duplicates of %s #%d", source.Kind, source.Number)
		if sourceTitle != "" {
			title += ": " + ansi.Truncate(sourceTitle, 50, "…")
		}

		out, err := runScript(root, "group", "create", "--title", title, "--description", "Candidates found by title similarity in the Duplicates screen; read each before deciding.", "--by", by)
		if err != nil {
			return groupsLoadedMsg{err: err}
		}

		var g Group
		if err := json.Unmarshal([]byte(out), &g); err != nil {
			return groupsLoadedMsg{err: err}
		}

		if _, err := runScript(root, "group", "add", g.ID, "--kind", source.Kind, "--number", strconv.Itoa(source.Number), "--notes", "Item being compared.", "--by", by); err != nil {
			return groupsLoadedMsg{err: err}
		}

		for _, c := range members {
			notes := fmt.Sprintf("Title similarity %.0f%% with %s #%d.", c.Score*100, source.Kind, source.Number)
			if _, err := runScript(root, "group", "add", g.ID, "--kind", c.Kind, "--number", strconv.Itoa(c.Number), "--notes", notes, "--by", by); err != nil {
				return groupsLoadedMsg{err: err}
			}
		}

		msg := groupsCmd(root)().(groupsLoadedMsg)
		msg.selectedID = g.ID
		msg.status = fmt.Sprintf("Created a duplicates group with %d items: b opens Groups, B adds more.", len(members)+1)

		return msg
	}
}

// dupsView compares an item with its likely duplicates: the item itself on the top card, always in view and with no similarity of its own, then a rule, then each candidate with its title similarity.
func (m model) dupsView() string {
	w, h := m.width-4, m.mainHeight()
	subtitle := "ranked by title similarity only: open both and read them before deciding; m marks one a duplicate of the top item"
	if m.dups.busy {
		subtitle = "working…"
	}

	candidates := m.dupCandidates()
	rows := []string{inset(titleBar("Possible duplicates", subtitle, w)), ""}
	sourceFirst, sourceSecond := m.dupSourceCard()
	rows = append(rows, markedCard(sourceFirst, sourceSecond, cardMark{}, m.selectedDup() == nil, w+2), cardSeparator(w))
	used := lipgloss.Height(strings.Join(rows, "\n"))
	cards := make([][2]string, len(candidates))
	for i, c := range candidates {
		first := fmt.Sprintf("%s #%d %s", c.Kind, c.Number, c.Title)
		if m.dups.checked[c.Key()] {
			first = "✓ " + first
		}

		second := fmt.Sprintf(" %3.0f%% · not in the ledger", c.Score*100)
		if it, ok := m.findItem(c.Key()); ok {
			second = fmt.Sprintf(" %3.0f%% · opened %s · %s · %s", c.Score*100, shortDate(it.CreatedAt), it.State, m.dupItemState(it))
		}

		if m.ruledOut(c.Kind, c.Number, m.dups.source.Number) {
			second += " · ruled out"
		}

		cards[i] = [2]string{first, progressBar(int(c.Score*100), 100, 10) + second}
	}

	switch {
	case len(candidates) > 0:
		rows = append(rows, cardList(cards, m.dups.selected, w+2, h-used))
	case !m.dups.busy:
		rows = append(rows, inset(mutedText("No similar titles found. Duplicates worded differently won't show up here.")))
	}

	// No panel padding, so the cards reach the borders; the other rows are inset instead.
	return m.titled(panelStyle(true).Width(w+4).Height(h+2).Render(fitScreen(strings.Join(rows, "\n"), w+2, h)), true)
}

// dupSourceCard is the top card: the original the candidates are marked duplicates of, named as such so it doesn't read as one of them, and carrying its date and decision rather than a similarity.
func (m model) dupSourceCard() (first, second string) {
	first = fmt.Sprintf("%s #%d", m.dups.source.Kind, m.dups.source.Number)
	second = "the original · not in the ledger"
	if it, ok := m.findItem(m.dups.source); ok {
		first += " " + it.Title
		second = fmt.Sprintf("the original · opened %s · %s · %s", shortDate(it.CreatedAt), it.State, m.dupItemState(it))
	}

	return first, second
}

// cardSeparator is the one-row rule between the compared item and its candidates.
func cardSeparator(width int) string {
	return inset(lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Border)).Render(strings.Repeat("─", maxInt(width, 1))))
}

// dupItemState is an item's decision on this screen, with an unsaved draft taking precedence over what the ledger holds.
func (m model) dupItemState(it Item) string {
	if _, ok := m.drafts[it.Key()]; ok {
		return "unsaved"
	}

	if m.detail.key == it.Key() && m.form.dirty {
		return "unsaved"
	}

	return dupTriage(it)
}

// dupTriage is an item's decision in a few words: untriaged, or triaged or reviewed with its category/action.
func dupTriage(it Item) string {
	switch {
	case it.Reviewed:
		return "reviewed: " + it.Category + "/" + it.Action
	case !it.Untriaged():
		return "triaged: " + it.Category + "/" + it.Action
	}

	return "untriaged"
}

// notDuplicatesMsg carries the pairs ruled out with bin/not-duplicate, so both the pairs list and a comparison can say which comparisons are settled.
type notDuplicatesMsg struct {
	repo  string
	pairs map[dupPairKey]bool
	err   error
}

// dupPairKey identifies a pair by kind and its two numbers, smaller first, as bin/not-duplicate stores them.
type dupPairKey struct {
	kind      string
	low, high int
}

func newDupPairKey(kind string, a, b int) dupPairKey {
	if a > b {
		a, b = b, a
	}

	return dupPairKey{kind: kind, low: a, high: b}
}

func notDuplicatesCmd(root, repo string) tea.Cmd {
	return func() tea.Msg {
		out, err := runScript(root, "not-duplicate", "--list")
		if err != nil {
			return notDuplicatesMsg{repo: repo, err: err}
		}

		var rows []struct {
			Kind string `json:"kind"`
			A    int    `json:"a"`
			B    int    `json:"b"`
		}
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			return notDuplicatesMsg{repo: repo, err: fmt.Errorf("parsing not-duplicate --list output: %w", err)}
		}

		pairs := make(map[dupPairKey]bool, len(rows))
		for _, row := range rows {
			pairs[newDupPairKey(row.Kind, row.A, row.B)] = true
		}

		return notDuplicatesMsg{repo: repo, pairs: pairs}
	}
}

func (m model) onNotDuplicates(msg notDuplicatesMsg) (tea.Model, tea.Cmd) {
	if msg.repo != m.repo {
		return m, nil
	}

	if msg.err != nil {
		m.failErr("Couldn't read the pairs ruled out", msg.err)

		return m, nil
	}

	m.notDuplicates = msg.pairs

	return m, nil
}

// ruledOut reports a pair a human has already judged not to be duplicates.
func (m model) ruledOut(kind string, a, b int) bool {
	return m.notDuplicates[newDupPairKey(kind, a, b)]
}

// ruleOutCmd records the verdict through bin/not-duplicate (the only writer of data/<owner>/<repo>/not-duplicates.jsonl), then re-reads both it and the pairs.
func ruleOutCmd(root, repo string, kind string, a, b int, by string) tea.Cmd {
	return func() tea.Msg {
		if _, err := runScript(root, "not-duplicate", "--key", fmt.Sprintf("%s:%d", kind, a), "--key", fmt.Sprintf("%s:%d", kind, b), "--by", by); err != nil {
			return notDuplicatesMsg{repo: repo, err: err}
		}

		return notDuplicatesCmd(root, repo)()
	}
}

// dupPair is one row of `bin/similar --pairs`: Item is the newer of two open, same-kind items with similar titles, Original the older.
type dupPair struct {
	Score    float64      `json:"score"`
	Item     dupCandidate `json:"item"`
	Original dupCandidate `json:"original"`
}

type pairsLoadedMsg struct {
	repo  string
	pairs []dupPair
	err   error
}

func pairsCmd(root, repo string) tea.Cmd {
	return func() tea.Msg {
		out, err := runScript(root, "similar", "--pairs")
		if err != nil {
			return pairsLoadedMsg{repo: repo, err: err}
		}

		var result struct {
			Pairs []dupPair `json:"pairs"`
		}

		if err := json.Unmarshal([]byte(out), &result); err != nil {
			return pairsLoadedMsg{repo: repo, err: fmt.Errorf("parsing similar --pairs output: %w", err)}
		}

		return pairsLoadedMsg{repo: repo, pairs: result.Pairs}
	}
}

// pairListItem shows a pair in the main list, the older item first since that is the one a comparison opens on. handled is set once either item is marked a duplicate or closed: the pair stays listed, marked, until the view is opened again.
type pairListItem struct {
	pair            dupPair
	newer, original Item
	handled         bool
}

func (p pairListItem) Mark() cardMark {
	if p.handled {
		return cardMark{"handled", currentTheme.Success}
	}

	return cardMark{}
}

func (p pairListItem) Title() string {
	return fmt.Sprintf("%.0f%% · %s #%d ↔ #%d · %s", p.pair.Score*100, p.original.Kind, p.original.Number, p.newer.Number, p.original.Title)
}

func (p pairListItem) Description() string {
	return fmt.Sprintf("#%d %s · %s", p.newer.Number, p.newer.Title, pairStatus(p.newer, p.original))
}

func (p pairListItem) FilterValue() string { return p.Title() }

func pairStatus(newer, original Item) string {
	status := func(it Item) string {
		if it.Untriaged() {
			return "untriaged"
		}

		return it.Category
	}

	return fmt.Sprintf("#%d %s / #%d %s", newer.Number, status(newer), original.Number, status(original))
}

func isDuplicateCategory(c string) bool { return c == "duplicate" || c == "duplicate-pr" }

// pairHandled reports a pair that no longer needs a look: either item is closed, or already marked as a duplicate.
func pairHandled(newer, original Item) bool {
	return newer.State != "open" || original.State != "open" || isDuplicateCategory(newer.Category) || isDuplicateCategory(original.Category)
}

// pairItems lists the pairs found when the view opened (onPairsLoaded keeps only those needing a look then), marking the ones handled since, so nothing vanishes from under the cursor mid-sitting.
func (m model) pairItems() []list.Item {
	var out []list.Item
	for _, p := range m.pairs {
		newer, ok1 := m.findItem(p.Item.Key())
		original, ok2 := m.findItem(p.Original.Key())
		if !ok1 || !ok2 {
			continue
		}

		out = append(out, pairListItem{pair: p, newer: newer, original: original, handled: pairHandled(newer, original)})
	}

	return out
}

// openPairCount is how many listed pairs still need a look, for the sidebar and the list title.
func (m model) openPairCount() int {
	n := 0
	for _, entry := range m.pairItems() {
		if !entry.(pairListItem).handled {
			n++
		}
	}

	return n
}

// requestRuleOutPair records the hovered pair as checked and not duplicates, on a second d: bin/not-duplicate keeps that verdict in the repo's data, so the pair stays off the list in later sessions too, for everyone who pulls it.
func (m model) requestRuleOutPair() (tea.Model, tea.Cmd) {
	hovered, ok := m.list.SelectedItem().(pairListItem)
	if !ok {
		return m, nil
	}

	if hovered.handled {
		m.listConfirm = ""
		m.status = fmt.Sprintf("#%d ↔ #%d is already decided: D clears the handled pairs from the list.", hovered.original.Number, hovered.newer.Number)

		return m, nil
	}

	if m.listConfirm != "d" {
		m.listConfirm = "d"
		m.status = fmt.Sprintf("Rule out #%d ↔ #%d as duplicates? It stays off this list for everyone, and no decision changes. Press d again.", hovered.original.Number, hovered.newer.Number)

		return m, nil
	}

	m.listConfirm = ""
	m.status = fmt.Sprintf("#%d and #%d aren't duplicates: recorded.", hovered.original.Number, hovered.newer.Number)

	return m, tea.Batch(ruleOutCmd(m.installRoot, m.repo, hovered.newer.Kind, hovered.newer.Number, hovered.original.Number, m.reviewer), pairsCmd(m.installRoot, m.repo))
}

// requestRuleOutCandidate does the same from a comparison, for the hovered candidate against the item on top.
func (m model) requestRuleOutCandidate(c dupCandidate) (tea.Model, tea.Cmd) {
	if m.ruledOut(c.Kind, c.Number, m.dups.source.Number) {
		m.dups.confirm = ""
		m.status = fmt.Sprintf("#%d and #%d are already ruled out. bin/not-duplicate --remove takes that back.", m.dups.source.Number, c.Number)

		return m, nil
	}

	if m.dups.confirm != "d" {
		m.dups.confirm = "d"
		m.status = fmt.Sprintf("Rule out #%d as a duplicate of #%d? The pair stays off Possible Duplicates for everyone. Press d again.", c.Number, m.dups.source.Number)

		return m, nil
	}

	m.dups.confirm = ""
	m.status = fmt.Sprintf("#%d and #%d aren't duplicates: recorded.", m.dups.source.Number, c.Number)

	return m, ruleOutCmd(m.installRoot, m.repo, c.Kind, c.Number, m.dups.source.Number, m.reviewer)
}

// requestClearHandledPairs drops every pair already resolved, on a second D: they stay out until the view is opened again, which recomputes them anyway.
func (m model) requestClearHandledPairs() (tea.Model, tea.Cmd) {
	var handled int
	for _, entry := range m.pairItems() {
		if entry.(pairListItem).handled {
			handled++
		}
	}

	if handled == 0 {
		m.listConfirm = ""
		m.status = "Nothing to clear: a pair counts as handled once either side is marked a duplicate or closed."

		return m, nil
	}

	if m.listConfirm != "D" {
		m.listConfirm = "D"
		m.status = fmt.Sprintf("Clear %s from the list? Nothing in the ledger changes. Press D again.", pluralize(handled, "handled pair", "handled pairs"))

		return m, nil
	}

	m.listConfirm = ""
	var kept []dupPair
	for _, p := range m.pairs {
		newer, ok1 := m.findItem(p.Item.Key())
		original, ok2 := m.findItem(p.Original.Key())
		if ok1 && ok2 && !pairHandled(newer, original) {
			kept = append(kept, p)
		}
	}

	m.pairs = kept
	m.refreshActiveList()
	m.status = fmt.Sprintf("Cleared %s.", pluralize(handled, "handled pair", "handled pairs"))

	return m, nil
}

// openPairs shows the Possible Duplicates view, recomputing pairs each time so new and renamed items are included.
func (m *model) openPairs() tea.Cmd {
	m.ticked, m.listConfirm = map[Key]bool{}, ""
	m.activeBatch = ""
	m.activePairs = true
	m.overview = false
	m.sidebar.selected = pairsIndex
	listW, _ := m.panelWidths()
	m.resetSearch()
	m.list = newItemList(nil, "Possible Duplicates", listW, m.mainHeight())
	m.listReady = true
	m.refreshActiveList()
	m.focus = FocusList
	m.status = "Finding likely duplicate pairs…"
	return pairsCmd(m.installRoot, m.repo)
}

func (m model) onPairsLoaded(msg pairsLoadedMsg) (tea.Model, tea.Cmd) {
	if msg.repo != m.repo {
		return m, nil
	}
	if msg.err != nil {
		m.failErr("Couldn't find duplicate pairs", msg.err)
		return m, nil
	}

	m.pairs = nil
	for _, p := range msg.pairs {
		newer, ok1 := m.findItem(p.Item.Key())
		original, ok2 := m.findItem(p.Original.Key())
		if ok1 && ok2 && !pairHandled(newer, original) {
			m.pairs = append(m.pairs, p)
		}
	}

	m.pairsLoaded = true
	m.refreshActiveList()
	if m.activePairs {
		m.status = fmt.Sprintf("%d pairs with similar titles. Enter compares a pair.", len(m.list.Items()))
	}

	return m, nil
}

// openPair opens the Duplicates screen on the pair's older item, the likely original, with the newer one selected as the candidate to mark; Esc returns to the pairs list.
func (m *model) openPair(p pairListItem) tea.Cmd {
	m.commitDraftIfDirty()
	cmd := m.openItem(p.original)
	m.dups = dupUI{open: true, source: p.original.Key(), checked: map[Key]bool{}, fromList: true, want: p.pair.Item}

	if _, ok := m.similar[m.dups.source]; ok {
		m.selectWantedDup()
	} else {
		m.dups.busy = true
	}

	return cmd
}

// selectWantedDup moves the Duplicates cursor to the pair's other item, adding it if the per-item candidate list (top 5 at a lower cutoff) happened not to include it.
func (m *model) selectWantedDup() {
	want := m.dups.want
	if want.Number == 0 {
		return
	}

	candidates := m.similar[m.dups.source]
	for i, c := range candidates {
		if c.Key() == want.Key() {
			m.dups.selected = i

			return
		}
	}
	m.similar[m.dups.source] = append(candidates, want)
	m.dups.selected = len(candidates)
}
