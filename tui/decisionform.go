package main

import (
	"fmt"
	"strings"

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
	fieldCategory
	fieldAction
	fieldConfidence
	fieldReason
	fieldCount
)

// decisionForm is the category / action / confidence / reason editor for the currently-selected item.
// category / action / confidence are picked from config/taxonomy.json's exact lists (j / k cycles the value).
type decisionForm struct {
	taxonomy Taxonomy
	kind     string
	focused  formField

	// A negative index preserves a blank field in an existing decision or proposal.
	categoryIdx   int
	actionIdx     int
	confidenceIdx int
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
	badCategory, badAction, badConfidence string
	// pick is the list a choice field opens on Enter.
	pick dropdown
}

const (
	reasonMaxLines = 5
	// formLabelWidth fits "confidence" plus the focus marker.
	formLabelWidth = 13
)

func newDecisionForm(tax Taxonomy) decisionForm {
	ta := textarea.New()
	ta.Placeholder = "one sentence a human can skim"
	ta.ShowLineNumbers = false
	ta.Prompt = ""
	ta.CharLimit = 0
	ta.MaxHeight = reasonMaxLines
	ta.SetHeight(1)
	themeTextarea(&ta)

	return decisionForm{taxonomy: tax, reason: ta}
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
	categoryIdx   int
	actionIdx     int
	confidenceIdx int
	reason        string
	badCategory   string
	badAction     string
	badConfidence string
}

func (f decisionForm) Snapshot() decisionSnapshot {
	return decisionSnapshot{
		categoryIdx:   f.categoryIdx,
		actionIdx:     f.actionIdx,
		confidenceIdx: f.confidenceIdx,
		reason:        f.reason.Value(),
		badCategory:   f.badCategory,
		badAction:     f.badAction,
		badConfidence: f.badConfidence,
	}
}

// ApplyDraft overrides whatever LoadItem just seeded from the ledger with an unsaved draft, and marks the form dirty so "unsaved changes" shows again.
func (f *decisionForm) ApplyDraft(s decisionSnapshot) {
	f.categoryIdx = s.categoryIdx
	f.actionIdx = s.actionIdx
	f.confidenceIdx = s.confidenceIdx
	f.reason.SetValue(s.reason)
	f.badCategory, f.badAction, f.badConfidence = s.badCategory, s.badAction, s.badConfidence
	f.dirty = true
	f.saved = false
	f.touched = true
}

// ApplyProposal seeds the form from a batch decisions file's proposal for an untriaged item. It counts as touched (someone chose these values) but not dirty: the proposal stays in the file, so leaving without saving loses nothing.
func (f *decisionForm) ApplyProposal(p proposal) {
	f.categoryIdx = indexOrZero(f.categories(), p.Category)
	f.actionIdx = indexOrZero(f.taxonomy.Actions, p.Action)
	f.confidenceIdx = indexOrZero(f.taxonomy.Confidence, p.Confidence)
	if p.Category == "" {
		f.categoryIdx = -1
	}

	if p.Action == "" {
		f.actionIdx = -1
	}

	if p.Confidence == "" {
		f.confidenceIdx = -1
	}

	f.reason.SetValue(p.Reason)
	f.proposalNotes = p.AgentNotes
	f.badCategory, f.badAction = unlisted(f.categories(), p.Category), unlisted(f.taxonomy.Actions, p.Action)
	f.badConfidence = unlisted(f.taxonomy.Confidence, p.Confidence)
	f.touched = true
	f.proposed = true
	f.proposalBy = p.ProposedBy
	f.proposalSnapshot = f.Snapshot()
}

