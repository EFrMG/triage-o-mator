package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/lucasb-eyer/go-colorful"
)

// dropdown is the list a choice field opens with l / →: j/k (or the arrows) move, Enter or l / → pick, Esc or h / ← close it unchanged.
type dropdown struct {
	open    bool
	options []string
	cursor  int
}

const dropdownRows = 8

func (d *dropdown) Open(options []string, current int) {
	d.open, d.options = true, options
	d.cursor = maxInt(minInt(current, len(options)-1), 0)
}

// Key handles a key while the list is open. picked reports Enter (or l / →) on an option (d.cursor holds it); the list closes on Enter or Esc.
func (d *dropdown) Key(msg tea.KeyMsg) (picked bool) {
	last := len(d.options) - 1
	switch {
	case key.Matches(msg, keys.Confirm), key.Matches(msg, keys.OpenList):
		d.open = false

		return true
	case key.Matches(msg, keys.CloseList):
		d.open = false
	case key.Matches(msg, keys.ValueNext):
		d.cursor = minInt(d.cursor+1, last)
	case key.Matches(msg, keys.ValuePrev):
		d.cursor = maxInt(d.cursor-1, 0)
	case key.Matches(msg, keys.Top):
		d.cursor = 0
	case key.Matches(msg, keys.Bottom):
		d.cursor = last
	}

	return false
}

// View draws the open list in a soft-accent box, at most dropdownRows options tall, scrolled to keep the cursor in view.
func (d dropdown) View(width int) string {
	start := maxInt(minInt(d.cursor-dropdownRows/2, len(d.options)-dropdownRows), 0)
	end := minInt(start+dropdownRows, len(d.options))
	inner := maxInt(width-4, 4)
	rows := make([]string, 0, end-start+1)
	for i := start; i < end; i++ {
		row := "  " + d.options[i]
		style := lipgloss.NewStyle().Width(inner)
		if i == d.cursor {
			row = "› " + d.options[i]
			style = style.Foreground(lipgloss.Color(currentTheme.Accent)).Background(lipgloss.Color(currentTheme.Selection)).Bold(true)
		}

		rows = append(rows, style.Render(ansi.Truncate(row, inner, "…")))
	}

	if len(d.options) > dropdownRows {
		rows = append(rows, mutedText(fmt.Sprintf("  %d of %d", d.cursor+1, len(d.options))))
	}

	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(softAccent()).Render(strings.Join(rows, "\n"))
}

// softAccent is the accent blended halfway toward the background: terminals have no opacity, so this is how a border is drawn "at 50%".
func softAccent() lipgloss.Color {
	a, errA := colorful.Hex(currentTheme.Accent)
	b, errB := colorful.Hex(currentTheme.Background)
	if errA != nil || errB != nil {
		return lipgloss.Color(currentTheme.Border)
	}

	return lipgloss.Color(a.BlendLab(b, 0.5).Clamped().Hex())
}
