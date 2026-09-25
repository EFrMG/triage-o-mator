package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Batches are bin/batch's working files in the current repo's data/<owner>/<repo>/batches/: <id>.items.jsonl (enriched context) and <id>.decisions.jsonl (blank template, or proposals an agent filled in).
// The TUI only reads those files directly; creating a batch shells out to bin/batch and applying its proposals shells out to bin/apply, same as every other mutation.

// proposal is one filled-in row of a batch's decisions file.
type proposal struct {
	Number     int    `json:"number"`
	Kind       string `json:"kind"`
	Category   string `json:"category"`
	Action     string `json:"action"`
	Confidence string `json:"confidence"`
	Reason     string `json:"reason"`
	AgentNotes string `json:"agent_notes"`
	ProposedBy string `json:"proposed_by"`
}

type batchRecord struct {
	ID         string
	Keys       []Key
	Enriched   map[Key]EnrichedItem
	Proposals  map[Key]proposal
	GroupTitle string
}

// batchItemLine is the subset of an items.jsonl row the TUI needs beyond EnrichedItem's fields.
type batchItemLine struct {
	EnrichedItem
	// DiffText shadows EnrichedItem's so a batch made with --diff can be told apart from one without: present (even empty) means the diff is already loaded.
	DiffText *string `json:"diff_text"`
	Group    *struct {
		Title string `json:"title"`
	} `json:"group"`
}

var (
	batchKinds  = []string{"all", "issue", "pr"}
	batchOrders = []string{"oldest", "newest"}
)

type batchUI struct {
	open, busy bool
	records    []batchRecord
	groups     []Group
	selected   int

	editing                        bool
	field                          int // 0 size, 1 kind, 2 order, 3 group
	size                           textinput.Model
	kindIdx, orderIdx, groupChoice int // groupChoice 0 = no group, i = groups[i-1]

	// confirm is the key ("A" apply, "d" delete) whose second press will act; any other key disarms it.
	confirm string
	// ticked are the batches ticked with Space, which A and d then act on.
	ticked map[string]bool
	// pick is the open list of a choice field in the new-batch form.
	pick dropdown
}

type batchesLoadedMsg struct {
	// clearTicks is set after a bulk change (items removed from a batch), so ticks on items that are gone don't linger.
	clearTicks bool
	records    []batchRecord
	groups     []Group
	selectedID string
	status     string
	err        error
}

type batchAppliedMsg struct {
	id, summary string
	err         error
}

func batchesDir(root, repo string) string { return filepath.Join(DataDir(root, repo), "batches") }

// loadBatches reads every <id>.items.jsonl in repo's batches folder, newest first, along with any filled-in proposals from its decisions file.
func loadBatches(root, repo string) ([]batchRecord, error) {
	paths, err := filepath.Glob(filepath.Join(batchesDir(root, repo), "*.items.jsonl"))
	if err != nil {
		return nil, err
	}

	sort.Sort(sort.Reverse(sort.StringSlice(paths)))
	var records []batchRecord
	for _, path := range paths {
		id := strings.TrimSuffix(filepath.Base(path), ".items.jsonl")
		rec := batchRecord{ID: id, Enriched: map[Key]EnrichedItem{}, Proposals: map[Key]proposal{}}
		if err := eachJSONLine(path, func(line []byte) error {
			var row batchItemLine
			if err := json.Unmarshal(line, &row); err != nil {
				return err
			}

			key := Key{Kind: row.Kind, Number: row.Number}
			rec.Keys = append(rec.Keys, key)
			if row.DiffText != nil {
				row.EnrichedItem.DiffText, row.EnrichedItem.DiffLoaded = *row.DiffText, true
			}

			if row.Evidence != nil {
				// Missing code in a fixed packet stays missing; opening a tab must not silently fetch different evidence.
				row.EnrichedItem.DiffLoaded = true
			}

			rec.Enriched[key] = row.EnrichedItem
			if row.Group != nil {
				rec.GroupTitle = row.Group.Title
			}

			return nil
		}); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}

		decisions := filepath.Join(batchesDir(root, repo), id+".decisions.jsonl")
		if _, err := os.Stat(decisions); err == nil {
			if err := eachJSONLine(decisions, func(line []byte) error {
				var p proposal
				if err := json.Unmarshal(line, &p); err != nil {
					return err
				}

				if p.Category != "" && p.Action != "" {
					rec.Proposals[Key{Kind: p.Kind, Number: p.Number}] = p
				}

				return nil
			}); err != nil {
				return nil, fmt.Errorf("%s: %w", decisions, err)
			}
		}

		records = append(records, rec)
	}

	return records, nil
}

