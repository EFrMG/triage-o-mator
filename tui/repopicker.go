package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Switch Repo lists everything already triaged from this machine: the repos this install has data for, then the repos of every other install bin/install-to has registered, so moving between a repository and its neighbours is a pick instead of a path.
// The text field is both a filter over that list and a way out of it: a value starting with "/" is another install's path, and an owner/repo that isn't listed starts that repo in the current install.

type repoInfo struct {
	name    string // owner/repo
	root    string // the install it lives in
	items   int
	fetched time.Time
}

// knownRepos finds every data/<owner>/<repo>/ledger.jsonl in one install, with its item count and last fetch, most recently fetched first.
func knownRepos(root string) []repoInfo {
	paths, _ := filepath.Glob(filepath.Join(root, "data", "*", "*", "ledger.jsonl"))
	var out []repoInfo
	for _, path := range paths {
		dir := filepath.Dir(path)
		name := filepath.Base(filepath.Dir(dir)) + "/" + filepath.Base(dir)
		if !validRepo(name) {
			continue
		}

		info := repoInfo{name: name, root: root}
		if data, err := os.ReadFile(path); err == nil {
			info.items = bytes.Count(data, []byte("\n"))
		}

		var meta struct {
			FetchedAt string `json:"fetched_at"`
		}

		if data, err := os.ReadFile(filepath.Join(dir, "raw", "fetch_meta.json")); err == nil && json.Unmarshal(data, &meta) == nil {
			info.fetched, _ = time.Parse(time.RFC3339, meta.FetchedAt)
		}

		out = append(out, info)
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].fetched.After(out[j].fetched) })

	return out
}

// registeredInstalls is every install bin/install-to has recorded for this user, most recent first, dropping any that is no longer there.
func registeredInstalls() []string {
	configHome := os.Getenv("XDG_CONFIG_HOME")
	if configHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil
		}

		configHome = filepath.Join(home, ".config")
	}

	data, err := os.ReadFile(filepath.Join(configHome, "triage-o-mator", "installs.json"))
	if err != nil {
		return nil
	}

	var registry struct {
		Installs []struct {
			Path      string `json:"path"`
			UpdatedAt string `json:"updated_at"`
		} `json:"installs"`
	}

	if json.Unmarshal(data, &registry) != nil {
		return nil
	}

	var out []string
	for i := len(registry.Installs) - 1; i >= 0; i-- {
		if path := registry.Installs[i].Path; isInstall(path) {
			out = append(out, path)
		}
	}

	return out
}

// everyKnownRepo is what the picker lists: this install's repos first, then every other registered install's, each repo once.
func everyKnownRepo(current string) []repoInfo {
	var out []repoInfo
	if current != "" {
		out = knownRepos(current)
	}

	seen := map[string]bool{current: true}
	for _, root := range registeredInstalls() {
		if seen[root] {
			continue
		}

		seen[root] = true
		out = append(out, knownRepos(root)...)
	}

	return out
}

// matchingRepos narrows the list as the picker's text field is typed in, on the repo name and on the install path, ignoring case. A query that is a path, or a valid owner/repo, is not a filter: those are handled on Enter.
func matchingRepos(all []repoInfo, query string) []repoInfo {
	query = strings.TrimSpace(query)
	if query == "" || strings.HasPrefix(query, "/") {
		return all
	}

	var out []repoInfo
	for _, info := range all {
		if matchesSearch(info.name+" "+info.root, query) {
			out = append(out, info)
		}
	}

	return out
}

