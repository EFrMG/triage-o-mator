package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
)

type Theme struct {
	Name       string `json:"name,omitempty"`
	Background string `json:"background"`
	Foreground string `json:"foreground"`
	Muted      string `json:"muted"`
	Border     string `json:"border"`
	Accent     string `json:"accent"`
	Selection  string `json:"selection"`
	Success    string `json:"success"`
	Warning    string `json:"warning"`
	Error      string `json:"error"`
	// Info is the palette's blue, for [human] marks. Optional, so palettes written before it keep loading: it falls back to the accent.
	Info string `json:"info,omitempty"`
}

// Runtime palettes come exclusively from the repository's themes directory.
var themes = map[string]Theme{}
var themeName = "catppuccin-mocha"
var currentTheme Theme

var themeIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)
var themeColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func readThemes(root string) (map[string]Theme, error) {
	paths, err := filepath.Glob(filepath.Join(root, "themes", "*.json"))
	if err != nil {
		return nil, err
	}

	if len(paths) == 0 {
		return nil, fmt.Errorf("no theme files in %s", filepath.Join(root, "themes"))
	}

	catalog := make(map[string]Theme, len(paths))
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".json")
		if !themeIDPattern.MatchString(name) {
			return nil, fmt.Errorf("%s: theme filenames must use lowercase letters, digits, - or _", path)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}

		var theme Theme
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&theme); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}

		if err := decoder.Decode(new(any)); err != io.EOF {
			return nil, fmt.Errorf("%s: expected one JSON object", path)
		}

		roles := []struct{ name, value string }{{"background", theme.Background}, {"foreground", theme.Foreground}, {"muted", theme.Muted}, {"border", theme.Border}, {"accent", theme.Accent}, {"selection", theme.Selection}, {"success", theme.Success}, {"warning", theme.Warning}, {"error", theme.Error}}
		for _, role := range roles {
			if !themeColorPattern.MatchString(role.value) {
				return nil, fmt.Errorf("%s: %s must be a #RRGGBB color", path, role.name)
			}
		}

		if theme.Info == "" {
			theme.Info = theme.Accent
		} else if !themeColorPattern.MatchString(theme.Info) {
			return nil, fmt.Errorf("%s: info must be a #RRGGBB color", path)
		}

		if strings.TrimSpace(theme.Name) == "" {
			theme.Name = name
		}

		theme.Name = singleLine(theme.Name)
		catalog[name] = theme
	}

	return catalog, nil
}

func screenStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Foreground)).Background(lipgloss.Color(currentTheme.Background))
}

// themeBaseSequence is the escape sequence that sets the theme's text and background colors ("" without color support).
func themeBaseSequence() string {
	marker := "\x00"
	seq := screenStyle().Render(marker)
	if i := strings.Index(seq, marker); i > 0 {
		return seq[:i]
	}

	return ""
}

// repaint keeps the theme's colors under everything on screen. Every styled span ends in a reset, which falls back to the terminal's own colors: invisible when the theme matches the terminal (Catppuccin Mocha on a Mocha terminal), dark blocks on a light theme and seams on the rest. Re-applying the theme's colors after each reset, and at each line start, means no text can fall through.
func repaint(frame string) string {
	base := themeBaseSequence()
	if base == "" {
		return frame
	}

	for _, reset := range []string{"\x1b[0m", "\x1b[m", "\x1b[49m", "\x1b[39m"} {
		frame = strings.ReplaceAll(frame, reset, reset+base)
	}

	lines := strings.Split(frame, "\n")
	for i := range lines {
		lines[i] = base + lines[i]
	}

	return strings.Join(lines, "\n") + "\x1b[0m"
}

func setTheme(name string) error {
	theme, ok := themes[name]
	if !ok {
		return fmt.Errorf("unknown theme %q", name)
	}

	themeName, currentTheme = name, theme
	focusedBorderColor = lipgloss.Color(theme.Accent)
	blurredBorderColor = lipgloss.Color(theme.Border)

	return nil
}

func loadTheme(root string) error {
	catalog, err := readThemes(root)
	if err != nil {
		return err
	}

	themes = catalog
	name := os.Getenv("TRIAGE_THEME")
	if name == "" {
		data, err := os.ReadFile(filepath.Join(root, "config", "theme.local"))
		if err != nil && !os.IsNotExist(err) {
			return err
		}

		name = strings.TrimSpace(string(data))
	}

	if name == "" {
		name = "catppuccin-mocha"
	}

	return setTheme(name)
}

func themeInput(input *textinput.Model) {
	input.TextStyle = screenStyle()
	input.PromptStyle = screenStyle().Foreground(lipgloss.Color(currentTheme.Accent))
	input.PlaceholderStyle = screenStyle().Foreground(lipgloss.Color(currentTheme.Muted))
	input.Cursor.Style = screenStyle().Foreground(lipgloss.Color(currentTheme.Accent))
}

