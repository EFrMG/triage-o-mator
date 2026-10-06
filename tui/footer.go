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
	return group("Menus", bind("", keys.Yank), bind("", keys.Group), bind("", keys.Corpus), bind("", keys.Theme), bind("", keys.Refresh), bind("", keys.RefreshFull))
}

// footerGroups lists the keys that work on the current screen, most specific first and Navigation last.
func (m model) footerGroups() []footerGroup {
	groups := m.contextFooterGroups()
	if m.lastError.text != "" && m.canOpenErrorDetails() && m.width >= 60 && m.height >= 24 {
		return append([]footerGroup{group("Error", bind("", keys.ErrorDetails))}, groups...)
	}

	return groups
}

func (m model) contextFooterGroups() []footerGroup {
	if m.width < 60 || m.height < 24 && !m.comment.open && m.groups.editing != "notes" && m.groups.editing != "edit" {
		return []footerGroup{group("Navigation", bind("back", keys.Cancel), bind("quit", keys.ForceQuit))}
	}

	switch {
	case m.comment.open:
		if m.comment.busy {
			return []footerGroup{group("Comment", hint{"", "working…"})}
		}
		if m.comment.answerCheckpoint != "" {
			if m.comment.previewing {
				return []footerGroup{group("Action answer", hint{"↑/↓", "scroll"}, hint{"Ctrl-P", "edit"}, bind("", keys.ComposerEditor), hint{"Ctrl-S", "save answer"}, hint{"Esc", "cancel"})}
			}
			return []footerGroup{group("Action answer", hint{"Ctrl-P", "preview"}, bind("", keys.ComposerEditor), hint{"Ctrl-S", "save answer"}, hint{"Esc", "cancel"})}
		}
		if m.comment.proposalEditCheckpoint != "" {
			if m.comment.previewing {
				return []footerGroup{group("Proposal edit", hint{"↑/↓", "scroll"}, hint{"Ctrl-P", "edit"}, bind("", keys.ComposerEditor), hint{"Ctrl-S", "save edit"}, hint{"Esc", "discard"})}
			}
			return []footerGroup{group("Proposal edit", hint{"Ctrl-P", "preview"}, bind("", keys.ComposerEditor), hint{"Ctrl-S", "save edit"}, hint{"Esc", "discard"})}
		}
		if m.comment.rejectionCheckpoint != "" {
			if m.comment.previewing {
				return []footerGroup{group("Rejection", hint{"↑/↓", "scroll"}, hint{"Ctrl-P", "edit"}, bind("$EDITOR reason", keys.ComposerEditor, keys.RejectEditor), hint{"Ctrl-S", "reject & dismiss"}, hint{"Esc", "cancel"})}
			}
			return []footerGroup{group("Rejection", hint{"Ctrl-P", "preview"}, bind("", keys.ComposerEditor), hint{"Ctrl-S", "reject & dismiss"}, hint{"Esc", "cancel"})}
		}
		action := "publish"
		if m.comment.close {
			action = "comment & close"
		}
		if m.comment.reopen {
			action = "comment & reopen"
		}
		if m.comment.previewing {
			editor := keys.CommentEditor
			if m.comment.close {
				editor = keys.CloseEditor
			}
			if m.comment.reopen {
				editor = keys.ReopenEditor
			}
			return []footerGroup{group("Comment", hint{"↑/↓", "scroll"}, hint{"Ctrl-P", "edit"}, bind("$EDITOR draft", keys.ComposerEditor, editor), hint{"Ctrl-S", action}, hint{"Esc", "discard"})}
		}
		if m.comment.reopen && len(m.comment.targets) > 1 {
			action = "review targets"
		}
		if m.comment.referenceActive {
			return []footerGroup{group("Comment", hint{"↑/↓ Ctrl-J/N/K/P", "choose reference"}, hint{"Enter/Tab", "insert"}, bind("", keys.ComposerEditor), hint{"Esc", "dismiss"}, hint{"Ctrl-S", action})}
		}
		return []footerGroup{group("Comment", hint{"Ctrl-P", "preview"}, bind("", keys.ComposerEditor), hint{"Ctrl-S", action}, hint{"Esc", "discard"})}
	case m.groups.open && m.groups.editing == "notes":
		if m.groups.busy {
			return []footerGroup{group("Member note", hint{"", "working…"})}
		}
		if m.groups.note.previewing {
			return []footerGroup{group("Member note", hint{"↑/↓", "scroll"}, hint{"Ctrl-P", "edit"}, bind("", keys.ComposerEditor), hint{"Ctrl-S", "save"}, hint{"Esc", "discard"})}
		}
		return []footerGroup{group("Member note", hint{"Ctrl-P", "preview"}, bind("", keys.ComposerEditor), hint{"Ctrl-S", "save"}, hint{"Esc", "discard"})}
	case m.groups.open && (m.groups.editing == "edit" || m.groups.editing == "new"):
		if m.groups.busy {
			return []footerGroup{group("Edit group", hint{"", "working…"})}
		}
		if m.groups.edit.previewing {
			return []footerGroup{group("Edit group", hint{"↑/↓", "scroll"}, hint{"Ctrl-P", "edit"}, bind("", keys.ComposerEditor), hint{"Ctrl-S", "save"}, hint{"Esc", "discard"})}
		}
		title := "Edit group"
		if m.groups.editing == "new" {
			title = "New group"
		}
		edit := group(title, hint{"Tab/Shift-Tab", "field"}, hint{"Ctrl-P", "preview"}, hint{"Ctrl-S", "save"}, hint{"Esc", "discard"})
		if m.onGroupStatusField() {
			edit.hints = append(edit.hints, hint{"←/→", "change status"})
		} else {
			edit.hints = append(edit.hints, bind("", keys.ComposerEditor))
		}
		return []footerGroup{edit}
	case m.confirmQuit:
		return []footerGroup{group("Quit", bind("discard drafts", keys.Quit), hint{"any key", "cancel"}), group("Navigation", bind("exit", keys.ForceQuit))}
	case m.lastError.open:
		return []footerGroup{group("Error", bind("scroll", keys.Down, keys.Up), bind("page", keys.HalfDown, keys.HalfUp)), group("Navigation", bind("close", keys.Back), bind("exit", keys.ForceQuit))}
	case m.settings.open && m.settings.defaults != nil:
		if m.settings.busy {
			return []footerGroup{group("Labels", hint{"", "working…"})}
		}
		return []footerGroup{group("Labels", hint{"↑/↓", "scroll"}, hint{"Ctrl-S", "confirm exact changes"}, hint{"Esc", "cancel"})}
	case m.settings.open && m.settings.editor != nil:
		if m.settings.busy {
			return []footerGroup{group("Settings", hint{"", "working…"})}
		}
		label := "Edit setting"
		if m.settings.editor.creating {
			label = "New setting"
		}
		save := "save"
		if m.settings.editor.row.kind == "label" {
			save = "preview GitHub change"
			if m.settings.editor.previewHash != "" {
				save = "confirm GitHub change"
			}
		}
		return []footerGroup{group(label, hint{"Tab/Shift-Tab", "field"}, hint{"Ctrl-P", "preview"}, bind("", keys.ComposerEditor), hint{"Ctrl-S", save}, hint{"Esc", "discard"}), group("Navigation", bind("exit", keys.ForceQuit))}
	case m.settings.open:
		if m.settings.busy {
			return []footerGroup{group("Settings", hint{"", "working…"})}
		}
		if m.settings.section == "" {
			return []footerGroup{group("Settings", hint{"j/k", "select"}, hint{"Enter", "open"}), group("Navigation", hint{"Esc", "back"}, hint{"q", "quit"})}
		}
		if m.settings.section == "automations" {
			if m.settings.selected == 1 {
				return []footerGroup{group("Automations", hint{"j/k", "select"}, hint{"Scoring", "planned"}, hint{"r", "refresh"}), group("Navigation", hint{"Esc", "back"}, hint{"q", "quit"})}
			}
			if m.settings.selected >= 2 {
				return []footerGroup{group("Automations", hint{"j/k", "select"}, hint{"Enter/Space", "toggle action mode"}, hint{"y", "copy agent prompt"}, hint{"r", "refresh"}), group("Navigation", hint{"Esc", "back"}, hint{"q", "quit"})}
			}
			return []footerGroup{group("Automations", hint{"j/k", "select"}, hint{"Enter/Space", "toggle Labeling"}, hint{"y", "copy agent prompt"}, hint{"r", "refresh"}), group("Navigation", hint{"Esc", "back"}, hint{"q", "quit"})}
		}
		settings := group("Settings", hint{"j/k", "select"}, hint{"n", "new"}, hint{"Enter/e", "edit"})
		if m.settings.section == "label" {
			settings.hints = append(settings.hints, hint{"r", "sync labels"}, hint{"i", "GitHub + defaults"}, hint{"I", "use local catalog"})
		}
		return []footerGroup{settings, group("Navigation", hint{"Esc", "back"}, hint{"q", "quit"})}
	case m.notificationPR.open:
		back := "back to Notifications"
		if m.notifications.actionReview != nil {
			back = "back to proposal"
		}
		read := group("Item", bind("previous tab", keys.TabPrev), bind("next tab", keys.TabNext), hint{"Tab/Shift-Tab", "tabs"}, hint{"1/2/3/4", "jump to tab"}, hint{"j/k/↑/↓", "scroll"}, hint{"Ctrl-D/U", "page"}, bind("", keys.Track))
		if it, ok := m.notificationActionItem(); ok {
			read.hints = append(read.hints, bind("", keys.Comment, keys.CommentEditor))
			if it.State == "open" {
				read.hints = append(read.hints, bind("", keys.Close, keys.CloseEditor))
			} else {
				read.hints = append(read.hints, bind("", keys.Reopen, keys.ReopenEditor))
			}
		}
		return []footerGroup{read, group("Navigation", hint{"Esc/h", back}, hint{"q", "quit"})}
	case m.attention.open:
		return []footerGroup{group("Comments", hint{"j/k/Tab", "select"}, hint{"Enter/l/→", "open PR or page"}), group("Navigation", hint{"Ctrl-D/U", "scroll"}, hint{"Esc/h", "back"})}
	case m.actionHistory.open:
		return []footerGroup{group("Explanations", hint{"j/k/Tab", "select"}, hint{"Enter/l/→", "open PR or page"}), group("Navigation", hint{"Ctrl-D/U", "scroll"}, hint{"Esc/h", "back"})}
	case m.notifications.open:
		if m.notifications.actionReview != nil {
			if m.notifications.notesOpen {
				return []footerGroup{group("Local notes", hint{"j/k Ctrl-D/U", "scroll"}, hint{"m or Esc", "close"}), group("Navigation", hint{"q", "quit"})}
			}
			if m.notifications.actionReview.busy {
				return []footerGroup{group("Action proposal", hint{"", "checking exact action…"})}
			}
			row := m.notifications.actionReview.row
			name := "Action proposal"
			if row.Status == "executed" || row.Status == "uncertain" {
				name = "Action outcome"
			}
			action := group(name, hint{"j/k Ctrl-D/U", "scroll"}, hint{"Enter", "view item"}, hint{"y", "copy for agent"}, hint{"w", "track comments"})
			if context := m.notifications.actionReview.context; context != nil {
				if hasExpandableProposalNotes(&autoCloseContext{ItemContext: context.ItemContext}) {
					action.hints = append(action.hints, hint{"m", "full notes"})
				}
				if context.ItemContext.Pagination.Offset > 0 || context.ItemContext.Pagination.Next != nil {
					action.hints = append(action.hints, hint{"[/]", "context pages"})
				}
			}
			if row.Status == "pending" {
				action.hints = append(action.hints, hint{"e", "edit"}, hint{"d", "reject"})
				if row.DecisionQuestion != "" {
					action.hints = append(action.hints, hint{"r", "answer question"})
				}
			} else {
				action.hints = append(action.hints, hint{"d", "dismiss"})
			}
			if row.Active && m.notifications.actionReview.context != nil && m.notifications.actionReview.context.Current {
				if row.DecisionQuestion != "" && row.DecisionResolution == nil {
					return []footerGroup{action, group("Navigation", hint{"Esc", "back"}, hint{"q", "quit"})}
				}
				label := "review exact action"
				if m.notifications.actionReview.approval != "" {
					label = "approve and publish"
				}
				action.hints = append(action.hints, hint{"a", label})
			}
			return []footerGroup{action, group("Navigation", hint{"Esc", "back"}, hint{"q", "quit"})}
		}
		if m.notifications.review != nil {
			if m.notifications.reviewBusy {
				return []footerGroup{group("PR closures", hint{"", "publishing approved comments and closures…"})}
			}
			proposal := group("Proposal", hint{"j/k Ctrl-D/U", "scroll"}, hint{m.notifications.reviewKey, "approve and execute"})
			return []footerGroup{proposal, group("Navigation", bind("dataset", keys.Corpus), hint{"Esc/h", "back to Notifications"}, hint{"q", "quit"})}
		}
		if m.notifications.reviewBusy {
			return []footerGroup{group("Notifications", hint{"", "preparing exact review…"}), group("Navigation", bind("dataset", keys.Corpus), hint{"Esc/h", "back"}, hint{"q", "quit"})}
		}
		notifications := group("Notifications", hint{"j/k/Tab", "select"}, hint{"Enter/l/→", "open"})
		choices := m.notifications.choices()
		if len(choices) > 0 && m.notifications.selected < len(choices) {
			choice := choices[m.notifications.selected]
			if choice.kind == "item" {
				if choice.suggestion >= 0 {
					notifications.hints = append(notifications.hints, hint{"y", "prepare exact action"})
				}
				if choice.actionProposal >= 0 {
					row := m.notifications.actions.Rows[choice.actionProposal]
					if row.Status == "pending" && row.Active {
						notifications.hints = append(notifications.hints, hint{"a", "review action"}, hint{"e", "edit proposal"})
						if row.Kind == "pr" && row.Operation == "close" {
							notifications.hints = append(notifications.hints, hint{"Space", "tick closure"}, hint{"A", "review all closures"})
						}
					}
					notifications.hints = append(notifications.hints, hint{"y", "copy for agent"})
				}
				if choice.attention >= 0 {
					notifications.hints = append(notifications.hints, hint{"t", "saved discussion"})
				}
				if choice.closure >= 0 {
					notifications.hints = append(notifications.hints, hint{"i", "closure history"})
				}
				if choice.tracked < 0 {
					notifications.hints = append(notifications.hints, hint{"w", "track comments"})
				}
				if len(m.notifications.itemOperations(m.repo, choice, "view")) > 0 {
					notifications.hints = append(notifications.hints, hint{"v", "viewed"})
				}
			}
		}
		dismiss := "dismiss"
		if len(choices) > 0 && m.notifications.selected < len(choices) {
			choice := choices[m.notifications.selected]
			if choice.kind == "item" && choice.actionProposal >= 0 && m.notifications.actions.Rows[choice.actionProposal].Status == "pending" {
				dismiss = "reject & dismiss"
				notifications.hints = append(notifications.hints, bind("", keys.RejectEditor))
			}
		}
		if len(choices) > 0 && m.notifications.selected < len(choices) {
			choice := choices[m.notifications.selected]
			if choice.kind == "item" && (choice.actionProposal >= 0 || choice.tracked >= 0 || choice.attention >= 0 || choice.closure >= 0) {
				notifications.hints = append(notifications.hints, hint{"d", dismiss})
			}
		}
		return []footerGroup{notifications, group("Navigation", bind("dataset", keys.Corpus), hint{"Esc/h", "back"}, hint{"q", "quit"})}
	case m.corpus.open:
		if m.corpus.busy {
			return []footerGroup{group("Download", hint{"o", "automatic ON/OFF"}, bind("stop", keys.CorpusStop)), group("Navigation", bind("close", keys.Back, keys.Corpus))}
		}
		return []footerGroup{group("Dataset", hint{"o", "automatic ON/OFF"}, hint{"y", "copy agent prompt"}, hint{"u", "size"}), group("Navigation", bind("scroll", keys.Down, keys.Up), bind("close", keys.Back, keys.Corpus))}
	case m.themePicker.open && m.themePicker.searching:
		return []footerGroup{group("Search", hint{"type", "theme name"}, hint{"↑/↓", "preview"}, bind("keep", keys.Enter), bind("clear", keys.Cancel)), group("Navigation", bind("exit", keys.ForceQuit))}
	case m.themePicker.open:
		return []footerGroup{group("Theme", bind("preview", keys.Down, keys.Up), bind("ends", keys.Top, keys.Bottom), bind("", keys.Search), bind("apply", keys.Enter)), group("Navigation", bind("cancel", keys.Back), bind("", keys.Quit), bind("exit", keys.ForceQuit))}
	case (m.groups.open && m.groups.busy) || (m.dups.open && m.dups.busy):
		return []footerGroup{group("Navigation", bind("", keys.ForceQuit))}
	case m.batches.open && m.batches.editing && m.batches.pick.open:
		return []footerGroup{group("List", bind("move", keys.ValueNext, keys.ValuePrev), bind("pick", keys.Confirm, keys.OpenList), bind("", keys.CloseList)), group("Navigation", bind("", keys.Quit), bind("exit", keys.ForceQuit))}
	case m.groups.open && m.groups.editing != "":
		edit := group("Edit", bind("fields", keys.FieldNext, keys.FieldPrev), bind("next/save", keys.Confirm), bind("save", keys.FormSubmit))
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
		return []footerGroup{group("Edit", bind("fields", keys.FieldNext, keys.FieldPrev), bind("save & approve", keys.Confirm)), group("Navigation", bind("back", keys.Cancel), bind("exit", keys.ForceQuit))}
	case m.groups.open:
		return m.groupFooter()
	case m.briefs.open:
		if m.briefs.markPreview != nil {
			if m.briefs.busy {
				return []footerGroup{group("Briefs", hint{"", "marking read…"})}
			}
			return []footerGroup{group("Briefs", hint{"d", "confirm renames"}, hint{"j/k", "scroll"}), group("Navigation", hint{"Esc", "cancel"})}
		}
		if m.briefs.busy {
			status := "reading…"
			if len(m.briefs.markIDs) > 0 {
				status = "checking renames…"
			}
			return []footerGroup{group("Briefs", hint{"", status}), group("Navigation", hint{"Esc", "back"}, hint{"q", "quit"})}
		}
		if m.briefs.reading {
			if m.briefs.cards {
				return []footerGroup{group("Brief items", hint{"j/k", "select"}, hint{"Enter/l", "open"}), group("Navigation", hint{"Esc/h", "brief"}, hint{"q", "quit"})}
			}
			return []footerGroup{group("Brief", hint{"j/k", "scroll"}, hint{"Ctrl-D/U", "page"}, hint{"Enter/l", "items"}, hint{"r", "reload"}), group("Navigation", hint{"Esc/h", "back"}, hint{"q", "quit"})}
		}
		return []footerGroup{group("Briefs", hint{"H/L", "section"}, hint{"j/k", "select"}, hint{"Enter", "read"}, hint{"Space", "tick"}, hint{"d", "mark read"}, hint{"r", "reload"}), group("Navigation", hint{"Esc/h", "back"}, hint{"q", "quit"})}
	case m.dups.open:
		return []footerGroup{
			group("Duplicate", bind("mark as duplicate", keys.MarkDup), bind("", keys.SwapDup), bind("", keys.Tick), bind("group ticked", keys.Group)),
			group("Item", bind("read", keys.Enter), bind("", keys.Save), bind("", keys.SaveApprove), bind("", keys.Open), bind("", keys.Reopen, keys.ReopenEditor)),
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
	items := group("Items", bind("", keys.Approve), bind("", keys.Reopen, keys.ReopenEditor), bind("", keys.Undo), bind("", keys.Group), bind("", keys.QuickGroup), bind("", keys.MarkDup), bind("", keys.Track), bind("", keys.Yank, keys.YankAll))
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
	score := "Score —"
	if it, ok := m.findItem(m.detail.key); ok {
		score = it.ScoreLabel()
		if value, assessed := it.ScoreValue(); assessed {
			score = lipgloss.NewStyle().Foreground(lipgloss.Color(itemScoreColor(value))).Bold(true).Render(score)
		}
	}
	item := group("Item", hint{"", score}, bind("", keys.Save), bind("", keys.SaveApprove), bind("", keys.Approve), bind("", keys.Undo), bind("", keys.MarkDup), bind("", keys.Track), bind("", keys.QuickGroup), bind("", keys.Open), bind("", keys.Comment, keys.CommentEditor), bind("", keys.Close, keys.CloseEditor), bind("", keys.Reopen, keys.ReopenEditor), bind("", keys.Yank))
	tabAction := "tabs"
	if m.sideBySide() {
		tabAction = "tabs / form"
	}
	read := group("Read", bind(tabAction, keys.TabPrev, keys.TabNext), bind("", keys.TabJump), bind("expand", keys.Enter), bind("scroll", keys.Down, keys.Up), bind("ends", keys.Top, keys.Bottom), bind("page", keys.HalfDown, keys.HalfUp))
	if m.detail.AnySectionFull() {
		return []footerGroup{item, read, m.menusGroup(), m.navigationGroup()}
	}

	if m.form.pick.open {
		if m.form.focused == fieldLabels {
			return []footerGroup{group("Labels", hint{"j/k", "select"}, hint{"Space", "toggle"}, hint{"Enter", "done"}, hint{"Esc", "cancel"}), group("Navigation", bind("", keys.Quit), bind("exit", keys.ForceQuit))}
		}
		return []footerGroup{group("List", bind("move", keys.ValueNext, keys.ValuePrev), bind("pick", keys.Confirm, keys.OpenList), bind("", keys.CloseList)), group("Navigation", bind("", keys.Quit), bind("exit", keys.ForceQuit))}
	}

	fields := group("Fields", bind("fields", keys.FieldNext, keys.FieldPrev), hint{"h/l", "content / form"})
	if m.form.focused == fieldLabels {
		fields = group("Labels", bind("fields", keys.FieldNext, keys.FieldPrev), hint{"l/→", "choose labels"})
		read = group("Read", bind("focus tab", keys.TabPrev), bind("", keys.TabJump))
	}
	if formFieldIsEnum(m.form.focused) && m.form.focused != fieldLabels {
		fields = group("Fields", bind("fields", keys.FieldNext, keys.FieldPrev, keys.ChoiceNext, keys.ChoicePrev), hint{"h", "content"}, bind("change", keys.ValueNext, keys.ValuePrev), bind("", keys.OpenList), bind("next", keys.Confirm))
		if m.form.focused == fieldAction {
			fields.hints = append(fields.hints, hint{"Backspace", "clear action"})
		}
		read = group("Read", bind("focus tab", keys.TabPrev), bind("", keys.TabJump))
	}

	return []footerGroup{item, fields, read, m.menusGroup(), m.navigationGroup()}
}

func (m model) groupFooter() []footerGroup {
	var groups []footerGroup
	g := m.selectedGroup()
	if m.groups.detail {
		if g != nil && len(g.Members) > 0 {
			groups = append(groups, group("Member", bind("open", keys.Enter), bind("notes", keys.Edit), bind("", keys.Tick), bind("", keys.Reopen, keys.ReopenEditor), bind("remove", keys.Delete)), group("Context", bind("scroll notes", keys.HalfDown, keys.HalfUp)))
			groups = append(groups, group("Handoff", bind("selected", keys.Yank), bind("all", keys.YankAll)))
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
	if m.needsResize() {
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
