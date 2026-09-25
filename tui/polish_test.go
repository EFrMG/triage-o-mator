package main

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestRepaintKeepsThemeColorsAfterResets(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	old := currentTheme
	defer func() { currentTheme = old }()
	currentTheme = Theme{Background: "#eff1f5", Foreground: "#4c4f69"}

	base := themeBaseSequence()
	out := repaint(lipgloss.NewStyle().Bold(true).Render("x") + " y\nz")
	if base == "" || !strings.Contains(out, "\x1b[0m"+base+" y") || !strings.Contains(out, "\n"+base+"z") {
		t.Fatalf("theme colors should follow every reset and start every line: %q", out)
	}
}

func TestDiffUsesTheThemesExactColors(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	old := currentTheme
	defer func() { currentTheme = old; forgetRenderers() }()

	currentTheme = Theme{Background: "#1e1e2e", Foreground: "#cdd6f4", Muted: "#a6adc8", Accent: "#cba6f7", Success: "#a6e3a1", Error: "#f38ba8"}
	forgetRenderers()
	renderDiff("@@ -1,2 +1,2 @@\n context line\n-old line\n+new line\n", 60)

	// The renderers cache their colors, so a theme change has to reach them too: this is the second render, in the second theme.
	currentTheme = Theme{Background: "#fbf1c7", Foreground: "#3c3836", Muted: "#665c54", Accent: "#076678", Success: "#79740e", Error: "#9d0006"}
	forgetRenderers()
	out := renderDiff("@@ -1,2 +1,2 @@\n context line\n-old line\n+new line\n", 60)
	for _, want := range []string{"\x1b[38;2;60;56;54m context line", "\x1b[38;2;157;0;6m-old line", "\x1b[38;2;121;116;14m+new line"} {
		if !strings.Contains(out, want) {
			t.Fatalf("diff lines should use the current theme's colors, not the nearest 256-color palette entry nor the previous theme's: want %q in %q", want, out)
		}
	}
}

func TestReasonTakesTheNewThemeWhileUnfocused(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii)
	old := currentTheme
	defer func() { currentTheme = old }()
	currentTheme = Theme{Background: "#1e1e2e", Foreground: "#cdd6f4", Muted: "#a6adc8", Accent: "#cba6f7"}

	f := newDecisionForm(testTaxonomy())
	f.reason.SetValue("a reason")
	currentTheme = Theme{Background: "#fbf1c7", Foreground: "#3c3836", Muted: "#665c54", Accent: "#076678"}
	themeTextarea(&f.reason)
	copied := f
	out := copied.reason.View()
	if !strings.Contains(out, "48;2;251;241;199") || strings.Contains(out, "48;2;30;30;46") {
		t.Fatalf("the unfocused reason should draw in the new theme's background: %q", out)
	}
}

func TestHelpDuringQuitConfirmationKeepsIt(t *testing.T) {
	m := testPRModel()
	m.drafts[m.detail.key] = decisionSnapshot{}
	m = press(m, "q")
	m = press(m, "?")
	if !m.confirmQuit || !m.showHelp {
		t.Fatalf("? should show help and keep the quit pending: confirm %v help %v", m.confirmQuit, m.showHelp)
	}
}

func TestReasonGrowsWithItsWrappedText(t *testing.T) {
	f := newDecisionForm(testTaxonomy())
	f.reason.SetValue(strings.Repeat("word ", 30))
	f.SetWidth(13 + 30)
	if h := f.reason.Height(); h < 5 {
		t.Fatalf("150 characters at 30 columns should use the full 5 lines, got %d", h)
	}

	f.reason.SetValue("short")
	f.SetWidth(13 + 30)
	if h := f.reason.Height(); h != 1 {
		t.Fatalf("a short reason should take one line, got %d", h)
	}
}