func ago(t time.Time) string {
	if t.IsZero() {
		return "never fetched"
	}

	switch d := time.Since(t); {
	case d < time.Hour:
		return fmt.Sprintf("fetched %dm ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("fetched %dh ago", int(d.Hours()))
	default:
		return fmt.Sprintf("fetched %dd ago", int(d.Hours()/24))
	}
}

// moveRepoPick moves the highlighted known repo with the arrow keys, or with j/k once a repo is highlighted (in the text field they're letters); -1 means "use what's typed". Tab / Shift-Tab switch between the text field and the known repos, keeping the last one highlighted there.
func (m *model) moveRepoPick(msg tea.KeyMsg) bool {
	picked := m.repoPick >= 0
	switch {
	case msg.Type == tea.KeyDown, picked && key.Matches(msg, keys.Down):
		m.repoPick = minInt(m.repoPick+1, len(m.repoRecent)-1)
	case msg.Type == tea.KeyUp, picked && key.Matches(msg, keys.Up):
		m.repoPick = maxInt(m.repoPick-1, -1)
	case key.Matches(msg, keys.FieldNext), key.Matches(msg, keys.FieldPrev):
		if m.repoPick >= 0 {
			m.repoLastPick, m.repoPick = m.repoPick, -1
		} else if len(m.repoRecent) > 0 {
			m.repoPick = minInt(m.repoLastPick, len(m.repoRecent)-1)
		}
	default:
		return false
	}

	return true
}

// installPlanView shows exactly what bin/install-to --dry-run printed, so what is about to change in a repository is on screen before a key can change it.
func (m model) installPlanView() string {
	w := m.menuWidth()
	mode := "tracked in that repository: its ledger, groups and reports are committed there, and a short section is added to its AGENTS.md"
	other := "s: plan it as a solo install instead, kept out of that repository's history"
	if m.installing.solo {
		mode = "solo: kept out of that repository's history through .git/info/exclude, and none of its tracked files are touched"
		other = "s: plan it as a tracked install instead, committed to that repository"
	}

	head := []string{inset(titleBar("Install triage-o-mator", m.installing.path, w)), ""}
	if m.installing.busy {
		return strings.Join(append(head, inset(mutedText("Working…"))), "\n")
	}

	// What the plan means and the question about it always stay on screen, so they are measured first and the plan scrolls in what is left.
	tail := []string{
		"",
		inset(mutedText(wrapText("Mode: "+mode, w))),
		"",
		inset(wrapText("Nothing has been written yet. Enter makes exactly the changes listed above; Esc leaves that repository as it is.", w)),
		"",
		inset(mutedText(wrapText(other, w))),
	}

	// Wrapped here rather than by the panel, so a window over these lines is a window over the rows they will really take.
	var plan []string
	for _, line := range strings.Split(strings.TrimRight(m.installing.plan, "\n"), "\n") {
		plan = append(plan, strings.Split(wrapText(line, w), "\n")...)
	}

	if m.installing.plan == "" {
		plan = []string{"(the script printed nothing)"}
	}

	window := maxInt(m.mainHeight()-rowsIn(head)-rowsIn(tail)-1, 3) // the spare row is the "N more" line
	offset := minInt(m.installing.offset, maxInt(len(plan)-window, 0))
	shown := plan[offset:minInt(offset+window, len(plan))]

	rows := append(head, inset(strings.Join(shown, "\n")))
	if len(plan) > len(shown) {
		rows = append(rows, inset(mutedText(fmt.Sprintf("%d more line(s) · j/k scrolls", len(plan)-len(shown)))))
	}

	return strings.Join(append(rows, tail...), "\n")
}

// rowsIn is how many terminal rows a set of rendered rows takes, counting the lines inside each.
func rowsIn(rows []string) int {
	total := 0
	for _, row := range rows {
		total += strings.Count(row, "\n") + 1
	}

	return total
}

func (m model) repoPromptView() string {
	if m.installing.path != "" {
		return m.installPlanView()
	}

	w := m.menuWidth()
	accent := softAccent()
	if m.repoPick < 0 {
		accent = focusedBorderColor
	}

	input := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(0, 1).Width(minInt(w-2, 60)).Render(m.repoInput.View())
	subtitle := "current: " + m.repo
	if m.noInstall() {
		subtitle = "no install open"
	}

	rows := []string{inset(titleBar("Switch repo", subtitle, w)), "", inset(input), ""}
	if len(m.repoRecent) > 0 {
		cards := make([][2]string, len(m.repoRecent))
		for i, r := range m.repoRecent {
			first := r.name
			second := fmt.Sprintf("%d items · %s", r.items, ago(r.fetched))
			switch {
			case r.root == m.installRoot && r.name == m.repo:
				first += " · current"
			case r.root != m.installRoot:
				second += " · " + r.root
			}

			cards[i] = [2]string{first, second}
		}

		rows = append(rows, inset(mutedText("Tab, then j/k to pick one; type to filter, or an owner/repo to start it here")), cardList(cards, m.repoPick, m.cardWidth(), m.mainHeight()-len(rows)-4))
	} else if m.noInstall() {
		rows = append(rows, inset(mutedText("No installs recorded on this machine yet. Type the path of a repository to install into one.")))
	} else {
		rows = append(rows, inset(mutedText("Nothing matches. Esc to go back.")))
	}

	if m.noInstall() {
		return strings.Join(append(rows, "", inset(mutedText(wrapText("This is a triage-o-mator checkout, not an install: the triage itself lives in the repositories you triage. Pick one of the installs above, or type the path of a repository: one that has an install opens it, and one that doesn't is offered a plan of what installing there would change, before anything is written. This repository's own path works too, and triages triage-o-mator's own backlog. Esc quits.", w)))), "\n")
	}

	rows = append(rows, "", inset(mutedText(wrapText("Each repo keeps its own ledger, batches, groups and exports in its install's data/<owner>/<repo>/, so switching back finds everything as you left it. A repo in another install switches this session to that install; an absolute path opens one directly (bin/install-to creates them). A repo with no data yet starts with a full fetch.", w))))

	return strings.Join(rows, "\n")
}