// MarkDuplicate prefills the form as a duplicate of #number (duplicate or duplicate-pr, close-duplicate), leaving confidence for the reviewer to set. It errors if the taxonomy has no such values.
func (f *decisionForm) MarkDuplicate(number int, title string) error {
	category := "duplicate"
	if f.kind == "pr" {
		category = "duplicate-pr"
	}

	cat, act := -1, -1
	for i, c := range f.categories() {
		if c == category {
			cat = i
		}
	}

	for i, a := range f.taxonomy.Actions {
		if a == "close-duplicate" {
			act = i
		}
	}

	if cat < 0 || act < 0 {
		return fmt.Errorf("config/taxonomy.json has no %q category or \"close-duplicate\" action", category)
	}

	f.categoryIdx, f.actionIdx = cat, act
	f.badCategory, f.badAction = "", ""
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
	f.categoryIdx = indexOrZero(f.categories(), it.Category)
	f.actionIdx = indexOrZero(f.taxonomy.Actions, it.Action)
	f.confidenceIdx = indexOrZero(f.taxonomy.Confidence, it.Confidence)
	hasDecision := it.Category != "" || it.Action != "" || it.Confidence != "" || it.Reason != "" || it.Reviewed
	if hasDecision {
		if it.Category == "" {
			f.categoryIdx = -1
		}

		if it.Action == "" {
			f.actionIdx = -1
		}

		if it.Confidence == "" {
			f.confidenceIdx = -1
		}
	}

	f.badCategory, f.badAction, f.badConfidence = unlisted(f.categories(), it.Category), unlisted(f.taxonomy.Actions, it.Action), unlisted(f.taxonomy.Confidence, it.Confidence)
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
	if f.badCategory != "" {
		bad = append(bad, fmt.Sprintf("category %q", f.badCategory))
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

func (f *decisionForm) categories() []string { return f.taxonomy.CategoriesFor(f.kind) }

func (f decisionForm) Category() string {
	if f.badCategory != "" {
		return f.badCategory
	}

	if f.categoryIdx < 0 {
		return ""
	}

	if opts := f.categories(); len(opts) > 0 {
		return opts[f.categoryIdx%len(opts)]
	}

	return ""
}

func (f decisionForm) Action() string {
	if f.badAction != "" {
		return f.badAction
	}

	if f.actionIdx < 0 {
		return ""
	}

	if opts := f.taxonomy.Actions; len(opts) > 0 {
		return opts[f.actionIdx%len(opts)]
	}

	return ""
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
	case fieldCategory:
		return f.categories(), f.categoryIdx
	case fieldAction:
		return f.taxonomy.Actions, f.actionIdx
	case fieldConfidence:
		return f.taxonomy.Confidence, f.confidenceIdx
	}

	return nil, 0
}

// OpenPick opens the focused choice field's list.
func (f *decisionForm) OpenPick() {
	if options, current := f.options(); len(options) > 0 {
		f.pick.Open(options, current)
	}
}

// PickKey handles a key while the list is open; picking sets the value and moves to the next field.
func (f *decisionForm) PickKey(msg tea.KeyPressMsg) {
	if !f.pick.Key(msg) {
		return
	}

	f.CycleValue(0)
	switch f.focused {
	case fieldCategory:
		f.categoryIdx = f.pick.cursor
	case fieldAction:
		f.actionIdx = f.pick.cursor
	case fieldConfidence:
		f.confidenceIdx = f.pick.cursor
	}

	f.NextField()
}

// CycleValue moves the focused enum field by delta (wrapping). No-op on reason.
func (f *decisionForm) CycleValue(delta int) {
	options, _ := f.options()
	if len(options) == 0 {
		return
	}

	f.dirty = true
	f.saved = false
	f.touched = true
	switch f.focused {
	case fieldCategory:
		f.badCategory = ""
		n := len(f.categories())
		f.categoryIdx = ((f.categoryIdx+delta)%n + n) % n
	case fieldAction:
		f.badAction = ""
		n := len(f.taxonomy.Actions)
		f.actionIdx = ((f.actionIdx+delta)%n + n) % n
	case fieldConfidence:
		f.badConfidence = ""
		n := len(f.taxonomy.Confidence)
		f.confidenceIdx = ((f.confidenceIdx+delta)%n + n) % n
	}
}

// View renders the form to width: the three choices, the reason (wrapping over up to reasonMaxLines), and one line of state.
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
	for _, field := range []struct {
		id         formField
		name       string
		bad, value string
	}{
		{fieldCategory, "category", f.badCategory, f.Category()},
		{fieldAction, "action", f.badAction, f.Action()},
		{fieldConfidence, "confidence", f.badConfidence, f.Confidence()},
	} {
		rows = append(rows, label(field.id, field.name)+value(field.id, field.bad, field.value))
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

	// "Saved." itself goes to the status line only, not here as well.
	state := ""
	switch {
	case f.dirty:
		state = lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Warning)).Render("unsaved changes")
	case f.proposed:
		state = lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Warning)).Render("proposed in the batch file, not saved yet")
	case !f.touched:
		state = muted.Render("untriaged: fields show defaults until you change them")
	}

	if state != "" {
		rows = append(rows, "", state)
	}

	for i := range rows {
		rows[i] = ansi.Truncate(rows[i], maxInt(width, 1), "…")
	}

	return strings.Join(rows, "\n")
}
