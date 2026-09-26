package main

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// agentNotesSection holds the agent's longer evidence (a code review, a duplicate comparison) from the ledger's agent_notes, or from a batch proposal not yet applied.
const agentNotesSection = "Agent notes"

type sectionKind int

const (
	markdownSection sectionKind = iota
	commentsSection
	diffSection
)

// detailSection is one tab of the item view. Its text renders (Markdown through glamour, the diff highlighted) only when it's shown, and again only when the width or theme changed, since rendering a long thread is the expensive part.
type detailSection struct {
	name           string
	kind           sectionKind
	source         string
	comments       []string
	commentAuthors []string
	commentDates   []string
	// loaded is false until the text arrived; requested marks a diff fetch in flight.
	loaded, requested bool
	viewport          viewport.Model
	renderedFor       string
}

func (s detailSection) empty() bool {
	switch s.kind {
	case commentsSection:
		return s.loaded && len(s.comments) == 0
	case diffSection:
		return s.loaded && strings.TrimSpace(s.source) == ""
	}

	return s.loaded && strings.TrimSpace(s.source) == ""
}

// detailModel owns the item view's tabs (Body, Agent notes, Comments, Diff) for the selected item: one is active, shown beside the decision form, or full screen.
type detailModel struct {
	key              Key
	item             Item
	enriched         EnrichedItem
	width            int
	height           int
	sections         []detailSection
	active           int
	full             bool
	loading          bool
	loadErr          error
	cache            map[Key]EnrichedItem
	generation       uint64
	blockLegacy      bool
	notificationOnly bool
}

func newDetailModel() detailModel {
	return detailModel{cache: make(map[Key]EnrichedItem)}
}

// SetItem resets the tabs for a newly selected item, on its Body, beside the form.
// Returns true if this item hasn't been enriched yet (caller should dispatch enrichItemCmd).
func (d *detailModel) SetItem(it Item) (needsFetch bool) {
	d.generation++
	d.blockLegacy = false
	d.key, d.item = it.Key(), it
	d.enriched = EnrichedItem{}
	d.sections = []detailSection{{name: "Body", kind: markdownSection}}
	if strings.TrimSpace(it.AgentNotes) != "" {
		// Local ledger/proposal text, so it's readable before (and even if) the GitHub fetch finishes.
		d.sections = append(d.sections, detailSection{name: agentNotesSection, kind: markdownSection, source: it.AgentNotes, loaded: true})
	}

	d.sections = append(d.sections, detailSection{name: "Comments", kind: commentsSection})
	if it.Kind == "pr" {
		d.sections = append(d.sections, detailSection{name: "Diff", kind: diffSection})
	}

	for i := range d.sections {
		d.sections[i].viewport = viewport.New(viewport.WithWidth(maxInt(d.width, 1)), viewport.WithHeight(maxInt(d.height, 1)))
	}

	d.active, d.full, d.loadErr = 0, false, nil
	if cached, ok := d.cache[d.key]; ok {
		d.populate(cached)

		return false
	}

	d.loading = true

	return true
}

func (d *detailModel) populate(e EnrichedItem) {
	if previous, ok := d.cache[d.key]; ok && previous.DiffLoaded && !e.DiffLoaded && e.Evidence == nil {
		e.DiffText, e.DiffLoaded = previous.DiffText, true
	}

	d.cache[d.key] = e
	d.enriched = e
	d.loading = false
	for i := range d.sections {
		s := &d.sections[i]
		switch s.name {
		case "Body":
			s.source, s.loaded = e.Body, true
		case "Comments":
			s.comments, s.commentAuthors, s.commentDates, s.loaded = e.CommentBodies, e.CommentAuthors, e.CommentDates, true
		case "Diff":
			s.source, s.loaded = e.DiffText, e.DiffLoaded
			s.requested = s.requested && !s.loaded
		}

		s.renderedFor = ""
	}

	d.renderActive()
}

