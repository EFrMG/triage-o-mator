package main

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The footer is the only home of in-app key hints. It shows groups of related keys side by side, centered, wrapping a whole group at a time; keys only by default, and with descriptions once ? is toggled on. The status row above it carries "? help" on the right.

type hint struct{ keys, desc string }

type footerGroup struct {
	name  string
	hints []hint
}

// bind turns bindings into one hint, labelled with desc (or the first binding's own description) and all their keys joined, e.g. "j/k move".
func bind(desc string, bindings ...key.Binding) hint {
	labels := make([]string, len(bindings))
	for i, b := range bindings {
		labels[i] = b.Help().Key
		// Ctrl-D/Ctrl-U reads as Ctrl-D/U.
		if i > 0 && strings.HasPrefix(labels[i], "Ctrl-") && strings.HasPrefix(labels[0], "Ctrl-") {
			labels[i] = strings.TrimPrefix(labels[i], "Ctrl-")
		}
	}

	if desc == "" {
		desc = bindings[0].Help().Desc
	}

	return hint{keys: strings.Join(labels, "/"), desc: desc}
}

func group(name string, hints ...hint) footerGroup { return footerGroup{name: name, hints: hints} }

func (m model) navigationGroup() footerGroup {
	return group("Navigation", bind("", keys.Back), bind("", keys.Quit), bind("exit", keys.ForceQuit))
}

func (m model) menusGroup() footerGroup {
	g := group("Menus", bind("", keys.Yank), bind("", keys.Group), bind("", keys.Corpus), bind("", keys.Theme), bind("", keys.Refresh), bind("", keys.RefreshFull))
	if m.lastError.text != "" {
		g.hints = append(g.hints, bind("", keys.ErrorDetails))
	}

	return g
}