func eachJSONLine(path string, fn func([]byte) error) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}

	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for lineNo := 1; scanner.Scan(); lineNo++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if err := fn([]byte(line)); err != nil {
			return fmt.Errorf("line %d: %w", lineNo, err)
		}
	}

	return scanner.Err()
}

// loadBatchesCmd re-reads batch files plus the group list (for the new-batch group filter).
func loadBatchesCmd(root, repo, selectedID, status string) tea.Cmd {
	return func() tea.Msg {
		records, err := loadBatches(root, repo)
		if err != nil {
			return batchesLoadedMsg{err: err}
		}

		var groups []Group
		if out, err := runScript(root, "group", "list"); err == nil {
			_ = json.Unmarshal([]byte(out), &groups)
		}

		return batchesLoadedMsg{records: records, groups: groups, selectedID: selectedID, status: status}
	}
}

var batchIDPattern = regexp.MustCompile(`(?m)^Batch (\S+):`)

// createBatchCmd runs bin/batch, which fetches each selected item's body and comments from GitHub (read-only), so it can take a while; the UI stays usable meanwhile.
func createBatchCmd(root, repo string, args []string) tea.Cmd {
	return func() tea.Msg {
		out, err := runScript(root, "batch", args...)
		if err != nil {
			return batchesLoadedMsg{err: err}
		}

		match := batchIDPattern.FindStringSubmatch(out)
		if match == nil {
			return loadBatchesCmd(root, repo, "", strings.TrimSpace(out))()
		}

		return loadBatchesCmd(root, repo, match[1], "Batch "+match[1]+" created.")()
	}
}

// applyBatchCmd merges batches' filled-in proposals into the ledger as agent decisions (never reviewed), one batch after another, skipping items someone already triaged.
func applyBatchCmd(root, repo string, ids ...string) tea.Cmd {
	return func() tea.Msg {
		applied, kept, warnings := 0, 0, 0
		for _, id := range ids {
			out, err := runScript(root, "apply", filepath.Join(batchesDir(root, repo), id+".decisions.jsonl"), "--only-untriaged")
			if err != nil {
				return batchAppliedMsg{id: id, err: err}
			}

			// Keep the status line to what was written and what was protected; bin/apply also lists the still-blank rows, which is noise here.
			for _, line := range strings.Split(out, "\n") {
				fields := strings.Fields(line)
				switch {
				case strings.HasPrefix(line, "Applied") && len(fields) > 1:
					n, _ := strconv.Atoi(fields[1])
					applied += n
				case strings.HasPrefix(line, "Kept") && len(fields) > 1:
					n, _ := strconv.Atoi(fields[1])
					kept += n
				case strings.HasPrefix(line, "warning:"):
					warnings++
				}
			}
		}

		summary := fmt.Sprintf("Applied %d decisions", applied)
		if len(ids) > 1 {
			summary += fmt.Sprintf(" from %d batches", len(ids))
		}

		if kept > 0 {
			summary += fmt.Sprintf(" · kept %d existing decisions", kept)
		}

		if warnings > 0 {
			summary += fmt.Sprintf(" · %d value(s) not in the taxonomy, see bin/apply --dry-run", warnings)
		}

		return batchAppliedMsg{id: ids[0], summary: summary + "."}
	}
}

// deleteBatchCmd removes a batch's working files through bin/batch --delete, then reloads the list.
func deleteBatchCmd(root, repo string, ids ...string) tea.Cmd {
	return func() tea.Msg {
		for _, id := range ids {
			if _, err := runScript(root, "batch", "--delete", id); err != nil {
				return batchesLoadedMsg{err: err}
			}
		}

		status := "Deleted batch " + ids[0] + "."
		if len(ids) > 1 {
			status = fmt.Sprintf("Deleted %d batches.", len(ids))
		}

		return loadBatchesCmd(root, repo, "", status)()
	}
}

func (m model) openBatches() (tea.Model, tea.Cmd) {
	m.batches.open = true
	m.batches.confirm = ""
	m.batches.ticked = map[string]bool{}
	if m.batches.busy {
		return m, nil
	}

	m.batches.busy = true

	return m, loadBatchesCmd(m.installRoot, m.repo, "", "Batches loaded.")
}

