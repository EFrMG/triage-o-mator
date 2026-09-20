package main

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// The overview (the main panel before a view is picked) shows where the backlog stands and what's worth doing next, from bin/next (offline, read-only), refreshed after every sync.

type nextStep struct {
	Who  string `json:"who"`
	What string `json:"what"`
	Do   string `json:"do"`
	Why  string `json:"why"`
}

type nextLoadedMsg struct {
	repo  string
	steps []nextStep
	err   error
}

func nextCmd(root, repo string) tea.Cmd {
	return func() tea.Msg {
		out, err := runScript(root, "next", "--json")
		if err != nil {
			return nextLoadedMsg{repo: repo, err: err}
		}

		var parsed struct {
			Suggestions []nextStep `json:"suggestions"`
		}

		if err := json.Unmarshal([]byte(out), &parsed); err != nil {
			return nextLoadedMsg{repo: repo, err: err}
		}

		return nextLoadedMsg{repo: repo, steps: parsed.Suggestions}
	}
}

// progressBar draws n of total as a bar width cells wide, in the accent color.
func progressBar(n, total, width int) string {
	filled := 0
	if total > 0 {
		filled = n * width / total
		if n > 0 && filled == 0 {
			filled = 1
		}
	}

	return lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Accent)).Render(strings.Repeat("█", filled)) +
		lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Border)).Render(strings.Repeat("░", width-filled))
}

func (m model) overviewView() string {
	open, triaged, reviewed := 0, 0, 0
	for _, it := range m.items {
		if it.State != "open" {
			continue
		}

		open++
		if !it.Untriaged() {
			triaged++
		}

		if it.Reviewed {
			reviewed++
		}
	}

	pct := func(n int) float64 {
		if open == 0 {
			return 0
		}

		return 100 * float64(n) / float64(open)
	}

	width := maxInt(m.mainAreaWidth()-4, 20)
	bar := minInt(40, maxInt(width-32, 10))
	heading := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Accent)).Bold(true)
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", ansi.Truncate(titleBar(m.repo, "", width)+"  "+fmt.Sprintf("%d items · %d open · you: %s", len(m.items), open, m.reviewer), width, "…"))
	fmt.Fprintf(&b, "%-10s %s %5d  %s\n\n", "Triaged", progressBar(triaged, open, bar), triaged, mutedText(fmt.Sprintf("%.0f%%", pct(triaged))))
	fmt.Fprintf(&b, "%-10s %s %5d  %s\n\n", "Reviewed", progressBar(reviewed, open, bar), reviewed, mutedText(fmt.Sprintf("%.0f%%", pct(reviewed))))
	fmt.Fprintf(&b, "%-10s %s\n", "Backlog", mutedText(fmt.Sprintf("%d untriaged, %d awaiting review", open-triaged, triaged-reviewed)))
	if len(m.drafts) > 0 {
		fmt.Fprintf(&b, "\n%s\n", lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Warning)).Render(fmt.Sprintf("Your unsaved decisions this session: %d", len(m.drafts))))
	}

	fmt.Fprintf(&b, "\n%s\n\n", heading.Render("Next steps"))
	switch {
	case m.nextErr != nil:
		fmt.Fprintln(&b, mutedText(wrapText("Couldn't work out the next steps: "+friendlyError(m.nextErr)+" (! for details)", width)))
	case m.nextSteps == nil:
		fmt.Fprintln(&b, mutedText("Working out what's next…"))
	case len(m.nextSteps) == 0:
		fmt.Fprintln(&b, mutedText("Nothing pending."))
	}

	tag := map[string]lipgloss.Style{
		"agent": lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Warning)).Bold(true),
		"human": lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Info)).Bold(true),
	}

	// As many steps (two lines each) as fit above the closing note; only then "…and N more".
	note := mutedText(wrapText("[agent] steps an agent can do for you (ask it in plain words, see AGENTS.md); [human] steps need you.", width))
	room := m.mainHeight() - strings.Count(b.String(), "\n") - 1 - lipgloss.Height(note)
	shown := len(m.nextSteps)
	if 2*shown > room {
		shown = maxInt((room-1)/2, 0)
	}

	for i, step := range m.nextSteps {
		if i == shown {
			fmt.Fprintln(&b, mutedText(fmt.Sprintf("…and %d more: bin/next lists them all", len(m.nextSteps)-shown)))

			break
		}

		label := tag[step.Who].Render(fmt.Sprintf("%-5s", step.Who))
		fmt.Fprintf(&b, "%s %s\n      %s\n", label, ansi.Truncate(step.What, width-6, "…"), mutedText(ansi.Truncate(step.Do, width-6, "…")))
	}

	fmt.Fprintf(&b, "\n%s", note)

	return b.String()
}