// themeTextarea styles a multi-line input like themeInput does single-line ones, without the default highlighted cursor line.
func themeTextarea(ta *textarea.Model) {
	for _, st := range []*textarea.Style{&ta.FocusedStyle, &ta.BlurredStyle} {
		st.Base = screenStyle()
		st.Text = screenStyle()
		st.CursorLine = screenStyle()
		st.Placeholder = screenStyle().Foreground(lipgloss.Color(currentTheme.Muted))
		st.EndOfBuffer = screenStyle().Foreground(lipgloss.Color(currentTheme.Background))
	}

	ta.Cursor.Style = screenStyle().Foreground(lipgloss.Color(currentTheme.Accent))

	// The textarea draws through a pointer to its active style, taken at its last Focus/Blur, so it points into an older copy of the model with the old colors: re-point it at the styles just set.
	if ta.Focused() {
		ta.Focus()
	} else {
		ta.Blur()
	}
}

func themeList(l *list.Model) {
	l.SetDelegate(cardDelegate{})
	l.Styles.Title = l.Styles.Title.Foreground(lipgloss.Color(currentTheme.Background)).Background(focusedBorderColor)
	l.Styles.StatusBar = l.Styles.StatusBar.Foreground(lipgloss.Color(currentTheme.Muted))
	l.Styles.StatusEmpty = l.Styles.StatusEmpty.Foreground(lipgloss.Color(currentTheme.Muted))
	l.Styles.StatusBarActiveFilter = l.Styles.StatusBarActiveFilter.Foreground(focusedBorderColor)
	l.Styles.StatusBarFilterCount = l.Styles.StatusBarFilterCount.Foreground(lipgloss.Color(currentTheme.Muted))
	l.Styles.NoItems = l.Styles.NoItems.Foreground(lipgloss.Color(currentTheme.Muted))
	l.Styles.PaginationStyle = l.Styles.PaginationStyle.Foreground(lipgloss.Color(currentTheme.Muted))
	l.Paginator.ActiveDot = screenStyle().Foreground(focusedBorderColor).Render("•")
	l.Paginator.InactiveDot = screenStyle().Foreground(lipgloss.Color(currentTheme.Muted)).Render("•")
	themeInput(&l.FilterInput)
}

// themePicker previews each theme as the cursor moves over it. / searches the names like the lists do (search.go): names holds every theme, shown() the ones matching query, and selected indexes shown().
type themePicker struct {
	open         bool
	names        []string
	selected     int
	originalName string
	original     Theme
	query        textinput.Model
	searching    bool
}

func (p themePicker) shown() []string {
	q := strings.TrimSpace(p.query.Value())
	if q == "" {
		return p.names
	}

	var out []string
	for _, name := range p.names {
		if matchesSearch(themes[name].Name+" "+name, q) {
			out = append(out, name)
		}
	}

	return out
}

func (m *model) restyle() {
	themeTextarea(&m.form.reason)
	forgetRenderers()
	themeInput(&m.repoInput)
	themeInput(&m.searchInput)
	themeInput(&m.themePicker.query)
	for i := range m.groups.inputs {
		themeInput(&m.groups.inputs[i])
	}

	if m.listReady {
		themeList(&m.list)
	}
}

func (m *model) openThemePicker() {
	catalog, err := readThemes(m.installRoot)
	if err != nil {
		m.status = "Theme error: " + err.Error()

		return
	}

	themes = catalog
	picker := themePicker{open: true, originalName: themeName, original: currentTheme, query: textinput.New()}
	picker.query.Prompt = ""
	picker.query.Placeholder = "theme name"
	picker.query.CharLimit = 100
	for name := range themes {
		picker.names = append(picker.names, name)
	}

	sort.Strings(picker.names)
	for i, name := range picker.names {
		if name == themeName {
			picker.selected = i
		}
	}

	m.themePicker = picker
	_ = setTheme(picker.names[picker.selected])
	m.restyle()
}

