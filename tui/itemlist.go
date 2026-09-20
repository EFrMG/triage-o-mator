package main

import (
	"fmt"
	"io"

	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/paginator"
	tea "github.com/charmbracelet/bubbletea"
)

// listItem adapts Item to bubbles/list's item interface.
// proposal is set when the item is listed through a batch whose decisions file proposes a category/action for it.
type listItem struct {
	Item
	proposal string
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
	comments := "comments"
	if li.CommentsCount == 1 {
		comments = "comment"
	}

	// An unsaved draft says so in its mark, in place of the decision it will replace.
	if li.unsaved {
		return fmt.Sprintf("%s · updated %s · %d %s", li.Kind, shortDate(li.UpdatedAt), li.CommentsCount, comments)
	}

	status := "untriaged"
	if li.Category != "" && li.Reviewed {
		status = "reviewed: " + li.Category + "/" + li.Action
	} else if li.Category != "" {
		status = "triaged: " + li.Category + "/" + li.Action
	} else if li.proposal != "" {
		status = "untriaged · proposed: " + li.proposal
	}

	// The decision comes first, after the card's [agent] / [human] mark, so a narrow pane cuts the dates rather than the call.
	return fmt.Sprintf("%s · %s · updated %s · %d %s", status, li.Kind, shortDate(li.UpdatedAt), li.CommentsCount, comments)
}

// Mark tags a decision with who made it: an agent's proposal applied to the ledger, in the warning color since nobody has checked it yet, or a person's own call, in the palette's blue (info). Untriaged items have no mark; a batch proposal says "proposed" instead. An unsaved draft is marked "unsaved", also in the warning color, whatever is saved.
func (li listItem) Mark() cardMark {
	switch {
	case li.unsaved:
		return cardMark{"unsaved", currentTheme.Warning}
	case li.Untriaged():
		return cardMark{}
	case li.ByAgent():
		return cardMark{"[agent]", currentTheme.Warning}
	default:
		return cardMark{"[human]", currentTheme.Info}
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