func TestMovingAroundTheRepoPicker(t *testing.T) {
	root := batchFixture(t)
	writeLedger(t, root, "other/repo", ledgerFixtureRow(7, "only in other/repo"))
	m := batchModel(t, root)
	m.focus = FocusSidebar
	m.sidebar.selected = switchRepoIndex
	m = press(m, "enter")
	if len(m.repoRecent) != 2 {
		t.Fatalf("both repos with a ledger should be listed: %+v", m.repoRecent)
	}

	if m.repoPick != -1 || !strings.Contains(m.View(), "other/repo") {
		t.Fatal("Switch Repo should open on the text field, with the known repos listed below")
	}

	if m = press(m, "tab"); m.repoPick != 0 {
		t.Fatalf("Tab from the text field should highlight the first known repo: %d", m.repoPick)
	}

	m = press(m, "down")
	if m = press(m, "tab"); m.repoPick != -1 {
		t.Fatalf("Tab from the repos should go back to the text field: %d", m.repoPick)
	}

	if m = press(m, "shift+tab"); m.repoPick != 1 {
		t.Fatalf("Tab back to the repos should return to the last highlighted one: %d", m.repoPick)
	}

	if m = press(m, "k"); m.repoPick != 0 || strings.HasSuffix(m.repoInput.Value(), "k") {
		t.Fatalf("with a repo highlighted, k moves up the repos instead of typing: %d %q", m.repoPick, m.repoInput.Value())
	}

	if m = press(m, "j"); m.repoPick != 1 {
		t.Fatalf("with a repo highlighted, j moves down the repos: %d", m.repoPick)
	}

	m = press(m, "tab")
	if m = press(m, "x"); m.repoPick != -1 || !strings.HasSuffix(m.repoInput.Value(), "x") {
		t.Fatal("letters go into the text field and drop the highlighted repo")
	}

	if m = press(m, "j"); m.repoPick != -1 || !strings.HasSuffix(m.repoInput.Value(), "xj") {
		t.Fatal("in the text field j is a letter")
	}

	if len(m.repoRecent) != 0 {
		t.Fatalf("what is typed filters the listed repos: %+v", m.repoRecent)
	}

	m = press(m, "backspace")
	if m = press(m, "backspace"); len(m.repoRecent) != 2 {
		t.Fatalf("clearing the filter brings them back: %+v", m.repoRecent)
	}

	for m.repoPick < 0 || m.repoRecent[m.repoPick].name != "other/repo" {
		m = press(m, "down")
	}

	m = press(m, "enter")
	if m.repo != "other/repo" {
		t.Fatalf("Enter should switch to the highlighted repo: %s (%q)", m.repo, m.status)
	}
}

func TestArrowsMoveOpenAndGoBackLikeHJKL(t *testing.T) {
	m := batchModel(t, batchFixture(t))
	m.focus = FocusSidebar
	start := m.sidebar.selected
	if m = press(m, "j"); m.sidebar.selected <= start {
		t.Fatalf("j should move down the sidebar: %d then %d", start, m.sidebar.selected)
	}

	if m = press(m, "k"); m.sidebar.selected != start {
		t.Fatalf("k should move back up: %d, want %d", m.sidebar.selected, start)
	}

	if m = press(m, "down"); m.sidebar.selected <= start {
		t.Fatal("↓ should move like j")
	}

	if m = press(m, "up"); m.sidebar.selected != start {
		t.Fatal("↑ should move like k")
	}

	if m = press(m, "right"); m.focus == FocusSidebar {
		t.Fatal("→ should open the sidebar selection, like l")
	}

	if m = press(m, "left"); m.focus != FocusSidebar {
		t.Fatal("← should go back, like h")
	}
}

