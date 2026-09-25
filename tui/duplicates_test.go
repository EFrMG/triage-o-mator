package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// dupFixture is a throwaway repo root with the real bin/similar, bin/group, and bin/apply, where issue #1 and #2 have near-identical titles and #3 is unrelated.
func dupFixture(t *testing.T) string {
	t.Helper()
	root := batchFixture(t)
	for _, name := range []string{"similar", "_similar.py", "not-duplicate", "_notdupes.py", "group"} {
		data, err := os.ReadFile(filepath.Join("..", "bin", name))
		if err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(root, "bin", name), data, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	ledger := ledgerFixtureRow(1, "Waybar crashes on suspend with NVIDIA driver") + ledgerFixtureRow(2, "Waybar crash on suspend with the NVIDIA driver") + ledgerFixtureRow(3, "Font picker excludes monospace fonts")
	if err := os.WriteFile(filepath.Join(root, "data", "owner", "repo", "ledger.jsonl"), []byte(ledger), 0o644); err != nil {
		t.Fatal(err)
	}

	return root
}

// runCmd executes cmd and feeds every resulting message back into the model, following tea.Batch.
func runCmd(m model, cmd tea.Cmd) model {
	if cmd == nil {
		return m
	}

	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			m = runCmd(m, c)
		}

		return m
	case nil:
		return m
	default:
		next, c := m.Update(msg)

		return runCmd(next.(model), c)
	}
}

func openFirstUntriaged(t *testing.T, root string) model {
	t.Helper()
	m := batchModel(t, root)
	m.activateTab(0)
	m.list.Select(1) // #2, the newer of the two similar reports
	m = runCmd(m, m.selectCurrentListItem())

	return m
}

func TestDuplicatesHeaderCompareAndMark(t *testing.T) {
	root := dupFixture(t)
	m := openFirstUntriaged(t, root)
	if m.detail.key.Number != 2 {
		t.Fatalf("opened #%d, want #2", m.detail.key.Number)
	}

	if label := m.similarLabel(); !strings.Contains(label, "#1 ") || strings.Contains(label, "#3") {
		t.Fatalf("header should list #1 only: %q", label)
	}

	m = press(m, "m")
	if !m.dups.open || m.selectedDup() == nil || m.selectedDup().Number != 1 {
		t.Fatal("Duplicates screen did not open on #1")
	}

	m = runCmd(m, func() tea.Cmd { next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter}); m = next.(model); return cmd }())
	if m.dups.open || m.detail.key.Number != 1 {
		t.Fatal("Enter should open the candidate for reading")
	}

	m = press(m, "esc")
	if !m.dups.open {
		t.Fatal("Esc from the candidate should return to the Duplicates screen")
	}

	// m marks the selected candidate as a duplicate of the item on the top card, so it's the candidate's own decision that gets prefilled.
	m = press(m, "m")
	if m.dups.open || m.detail.key.Number != 1 {
		t.Fatalf("marking should open the candidate's decision, not #%d's", m.detail.key.Number)
	}

	// #1 is older than #2 here, so closing it says so rather than going along quietly.
	if !strings.Contains(m.status, "older than") {
		t.Fatalf("marking an older candidate should warn: %q", m.status)
	}

	if m = press(m, "esc"); !m.dups.open || m.selectedDup() == nil || m.selectedDup().Number != 1 {
		t.Fatal("Esc from the prefilled item should return to the comparison, on the same candidate")
	}

	// Esc from the comparison goes back to the item it was opened from, keeping the candidate's prefill as a draft.
	draft, kept := m.drafts[Key{Kind: "issue", Number: 1}]
	if m = press(m, "esc"); m.dups.open || m.detail.key.Number != 2 || !kept || !strings.Contains(draft.reason, "#2") {
		t.Fatalf("Esc should return to #2, #1's prefill kept as a draft: on #%d, draft %v", m.detail.key.Number, kept)
	}

	// Marking it again picks the draft back up, ready to save.
	m = press(m, "m")
	m = press(m, "m")
	if m.detail.key.Number != 1 || m.form.Category() != "duplicate" || m.form.Action() != "close-duplicate" || !strings.Contains(m.form.Reason(), "#2") || !m.form.dirty {
		t.Fatalf("form not prefilled as an unsaved duplicate of #2: #%d %s/%s %q", m.detail.key.Number, m.form.Category(), m.form.Action(), m.form.Reason())
	}

	if ledgerRow(t, root, 1)["category"] != "" {
		t.Fatal("marking must not write the ledger before Ctrl-S")
	}

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	if cmd == nil {
		t.Fatal("a prefilled duplicate should save on the first Ctrl-S")
	}

	runCmd(next.(model), cmd)
	if row := ledgerRow(t, root, 1); row["category"] != "duplicate" || row["reviewed"] == true {
		t.Fatalf("duplicate decision not saved as unreviewed: %v", row)
	}
}