func (d *detailModel) OnEnriched(msg enrichedMsg) {
	if msg.key != d.key || d.enriched.Evidence != nil || d.blockLegacy {
		// A legacy read started before opening a fixed packet must not replace its recorded evidence.
		return
	}

	d.loading = false
	if msg.err != nil {
		for i := range d.sections {
			d.sections[i].requested = false
		}

		d.loadErr = msg.err

		return
	}

	d.loadErr = nil
	d.populate(msg.data)
}

// renderTabLabel names tab i. The Diff tab shows additions in the theme's success color and deletions in its error color; comments carry no count because each comment's separator says "comment 2 of 8".
func (d detailModel) renderTabLabel(i int, style lipgloss.Style) string {
	s := d.sections[i]
	if s.kind == diffSection {
		if d.enriched.Additions+d.enriched.Deletions > 0 {
			additions := style.Foreground(lipgloss.Color(currentTheme.Success)).Render(fmt.Sprintf("+%d", d.enriched.Additions))
			deletions := style.Foreground(lipgloss.Color(currentTheme.Error)).Render(fmt.Sprintf("−%d", d.enriched.Deletions))

			return style.Render(s.name+" ") + additions + style.Render(" ") + deletions
		}
	}

	return style.Render(s.name)
}

// TabBar draws the tabs side by side with an underline marking the active one; focused (the content has the keys) colors it with the accent, otherwise it's quieter. Empty tabs are dimmed.
func (d detailModel) TabBar(width int, focused bool) string {
	accent := lipgloss.Color(currentTheme.Accent)
	muted := lipgloss.Color(currentTheme.Muted)
	activeColor := accent
	if !focused {
		activeColor = lipgloss.Color(currentTheme.Foreground)
	}

	var labels, rules []string
	for i, s := range d.sections {
		color := lipgloss.Color(currentTheme.Foreground)
		style := lipgloss.NewStyle().Foreground(color)
		switch {
		case i == d.active:
			color = activeColor
			style = style.Foreground(color).Bold(true)
		case s.empty():
			color = muted
			style = style.Foreground(color).Faint(true)
		}

		blendColor := currentTheme.Foreground
		if i == d.active && focused {
			blendColor = currentTheme.Accent
		} else if i != d.active && s.empty() {
			blendColor = currentTheme.Muted
		}
		brackets := lipgloss.NewStyle().Foreground(themeOpacity(blendColor, opacityMedium))
		number := lipgloss.NewStyle().Foreground(accent)
		badge := brackets.Render("[") + number.Render(fmt.Sprint(i+1)) + brackets.Render("]")
		label := style.Render(" ") + badge + style.Render(" ") + d.renderTabLabel(i, style) + style.Render(" ")
		ruleColor, ruleGlyph := lipgloss.Color(currentTheme.Border), "─"
		if i == d.active {
			ruleColor, ruleGlyph = themeOpacity(blendColor, opacityStrong), "━"
		}

		rule := lipgloss.NewStyle().Foreground(ruleColor).Render(strings.Repeat(ruleGlyph, ansi.StringWidth(label)))
		labels = append(labels, label)
		rules = append(rules, rule)
	}

	used := ansi.StringWidth(strings.Join(labels, " "))
	tail := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Border)).Render(strings.Repeat("─", maxInt(width-used, 0)))
	hint := ""
	if d.full && !d.notificationOnly {
		hint = mutedText("  full screen · Enter to go back to the form")
	}

	joiner := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Border)).Render("─")

	return ansi.Truncate(strings.Join(labels, " ")+hint, width, "…") + "\n" + ansi.Truncate(strings.Join(rules, joiner)+tail, width, "")
}

// AnySectionFull reports whether the active tab fills the screen, in which case the caller hides the decision form.
func (d detailModel) AnySectionFull() bool { return d.full }