// footerGroups lists the keys that work on the current screen, most specific first and Navigation last.
func (m model) footerGroups() []footerGroup {
	if m.width < 60 || m.height < 24 {
		return []footerGroup{group("Navigation", bind("back", keys.Cancel), bind("quit", keys.ForceQuit))}
	}

	switch {
	case m.comment.open:
		if m.comment.busy {
			return []footerGroup{group("Comment", hint{"", "working…"})}
		}
		if m.comment.previewing {
			return []footerGroup{group("Comment", hint{"↑/↓", "scroll"}, hint{"Ctrl-P", "edit"}, bind("", keys.CommentEditor), hint{"Ctrl-S", "publish"}, hint{"Esc", "edit"})}
		}
		return []footerGroup{group("Comment", hint{"Ctrl-P", "preview"}, hint{"Ctrl-S", "publish"}, hint{"Esc", "discard"})}
	case m.confirmQuit:
		return []footerGroup{group("Quit", bind("discard drafts", keys.Quit), hint{"any key", "cancel"}), group("Navigation", bind("exit", keys.ForceQuit))}
	case m.lastError.open:
		return []footerGroup{group("Error", bind("scroll", keys.Down, keys.Up), bind("page", keys.HalfDown, keys.HalfUp)), group("Navigation", bind("close", keys.Back), bind("exit", keys.ForceQuit))}
	case m.notificationPR.open:
		return []footerGroup{group("PR", hint{"Tab/1/2/3/4", "tabs"}, hint{"j/k/↑/↓", "scroll"}, hint{"Ctrl-D/U", "page"}), group("Navigation", hint{"Esc/h", "back to Notifications"}, hint{"q", "quit"})}
	case m.attention.open:
		return []footerGroup{group("Comments", hint{"j/k/Tab", "select"}, hint{"Enter/l/→", "open PR or page"}), group("Navigation", hint{"Ctrl-D/U", "scroll"}, hint{"Esc/h", "back"})}
	case m.actionHistory.open:
		return []footerGroup{group("Explanations", hint{"j/k/Tab", "select"}, hint{"Enter/l/→", "open PR or page"}), group("Navigation", hint{"Ctrl-D/U", "scroll"}, hint{"Esc/h", "back"})}
	case m.notifications.open:
		return []footerGroup{group("Notifications", hint{"j/k/Tab", "select"}, hint{"Enter/l/→", "open item or page"}, hint{"v", "viewed"}, hint{"d", "dismiss"}, hint{"r", "refresh"}), group("Navigation", hint{"Esc/h", "back"}, hint{"q", "quit"})}
	case m.corpus.open:
		if m.corpus.busy {
			return []footerGroup{group("Download", bind("stop", keys.CorpusStop)), group("Navigation", bind("close", keys.Back, keys.Corpus))}
		}
		return []footerGroup{group("Dataset", hint{"d", "download/update"}, bind("resume", keys.CorpusRun), hint{"y", "copy agent prompt"}, hint{"u", "size"}), group("Limits", bind("items per run", keys.CorpusBudget)), group("Navigation", bind("scroll", keys.Down, keys.Up), bind("close", keys.Back, keys.Corpus))}
	case m.themePicker.open && m.themePicker.searching:
		return []footerGroup{group("Search", hint{"type", "theme name"}, hint{"↑/↓", "preview"}, bind("keep", keys.Enter), bind("clear", keys.Cancel)), group("Navigation", bind("exit", keys.ForceQuit))}
	case m.themePicker.open:
		return []footerGroup{group("Theme", bind("preview", keys.Down, keys.Up), bind("ends", keys.Top, keys.Bottom), bind("", keys.Search), bind("apply", keys.Enter)), group("Navigation", bind("cancel", keys.Back), bind("", keys.Quit), bind("exit", keys.ForceQuit))}
	case (m.groups.open && m.groups.busy) || (m.dups.open && m.dups.busy):
		return []footerGroup{group("Navigation", bind("", keys.ForceQuit))}
	case (m.groups.open && m.groups.editing != "" && m.groups.pick.open) || (m.batches.open && m.batches.editing && m.batches.pick.open):
		return []footerGroup{group("List", bind("move", keys.ValueNext, keys.ValuePrev), bind("pick", keys.Confirm, keys.OpenList), bind("", keys.CloseList)), group("Navigation", bind("", keys.Quit), bind("exit", keys.ForceQuit))}
	case m.groups.open && m.groups.editing != "":
		edit := group("Edit", bind("fields", keys.FieldNext, keys.FieldPrev), bind("next/save", keys.Confirm), bind("save", keys.FormSubmit))
		if m.onGroupStatusField() {
			edit = group("Edit", bind("fields", keys.FieldNext, keys.FieldPrev, keys.ChoiceNext, keys.ChoicePrev), bind("change", keys.ValueNext, keys.ValuePrev), bind("", keys.OpenList), bind("next/save", keys.Confirm), bind("save", keys.FormSubmit))
		}

		return []footerGroup{edit, group("Navigation", bind("", keys.Cancel), bind("exit", keys.ForceQuit))}
	case m.batches.open && m.batches.editing:
		edit := group("Edit", bind("fields", keys.FieldNext, keys.FieldPrev), bind("next", keys.Confirm), bind("create", keys.FormSubmit))
		if m.batches.field > 0 {
			edit = group("Edit", bind("fields", keys.FieldNext, keys.FieldPrev, keys.ChoiceNext, keys.ChoicePrev), bind("change", keys.ValueNext, keys.ValuePrev), bind("", keys.OpenList), bind("next/create", keys.Confirm), bind("create", keys.FormSubmit))
		}

		return []footerGroup{edit, group("Navigation", bind("", keys.Cancel), bind("exit", keys.ForceQuit))}
	case m.editingRepo && m.installing.path != "":
		return []footerGroup{group("Install", bind("install", keys.Enter), hint{"s", "solo / tracked"}), group("Navigation", bind("cancel", keys.Cancel), bind("exit", keys.ForceQuit))}
	case m.editingRepo && m.noInstall():
		return []footerGroup{group("Install", hint{"Tab/j/k", "your installs"}, bind("open", keys.Enter)), group("Navigation", bind("quit", keys.Cancel), bind("exit", keys.ForceQuit))}
	case m.editingRepo:
		return []footerGroup{group("Repo", hint{"Tab/j/k", "your repos"}, bind("switch", keys.Enter)), group("Navigation", bind("", keys.Cancel), bind("exit", keys.ForceQuit))}
	case m.searching:
		return []footerGroup{group("Search", hint{"type", "title words or #number"}, hint{"↑/↓", "move"}, bind("keep", keys.Enter), bind("clear", keys.Cancel)), group("Navigation", bind("exit", keys.ForceQuit))}
	case m.typingReason():
		return []footerGroup{group("Edit", bind("fields; then s saves for review", keys.FieldNext, keys.FieldPrev), bind("save & approve", keys.Confirm)), group("Navigation", bind("back", keys.Cancel), bind("exit", keys.ForceQuit))}
	case m.groups.open:
		return m.groupFooter()
	case m.dups.open:
		return []footerGroup{
			group("Duplicate", bind("mark as duplicate", keys.MarkDup), bind("", keys.SwapDup), bind("", keys.Tick), bind("group ticked", keys.Group)),
			group("Item", bind("read", keys.Enter), bind("", keys.Save), bind("", keys.SaveApprove), bind("", keys.Open)),
			group("Select", bind("move", keys.Down, keys.Up), bind("ends", keys.Top, keys.Bottom)),
			group("Menus", bind("", keys.Theme)),
			m.navigationGroup(),
		}
	case m.batches.open:
		if m.batches.busy {
			return []footerGroup{group("Batch", hint{"", "working… Esc leaves it running"}), m.navigationGroup()}
		}

		actions := group("Batch", bind("", keys.New))
		if m.selectedBatch() != nil {
			actions.hints = append(actions.hints, bind("", keys.ApplyAll), bind("", keys.Delete))
		}

		return []footerGroup{actions, group("Select", bind("move", keys.Down, keys.Up), bind("ends", keys.Top, keys.Bottom), bind("", keys.Tick), bind("", keys.Enter)), group("Menus", bind("", keys.Theme), bind("", keys.Refresh)), m.navigationGroup()}
	case m.focus == FocusDetail:
		return m.itemFooter()
	case m.activePairs && m.focus == FocusList:
		return []footerGroup{group("Pairs", bind("compare", keys.Enter, keys.MarkDup), bind("clear", keys.Delete), bind("", keys.DeleteAll)), group("Select", bind("move", keys.Down, keys.Up), bind("ends", keys.Top, keys.Bottom), bind("", keys.Search)), m.menusGroup(), m.navigationGroup()}
	}

	if m.focus != FocusList {
		return []footerGroup{group("Select", bind("move", keys.Down, keys.Up), bind("ends", keys.Top, keys.Bottom), bind("", keys.Enter)), m.menusGroup(), m.navigationGroup()}
	}

	// An item list: list actions apply to the ticked items, or the hovered one.
	items := group("Items", bind("", keys.Approve), bind("", keys.Undo), bind("", keys.Group), bind("", keys.QuickGroup), bind("", keys.MarkDup), bind("", keys.Track), bind("", keys.Yank, keys.YankAll))
	if len(m.ticked) > 0 {
		items.name = fmt.Sprintf("%d ticked", len(m.ticked))
	}

	groups := []footerGroup{group("Select", bind("move", keys.Down, keys.Up), bind("ends", keys.Top, keys.Bottom), bind("", keys.Search), bind("", keys.Tick), bind("", keys.Enter)), items}
	if m.activeTab == untriagedTab && m.activeBatch == "" && !m.activePairs {
		groups = append(groups, group("Untriaged", bind("cycle kind", keys.UntriagedKind), bind("reverse order", keys.UntriagedOrder)))
	}
	if m.activeBatch != "" {
		groups = append(groups, group("Batch", bind("apply proposals", keys.ApplyAll), bind("remove from batch", keys.Delete)))
	}

	return append(groups, group("Menus", bind("", keys.Theme), bind("", keys.Refresh), bind("", keys.RefreshFull)), m.navigationGroup())
}