func TestDuplicatesGroupChecked(t *testing.T) {
	root := dupFixture(t)
	m := openFirstUntriaged(t, root)
	before, _ := os.ReadFile(filepath.Join(root, "data", "owner", "repo", "ledger.jsonl"))
	m = press(m, "m")
	m = press(m, " ")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	m = runCmd(next.(model), cmd)
	if m.dups.busy || m.lastGroup() == nil {
		t.Fatalf("group not created: %q", m.status)
	}

	g := m.lastGroup()
	if len(g.Members) != 2 || !strings.Contains(g.Title, "#2") {
		t.Fatalf("group should hold #2 and #1: %+v", g)
	}

	out, err := runScript(root, "group", "show", g.ID)
	if err != nil {
		t.Fatal(err)
	}

	var saved Group
	if err := json.Unmarshal([]byte(out), &saved); err != nil || len(saved.Members) != 2 {
		t.Fatalf("group not persisted through bin/group: %s", out)
	}

	after, _ := os.ReadFile(filepath.Join(root, "data", "owner", "repo", "ledger.jsonl"))
	if string(before) != string(after) {
		t.Fatal("grouping duplicates changed the item ledger")
	}
}

func TestFullRefreshKey(t *testing.T) {
	m := newModel("/tmp", "owner/repo", testTaxonomy(), "tester", testItems())
	m = send(m, fetchSyncDoneMsg{}) // the startup fetch
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("R")})
	m = next.(model)
	if cmd == nil || !m.refreshing || !strings.Contains(m.status, "full") {
		t.Fatalf("R should start a full refetch: %q", m.status)
	}

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("r")})
	if cmd != nil {
		t.Fatal("a second refresh must not start while one is running")
	}
}

func TestPossibleDuplicatesViewComparesAndMarksResolvedPairs(t *testing.T) {
	root := dupFixture(t)
	m := batchModel(t, root)
	m.sidebar.selected = pairsIndex
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = runCmd(next.(model), cmd)
	if !m.activePairs || len(m.list.Items()) != 1 || m.sidebar.pairCount != 1 {
		t.Fatalf("expected one pair (#1 ↔ #2), got %d; title %q", len(m.list.Items()), m.list.Title)
	}

	if !strings.Contains(m.list.Items()[0].(pairListItem).Title(), "#1 ↔ #2") {
		t.Fatal("the older item, the one a comparison opens on, should lead the pair")
	}

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = runCmd(next.(model), cmd)
	if !m.dups.open || m.dups.source.Number != 1 || m.selectedDup() == nil || m.selectedDup().Number != 2 {
		t.Fatal("Enter should compare the pair on the older item, with the newer one selected")
	}

	m = press(m, "esc")
	if m.dups.open || m.focus != FocusList || !m.activePairs {
		t.Fatal("Esc should return to the Possible Duplicates list")
	}

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = runCmd(next.(model), cmd)
	m = press(m, "m")
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = runCmd(next.(model), cmd)
	if ledgerRow(t, root, 2)["category"] != "duplicate" {
		t.Fatal("marked duplicate was not saved")
	}

	// The save goes back to the comparison, still on the original, with the candidate just decided showing its new decision, so the others can be checked too.
	if !m.dups.open || m.dups.source.Number != 1 || m.selectedDup() == nil || m.selectedDup().Number != 2 || !strings.Contains(m.dupsView(), "triaged: duplicate/") {
		t.Fatal("the save should return to the comparison, showing the candidate's new decision")
	}

	// Back in the list, the pair stays for this sitting, marked handled, and leaves the count.
	m = press(m, "esc")
	if m.focus != FocusList || len(m.list.Items()) != 1 || !m.list.Items()[0].(pairListItem).handled || m.sidebar.pairCount != 0 {
		t.Fatalf("a resolved pair should stay listed, marked handled; %d listed, count %d", len(m.list.Items()), m.sidebar.pairCount)
	}

	// Opened again, the view lists only pairs still needing a look.
	m = press(m, "esc")
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = runCmd(next.(model), cmd)
	if !m.activePairs || len(m.list.Items()) != 0 {
		t.Fatalf("a pair resolved earlier should be gone when the view reopens; %d listed", len(m.list.Items()))
	}
}