func (m model) onBatchesLoaded(msg batchesLoadedMsg) (tea.Model, tea.Cmd) {
	m.batches.busy = false
	if msg.err != nil {
		m.failErr("Batches failed", msg.err)

		return m, nil
	}

	oldID := ""
	if b := m.selectedBatch(); b != nil {
		oldID = b.ID
	}

	m.batches.records = msg.records
	m.batches.groups = msg.groups
	m.sidebar.batchCount = len(msg.records)
	id := msg.selectedID
	if id == "" {
		id = oldID
	}

	m.batches.selected = 0
	for i, rec := range msg.records {
		if rec.ID == id {
			m.batches.selected = i
		}
	}

	if m.activeBatch != "" && m.batchByID(m.activeBatch) == nil {
		// The batch on screen was deleted: fall back to the overview rather than keep showing a list that no longer exists.
		m.activeBatch = ""
		m.listReady = false
		m.overview = true
		if m.focus == FocusList {
			m.focus = FocusSidebar
		}
	} else if m.activeBatch != "" {
		if msg.clearTicks {
			m.clearTicks()
		}

		m.refreshActiveList()
	}

	m.status = msg.status

	return m, nil
}

func (m model) selectedBatch() *batchRecord {
	if m.batches.selected < 0 || m.batches.selected >= len(m.batches.records) {
		return nil
	}

	return &m.batches.records[m.batches.selected]
}

func (m model) batchByID(id string) *batchRecord {
	for i := range m.batches.records {
		if m.batches.records[i].ID == id {
			return &m.batches.records[i]
		}
	}

	return nil
}

// activeProposal returns the batch file's proposal for key, if the item is being viewed through a batch.
func (m model) activeProposal(key Key) (proposal, bool) {
	if b := m.batchByID(m.activeBatch); b != nil {
		p, ok := b.Proposals[key]

		return p, ok
	}

	return proposal{}, false
}

func (m *model) startBatchForm() {
	size := textinput.New()
	size.CharLimit = 3
	size.SetValue("25")
	size.CursorEnd()
	size.Focus()
	themeInput(&size)
	m.batches.size = size
	m.batches.editing = true
	m.batches.field = 0
	if m.batches.groupChoice > len(m.batches.groups) {
		m.batches.groupChoice = 0
	}
}

func (m model) batchFormArgs() ([]string, error) {
	n, err := strconv.Atoi(strings.TrimSpace(m.batches.size.Value()))
	if err != nil || n < 1 || n > 100 {
		return nil, fmt.Errorf("batch size must be a number from 1 to 100")
	}

	args := []string{strconv.Itoa(n), "--order", batchOrders[m.batches.orderIdx]}
	if kind := batchKinds[m.batches.kindIdx]; kind != "all" {
		args = append(args, "--kind", kind)
	}

	if m.batches.groupChoice > 0 {
		args = append(args, "--group", m.batches.groups[m.batches.groupChoice-1].ID)
	}

	return args, nil
}

func (m model) batchGroupLabel() string {
	if m.batches.groupChoice == 0 || m.batches.groupChoice > len(m.batches.groups) {
		return "any (no group filter)"
	}

	return m.batches.groups[m.batches.groupChoice-1].Title
}

func (m *model) cycleBatchField(delta int) {
	wrap := func(v, n int) int { return ((v+delta)%n + n) % n }
	switch m.batches.field {
	case 1:
		m.batches.kindIdx = wrap(m.batches.kindIdx, len(batchKinds))
	case 2:
		m.batches.orderIdx = wrap(m.batches.orderIdx, len(batchOrders))
	case 3:
		m.batches.groupChoice = wrap(m.batches.groupChoice, len(m.batches.groups)+1)
	}
}

