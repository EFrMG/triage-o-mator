package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// pressCmd presses k and runs whatever command it returns, feeding the result back in.
func pressCmd(t *testing.T, m model, k string) model {
	t.Helper()
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
	m = next.(model)
	if cmd == nil {
		t.Fatalf("%s returned no command; status %q", k, m.status)
	}

	return send(m, cmd())
}

func TestTickAndBulkApproveFromAList(t *testing.T) {
	root := batchFixture(t)
	for _, n := range []string{"1", "2"} {
		if _, err := runScript(root, "apply", "--number", n, "--kind", "issue", "--category", "bug", "--action", "label-only"); err != nil {
			t.Fatal(err)
		}
	}

	m := batchModel(t, root)
	m.activateTab(pendingReviewTab)
	m = press(m, " ")
	m = press(m, " ")
	if len(m.ticked) != 2 || !strings.HasSuffix(m.list.Title, "· 2 ticked") {
		t.Fatalf("two Spaces should tick two items: %v, title %q", m.ticked, m.list.Title)
	}

	if li := m.list.Items()[0].(listItem); !li.ticked || !strings.HasPrefix(li.Title(), "✓ ") {
		t.Fatalf("a ticked item should show its mark: %q", li.Title())
	}

	m = press(m, "a")
	if m.listConfirm != "a" || !strings.Contains(m.status, "2 ticked items") {
		t.Fatalf("a on ticked items should ask first: %q", m.status)
	}

	m = pressCmd(t, m, "a")
	for _, n := range []int{1, 2} {
		if row := ledgerRow(t, root, n); row["reviewed"] != true || row["reviewed_by"] != "tester" {
			t.Fatalf("#%d should be approved by the reviewer: %v", n, row)
		}
	}

	if len(m.ticked) != 0 || strings.Contains(m.list.Title, "ticked") || !strings.Contains(m.status, "Approved 2") {
		t.Fatalf("a finished bulk action should clear the ticks: %v %q %q", m.ticked, m.list.Title, m.status)
	}
}

func TestTicksBelongToTheListOnScreen(t *testing.T) {
	m := batchModel(t, batchFixture(t))
	m.activateTab(0)
	m = press(m, " ")
	m.refreshActiveList() // a save or sync rebuilds the list
	if li := m.list.Items()[0].(listItem); !li.ticked {
		t.Fatal("a rebuilt list should keep its ticks")
	}

	m.activateTab(allItemsTab)
	if len(m.ticked) != 0 {
		t.Fatal("opening another list should clear the ticks")
	}
}

func TestDuplicatesForTheHoveredItemReturnToTheList(t *testing.T) {
	m := batchModel(t, batchFixture(t))
	m.activateTab(0)
	m.similar[Key{Kind: "issue", Number: 1}] = []dupCandidate{{Number: 2, Kind: "issue", Title: "second"}}
	m = press(m, "m")
	if !m.dups.open || m.dups.source.Number != 1 {
		t.Fatalf("m on a hovered item should compare its duplicates: %+v", m.dups)
	}

	m = press(m, "esc")
	if m.dups.open || m.focus != FocusList {
		t.Fatal("Esc should go back to the list, not into an item")
	}
}

func TestRemoveTickedItemsFromABatch(t *testing.T) {
	m := batchModel(t, batchFixture(t))
	m.openBatch("b20260101-000000")
	m = press(m, " ")
	m = press(m, "d")
	if m.listConfirm != "d" || !strings.Contains(m.status, "Press d again") {
		t.Fatalf("d inside a batch should ask first: %q", m.status)
	}

	m = pressCmd(t, m, "d")
	if b := m.batchByID("b20260101-000000"); b == nil || len(b.Keys) != 1 || b.Keys[0].Number != 2 {
		t.Fatalf("#1 should be gone from the batch: %+v", b)
	}

	if len(m.ticked) != 0 || len(m.list.Items()) != 1 {
		t.Fatalf("the batch list should shrink and lose its ticks: %d items, %v", len(m.list.Items()), m.ticked)
	}
}

func TestBulkRemoveGroupMembers(t *testing.T) {
	root, g := groupFixture(t)
	ledger := filepath.Join(root, "data", "owner", "repo", "ledger.jsonl")
	f, err := os.OpenFile(ledger, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}

	f.WriteString("{\"kind\":\"pr\",\"number\":2}\n")
	f.Close()

	for _, n := range []string{"1", "2"} {
		out, err := runScript(root, "group", "add", g.ID, "--kind", "pr", "--number", n, "--by", "tester")
		if err != nil {
			t.Fatal(err)
		}

		json.Unmarshal([]byte(out), &g)
	}

	m := testPRModel()
	m.installRoot = root
	m.groups = groupUI{open: true, records: []Group{g}, detail: true, ticked: map[Key]bool{}}
	m = press(m, " ")
	m = press(m, " ")
	m = press(m, "d")
	if !strings.Contains(m.status, "2 ticked members") {
		t.Fatalf("d on ticked members should name them: %q", m.status)
	}

	m = pressCmd(t, m, "d")
	if len(m.groups.records) != 1 || len(m.groups.records[0].Members) != 0 {
		t.Fatalf("both members should be removed: %+v", m.groups.records)
	}
}

func TestTickAndDeleteSeveralBatches(t *testing.T) {
	root := batchFixture(t)
	dir := filepath.Join(root, "data", "owner", "repo", "batches")
	for _, suffix := range []string{".items.jsonl", ".decisions.jsonl"} {
		data, _ := os.ReadFile(filepath.Join(dir, "b20260101-000000"+suffix))
		os.WriteFile(filepath.Join(dir, "b20260102-000000"+suffix), data, 0o644)
	}

	m := batchModel(t, root)
	m, _ = openBatchesNow(t, m)
	m = press(m, " ")
	m = press(m, " ")
	m = press(m, "d")
	if !strings.Contains(m.status, "2 ticked batches") {
		t.Fatalf("d on ticked batches should name them: %q", m.status)
	}

	m = pressCmd(t, m, "d")
	if len(m.batches.records) != 0 || !strings.Contains(m.status, "Deleted 2 batches") {
		t.Fatalf("both batches should be deleted: %d left, %q", len(m.batches.records), m.status)
	}
}

// openBatchesNow opens the Batches screen and loads it synchronously.
func openBatchesNow(t *testing.T, m model) (model, tea.Cmd) {
	t.Helper()
	next, cmd := m.openBatches()
	m = next.(model)
	if cmd != nil {
		m = send(m, cmd())
	}

	return m, nil
}

func TestGroupKeyCarriesTickedItems(t *testing.T) {
	m := batchModel(t, batchFixture(t))
	m.activateTab(0)
	m = press(m, " ")
	m = press(m, " ")
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("b")})
	m = next.(model)
	if !m.groups.open || len(m.groups.sources) != 2 {
		t.Fatalf("b on two ticked items should open Groups for both: %+v", m.groups.sources)
	}

	m = send(m, groupsLoadedMsg{groups: []Group{{ID: "g", Title: "Related", Status: "draft"}}})
	m.showHelp = true
	if footer := m.footerView(); !strings.Contains(footer, "add 2 items") {
		t.Fatalf("the Groups footer should say what b adds:\n%s", footer)
	}
}