func (m model) handleThemeKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.themePicker.searching {
		return m.handleThemeSearchKey(msg)
	}

	shown := m.themePicker.shown()
	switch {
	case key.Matches(msg, keys.Back) && m.themePicker.query.Value() != "":
		// As in the lists, going back clears the search first.
		m.clearThemeSearch()
	case key.Matches(msg, keys.Back):
		themeName, currentTheme = m.themePicker.originalName, m.themePicker.original
		focusedBorderColor, blurredBorderColor = lipgloss.Color(currentTheme.Accent), lipgloss.Color(currentTheme.Border)
		m.restyle()
		m.themePicker.open = false
	case key.Matches(msg, keys.Search):
		m.themePicker.searching = true
		m.themePicker.query.Focus()
		m.themePicker.query.CursorEnd()
	case key.Matches(msg, keys.Down), key.Matches(msg, keys.Up), key.Matches(msg, keys.Top), key.Matches(msg, keys.Bottom):
		last := maxInt(len(shown)-1, 0)
		switch {
		case key.Matches(msg, keys.Down):
			m.themePicker.selected = minInt(m.themePicker.selected+1, last)
		case key.Matches(msg, keys.Up):
			m.themePicker.selected = maxInt(m.themePicker.selected-1, 0)
		case key.Matches(msg, keys.Top):
			m.themePicker.selected = 0
		default:
			m.themePicker.selected = last
		}

		m.previewSelectedTheme()
	case key.Matches(msg, keys.Enter):
		if len(shown) == 0 {
			return m, nil
		}

		if err := os.WriteFile(filepath.Join(m.installRoot, "config", "theme.local"), []byte(themeName+"\n"), 0o644); err != nil {
			m.status = "Theme preference error: " + err.Error()

			return m, nil
		}

		m.themePicker.open = false
		m.status = "Theme: " + currentTheme.Name
	case key.Matches(msg, keys.Help):
		m.showHelp = !m.showHelp
	case key.Matches(msg, keys.Quit):
		return m.requestQuit()
	}

	return m, nil
}

// handleThemeSearchKey takes every key while the query is being typed, like handleSearchKey: Enter keeps the matches, Esc clears the search, ↑/↓ move, anything else edits the query and previews the first match.
func (m model) handleThemeSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch {
	case key.Matches(msg, keys.Cancel):
		m.clearThemeSearch()
	case key.Matches(msg, keys.Enter):
		m.themePicker.searching = false
		m.themePicker.query.Blur()
		if strings.TrimSpace(m.themePicker.query.Value()) == "" {
			m.clearThemeSearch()
		}
	case msg.Type == tea.KeyUp:
		m.themePicker.selected = maxInt(m.themePicker.selected-1, 0)
		m.previewSelectedTheme()
	case msg.Type == tea.KeyDown:
		m.themePicker.selected = minInt(m.themePicker.selected+1, maxInt(len(m.themePicker.shown())-1, 0))
		m.previewSelectedTheme()
	default:
		before := m.themePicker.query.Value()
		var cmd tea.Cmd
		m.themePicker.query, cmd = m.themePicker.query.Update(msg)
		if m.themePicker.query.Value() != before {
			m.themePicker.selected = 0
			m.previewSelectedTheme()
		}

		return m, cmd
	}

	return m, nil
}

// previewSelectedTheme applies the theme under the cursor for a preview; with no match, the current preview stays.
func (m *model) previewSelectedTheme() {
	shown := m.themePicker.shown()
	if m.themePicker.selected >= len(shown) {
		return
	}

	_ = setTheme(shown[m.themePicker.selected])
	m.restyle()
}

// clearThemeSearch lists every theme again, with the cursor on the one being previewed.
func (m *model) clearThemeSearch() {
	m.themePicker.searching = false
	m.themePicker.query.Reset()
	m.themePicker.query.Blur()
	for i, name := range m.themePicker.names {
		if name == themeName {
			m.themePicker.selected = i
		}
	}
}

func (m model) themePickerView() string {
	w, h := m.width-4, m.mainHeight()
	shown := m.themePicker.shown()
	accent := lipgloss.NewStyle().Foreground(focusedBorderColor).Bold(true)
	search := mutedText(fmt.Sprintf("%d themes · / to search", len(shown)))
	switch {
	case m.themePicker.searching:
		search = accent.Render("/ ") + m.themePicker.query.View() + "  " + mutedText(fmt.Sprintf("%d of %d · Enter keeps, Esc clears", len(shown), len(m.themePicker.names)))
	case m.themePicker.query.Value() != "":
		search = accent.Render("/ "+strings.TrimSpace(m.themePicker.query.Value())) + "  " + mutedText(fmt.Sprintf("%d of %d · / edits, Esc clears", len(shown), len(m.themePicker.names)))
	}

	rows := []string{"Themes", "Preview: " + currentTheme.Name, ansi.Truncate(search, w, "…"), ""}
	if len(shown) == 0 {
		rows = append(rows, mutedText("No theme names match the search."))
	}

	available := maxInt(h-len(rows), 1)
	start := maxInt(m.themePicker.selected-available+1, 0)
	for i := start; i < len(shown) && i < start+available; i++ {
		name := shown[i]
		prefix := "  "
		if i == m.themePicker.selected {
			prefix = "> "
		}

		line := ansi.Truncate(prefix+themes[name].Name+" · "+name, w, "…")
		if i == m.themePicker.selected {
			line = screenStyle().Foreground(focusedBorderColor).Background(lipgloss.Color(currentTheme.Selection)).Render(line)
		}

		rows = append(rows, line)
	}

	return m.titled(panelStyle(true).Width(w+2).Height(h).Padding(0, 1).Render(strings.Join(rows, "\n")), true)
}