func (m model) handleBatchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.batches.confirm != "" && !key.Matches(msg, keys.ApplyAll) && !key.Matches(msg, keys.Delete) {
		m.batches.confirm = ""
		m.status = ""
	}

	if m.batches.editing {
		return m.handleBatchFormKey(msg)
	}

	switch {
	case key.Matches(msg, keys.Back):
		m.batches.open = false

		return m, nil
	case key.Matches(msg, keys.Quit):
		return m.requestQuit()
	case key.Matches(msg, keys.Help):
		m.showHelp = !m.showHelp

		return m, nil
	case key.Matches(msg, keys.Theme):
		m.openThemePicker()

		return m, nil
	case key.Matches(msg, keys.Refresh), key.Matches(msg, keys.RefreshFull):
		return m.startRefresh(key.Matches(msg, keys.RefreshFull))
	}

	if m.batches.busy {
		return m, nil
	}

	b := m.selectedBatch()
	last := maxInt(len(m.batches.records)-1, 0)
	switch {
	case key.Matches(msg, keys.New):
		m.startBatchForm()
	case key.Matches(msg, keys.Down):
		m.batches.selected = minInt(m.batches.selected+1, last)
	case key.Matches(msg, keys.Up):
		m.batches.selected = maxInt(m.batches.selected-1, 0)
	case key.Matches(msg, keys.Top):
		m.batches.selected = 0
	case key.Matches(msg, keys.Bottom):
		m.batches.selected = last
	case key.Matches(msg, keys.Tick):
		if b != nil {
			m.batches.ticked[b.ID] = !m.batches.ticked[b.ID]
			m.batches.selected = minInt(m.batches.selected+1, last)
		}
	case key.Matches(msg, keys.ApplyAll):
		return m.requestApplyBatch(m.batchTargets()...)
	case key.Matches(msg, keys.Delete):
		targets := m.batchTargets()
		if len(targets) == 0 {
			return m, nil
		}

		if m.batches.confirm != "d" {
			m.batches.confirm = "d"
			pending := 0
			for _, t := range targets {
				pending += m.pendingProposals(t)
			}

			warning := ""
			if pending > 0 {
				warning = fmt.Sprintf(" %d unapplied proposals will be lost.", pending)
			}

			what := "batch " + targets[0].ID
			if len(targets) > 1 {
				what = fmt.Sprintf("%d ticked batches", len(targets))
			}

			m.status = fmt.Sprintf("Delete %s? Saved decisions stay in the ledger.%s Press d again to delete.", what, warning)

			return m, nil
		}

		ids := make([]string, len(targets))
		for i, t := range targets {
			ids[i] = t.ID
		}

		m.batches.confirm = ""
		m.batches.busy = true
		m.batches.ticked = map[string]bool{}

		return m, deleteBatchCmd(m.installRoot, m.repo, ids...)
	case key.Matches(msg, keys.Enter), key.Matches(msg, keys.Forward):
		if b != nil {
			m.openBatch(b.ID)
		}
	}

	return m, nil
}

// batchFormFields: 0 size (text), 1 kind, 2 order, 3 group (choices).
const batchFormFields = 4

// batchFieldOptions lists a choice field's values (1 kind, 2 order, 3 group) and the current one's index.
func (m model) batchFieldOptions(field int) ([]string, int) {
	switch field {
	case 1:
		return batchKinds, m.batches.kindIdx
	case 2:
		return []string{"oldest first", "newest first"}, m.batches.orderIdx
	case 3:
		options := []string{"any (no group filter)"}
		for _, g := range m.batches.groups {
			options = append(options, g.Title)
		}

		return options, m.batches.groupChoice
	}

	return nil, 0
}

// handleBatchFormKey edits the new-batch form: Tab / Enter move down the fields, and Enter on the last one creates the batch, like Ctrl-S. On a choice field j/k (or the arrows) change the value and l / → open its list; picking moves on, and on the last field creates the batch.
func (m model) handleBatchFormKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	onChoice := m.batches.field > 0
	// A choice isn't typing, so q quits there as everywhere else, list open or not.
	if onChoice && key.Matches(msg, keys.Quit) {
		return m.requestQuit()
	}

	if onChoice && m.batches.pick.open {
		if !m.batches.pick.Key(msg) {
			return m, nil
		}

		switch m.batches.field {
		case 1:
			m.batches.kindIdx = m.batches.pick.cursor
		case 2:
			m.batches.orderIdx = m.batches.pick.cursor
		case 3:
			m.batches.groupChoice = m.batches.pick.cursor
		}

		if m.batches.field == batchFormFields-1 {
			return m.createBatch()
		}

		m.batches.field++

		return m, nil
	}

	move := func(delta int) {
		m.batches.field = (m.batches.field + delta + batchFormFields) % batchFormFields
		if m.batches.field == 0 {
			m.batches.size.Focus()
		} else {
			m.batches.size.Blur()
		}
	}

	switch {
	case key.Matches(msg, keys.Cancel):
		m.batches.editing = false
	case key.Matches(msg, keys.FormSubmit), key.Matches(msg, keys.Confirm) && m.batches.field == batchFormFields-1:
		return m.createBatch()
	case onChoice && key.Matches(msg, keys.OpenList):
		m.batches.pick.Open(m.batchFieldOptions(m.batches.field))
	case onChoice && key.Matches(msg, keys.ChoiceNext):
		move(1)
	case onChoice && key.Matches(msg, keys.ChoicePrev):
		move(-1)
	case key.Matches(msg, keys.FieldNext), key.Matches(msg, keys.Confirm):
		move(1)
	case key.Matches(msg, keys.FieldPrev):
		move(-1)
	case onChoice && key.Matches(msg, keys.ValueNext):
		m.cycleBatchField(1)
	case onChoice && key.Matches(msg, keys.ValuePrev):
		m.cycleBatchField(-1)
	case !onChoice:
		var cmd tea.Cmd
		m.batches.size, cmd = m.batches.size.Update(msg)

		return m, cmd
	}

	return m, nil
}