func (d *detailModel) CycleSection(delta int) {
	if len(d.sections) == 0 {
		return
	}

	d.active = (d.active + delta + len(d.sections)) % len(d.sections)
	d.renderActive()
}

// JumpSection makes tab i active (1-4 in the item view).
func (d *detailModel) JumpSection(i int) {
	if i >= 0 && i < len(d.sections) {
		d.CycleSection(i - d.active)
	}
}

// ToggleActiveSection switches the active tab between beside-the-form and full screen. An empty tab has nothing to fill the screen with, so it stays put. Returns true if the Diff tab now needs its diff fetched.
func (d *detailModel) ToggleActiveSection() (needsDiffFetch bool) {
	if len(d.sections) == 0 || d.sections[d.active].empty() {
		return false
	}

	d.full = !d.full

	return d.NeedsActiveDiff()
}

// NeedsActiveDiff reports (once) that the active tab is a Diff not fetched yet, marking the fetch as requested.
func (d *detailModel) NeedsActiveDiff() bool {
	if len(d.sections) == 0 || d.enriched.Evidence != nil || d.blockLegacy {
		return false
	}

	s := &d.sections[d.active]
	if s.kind == diffSection && !s.loaded && !s.requested && !d.loading {
		s.requested = true
		d.renderActive()

		return true
	}

	return false
}

func (d *detailModel) activeViewport() *viewport.Model {
	if len(d.sections) == 0 {
		return nil
	}

	return &d.sections[d.active].viewport
}

func (d *detailModel) LineDown(n int) {
	if vp := d.activeViewport(); vp != nil {
		vp.ScrollDown(n)
	}
}

func (d *detailModel) LineUp(n int) {
	if vp := d.activeViewport(); vp != nil {
		vp.ScrollUp(n)
	}
}

func (d *detailModel) GotoTop() {
	if vp := d.activeViewport(); vp != nil {
		vp.GotoTop()
	}
}

func (d *detailModel) GotoBottom() {
	if vp := d.activeViewport(); vp != nil {
		vp.GotoBottom()
	}
}

func (d *detailModel) HalfPageDown() {
	if vp := d.activeViewport(); vp != nil {
		vp.HalfPageDown()
	}
}

func (d *detailModel) HalfPageUp() {
	if vp := d.activeViewport(); vp != nil {
		vp.HalfPageUp()
	}
}

// wrapText handles long URLs, Unicode cell widths and tabs, and removes terminal control sequences from remote text before it reaches the renderer.
func wrapText(text string, width int) string {
	return ansi.Wrap(sanitize(text), maxInt(width, 1), "")
}

// Resize sets the content area every tab shares, and re-renders the active one if its width changed.
func (d *detailModel) Resize(width, height int) {
	d.width, d.height = maxInt(width, 1), maxInt(height, 1)
	for i := range d.sections {
		d.sections[i].viewport.SetWidth(d.width)
		d.sections[i].viewport.SetHeight(d.height)
	}

	d.renderActive()
}

