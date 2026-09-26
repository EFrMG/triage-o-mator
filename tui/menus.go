package main

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Shared building blocks for the menu screens (Batches, Duplicates, Switch Repo): a title bar like the lists', and two-line "cards" where the selected one gets a soft-accent border and the selection background.

// titleBar is the screen title in the list title's pill style, then a muted subtitle.
func titleBar(title, subtitle string, width int) string {
	pill := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Background)).Background(focusedBorderColor).Bold(true).Padding(0, 1).Render(title)
	if subtitle == "" {
		return pill
	}

	return ansi.Truncate(pill+"  "+mutedText(subtitle), width, "…")
}

// cardHeight is a card's rows: an edge above, its two lines, and an edge below (blank on unselected cards, so every card is the same height and nothing jumps as the selection moves).
const cardHeight = 4

// cardMark is a short tag in its own color at the start of a card's second line, e.g. who made an item's decision.
type cardMark struct {
	text  string
	color string
}

// markedCard draws one entry across the full width of its pane, flush with the pane's borders: first line bold (accent when selected), second muted, with mark leading that second line. The selected one is filled with the selection background across all four rows, framed by low-contrast accent lines drawn at the outer edge of its top and bottom rows (▔ and ▁), so the fill reaches them without the half-row gap a mid-row ─ would leave.
// The mark is drawn as its own span on the same background, since a styled span inside the line would end the line's style at its reset.
func markedCard(first, second string, mark cardMark, selected bool, width int) string {
	width = maxInt(width, 6)
	inner := width - 4
	firstStyle := lipgloss.NewStyle().Width(width).Padding(0, 2).Bold(true)
	secondStyle := lipgloss.NewStyle().Padding(0, 2).Foreground(lipgloss.Color(currentTheme.Muted))
	markStyle := lipgloss.NewStyle().PaddingLeft(2).Foreground(lipgloss.Color(mark.color)).Bold(true)
	top, bottom := strings.Repeat(" ", width), strings.Repeat(" ", width)
	if selected {
		bg := themeOpacity(currentTheme.Selection, opacityMedium)
		firstStyle = firstStyle.Foreground(focusedBorderColor).Background(bg)
		secondStyle = secondStyle.Foreground(lipgloss.Color(currentTheme.Foreground)).Background(bg)
		markStyle = markStyle.Background(bg)
		edge := lipgloss.NewStyle().Foreground(themeOpacity(currentTheme.Accent, opacitySoft)).Background(bg)
		top, bottom = edge.Render(strings.Repeat("▔", width)), edge.Render(strings.Repeat("▁", width))
		second = continueStyleAfterReset(second, lipgloss.Color(currentTheme.Foreground), bg)
	}

	secondLine := secondStyle.Width(width).Render(ansi.Truncate(second, inner, "…"))
	if mark.text != "" && lipgloss.Width(mark.text)+1 < inner {
		lead := markStyle.Render(mark.text)
		rest := width - lipgloss.Width(lead)
		secondLine = lead + secondStyle.Width(rest).PaddingLeft(1).Render(ansi.Truncate(second, rest-3, "…"))
	}

	return top + "\n" + firstStyle.Render(ansi.Truncate(first, inner, "…")) + "\n" + secondLine + "\n" + bottom
}

// continueStyleAfterReset keeps nested styled spans, such as a progress bar, from dropping the selected card's foreground and background for the text that follows them.
func continueStyleAfterReset(text string, foreground, background color.Color) string {
	marker := "\x00"
	rendered := lipgloss.NewStyle().Foreground(foreground).Background(background).Render(marker)
	i := strings.Index(rendered, marker)
	if i < 0 {
		return text
	}

	sequence := rendered[:i]
	for _, reset := range []string{"\x1b[0m", "\x1b[m", "\x1b[49m", "\x1b[39m"} {
		text = strings.ReplaceAll(text, reset, reset+sequence)
	}

	return text
}

// cardList stacks cards into height rows, scrolled so the selected one is visible; selected -1 means none is (Switch Repo while typing), and the list stays at the top. width is the pane's full inner width: cards reach its borders.
func cardList(cards [][2]string, selected, width, height int) string {
	return markedCardList(cards, nil, selected, width, height)
}

// markedCardList is cardList with a mark leading each card's second line (marks[i], when there is one).
func markedCardList(cards [][2]string, marks []cardMark, selected, width, height int) string {
	visible := maxInt(height/cardHeight, 1)
	start := maxInt(minInt(selected-visible+1, len(cards)-visible), 0)
	if selected >= 0 && selected < start {
		start = selected
	}

	var out []string
	for i := start; i < len(cards) && i < start+visible; i++ {
		var mark cardMark
		if i < len(marks) {
			mark = marks[i]
		}

		out = append(out, markedCard(cards[i][0], cards[i][1], mark, i == selected, width))
	}

	return strings.Join(out, "\n")
}

// inset indents every line a column, as a padded panel would: the menu panels have no padding of their own, so their cards can reach the borders.
func inset(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if line != "" {
			lines[i] = " " + line
		}
	}

	return strings.Join(lines, "\n")
}

// withSidebar lays content out in the main panel beside the sidebar (or alone on narrow terminals), as the list views do. The panel has no padding: indent text with inset, and give cards cardWidth.
func (m model) withSidebar(content string, mainFocused bool) string {
	sidebarBox := m.sidebarBox(!mainFocused)
	mainView := m.titled(panelStyle(mainFocused).Width(m.mainAreaWidth()).Height(m.mainHeight()+2).Render(fitScreen(content, m.cardWidth(), m.mainHeight())), mainFocused)
	if m.width < 100 {
		return mainView
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, sidebarBox, mainView)
}

// sidebarBox is the sidebar's panel, as tall as its entries and centered vertically beside the main panel; on a terminal too short for that, it takes the full height like the main panel.
func (m model) sidebarBox(focused bool) string {
	box := panelStyle(focused).Width(sidebarContentWidth+4).Padding(1, 1).Render(m.sidebar.View(focused))
	if lipgloss.Height(box) > m.mainHeight()+2 {
		return panelStyle(focused).Width(sidebarContentWidth+4).Height(m.mainHeight()+2).Padding(0, 1).Render(m.sidebar.View(focused))
	}

	return lipgloss.PlaceVertical(m.mainHeight()+2, lipgloss.Center, box)
}

// menuWidth is the content width of the main panel beside the sidebar.
func (m model) menuWidth() int { return maxInt(m.mainAreaWidth()-4, 1) }

// cardWidth is the full inner width of the main panel beside the sidebar, padding included, for cards flush with its borders.
func (m model) cardWidth() int { return maxInt(m.mainAreaWidth()-2, 1) }