func (m model) itemFooter() []footerGroup {
	item := group("Item", bind("", keys.Save), bind("", keys.SaveApprove), bind("", keys.Approve), bind("", keys.Undo), bind("", keys.MarkDup), bind("", keys.Track), bind("", keys.QuickGroup), bind("", keys.Open), bind("", keys.Comment, keys.CommentEditor), bind("", keys.Yank))
	read := group("Read", bind("tabs", keys.TabPrev, keys.TabNext), bind("", keys.TabJump), bind("expand", keys.Enter), bind("scroll", keys.Down, keys.Up), bind("ends", keys.Top, keys.Bottom), bind("page", keys.HalfDown, keys.HalfUp))
	if m.detail.AnySectionFull() {
		return []footerGroup{item, read, m.menusGroup(), m.navigationGroup()}
	}

	if m.form.pick.open {
		return []footerGroup{group("List", bind("move", keys.ValueNext, keys.ValuePrev), bind("pick", keys.Confirm, keys.OpenList), bind("", keys.CloseList)), group("Navigation", bind("", keys.Quit), bind("exit", keys.ForceQuit))}
	}

	fields := group("Fields", bind("fields", keys.FieldNext, keys.FieldPrev), hint{"h/l", "content / form"})
	if formFieldIsEnum(m.form.focused) {
		fields = group("Fields", bind("fields", keys.FieldNext, keys.FieldPrev, keys.ChoiceNext, keys.ChoicePrev), hint{"h", "content"}, bind("change", keys.ValueNext, keys.ValuePrev), bind("", keys.OpenList), bind("next", keys.Confirm))
		read = group("Read", bind("tabs", keys.TabPrev, keys.TabNext), bind("", keys.TabJump))
	}

	return []footerGroup{item, fields, read, m.menusGroup(), m.navigationGroup()}
}

