package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func preserveTheme(t *testing.T) {
	t.Helper()
	catalog, name, theme := themes, themeName, currentTheme
	t.Cleanup(func() {
		themes, themeName, currentTheme = catalog, name, theme
		focusedBorderColor = lipgloss.Color(theme.Accent)
		blurredBorderColor = lipgloss.Color(theme.Border)
	})
}

func themeFixture(t *testing.T) string {
	t.Helper()
	preserveTheme(t)
	root := t.TempDir()

	for _, dir := range []string{"themes", "config"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	catalog, err := readThemes("..")
	if err != nil {
		t.Fatal(err)
	}

	themes = catalog
	if err := setTheme("catppuccin-mocha"); err != nil {
		t.Fatal(err)
	}

	for name, theme := range catalog {
		writeThemeFixture(t, root, name, theme)
	}

	return root
}

func writeThemeFixture(t *testing.T, root, name string, theme Theme) {
	t.Helper()
	data, err := json.Marshal(theme)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "themes", name+".json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCustomThemesAndValidation(t *testing.T) {
	root := themeFixture(t)
	custom := currentTheme
	custom.Name = "My palette"
	custom.Accent = "#abcdef"
	writeThemeFixture(t, root, "my-palette", custom)
	t.Setenv("TRIAGE_THEME", "my-palette")

	if err := loadTheme(root); err != nil {
		t.Fatal(err)
	}

	if themeName != "my-palette" || currentTheme.Accent != "#abcdef" {
		t.Fatal("custom theme was not loaded")
	}

	for name, body := range map[string]string{
		"missing-role":  `{"name":"Incomplete"}`,
		"bad-hex":       strings.Replace(string(mustJSON(t, custom)), "#abcdef", "red", 1),
		"unknown-field": strings.TrimSuffix(string(mustJSON(t, custom)), "}") + `,"accnet":"#ffffff"}`,
		"extra-json":    string(mustJSON(t, custom)) + ` {}`,
		"bad-info":      strings.TrimSuffix(string(mustJSON(t, custom)), "}") + `,"info":"blue"}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(root, "themes", "invalid.json")
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			defer os.Remove(path)
			if _, err := readThemes(root); err == nil || !strings.Contains(err.Error(), "invalid.json") {
				t.Fatalf("expected a useful validation error, got %v", err)
			}
		})
	}
}

// Every shipped palette has its own blue for [human] marks, apart from its accent so a [human] mark doesn't read as focus; a palette without one still loads, using its accent.
func TestInfoRoleFallsBackToAccent(t *testing.T) {
	root := themeFixture(t)
	for name, theme := range themes {
		if !themeColorPattern.MatchString(theme.Info) {
			t.Errorf("%s: shipped palette has no info color", name)
		}

		if strings.EqualFold(theme.Info, theme.Accent) {
			t.Errorf("%s: info is the accent, so [human] marks look focused", name)
		}
	}

	old := currentTheme
	old.Info = ""
	writeThemeFixture(t, root, "older", old)
	catalog, err := readThemes(root)
	if err != nil {
		t.Fatal(err)
	}

	if got := catalog["older"].Info; got != old.Accent {
		t.Fatalf("info without a value = %q, want the accent %q", got, old.Accent)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func TestThemePickerPreviewCancelApplyAndReload(t *testing.T) {
	root := themeFixture(t)
	t.Setenv("TRIAGE_THEME", "")
	if err := loadTheme(root); err != nil {
		t.Fatal(err)
	}

	original := currentTheme
	originalName := themeName
	m := testPRModel()
	m.installRoot = root
	m = press(m, "t")

	if !m.themePicker.open || len(m.themePicker.names) < 15 {
		t.Fatal("theme list did not open")
	}

	m = press(m, "G")
	if themeName == originalName {
		t.Fatal("selection was not previewed")
	}

	m = press(m, "esc")
	if currentTheme != original || themeName != originalName {
		t.Fatal("cancel did not restore original theme")
	}

	if _, err := os.Stat(filepath.Join(root, "config", "theme.local")); !os.IsNotExist(err) {
		t.Fatal("preview persisted before confirmation")
	}

	custom := original
	custom.Name = "Custom"
	writeThemeFixture(t, root, "zzz-custom", custom)
	m = press(m, "t")
	m = press(m, "G")
	m = press(m, "enter")

	if m.themePicker.open || themeName != "zzz-custom" {
		t.Fatal("new file not discovered/applied")
	}

	data, err := os.ReadFile(filepath.Join(root, "config", "theme.local"))
	if err != nil || strings.TrimSpace(string(data)) != "zzz-custom" {
		t.Fatalf("preference not saved: %q %v", data, err)
	}

	if m.focus != FocusDetail || m.form.dirty {
		t.Fatal("theme picker changed underlying item state")
	}

	m.groups.open = true
	m = press(m, "t")
	if !m.themePicker.open {
		t.Fatal("picker unavailable from groups")
	}

	m = press(m, "esc")
	if !m.groups.open {
		t.Fatal("picker lost group context")
	}
}

func TestFooterContextsAndScreenBounds(t *testing.T) {
	for _, width := range []int{60, 80, 100} {
		for _, context := range []string{"item", "expanded", "full", "reason", "group-list", "group-members", "group-form", "batch-list", "batch-form", "dups", "pairs", "repo", "picker", "quit"} {
			t.Run(fmt.Sprintf("%s/%d", context, width), func(t *testing.T) {
				m := testPRModel()
				m = send(m, tea.WindowSizeMsg{Width: width, Height: 24})
				m.form.dirty = true
				m.groups.records = []Group{{ID: "g", Title: "Related bugs", Members: []GroupMember{{Kind: "pr", Number: 1, Notes: strings.Repeat("context ", 30)}}}}

				switch context {
				case "expanded":
					m.detail.JumpSection(1)
				case "full":
					m.detail.full = true
				case "reason":
					m.form.focused = fieldReason
				case "group-list":
					m.groups.open = true
				case "group-members":
					m.groups.open = true
					m.groups.detail = true
					key := m.detail.key
					m.groups.sources = []Key{key}
				case "group-form":
					m.groups.open = true
					m.editGroup("edit")
				case "batch-list":
					m.batches.open = true
					m.batches.records = []batchRecord{{ID: "b20260101-000000", Keys: []Key{{Kind: "pr", Number: 1}}}}
				case "batch-form":
					m.batches.open = true
					m.startBatchForm()
				case "dups":
					m.similar[m.detail.key] = []dupCandidate{{Number: 9, Kind: "pr", Title: strings.Repeat("similar ", 20), Score: 0.8}}
					m.dups = dupUI{open: true, source: m.detail.key, checked: map[Key]bool{}}
				case "pairs":
					m.focus = FocusList
					m.openPairs()

					m.pairsLoaded = true
					m.pairs = []dupPair{{Score: 0.9, Item: dupCandidate{Kind: "pr", Number: 1}, Original: dupCandidate{Kind: "pr", Number: 1}}}
					m.refreshActiveList()
				case "repo":
					m.focus = FocusSidebar
					m.editingRepo = true
				case "picker":
					m.themePicker = themePicker{open: true, names: []string{"example"}}
				case "quit":
					m.confirmQuit = true
				}

				for _, help := range []bool{false, true} {
					m.showHelp = help
					m.layout()

					view := m.viewContent()
					assertBounds(t, view, width, 24)

					lines := strings.Split(ansi.Strip(view), "\n")
					if len(lines) != 24 || !strings.Contains(lines[len(lines)-1], "Navigation:") {
						t.Fatalf("the Navigation group is not anchored at the bottom:\n%s", view)
					}

					footerHeight := lipgloss.Height(m.footerView())
					if !help && width >= 100 && footerHeight > 2 {
						t.Fatalf("keys-only footer should fit in 2 rows at %d columns, took %d:\n%s", width, footerHeight, m.footerView())
					}

					statusLine := strings.TrimRight(lines[24-footerHeight-1], " ")
					wantHelp := "? help"
					if help {
						wantHelp = "? less"
					}

					if !strings.HasSuffix(statusLine, wantHelp) {
						t.Fatalf("status row should end with %q: %q", wantHelp, statusLine)
					}

					body := strings.Join(lines[:24-footerHeight-1], "\n")
					for _, leaked := range []string{"Navigation:", "Menus:", "? help", "? less"} {
						if strings.Contains(body, leaked) {
							t.Fatalf("footer text %q leaked into content", leaked)
						}
					}

					if context == "expanded" || context == "item" {
						if !strings.Contains(body, "reason") {
							t.Fatal("footer obscured decision form")
						}
					}
				}
			})
		}
	}
}

func groupFixture(t *testing.T) (string, Group) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	for _, dir := range []string{"bin", "config", "data/owner/repo"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	copyFixtureScripts(t, root, "group")

	if err := os.WriteFile(filepath.Join(root, MarkerName), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "config", "repo"), []byte("owner/repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(root, "data", "owner", "repo", "ledger.jsonl"), []byte("{\"kind\":\"pr\",\"number\":1}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := runScript(root, "group", "create", "--title", "Review group", "--by", "tester")
	if err != nil {
		t.Fatal(err)
	}

	var g Group
	if err := json.Unmarshal([]byte(out), &g); err != nil {
		t.Fatal(err)
	}

	return root, g
}

func TestEnterAddsAndQuickAddPreservesNotes(t *testing.T) {
	root, g := groupFixture(t)
	m := testPRModel()
	m.installRoot = root
	key := m.detail.key
	m.groups = groupUI{open: true, records: []Group{g}, sources: []Key{key}}
	m.editGroup("add")
	m.groups.inputs[0].SetValue("Preserve this evidence")
	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)

	if cmd == nil {
		t.Fatal("Enter did not submit addition")
	}

	m = send(m, cmd())
	if m.groups.editing != "" || m.lastGroupID != g.ID {
		t.Fatal("addition did not remember target group")
	}

	m.groups.open = false
	next, cmd = m.Update(tea.KeyPressMsg{Text: "B"})
	m = next.(model)
	if cmd == nil {
		t.Fatal("quick-add shortcut did not dispatch")
	}

	m = send(m, cmd())
	if !strings.Contains(m.status, "Already in group") {
		t.Fatal(m.status)
	}

	out, err := runScript(root, "group", "show", g.ID)
	if err != nil {
		t.Fatal(err)
	}

	var after Group
	if err := json.Unmarshal([]byte(out), &after); err != nil {
		t.Fatal(err)
	}

	if len(after.Members) != 1 || after.Members[0].Notes != "Preserve this evidence" || after.Revision != 2 {
		t.Fatal("quick-add rewrote existing membership")
	}

	// Remove the membership, then add again using the fresh on-disk revision.
	if _, err := runScript(root, "group", "remove", g.ID, "--kind", "pr", "--number", "1", "--by", "other"); err != nil {
		t.Fatal(err)
	}

	m = send(m, quickAddGroupCmd(root, g.ID, []Key{key}, "tester")())
	if m.lastGroup() == nil || len(m.lastGroup().Members) != 1 {
		t.Fatal("quick-add failed to reload and add membership")
	}

	if !strings.Contains(m.lastGroupLabel(), "Review group") {
		t.Fatal("last group absent from item context")
	}

	data, err := os.ReadFile(filepath.Join(root, "data", "owner", "repo", "ledger.jsonl"))
	if err != nil || string(data) != "{\"kind\":\"pr\",\"number\":1}\n" {
		t.Fatal("group shortcut changed item ledger")
	}
}
