package main

import (
	"fmt"
	"slices"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// formField indexes the Tab-cycle stops within the detail panel: the enrichment content (Body/Comments/Diff, owned by detailModel) plus the four decision fields.
// fieldContent is the default on selecting an item, so Enter / H / L / j / k / gg / G immediately act on the content sections rather than being swallowed by the category picker.
type formField int

const (
	fieldContent formField = iota
	fieldLabels
	fieldAction
	fieldConfidence
	fieldReason
	fieldCount
)

// decisionForm edits proposed labels, action, confidence and reason for the selected item. Legacy category values remain visible as saved context.
type decisionForm struct {
	taxonomy Taxonomy
	repo     string
	kind     string
	focused  formField

	// A negative index preserves a blank field in an existing decision or proposal.
	actionIdx      int
	confidenceIdx  int
	proposedLabels []string
	legacyCategory string
	// The reason editor grows up to reasonMaxLines visible rows; Enter saves rather than inserting a newline.
	reason textarea.Model

	dirty bool
	saved bool // true right after a successful save, until the item changes again
	// touched is false while category/action/confidence are still the placeholder defaults LoadItem seeds for an untriaged item; saving untouched defaults needs a repeated save action.
	touched bool
	// proposed is true while the fields show a batch proposal that hasn't been saved to the ledger yet.
	proposed bool
	// proposalNotes is that proposal's agent_notes, saved along with the decision so accepting a proposal in the TUI keeps the agent's evidence.
	proposalNotes    string
	proposalBy       string
	proposalSnapshot decisionSnapshot
	// Unlisted values from the ledger or a proposal stay visible and block saving until the reviewer picks supported values.
	badAction, badConfidence string
	badLabels                []string
	// pick is the list a choice field opens on Enter.
	pick dropdown
}

const (
	reasonMaxLines = 5
	// formLabelWidth fits "confidence" plus the focus marker.
	formLabelWidth = 13
)

func newDecisionForm(tax Taxonomy, repo string) decisionForm {
	ta := textarea.New()
	ta.Placeholder = "why this action; what to ask or explain"
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.CharLimit = 0
	ta.MaxHeight = reasonMaxLines
	ta.SetHeight(1)
	themeTextarea(&ta)

	return decisionForm{taxonomy: tax, repo: repo, reason: ta}
}

func (f *decisionForm) SetTaxonomy(tax Taxonomy) {
	action, confidence := f.Action(), f.Confidence()
	f.taxonomy = tax
	f.actionIdx = indexOrZero(tax.SelectableActions(), action)
	f.confidenceIdx = indexOrZero(tax.Confidence, confidence)
	if action == "" {
		f.actionIdx = -1
	}
	if confidence == "" {
		f.confidenceIdx = -1
	}
	f.badAction = unlisted(tax.SelectableActions(), action)
	f.badConfidence = unlisted(tax.Confidence, confidence)
	f.badLabels = f.unlistedLabels()
}

// SetWidth sizes the reason to the form panel, growing its height with the wrapped text (plus a line for the cursor while typing) up to reasonMaxLines.
func (f *decisionForm) SetWidth(width int) {
	w := maxInt(width-formLabelWidth, 8)
	f.reason.SetWidth(w)
	lines := 1
	if v := f.reason.Value(); v != "" {
		// Word-wrap the way the textarea does, one column short, so a line that just fits still counts.
		lines = strings.Count(ansi.Wordwrap(v, maxInt(w-1, 1), " -"), "\n") + 1
	}

	if f.focused == fieldReason {
		lines++
	}

	f.reason.SetHeight(minInt(maxInt(lines, 1), reasonMaxLines))
}

// decisionSnapshot is an in-memory, unsaved draft of a decision for one item: lets a reviewer jump between items without losing edits made before pressing ctrl+s. See model.drafts.
type decisionSnapshot struct {
	actionIdx      int
	confidenceIdx  int
	proposedLabels []string
	legacyCategory string
	reason         string
	badLabels      []string
	badAction      string
	badConfidence  string
}

func (s decisionSnapshot) Equal(other decisionSnapshot) bool {
	return s.actionIdx == other.actionIdx && s.confidenceIdx == other.confidenceIdx && s.legacyCategory == other.legacyCategory &&
		s.reason == other.reason && s.badAction == other.badAction && s.badConfidence == other.badConfidence &&
		slices.Equal(s.proposedLabels, other.proposedLabels) && slices.Equal(s.badLabels, other.badLabels)
}

func (f decisionForm) Snapshot() decisionSnapshot {
	return decisionSnapshot{
		actionIdx:      f.actionIdx,
		confidenceIdx:  f.confidenceIdx,
		proposedLabels: slices.Clone(f.proposedLabels),
		legacyCategory: f.legacyCategory,
		reason:         f.reason.Value(),
		badLabels:      slices.Clone(f.badLabels),
		badAction:      f.badAction,
		badConfidence:  f.badConfidence,
	}
}

// ApplyDraft overrides whatever LoadItem just seeded from the ledger with an unsaved draft, and marks the form dirty so "unsaved changes" shows again.
func (f *decisionForm) ApplyDraft(s decisionSnapshot) {
	f.actionIdx = s.actionIdx
	f.confidenceIdx = s.confidenceIdx
	f.proposedLabels = slices.Clone(s.proposedLabels)
	f.legacyCategory = s.legacyCategory
	f.reason.SetValue(s.reason)
	f.badLabels, f.badAction, f.badConfidence = slices.Clone(s.badLabels), s.badAction, s.badConfidence
	f.dirty = true
	f.saved = false
	f.touched = true
}

// ApplyProposal seeds the form from a batch decisions file's proposal for an untriaged item. It counts as touched (someone chose these values) but not dirty: the proposal stays in the file, so leaving without saving loses nothing.
func (f *decisionForm) ApplyProposal(p proposal) {
	f.legacyCategory = p.Category
	f.proposedLabels = slices.Clone(p.ProposedLabels)
	f.actionIdx = indexOrZero(f.taxonomy.SelectableActions(), p.Action)
	f.confidenceIdx = indexOrZero(f.taxonomy.Confidence, p.Confidence)

	if p.Action == "" {
		f.actionIdx = -1
	}

	if p.Confidence == "" {
		f.confidenceIdx = -1
	}

	f.reason.SetValue(p.Reason)
	f.proposalNotes = p.AgentNotes
	f.badLabels, f.badAction = f.unlistedLabels(), unlisted(f.taxonomy.SelectableActions(), p.Action)
	f.badConfidence = unlisted(f.taxonomy.Confidence, p.Confidence)
	f.touched = true
	f.proposed = true
	f.proposalBy = p.ProposedBy
	f.proposalSnapshot = f.Snapshot()
}

// MarkDuplicate prefills a matching GitHub label and a close operation, leaving confidence for the reviewer to set.
func (f *decisionForm) MarkDuplicate(number int, title string) error {
	label := "duplicate"
	if f.kind == "pr" {
		label = "duplicate-pr"
	}

	act := -1
	for i, a := range f.taxonomy.SelectableActions() {
		if (a == "close" || a == "close-duplicate") && f.taxonomy.OperationFor(a) == "close" {
			act = i
		}
	}
	if act < 0 {
		for i, a := range f.taxonomy.SelectableActions() {
			if f.taxonomy.OperationFor(a) == "close" {
				act = i
				break
			}
		}
	}

	if act < 0 {
		return fmt.Errorf("config/taxonomy.json has no close action")
	}

	if slices.Contains(f.labelOptions(), label) && !slices.Contains(f.proposedLabels, label) {
		f.proposedLabels = append(f.proposedLabels, label)
	}
	f.actionIdx = act
	f.badLabels, f.badAction = f.unlistedLabels(), ""
	f.reason.SetValue(fmt.Sprintf("Duplicate of #%d (%s).", number, title))
	f.reason.CursorEnd()
	f.dirty, f.saved, f.touched, f.proposed = true, false, true, false
	f.reason.Blur()
	f.focused = fieldConfidence

	return nil
}

// LoadItem seeds the form from an existing ledger row (empty strings if untriaged).
func (f *decisionForm) LoadItem(it Item) {
	f.kind = it.Kind
	f.legacyCategory = it.Category
	f.proposedLabels = slices.Clone(it.ProposedLabels)
	f.actionIdx = indexOrZero(f.taxonomy.SelectableActions(), it.Action)
	f.confidenceIdx = indexOrZero(f.taxonomy.Confidence, it.Confidence)
	hasDecision := !it.Untriaged() || it.Confidence != "" || it.Reason != "" || it.Reviewed
	if it.Action == "" {
		f.actionIdx = -1
	}
	if hasDecision {
		if it.Confidence == "" {
			f.confidenceIdx = -1
		}
	}

	f.badLabels, f.badAction, f.badConfidence = f.unlistedLabels(), unlisted(f.taxonomy.SelectableActions(), it.Action), unlisted(f.taxonomy.Confidence, it.Confidence)
	f.reason.SetValue(it.Reason)
	f.focused = fieldContent
	f.dirty = false
	f.saved = false
	f.touched = hasDecision
	f.proposed = false
	f.proposalNotes = ""
	f.proposalBy = ""
	f.proposalSnapshot = decisionSnapshot{}
	f.pick.open = false
	f.reason.Blur()
}

// unlisted returns value if it's set but not one of options, else "".
func unlisted(options []string, value string) string {
	if value == "" {
		return ""
	}

	for _, o := range options {
		if o == value {
			return ""
		}
	}

	return value
}

// InvalidValues describes ledger or proposal values the taxonomy doesn't have, or "" if there are none.
func (f decisionForm) InvalidValues() string {
	var bad []string
	for _, label := range f.badLabels {
		bad = append(bad, fmt.Sprintf("label %q", label))
	}

	if f.badAction != "" {
		bad = append(bad, fmt.Sprintf("action %q", f.badAction))
	}

	if f.badConfidence != "" {
		bad = append(bad, fmt.Sprintf("confidence %q", f.badConfidence))
	}

	return strings.Join(bad, " and ")
}

func indexOrZero(options []string, value string) int {
	for i, o := range options {
		if o == value {
			return i
		}
	}

	return 0
}

func (f decisionForm) labelOptions() []string {
	catalog := f.taxonomy.LabelCatalog
	if catalog.Repository != f.repo || catalog.Status != "observed" {
		return nil
	}
	options := make([]string, 0, len(catalog.Labels))
	for _, label := range catalog.Labels {
		options = append(options, label.Name)
	}
	return options
}

func (f decisionForm) unlistedLabels() []string {
	options := f.labelOptions()
	if options == nil {
		return nil
	}
	var bad []string
	for _, name := range f.proposedLabels {
		if !slices.Contains(options, name) {
			bad = append(bad, name)
		}
	}
	return bad
}

func (f decisionForm) Category() string { return f.legacyCategory }

func (f decisionForm) ProposedLabels() []string { return slices.Clone(f.proposedLabels) }

func (f decisionForm) ReplaceProposedLabels() bool { return f.labelOptions() != nil }

func (f decisionForm) Action() string {
	if f.badAction != "" {
		return f.badAction
	}

	if f.actionIdx < 0 {
		return ""
	}

	if opts := f.taxonomy.SelectableActions(); len(opts) > 0 {
		return opts[f.actionIdx%len(opts)]
	}

	return ""
}

func (f *decisionForm) ClearAction() {
	if f.actionIdx < 0 && f.badAction == "" {
		return
	}

	f.actionIdx = -1
	f.badAction = ""
	f.dirty, f.saved, f.touched = true, false, true
}

func (f decisionForm) Confidence() string {
	if f.badConfidence != "" {
		return f.badConfidence
	}

	if f.confidenceIdx < 0 {
		return ""
	}

	if opts := f.taxonomy.Confidence; len(opts) > 0 {
		return opts[f.confidenceIdx%len(opts)]
	}

	return ""
}

func (f decisionForm) Reason() string { return f.reason.Value() }

func (f *decisionForm) NextField() {
	f.pick.open = false
	f.reason.Blur()
	f.focused = (f.focused + 1) % fieldCount
	if f.focused == fieldReason {
		f.reason.Focus()
	}
}

// FocusField moves the focus straight to field, e.g. the content or the first choice with h / l.
func (f *decisionForm) FocusField(field formField) {
	f.pick.open = false
	f.reason.Blur()
	f.focused = field
	if field == fieldReason {
		f.reason.Focus()
	}
}

func (f *decisionForm) PrevField() {
	f.pick.open = false
	f.reason.Blur()
	f.focused = (f.focused - 1 + fieldCount) % fieldCount
	if f.focused == fieldReason {
		f.reason.Focus()
	}
}

// options lists the focused choice field's values and the current one's index.
func (f decisionForm) options() ([]string, int) {
	switch f.focused {
	case fieldLabels:
		return f.labelOptions(), 0
	case fieldAction:
		return f.taxonomy.SelectableActions(), f.actionIdx
	case fieldConfidence:
		return f.taxonomy.Confidence, f.confidenceIdx
	}

	return nil, 0
}

// OpenPick opens the focused choice field's list.
func (f *decisionForm) OpenPick() {
	if f.focused == fieldLabels {
		if options := f.labelOptions(); len(options) > 0 {
			f.pick.OpenMulti(options, f.proposedLabels)
		}
		return
	}
	if options, current := f.options(); len(options) > 0 {
		f.pick.Open(options, current)
	}
}

// PickKey handles a key while the list is open; picking sets the value and moves to the next field.
func (f *decisionForm) PickKey(msg tea.KeyPressMsg) {
	if f.focused == fieldLabels {
		switch {
		case key.Matches(msg, keys.Tick):
			name := f.pick.options[f.pick.cursor]
			f.pick.checked[name] = !f.pick.checked[name]
			return
		case key.Matches(msg, keys.Confirm), key.Matches(msg, keys.OpenList):
			var selected []string
			for _, name := range f.pick.options {
				if f.pick.checked[name] {
					selected = append(selected, name)
				}
			}
			if !slices.Equal(selected, f.proposedLabels) {
				f.proposedLabels = selected
				f.badLabels = nil
				f.dirty, f.saved, f.touched = true, false, true
			}
			f.pick.open = false
			f.NextField()
			return
		}
		f.pick.Key(msg)
		return
	}
	if !f.pick.Key(msg) {
		return
	}

	f.CycleValue(0)
	switch f.focused {
	case fieldAction:
		f.actionIdx = f.pick.cursor
	case fieldConfidence:
		f.confidenceIdx = f.pick.cursor
	}

	f.NextField()
}

// CycleValue moves the focused enum field by delta (wrapping). No-op on reason.
func (f *decisionForm) CycleValue(delta int) {
	if f.focused == fieldLabels {
		return
	}
	options, _ := f.options()
	if len(options) == 0 {
		return
	}

	f.dirty = true
	f.saved = false
	f.touched = true
	switch f.focused {
	case fieldAction:
		f.badAction = ""
		n := len(f.taxonomy.SelectableActions())
		f.actionIdx = ((f.actionIdx+delta)%n + n) % n
	case fieldConfidence:
		f.badConfidence = ""
		n := len(f.taxonomy.Confidence)
		f.confidenceIdx = ((f.confidenceIdx+delta)%n + n) % n
	}
}

// View renders the form to width: the three choices, the reason (wrapping over up to reasonMaxLines), and its state.
func (f decisionForm) View(width int) string {
	accent := lipgloss.NewStyle().Foreground(focusedBorderColor).Bold(true)
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Muted))
	label := func(field formField, name string) string {
		text := fmt.Sprintf("  %-*s", formLabelWidth-2, name)
		if field == f.focused {
			return accent.Render(fmt.Sprintf("› %-*s", formLabelWidth-2, name))
		}

		return muted.Render(text)
	}

	value := func(field formField, bad, v string) string {
		if bad != "" {
			v = bad + lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Error)).Render(" (not in taxonomy)")
		}

		if field == f.focused {
			return accent.Render(v)
		}

		return v
	}

	var rows []string
	labels := strings.Join(f.proposedLabels, ", ")
	if labels == "" {
		labels = "none"
		if f.labelOptions() == nil {
			labels = "catalog pending · sync in Settings"
		}
	}
	if len(f.badLabels) > 0 {
		labels += " (missing from catalog)"
	}
	action := f.Action()
	if action == "" {
		action = "not assessed"
	}
	for _, field := range []struct {
		id         formField
		name       string
		bad, value string
	}{
		{fieldLabels, "labels", "", labels},
		{fieldAction, "action", f.badAction, action},
		{fieldConfidence, "confidence", f.badConfidence, f.Confidence()},
	} {
		rows = append(rows, ansi.Truncate(label(field.id, field.name)+value(field.id, field.bad, field.value), width, "…"))
		if f.pick.open && f.focused == field.id {
			for _, line := range strings.Split(f.pick.View(maxInt(width-formLabelWidth, 16)), "\n") {
				rows = append(rows, strings.Repeat(" ", formLabelWidth)+line)
			}
		}
	}

	for i, line := range strings.Split(f.reason.View(), "\n") {
		prefix := strings.Repeat(" ", formLabelWidth)
		if i == 0 {
			prefix = label(fieldReason, "reason")
		}

		rows = append(rows, prefix+line)
	}
	if f.legacyCategory != "" {
		rows = append(rows, muted.Render("  legacy category: "+sanitize(f.legacyCategory)))
	}

	// "Saved." itself goes to the status line only, not here as well.
	state := ""
	stateStyle := muted
	switch {
	case f.dirty:
		state = "unsaved changes"
		stateStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Warning))
	case f.proposed:
		state = "proposed in the batch file, not saved yet"
		stateStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Warning))
	case !f.touched:
		state = "untriaged: fields show defaults until you change them"
	}

	if state != "" {
		rows = append(rows, "")
		for _, line := range strings.Split(ansi.Wrap(state, maxInt(width, 1), ""), "\n") {
			rows = append(rows, stateStyle.Render(line))
		}
	}

	for i := range rows {
		rows[i] = ansi.Truncate(rows[i], maxInt(width, 1), "…")
	}

	return strings.Join(rows, "\n")
}