// renderActive renders the active tab for the current width and theme, keeping the scroll position.
func (d *detailModel) renderActive() {
	if len(d.sections) == 0 || d.width <= 1 {
		return
	}

	s := &d.sections[d.active]
	key := fmt.Sprintf("%d/%s/%v/%v", d.width, themeName, s.loaded, s.requested)
	if s.renderedFor == key {
		return
	}

	var text string
	component := map[string]string{"Body": "summary", "Comments": "comments", "Diff": "diff"}[s.name]
	missing := false
	if d.enriched.CachedRead && d.enriched.Evidence != nil && component != "" {
		c := d.enriched.Evidence.Components[component]
		missing = c == nil || len(c.Object) == 0 || string(c.Object) == "null"
	}
	switch {
	case missing:
		text = mutedText("No cached payload for this component. No automatic fetch.")
	case !s.loaded && s.kind == diffSection && s.requested:
		text = mutedText("Loading the diff from GitHub…")
	case !s.loaded && s.kind == diffSection:
		text = mutedText("The diff loads when you open this tab.")
	case !s.loaded:
		text = mutedText("Loading…")
	case s.kind == commentsSection:
		text = renderComments(s.comments, s.commentAuthors, s.commentDates, d.width)
	case s.kind == diffSection && strings.TrimSpace(s.source) == "":
		if d.enriched.CachedRead && len(d.enriched.Evidence.Problems["diff"]) > 0 {
			text = mutedText("(no diff text; inspect the component diagnostics)")
		} else if d.enriched.Evidence != nil && !d.enriched.CachedRead {
			text = mutedText("(no diff text in this fixed packet; inspect its evidence coverage)")
		} else {
			text = mutedText("(empty diff)")
		}
	case s.kind == diffSection:
		text = renderDiff(s.source, d.width)
	case strings.TrimSpace(s.source) == "":
		text = mutedText("(no description)")
	default:
		text = renderMarkdown(s.source, d.width)
	}

	if evidence := d.enriched.Evidence; evidence != nil {
		id := evidence.SnapshotID
		if id == "" {
			id = "none"
		}
		notice := "Fixed packet snapshot: " + id + ". Coverage recorded at creation, not current GitHub state."
		if d.enriched.CachedRead {
			notice = "Cache read (" + evidence.Mode + "), snapshot: " + id + ". Recorded evidence, not guaranteed current GitHub state."
			if d.notificationOnly {
				notice = "Item details refreshed when opened. Saved notification records may describe an earlier state."
			}
			if c := evidence.Components[component]; c != nil {
				notice += "\n" + component + ": " + c.Status + "; observed " + c.FetchedAt
			}
		}
		var names []string
		for name := range evidence.Problems {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			if problems := evidence.Problems[name]; len(problems) > 0 {
				notice += "\n" + name + ": " + strings.Join(problems, ", ")
			}
		}
		if _, included := evidence.Problems["diff"]; !included && s.kind == diffSection {
			notice += "\ndiff: not requested for this packet"
		}

		text = wrapText(notice, d.width) + "\n\n" + text
	}

	offset := s.viewport.YOffset()
	s.viewport.SetContent(text)
	s.viewport.SetYOffset(offset)
	s.renderedFor = key
}

// View is the active tab's content, sized to the content area.
func (d detailModel) View() string {
	if d.loadErr != nil {
		if d.notificationOnly {
			return wrapText("Couldn't fetch item details: "+friendlyError(d.loadErr)+"\n\nEsc or h returns to Notifications. Open the item again to retry.", d.width)
		}
		return wrapText("Couldn't load the item: "+friendlyError(d.loadErr)+"\n\nEsc and open it again to retry, o opens it on GitHub, ! shows the details.", d.width)
	}

	if len(d.sections) == 0 {
		return ""
	}

	s := d.sections[d.active]
	if d.loading && !s.loaded {
		if d.notificationOnly {
			return mutedText("Fetching current item details from GitHub…")
		}
		return mutedText("Loading item details from GitHub…")
	}

	return s.viewport.View()
}

func (m model) enrichDetailCmd(withDiff bool) tea.Cmd {
	return enrichItemCmd(m.installRoot, m.repo, m.detail.key, m.detail.generation, withDiff)
}

// refreshLiveDetail invalidates live details, but fetches them immediately only while the item is open. Fixed evidence views stay untouched.
func (m *model) refreshLiveDetail() tea.Cmd {
	if m.detail.key.Number <= 0 || m.detail.blockLegacy || m.detail.enriched.Evidence != nil {
		return nil
	}

	delete(m.detail.cache, m.detail.key)
	m.detail.generation++
	m.detail.loading = false
	m.detail.loadErr = nil
	if m.focus != FocusDetail {
		return nil
	}

	m.detail.loading = true
	return m.enrichDetailCmd(m.detail.enriched.DiffLoaded)
}
