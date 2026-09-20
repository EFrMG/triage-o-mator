package main

import "github.com/charmbracelet/bubbles/key"

// Every key the TUI understands is defined here, once. Handlers match against these bindings and the footer renders its hints from them, so the two can't drift apart. The scheme (docs/keybindings.md): one key means one thing on every screen, a capital letter is the bigger version of the same action, destructive and bulk actions need a second press of the same key, and item actions work on whatever item is in front of you.
type keyMap struct {
	// Global: any screen, except while typing in a text field.
	Help        key.Binding
	Theme       key.Binding
	Refresh     key.Binding
	RefreshFull key.Binding
	Quit        key.Binding
	ForceQuit   key.Binding
	Back        key.Binding

	// Navigation.
	Up       key.Binding
	Down     key.Binding
	Top      key.Binding
	Bottom   key.Binding
	HalfDown key.Binding
	HalfUp   key.Binding
	Enter    key.Binding
	Forward  key.Binding
	TabPrev  key.Binding
	TabNext  key.Binding
	TabJump  key.Binding
	Tick     key.Binding
	Search   key.Binding
	// ErrorDetails (!) shows the last failure in full.
	ErrorDetails key.Binding

	// Item actions: the open item, or in a list the ticked items (else the hovered one).
	Save    key.Binding
	Approve key.Binding
	MarkDup key.Binding
	// SwapDup (M) is the one capital that isn't a bigger m: on the Duplicates screen it swaps the two sides, making the hovered candidate the original, so duplicates can be marked in either direction.
	SwapDup    key.Binding
	Group      key.Binding
	QuickGroup key.Binding
	Open       key.Binding
	Undo       key.Binding
	Comment    key.Binding
	Yank       key.Binding
	YankAll    key.Binding

	// Batches and groups.
	New    key.Binding
	Edit   key.Binding
	Delete key.Binding
	// DeleteAll (D) is the bigger d: in Possible Duplicates it clears every handled pair, where d clears the hovered one.
	DeleteAll  key.Binding
	ApplyAll   key.Binding
	Export     key.Binding
	ExportFull key.Binding

	// Forms and editors. On a choice field j/k (or the arrows) change the value and l / → open the list of all values, where Enter or l / → pick and h / ← (or Esc) close; Tab / Shift-Tab and Enter move between fields.
	FieldNext key.Binding
	FieldPrev key.Binding
	ValueNext key.Binding
	ValuePrev key.Binding
	// ChoiceNext / ChoicePrev (J / K) move between fields like Tab / Shift-Tab, but only from a choice field: in a text field they're letters.
	ChoiceNext key.Binding
	ChoicePrev key.Binding
	OpenList   key.Binding
	CloseList  key.Binding
	Confirm    key.Binding
	FormSubmit key.Binding
	Cancel     key.Binding
}

var keys = keyMap{
	Help:        key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
	Theme:       key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "themes")),
	Refresh:     key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "fetch")),
	RefreshFull: key.NewBinding(key.WithKeys("R"), key.WithHelp("R", "full fetch")),
	Quit:        key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
	// ForceQuit always exits immediately, even while typing or mid-confirmation; q is just a letter in a text field, so Quit alone must not be the only way out.
	ForceQuit: key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("Ctrl-C", "force quit")),
	// Back is Esc or h (or ←, as → opens). Not Ctrl-H: some terminals send it for Backspace, and inside a text field it deletes a character. Text fields take their keys before these, so ← and → still move the cursor there.
	Back: key.NewBinding(key.WithKeys("esc", "h", "left"), key.WithHelp("Esc/h", "back")),

	Up:           key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("k", "up")),
	Down:         key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("j", "down")),
	Top:          key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g", "top")),
	Bottom:       key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G", "bottom")),
	HalfDown:     key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("Ctrl-D", "half page down")),
	HalfUp:       key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("Ctrl-U", "half page up")),
	Enter:        key.NewBinding(key.WithKeys("enter"), key.WithHelp("Enter", "open")),
	Forward:      key.NewBinding(key.WithKeys("l", "right"), key.WithHelp("l", "open")),
	TabPrev:      key.NewBinding(key.WithKeys("H"), key.WithHelp("H", "previous tab")),
	TabNext:      key.NewBinding(key.WithKeys("L"), key.WithHelp("L", "next tab")),
	TabJump:      key.NewBinding(key.WithKeys("1", "2", "3", "4"), key.WithHelp("1-4", "jump to tab")),
	Tick:         key.NewBinding(key.WithKeys(" "), key.WithHelp("Space", "tick")),
	Search:       key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "search")),
	ErrorDetails: key.NewBinding(key.WithKeys("!"), key.WithHelp("!", "last error")),

	Save:       key.NewBinding(key.WithKeys("ctrl+s"), key.WithHelp("Ctrl-S", "save")),
	Approve:    key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "approve")),
	MarkDup:    key.NewBinding(key.WithKeys("m"), key.WithHelp("m", "duplicates")),
	SwapDup:    key.NewBinding(key.WithKeys("M"), key.WithHelp("M", "make it the original")),
	Group:      key.NewBinding(key.WithKeys("b"), key.WithHelp("b", "groups")),
	QuickGroup: key.NewBinding(key.WithKeys("B"), key.WithHelp("B", "last group")),
	Open:       key.NewBinding(key.WithKeys("o"), key.WithHelp("o", "GitHub")),
	Undo:       key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "undo")),
	Comment:    key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "comment (not yet available)")),
	// Yank / YankAll take context out of the app for an agent to read: y what is in front of you, Y the whole screen's worth, in the same relationship as every other lowercase/uppercase pair.
	Yank:    key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "take context")),
	YankAll: key.NewBinding(key.WithKeys("Y"), key.WithHelp("Y", "take all of it")),

	New:        key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "new")),
	Edit:       key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "edit")),
	Delete:     key.NewBinding(key.WithKeys("d"), key.WithHelp("d", "delete")),
	DeleteAll:  key.NewBinding(key.WithKeys("D"), key.WithHelp("D", "clear handled")),
	ApplyAll:   key.NewBinding(key.WithKeys("A"), key.WithHelp("A", "apply proposals")),
	Export:     key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "export")),
	ExportFull: key.NewBinding(key.WithKeys("X"), key.WithHelp("X", "full export")),

	FieldNext:  key.NewBinding(key.WithKeys("tab"), key.WithHelp("Tab", "next field")),
	FieldPrev:  key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("Shift-Tab", "previous field")),
	ValueNext:  key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("j", "next value")),
	ValuePrev:  key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("k", "previous value")),
	ChoiceNext: key.NewBinding(key.WithKeys("J"), key.WithHelp("J", "next field")),
	ChoicePrev: key.NewBinding(key.WithKeys("K"), key.WithHelp("K", "previous field")),
	OpenList:   key.NewBinding(key.WithKeys("l", "right"), key.WithHelp("l", "list")),
	CloseList:  key.NewBinding(key.WithKeys("esc", "h", "left"), key.WithHelp("Esc/h", "close")),
	Confirm:    key.NewBinding(key.WithKeys("enter"), key.WithHelp("Enter", "next field")),
	FormSubmit: key.NewBinding(key.WithKeys("ctrl+s"), key.WithHelp("Ctrl-S", "submit")),
	Cancel:     key.NewBinding(key.WithKeys("esc"), key.WithHelp("Esc", "cancel")),
}