func TestMenuScreensStayInBounds(t *testing.T) {
	for _, size := range [][2]int{{60, 24}, {100, 30}, {160, 40}} {
		m := batchModel(t, batchFixture(t))
		m = send(m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		m, _ = openBatchesNow(t, m)
		assertBounds(t, m.View(), size[0], size[1])
		if size[0] >= 100 && !strings.Contains(m.View(), "Untriaged") {
			t.Fatal("Batches should keep the sidebar visible")
		}

		m = press(m, "n")
		m = press(m, "enter")
		m = press(m, "enter") // Kind's list
		assertBounds(t, m.View(), size[0], size[1])

		d := testPRModel()
		d = send(d, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		d.similar[d.detail.key] = []dupCandidate{{Number: 9, Kind: "pr", Title: strings.Repeat("similar ", 30), Score: 0.8}}
		d.dups = dupUI{open: true, source: d.detail.key, checked: map[Key]bool{}}
		assertBounds(t, d.View(), size[0], size[1])
	}
}

func TestItemListCardsSpanTheFullWidth(t *testing.T) {
	items := []Item{{Number: 1, Kind: "issue", Title: "first"}, {Number: 2, Kind: "issue", Title: "second"}}
	l := newItemList(items, "Items", 60, 20)
	l.Select(1)
	lines := strings.Split(ansi.Strip(l.View()), "\n")
	for i, line := range lines {
		if strings.Contains(line, "#2 second") {
			if i == 0 || lines[i-1] != strings.Repeat("▔", 60) || lines[i+2] != strings.Repeat("▁", 60) || !strings.HasPrefix(line, "  #2") {
				t.Fatalf("the hovered item should sit between rules spanning the full width, its text inset:\n%s", strings.Join(lines, "\n"))
			}

			return
		}
	}

	t.Fatalf("the item should be listed:\n%s", strings.Join(lines, "\n"))
}

func TestSidebarIsCenteredAndOverviewNoteWraps(t *testing.T) {
	m := batchModel(t, batchFixture(t))
	m = send(m, tea.WindowSizeMsg{Width: 110, Height: 30})
	lines := strings.Split(ansi.Strip(m.View()), "\n")
	if strings.Contains(lines[1], "Untriaged") {
		t.Fatal("the sidebar's entries should be centered vertically, not start at the top")
	}

	for _, line := range lines {
		if strings.Contains(line, "││AGENTS.md") {
			t.Fatalf("the overview's note should wrap inside its padding: %q", line)
		}
	}
}

func TestOverviewShowsEveryNextStepThatFits(t *testing.T) {
	m := batchModel(t, batchFixture(t))
	m = send(m, tea.WindowSizeMsg{Width: 110, Height: 36})
	for i := 0; i < 5; i++ {
		m.nextSteps = append(m.nextSteps, nextStep{Who: "agent", What: fmt.Sprintf("Step %d", i), Do: "do it"})
	}

	if view := ansi.Strip(m.View()); !strings.Contains(view, "Step 4") || strings.Contains(view, "more: bin/next") {
		t.Fatalf("steps that fit should all show, without a \"more\" line:\n%s", view)
	}

	for i := 5; i < 30; i++ {
		m.nextSteps = append(m.nextSteps, nextStep{Who: "agent", What: fmt.Sprintf("Step %d", i), Do: "do it"})
	}

	view := ansi.Strip(m.View())
	assertBounds(t, m.View(), 110, 36)
	if !strings.Contains(view, "Step 8") || !strings.Contains(view, "more: bin/next lists them all") || !strings.Contains(view, "AGENTS.md") {
		t.Fatalf("steps should fill the panel, then say how many more there are, above the note:\n%s", view)
	}
}

// Lists tell an agent's decisions from a person's: [agent] for "agent" and "agent:<name>", [human] for anyone else, and nothing on untriaged items.
func TestListMarksWhoDecided(t *testing.T) {
	cases := []struct {
		by, category, want string
	}{
		{"agent", "bug", "[agent]"},
		{"agent:someone", "bug", "[agent]"},
		{"someone", "bug", "[human]"},
		{"agentina", "bug", "[human]"},
		{"", "", ""},
	}

	for _, c := range cases {
		li := listItem{Item: Item{Number: 1, Kind: "issue", Title: "t", Category: c.category, Action: "label-only", TriagedBy: c.by}}
		if got := li.Mark().text; got != c.want {
			t.Errorf("triaged by %q: mark %q, want %q", c.by, got, c.want)
		}

		for _, selected := range []bool{false, true} {
			out := markedCard(li.Title(), li.Description(), li.Mark(), selected, 40)
			for i, line := range strings.Split(out, "\n") {
				if w := lipgloss.Width(line); w != 40 {
					t.Errorf("triaged by %q, selected %v: line %d is %d wide, want 40", c.by, selected, i, w)
				}
			}

			if !strings.Contains(ansi.Strip(out), c.want) {
				t.Errorf("triaged by %q: card doesn't show %q:\n%s", c.by, c.want, ansi.Strip(out))
			}
		}
	}
}