func (m model) createBatch() (tea.Model, tea.Cmd) {
	args, err := m.batchFormArgs()
	if err != nil {
		m.fail("Can't create the batch: " + err.Error())

		return m, nil
	}

	m.batches.editing = false
	m.batches.busy = true
	m.status = fmt.Sprintf("Creating batch of up to %s items (fetching bodies and comments from GitHub)…", args[0])

	return m, createBatchCmd(m.installRoot, m.repo, args)
}

// pendingProposals counts proposals whose item is still untriaged in the ledger (the ones bin/apply --only-untriaged would write).
// requestApplyBatch applies the pending proposals of one or more batches on the second A, from the batch list (ticked batches, or the selected one) or from inside a batch.
func (m model) requestApplyBatch(targets ...batchRecord) (tea.Model, tea.Cmd) {
	if m.batches.busy || len(targets) == 0 {
		return m, nil
	}

	pending, invalid := 0, 0
	ids := make([]string, len(targets))
	for i, b := range targets {
		p, bad := m.countProposals(b)
		pending, invalid, ids[i] = pending+p, invalid+bad, b.ID
	}

	if pending == 0 {
		m.status = "No proposals to apply: the decisions file is blank, or every proposed item is already triaged."

		return m, nil
	}

	if m.batches.confirm != "A" {
		m.batches.confirm = "A"
		from := ids[0]
		if len(ids) > 1 {
			from = fmt.Sprintf("%d ticked batches", len(ids))
		}

		m.status = fmt.Sprintf("Apply %d proposals from %s as unreviewed agent decisions? Press A again to confirm.", pending, from)
		if invalid > 0 {
			m.status = fmt.Sprintf("Apply %d proposals from %s? %d use values not in the taxonomy and would be saved as-is. Press A again to confirm.", pending, from, invalid)
		}

		return m, nil
	}

	m.batches.confirm = ""
	m.batches.busy = true
	m.batches.ticked = map[string]bool{}

	return m, applyBatchCmd(m.installRoot, m.repo, ids...)
}

// batchTargets are the batches a list action applies to: the ticked ones, else the selected one.
func (m model) batchTargets() []batchRecord {
	var ticked []batchRecord
	for _, b := range m.batches.records {
		if m.batches.ticked[b.ID] {
			ticked = append(ticked, b)
		}
	}

	if len(ticked) > 0 {
		return ticked
	}

	if b := m.selectedBatch(); b != nil {
		return []batchRecord{*b}
	}

	return nil
}

func (m model) pendingProposals(b batchRecord) int {
	n, _ := m.countProposals(b)

	return n
}

// countProposals counts the proposals A would apply, and how many of those use a category or action config/taxonomy.json doesn't have (bin/apply records them as-is, with a warning).
func (m model) countProposals(b batchRecord) (pending, invalid int) {
	for key, p := range b.Proposals {
		if it, ok := m.findItem(key); ok && it.Untriaged() {
			pending++
			if unlisted(m.taxonomy.CategoriesFor(key.Kind), p.Category) != "" || unlisted(m.taxonomy.Actions, p.Action) != "" {
				invalid++
			}
		}
	}

	return pending, invalid
}

