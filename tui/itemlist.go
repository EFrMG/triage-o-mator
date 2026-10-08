package main

import (
	"fmt"
	"io"
	"slices"
	"strings"

	"charm.land/bubbles/v2/list"
	"charm.land/bubbles/v2/paginator"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// listItem adapts Item to bubbles/list's item interface.
// proposalLabels and proposalAction are set when a batch proposes a decision for the item.
type listItem struct {
	Item
	proposalLabels []string
	proposalAction string
	// ticked marks an item ticked with Space for a bulk action; unsaved, one with an unsaved draft of its decision (model.drafts).
	ticked, unsaved bool
}

func (li listItem) Title() string {
	mark := ""
	if li.ticked {
		mark = "✓ "
	}

	return fmt.Sprintf("%s#%d %s", mark, li.Number, li.Item.Title)
}

func (li listItem) Description() string {
	kind, color := "Issue", currentTheme.Warning
	if li.Kind == "pr" {
		kind, color = "PR", currentTheme.Info
	}
	parts := []string{lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true).Render(kind)}
	if mark := li.Mark(); mark.text != "" {
		parts = append(parts, lipgloss.NewStyle().Foreground(lipgloss.Color(mark.color)).Render(mark.text))
	}
	if !li.unsaved && !li.Untriaged() && li.Action != "" {
		parts = append(parts, lipgloss.NewStyle().Bold(true).Render(singleLine(li.Action)))
	} else if !li.unsaved && (len(li.proposalLabels) > 0 || li.proposalAction != "") {
		parts = append(parts, "proposed")
		if li.proposalAction != "" {
			parts = append(parts, lipgloss.NewStyle().Bold(true).Render(singleLine(li.proposalAction)))
		}
	}

	return strings.Join(parts, " · ")
}

func (li listItem) Tags() string {
	labels := append([]string{}, li.ProposedLabels...)
	if li.Untriaged() {
		labels = append(labels, li.proposalLabels...)
	}
	for _, label := range li.Labels {
		if !slices.Contains(labels, label) {
			labels = append(labels, label)
		}
	}

	var tags []string
	for _, label := range labels {
		if label = singleLine(label); label != "" {
			tags = append(tags, label)
		}
	}
	if li.PendingReview() {
		tags = append(tags, "pending review")
	}

	return strings.Join(tags, " · ")
}

func (li listItem) CommentsLabel() string {
	if li.CommentsCount == 1 {
		return "1 comment"
	}

	return fmt.Sprintf("%d comments", li.CommentsCount)
}

func itemScoreMark(item Item) cardMark {
	// A stale score keeps its number's color; only its "(stale?)" suffix says it may be out of date.
	if value, ok := item.savedScore(); ok {
		return cardMark{text: item.ScoreLabel(), color: itemScoreColor(value)}
	}

	return cardMark{text: item.ScoreLabel(), color: currentTheme.Muted}
}

func itemScoreColor(value int) string {
	if value <= 2 {
		return currentTheme.Error
	}
	if value == 3 {
		return currentTheme.Warning
	}

	return currentTheme.Success
}

// Mark identifies who made a decision, or an unsaved draft. Pending review appears in the card's right-aligned tags.
func (li listItem) Mark() cardMark {
	switch {
	case li.unsaved:
		return cardMark{"unsaved", currentTheme.Warning}
	case li.Untriaged():
		return cardMark{}
	case li.ByAgent():
		return cardMark{"agent", currentTheme.Warning}
	default:
		return cardMark{"human", currentTheme.Info}
	}
}

func (li listItem) FilterValue() string { return fmt.Sprintf("#%d %s", li.Number, li.Item.Title) }

func shortDate(iso string) string {
	if len(iso) >= 10 {
		return iso[:10]
	}

	return iso
}

// cardDelegate draws list entries as the menu screens' cards: title and description, with the hovered entry framed in a soft-accent border and highlighted inside it.
type cardDelegate struct{}

func (cardDelegate) Height() int                         { return cardHeight }
func (cardDelegate) Spacing() int                        { return 0 }
func (cardDelegate) Update(tea.Msg, *list.Model) tea.Cmd { return nil }

func (cardDelegate) Render(w io.Writer, m list.Model, index int, item list.Item) {
	entry, ok := item.(list.DefaultItem)
	if !ok {
		return
	}

	if li, ok := item.(listItem); ok {
		fmt.Fprint(w, markedCardWithRightAndMeta(entry.Title(), entry.Description(), cardMark{}, itemScoreMark(li.Item), cardMeta{li.Tags(), li.CommentsLabel()}, index == m.Index(), m.Width()))
		return
	}

	var mark cardMark
	if marked, ok := item.(interface{ Mark() cardMark }); ok {
		mark = marked.Mark()
	}

	fmt.Fprint(w, markedCard(entry.Title(), entry.Description(), mark, index == m.Index(), m.Width()))
}

func newItemList(items []Item, title string, width, height int) list.Model {
	entries := make([]list.Item, len(items))
	for i, it := range items {
		entries[i] = listItem{Item: it}
	}

	// The title and count are drawn above the list by listHeader, with the search.
	l := list.New(entries, cardDelegate{}, width, maxInt(height-listHeaderHeight, 1))
	l.Title = fmt.Sprintf("%s (%d)", title, len(items))
	l.SetShowHelp(false)
	l.SetShowTitle(false)
	l.SetShowStatusBar(false)
	// The list's own filter would still draw its title row; search.go filters instead.
	l.SetFilteringEnabled(false)
	// "3/52" rather than a dot per page, which runs to a row of dots on long lists.
	l.Paginator.Type = paginator.Arabic
	themeList(&l)

	return l
}
