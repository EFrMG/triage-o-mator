package main

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func send(m model, msg tea.Msg) model { next, _ := m.Update(msg); return next.(model) }
func press(m model, k string) model {
	special := map[string]tea.KeyType{"enter": tea.KeyEnter, "backspace": tea.KeyBackspace, "esc": tea.KeyEsc, "ctrl+h": tea.KeyCtrlH, "tab": tea.KeyTab, "shift+tab": tea.KeyShiftTab, "up": tea.KeyUp, "down": tea.KeyDown, "left": tea.KeyLeft, "right": tea.KeyRight, " ": tea.KeySpace}
	if typ, ok := special[k]; ok {
		return send(m, tea.KeyMsg{Type: typ})
	}

	return send(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
}
func testPRModel() model {
	items := []Item{{Kind: "pr", Number: 1, Title: strings.Repeat("long title ", 20), State: "open"}}
	m := newModel("/tmp", "owner/repo", testTaxonomy(), "tester", items)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.activateTab(1)
	m.selectCurrentListItem()
	content := strings.Repeat("word 中文 👩‍💻 https://example.com/"+strings.Repeat("x", 120)+"\n", 30)

	return send(m, enrichedMsg{key: items[0].Key(), data: EnrichedItem{Body: content, CommentBodies: []string{content}, DiffText: content}})
}
func assertBounds(t *testing.T, view string, w, h int) {
	t.Helper()
	if got := lipgloss.Height(view); got > h {
		t.Fatalf("height %d > %d\n%s", got, h, view)
	}

	for _, line := range strings.Split(view, "\n") {
		if got := ansi.StringWidth(line); got > w {
			t.Fatalf("width %d > %d: %q", got, w, line)
		}
	}
}
func TestLayoutAllTabsAndResize(t *testing.T) {
	for _, size := range [][2]int{{60, 24}, {80, 24}, {100, 30}, {190, 50}, {40, 12}} {
		for _, full := range []bool{false, true} {
			for tab := 0; tab < 3; tab++ {
				t.Run(fmt.Sprintf("%dx%d/full=%v/tab%d", size[0], size[1], full, tab), func(t *testing.T) {
					m := testPRModel()
					m.detail.JumpSection(tab)
					m.detail.full = full
					m = send(m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
					assertBounds(t, m.View(), size[0], size[1])
					if size[0] >= 60 {
						if !full && !strings.Contains(m.View(), "reason") {
							t.Fatalf("decision form lost:\n%s", m.View())
						}

						if full && strings.Contains(m.View(), "confidence") {
							t.Fatalf("a full-screen tab should hide the form:\n%s", m.View())
						}

						assertBounds(t, m.detail.View(), m.detail.width, m.detail.height)
					}

					m = press(m, "esc")
					assertBounds(t, m.View(), size[0], size[1])
					m = press(m, "esc")
					assertBounds(t, m.View(), size[0], size[1])
				})
			}
		}
	}
}

func TestWrapAndScrollRetainsAllText(t *testing.T) {
	m := testPRModel()
	s := &m.detail.sections[0]
	if len(strings.Split(s.viewport.View(), "\n")) < 2 {
		t.Fatal("body not wrapped")
	}

	m.detail.GotoBottom()
	if s.viewport.YOffset == 0 {
		t.Fatal("cannot scroll wrapped content")
	}

	m = send(m, tea.WindowSizeMsg{Width: 60, Height: 24})
	m.detail.GotoBottom()
	if !m.detail.sections[0].viewport.AtBottom() {
		t.Fatal("cannot reach bottom after resize")
	}

	raw := strings.Repeat("a", 300) + "中文"
	wrapped := wrapText(raw, 20)
	if strings.ReplaceAll(wrapped, "\n", "") != raw {
		t.Fatal("wrapping lost text")
	}

	assertBounds(t, wrapped, 20, 20)
}
func TestBackNavigationAndFullSectionCycling(t *testing.T) {
	for _, back := range []string{"esc", "h"} {
		m := testPRModel()
		if strings.Contains(m.View(), "Untriaged Issues") {
			t.Fatal("sidebar still visible in full-screen item")
		}

		m = press(m, "enter") // Body full.
		m = press(m, "L")
		if !m.detail.full || m.detail.active != 1 {
			t.Fatal("full screen should carry over to the next tab")
		}

		if back == "h" {
			// Inside the reason field h is a letter; Esc is the way out of it.
			m = press(m, "enter")     // leave full screen so the form (and its reason field) is back
			m = press(m, "shift+tab") // from the content, Shift-Tab wraps to the reason field
			m = press(m, "h")
			if m.focus != FocusDetail || m.form.Reason() != "h" {
				t.Fatalf("h in the reason field should be typed, not go back: focus %v, reason %q", m.focus, m.form.Reason())
			}

			m.form.focused = fieldContent
		} else {
			m.form.focused = fieldReason
		}

		m = press(m, back)
		if m.focus != FocusList {
			t.Fatal("back did not return to list")
		}

		m = press(m, back)
		if m.focus != FocusSidebar {
			t.Fatal("back did not return to sidebar")
		}

		m = press(m, back)
		if m.listReady {
			t.Fatal("sidebar back did not return to overview")
		}
	}
}
func TestGroupNavigationAndEditorKeys(t *testing.T) {
	m := testPRModel()
	m.groups = groupUI{open: true, records: []Group{{ID: "test", Title: "Group", Members: []GroupMember{{Kind: "pr", Number: 1}}}}}
	m = press(m, "enter")
	m = press(m, "enter")
	if m.groups.open || m.focus != FocusDetail {
		t.Fatal("group item did not open")
	}

	m = press(m, "esc")
	if !m.groups.open || !m.groups.detail {
		t.Fatal("item back lost group context")
	}

	m = press(m, "n")
	for _, k := range []string{"q", "r", "a", "t", "b"} {
		m = press(m, k)
	}

	if m.groups.inputs[0].Value() != "qratb" {
		t.Fatal("global keys intercepted group text")
	}

	for i := 0; i < 20; i++ {
		m = press(m, "tab")
	}

	assertBounds(t, m.View(), 100, 30)
}
func TestThemePalettes(t *testing.T) {
	preserveTheme(t)
	catalog, err := readThemes("..")
	if err != nil {
		t.Fatal(err)
	}

	themes = catalog
	for name, theme := range themes {
		if err := setTheme(name); err != nil {
			t.Fatal(err)
		}

		for _, color := range []string{theme.Background, theme.Foreground, theme.Muted, theme.Border, theme.Accent, theme.Selection, theme.Success, theme.Warning, theme.Error} {
			if len(color) != 7 || color[0] != '#' {
				t.Fatalf("invalid color in %s", name)
			}
		}
	}

	if themes["tokyo-night-day"].Background == themes["tokyo-night-night"].Background {
		t.Fatal("day theme is not light")
	}

	if err := setTheme("nonexistent"); err == nil {
		t.Fatal("unknown theme accepted")
	}
}

func TestNarrowSidebarBackShowsOverview(t *testing.T) {
	m := testPRModel()
	m = send(m, tea.WindowSizeMsg{Width: 60, Height: 24})
	for i := 0; i < 3; i++ {
		m = press(m, "esc")
	}

	if !strings.Contains(m.View(), "Next steps") {
		t.Fatal("overview inaccessible on narrow terminal")
	}

	m = press(m, "j")
	if !strings.Contains(m.View(), "Untriaged Issues") {
		t.Fatal("sidebar inaccessible from overview")
	}
}