// openBatch shows a batch's items in the main list, reusing the bodies and comments bin/batch already fetched so opening an item doesn't hit GitHub again.
func (m *model) openBatch(id string) {
	b := m.batchByID(id)
	if b == nil {
		return
	}

	for key, e := range b.Enriched {
		if _, ok := m.detail.cache[key]; !ok || e.Evidence != nil {
			m.detail.cache[key] = e
		}
	}

	m.batches.open = false
	m.ticked, m.listConfirm = map[Key]bool{}, ""
	m.activeBatch = id
	m.activePairs = false
	m.overview = false
	m.sidebar.selected = batchesIndex
	listW, _ := m.panelWidths()
	m.resetSearch()
	m.list = newItemList(nil, "", listW, m.mainHeight())
	m.listReady = true
	m.refreshActiveList()
	m.focus = FocusList
}

func (m model) batchItems(b batchRecord) ([]list.Item, int) {
	var out []list.Item
	triaged := 0
	for _, key := range b.Keys {
		it, ok := m.findItem(key)
		if !ok {
			continue
		}

		if !it.Untriaged() {
			triaged++
		}

		li := listItem{Item: it}
		if p, ok := b.Proposals[key]; ok && it.Untriaged() {
			li.proposal = p.Category + "/" + p.Action
		}

		out = append(out, li)
	}

	return out, triaged
}

// batchesView is the Batches screen, beside the sidebar: the new-batch form, or the batches as cards with their progress.
func (m model) batchesView() string {
	w, h := m.menuWidth(), m.mainHeight()
	if m.batches.editing {
		return m.withSidebar(inset(m.batchFormView(w)), true)
	}

	subtitle := "no batches yet: n creates one"
	if n := len(m.batches.records); n > 0 {
		subtitle = pluralize(n, "batch", "batches")
		if t := len(m.batches.ticked); t > 0 {
			subtitle += " · " + pluralize(t, "ticked", "ticked")
		}
	}

	if m.batches.busy {
		subtitle = "working…"
	}

	cards := make([][2]string, len(m.batches.records))
	bar := minInt(24, maxInt(w/5, 8))
	for i, b := range m.batches.records {
		_, triaged := m.batchItems(b)
		first := b.ID
		if m.batches.ticked[b.ID] {
			first = "✓ " + first
		}

		if b.GroupTitle != "" {
			first += " · group: " + b.GroupTitle
		}

		second := progressBar(triaged, len(b.Keys), bar) + fmt.Sprintf(" %d/%d triaged", triaged, len(b.Keys))
		switch pending := m.pendingProposals(b); {
		case pending > 0:
			second += fmt.Sprintf(" · %d proposals to apply", pending)
		case len(b.Proposals) == 0 && triaged < len(b.Keys):
			second += " · decisions file still blank"
		}

		cards[i] = [2]string{first, second}
	}

	body := inset(titleBar("Batches", subtitle, w)) + "\n\n" + cardList(cards, m.batches.selected, m.cardWidth(), h-2)

	return m.withSidebar(body, true)
}

// batchFormView is the new-batch form, styled like the decision form: muted labels, the focused one marked, a choice field's list under it.
func (m model) batchFormView(w int) string {
	accent := lipgloss.NewStyle().Foreground(focusedBorderColor).Bold(true)
	label := func(i int, name string) string {
		if i == m.batches.field {
			return accent.Render(fmt.Sprintf("› %-8s", name))
		}

		return mutedText(fmt.Sprintf("  %-8s", name))
	}

	rows := []string{titleBar("New batch", "untriaged open items, with bodies and comments fetched from GitHub (read-only)", w), ""}
	values := []string{m.batches.size.View(), batchKinds[m.batches.kindIdx], batchOrders[m.batches.orderIdx] + " first", m.batchGroupLabel()}
	for i, name := range []string{"Size", "Kind", "Order", "Group"} {
		value := values[i]
		if i == m.batches.field && i > 0 {
			value = accent.Render(value)
		}

		rows = append(rows, label(i, name)+" "+value)
		if i == m.batches.field && m.batches.pick.open {
			for _, line := range strings.Split(m.batches.pick.View(minInt(w-11, 48)), "\n") {
				rows = append(rows, strings.Repeat(" ", 11)+line)
			}
		}
	}

	rows = append(rows, "", mutedText("25–40 is a size you can read carefully; larger batches take longer to fetch."))

	return strings.Join(rows, "\n")
}

// pluralize is "1 batch" / "3 batches".
func pluralize(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}

	return fmt.Sprintf("%d %s", n, many)
}