func (m model) groupFooter() []footerGroup {
	var groups []footerGroup
	g := m.selectedGroup()
	if m.groups.detail {
		if g != nil && len(g.Members) > 0 {
			groups = append(groups, group("Member", bind("open", keys.Enter), bind("notes", keys.Edit), bind("", keys.Tick), bind("remove", keys.Delete)), group("Context", bind("scroll notes", keys.HalfDown, keys.HalfUp)))
		}
	}

	actions := group("Group", bind("", keys.New))
	if g != nil {
		if !m.groups.detail {
			actions.hints = append(actions.hints, bind("", keys.Edit), bind("delete group", keys.Delete))
		}

		actions.hints = append(actions.hints, bind("", keys.Export), bind("", keys.ExportFull))
		switch n := len(m.groups.sources); {
		case n == 1:
			actions.hints = append(actions.hints, bind("add current item", keys.Group))
		case n > 1:
			actions.hints = append(actions.hints, bind(fmt.Sprintf("add %d items", n), keys.Group))
		}
	}

	groups = append(groups, actions)
	if m.groups.detail {
		groups = append(groups, group("Select", bind("member", keys.Down, keys.Up)))
	} else {
		groups = append(groups, group("Select", bind("move", keys.Down, keys.Up), bind("", keys.Enter)))
	}

	return append(groups, group("Menus", bind("", keys.Theme), bind("", keys.Refresh)), m.navigationGroup())
}

// renderGroup draws one group as lines no wider than width: its name, then each hint's keys in the accent color, with descriptions when ? is on. A group too wide for one line breaks between hints, never inside one.
func (m model) renderGroup(g footerGroup, width int) []string {
	keyStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Accent)).Bold(true)
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Muted))
	separator := " "
	if m.showHelp {
		separator = muted.Render(" · ")
	}

	lines := []string{muted.Render(g.name + ":")}
	for i, h := range g.hints {
		part := keyStyle.Render(h.keys)
		switch {
		case h.keys == "":
			part = h.desc
		case m.showHelp:
			part += " " + h.desc
		}

		last := &lines[len(lines)-1]
		joiner := separator
		if i == 0 {
			joiner = " "
		}

		if i > 0 && ansi.StringWidth(*last+joiner+part) > width {
			lines = append(lines, part)

			continue
		}

		*last += joiner + part
	}

	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "…")
	}

	return lines
}

// footerRows packs whole groups into rows no wider than the screen, separated by │, and centers each row. A group that needs more than one line gets rows of its own.
func (m model) footerRows() []string {
	width := maxInt(m.width, 1)
	separator := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Muted)).Render(" │ ")
	var rows, current []string
	currentWidth := 0
	flush := func() {
		if len(current) > 0 {
			rows = append(rows, strings.Join(current, separator))
			current, currentWidth = nil, 0
		}
	}

	for _, g := range m.footerGroups() {
		lines := m.renderGroup(g, width)
		if len(lines) > 1 {
			flush()
			rows = append(rows, lines...)

			continue
		}

		w := ansi.StringWidth(lines[0])
		if len(current) > 0 && currentWidth+3+w > width {
			flush()
		}

		if len(current) > 0 {
			currentWidth += 3
		}

		current = append(current, lines[0])
		currentWidth += w
	}

	flush()
	for i, row := range rows {
		rows[i] = lipgloss.PlaceHorizontal(width, lipgloss.Center, row)
	}

	return rows
}

func (m model) footerView() string { return strings.Join(m.footerRows(), "\n") }

// statusRow is the status message, with "? help" (or "? less" while descriptions show) at the right edge.
func (m model) statusRow() string {
	helpLabel := "help"
	if m.showHelp {
		helpLabel = "less"
	}

	help := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Accent)).Bold(true).Render(keys.Help.Help().Key) + " " + helpLabel
	if m.width < 60 || m.height < 24 {
		help = ""
	}

	room := maxInt(m.width-ansi.StringWidth(help)-1, 0)
	status := ansi.Truncate(singleLine(m.status), room, "…")
	statusColor := currentTheme.Success
	if m.statusIsError() {
		statusColor = currentTheme.Error
	} else if (m.statusWarning != "" && m.status == m.statusWarning) || m.refreshing || m.statusPinned() {
		statusColor = currentTheme.Warning
	}

	left := screenStyle().Foreground(lipgloss.Color(statusColor)).Render(status)
	gap := maxInt(m.width-ansi.StringWidth(status)-ansi.StringWidth(help), 0)

	return left + strings.Repeat(" ", gap) + help
}