// The original leads the Duplicates screen on its own card, with no similarity of its own: the cursor reaches it, Enter reads it, and Ctrl-S saves a decision without leaving the comparison.
func TestDuplicatesSourceCardLeadsAndSaves(t *testing.T) {
	root := dupFixture(t)
	m := batchModel(t, root)
	m.sidebar.selected = pairsIndex
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = runCmd(next.(model), cmd)
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = runCmd(next.(model), cmd)
	view := m.dupsView()
	original, candidate := strings.Index(view, "#1"), strings.Index(view, "#2")
	if original < 0 || candidate < original || !strings.Contains(view, "the original · opened") {
		t.Fatalf("the original should lead, named as such and dated:\n%s", view)
	}

	if strings.Count(view, "% · opened") != 1 {
		t.Fatalf("only the candidate should show a similarity:\n%s", view)
	}

	// k reaches the top card, and Enter reads that item rather than a candidate.
	if m = press(m, "k"); m.selectedDup() != nil || m.dups.selected != sourceRow {
		t.Fatal("k from the first candidate should move to the original's card")
	}

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = runCmd(next.(model), cmd)
	if m.dups.open || m.detail.key.Number != 1 {
		t.Fatalf("Enter on the top card should open the original, not a candidate: #%d", m.detail.key.Number)
	}

	if m = press(m, "esc"); !m.dups.open || m.dups.selected != sourceRow {
		t.Fatal("Esc should come back to the comparison, cursor kept")
	}

	// m and Space on the original's own card explain themselves instead of doing nothing.
	if !strings.Contains(press(m, "m").status, "This is the original") || !strings.Contains(press(m, " ").status, "item being compared") {
		t.Fatal("m and Space on the top card should say why they don't apply")
	}

	// A decision prefilled with m is saved from the comparison itself, which is where you are when it's ready.
	m = press(m, "j")
	m = press(m, "m")
	m = press(m, "esc")
	if _, ok := m.drafts[Key{Kind: "issue", Number: 2}]; !ok || !m.dups.open {
		t.Fatal("setup: Esc should keep the candidate's prefilled decision as a draft, back on the comparison")
	}

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = runCmd(next.(model), cmd)
	if row := ledgerRow(t, root, 2); row["category"] != "duplicate" || !m.dups.open {
		t.Fatalf("Ctrl-S on the comparison should save the marked candidate and stay: %v, open %v", row["category"], m.dups.open)
	}

	if !strings.Contains(m.dupsView(), "triaged: duplicate/") {
		t.Fatal("the candidate's card should show the decision just saved")
	}
}

// m works on whatever is hovered: a pair in the list opens its comparison, where m marks the hovered candidate a duplicate of the original on top.
func TestMarkFromHoveredPair(t *testing.T) {
	root := dupFixture(t)
	m := batchModel(t, root)
	m.sidebar.selected = pairsIndex
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = runCmd(next.(model), cmd)
	m = runCmd(m, func() tea.Cmd {
		n, c := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
		m = n.(model)
		return c
	}())
	if !m.dups.open || m.dups.source.Number != 1 || m.selectedDup() == nil || m.selectedDup().Number != 2 {
		t.Fatal("m on a hovered pair should open its comparison, on the original")
	}

	m = press(m, "m")
	if m.detail.key.Number != 2 || m.form.Category() != "duplicate" {
		t.Fatalf("m there should prefill the hovered candidate as a duplicate: #%d %s", m.detail.key.Number, m.form.Category())
	}
}

// M swaps the two sides, so a duplicate can be marked in either direction: the hovered candidate becomes the original, with the item that was on top among its candidates.
func TestSwapTheOriginalWithM(t *testing.T) {
	root := dupFixture(t)
	m := batchModel(t, root)
	m.sidebar.selected = pairsIndex
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = runCmd(next.(model), cmd)
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = runCmd(next.(model), cmd)
	if m.dups.source.Number != 1 || m.selectedDup().Number != 2 {
		t.Fatal("setup: the pair should open on #1 with #2 selected")
	}

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("M")})
	m = runCmd(next.(model), cmd)
	if m.dups.source.Number != 2 || m.selectedDup() == nil || m.selectedDup().Number != 1 {
		t.Fatalf("M should make #2 the original, with #1 selected under it: on #%d", m.dups.source.Number)
	}

	if !strings.Contains(m.dupsView(), "#2") || !strings.Contains(m.status, "original now") {
		t.Fatalf("the swap should say what it did: %q", m.status)
	}

	// Marking now runs the other way: #1 becomes a duplicate of #2.
	m = press(m, "m")
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = runCmd(next.(model), cmd)
	if ledgerRow(t, root, 1)["category"] != "duplicate" || ledgerRow(t, root, 2)["category"] != "" {
		t.Fatal("after the swap, m should close #1 as a duplicate of #2")
	}

	// M on the top card says why it doesn't apply, rather than swapping with itself.
	m = press(m, "k")
	if !strings.Contains(press(m, "M").status, "already the original") {
		t.Fatal("M on the top card should say why it doesn't apply")
	}
}

// d rules the hovered pair out for good (through bin/not-duplicate) and D clears every handled one from the list; neither touches the ledger.
func TestClearHandledPairs(t *testing.T) {
	root := dupFixture(t)
	m := batchModel(t, root)
	m.sidebar.selected = pairsIndex
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = runCmd(next.(model), cmd)
	if m = press(m, "D"); !strings.Contains(m.status, "Nothing to clear") {
		t.Fatalf("D with no handled pair should say so: %q", m.status)
	}

	// d rules the hovered pair out, on a second press, and that verdict is recorded for good.
	if m = press(m, "d"); !strings.Contains(m.status, "Press d again") {
		t.Fatalf("the first d should ask: %q", m.status)
	}

	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = runCmd(next.(model), cmd)
	if len(m.list.Items()) != 0 || !m.ruledOut("issue", 2, 1) {
		t.Fatalf("d d should rule the pair out: %d listed, %q", len(m.list.Items()), m.status)
	}

	verdicts, err := os.ReadFile(filepath.Join(root, "data", "owner", "repo", "not-duplicates.jsonl"))
	if err != nil || !strings.Contains(string(verdicts), `"a": 1`) || !strings.Contains(string(verdicts), `"by": "tester"`) {
		t.Fatalf("the verdict should be recorded with its author: %s (%v)", verdicts, err)
	}

	m = press(m, "esc")
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = runCmd(next.(model), cmd)
	if !m.activePairs || len(m.list.Items()) != 0 {
		t.Fatalf("a pair ruled out should stay off the list when the view reopens; %d listed", len(m.list.Items()))
	}

	// Taking the verdict back offers it again, so nothing is lost for good.
	if _, err := runScript(root, "not-duplicate", "--remove", "--key", "issue:1", "--key", "issue:2"); err != nil {
		t.Fatal(err)
	}

	m = press(m, "esc")
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = runCmd(next.(model), cmd)
	if len(m.list.Items()) != 1 {
		t.Fatalf("bin/not-duplicate --remove should bring the pair back; %d listed", len(m.list.Items()))
	}

	// Resolve the pair, which leaves it listed as handled.
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = runCmd(next.(model), cmd)
	m = press(m, "m")
	next, cmd = m.Update(tea.KeyMsg{Type: tea.KeyCtrlS})
	m = runCmd(next.(model), cmd)
	m = press(m, "esc")
	if len(m.list.Items()) != 1 || !m.list.Items()[0].(pairListItem).handled {
		t.Fatal("setup: the resolved pair should still be listed, marked handled")
	}

	if m = press(m, "D"); !strings.Contains(m.status, "Press D again") {
		t.Fatalf("the first D should ask: %q", m.status)
	}

	before, err := os.ReadFile(filepath.Join(root, "data", "owner", "repo", "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	if m = press(m, "D"); len(m.list.Items()) != 0 || !strings.Contains(m.status, "Cleared 1 handled pair") {
		t.Fatalf("the second D should clear it: %d listed, %q", len(m.list.Items()), m.status)
	}

	after, _ := os.ReadFile(filepath.Join(root, "data", "owner", "repo", "ledger.jsonl"))
	if string(before) != string(after) {
		t.Fatal("clearing handled pairs must not touch the ledger")
	}
}

// d on a comparison rules the hovered candidate out against the item on top, and the card says so from then on.
func TestRuleOutFromComparison(t *testing.T) {
	root := dupFixture(t)
	m := batchModel(t, root)
	m.activateTab(0)
	m = runCmd(m, func() tea.Cmd {
		n, c := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("m")})
		m = n.(model)
		return c
	}())
	if !m.dups.open || m.selectedDup() == nil {
		t.Fatal("setup: m should open the comparison on a candidate")
	}

	if m = press(m, "d"); !strings.Contains(m.status, "Press d again") {
		t.Fatalf("the first d should ask: %q", m.status)
	}

	// Only a run of d acts: any other key takes the question back. (With one candidate, j keeps the cursor where it is.)
	if m = press(m, "j"); m.dups.confirm != "" || m.selectedDup() == nil {
		t.Fatal("any other key should take the question back")
	}

	m = press(m, "d")
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("d")})
	m = runCmd(next.(model), cmd)
	candidate := m.selectedDup()
	if candidate == nil || !m.ruledOut(candidate.Kind, candidate.Number, m.dups.source.Number) {
		t.Fatalf("d d should record the verdict: %q", m.status)
	}

	if !strings.Contains(m.dupsView(), "ruled out") {
		t.Fatal("the candidate's card should say it's ruled out")
	}

	// It's a pair verdict, not a decision: neither item's triage changes.
	if row := ledgerRow(t, root, candidate.Number); row["category"] != "" {
		t.Fatalf("ruling a pair out must not decide either item: %v", row)
	}
}

func TestNewSuggestionsExcludeRecordedPairsAndCachedHeaderHonorsVerdicts(t *testing.T) {
	root := dupFixture(t)
	if _, err := runScript(root, "not-duplicate", "--key", "issue:1", "--key", "issue:2", "--by", "reviewer"); err != nil {
		t.Fatal(err)
	}

	key := Key{Kind: "issue", Number: 1}
	msg := similarCmd(root, "owner/repo", key)().(similarLoadedMsg)
	if msg.err != nil || len(msg.candidates) != 0 {
		t.Fatalf("a new title query must exclude the recorded pair: %+v", msg)
	}

	m := batchModel(t, root)
	m.detail.key = key
	m.similar[key] = []dupCandidate{{Kind: "issue", Number: 2, Score: 1}}
	m.notDuplicates = notDuplicatesCmd(root, "owner/repo")().(notDuplicatesMsg).pairs
	if strings.Contains(m.similarLabel(), "#2") {
		t.Fatalf("cached candidates must not advertise a settled pair: %s", m.similarLabel())
	}
}
