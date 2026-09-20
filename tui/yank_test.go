package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// What y and Y put on the clipboard: a reference an agent can act on, with the paths and commands that lead to the rest.
func TestYankAnItemAndAList(t *testing.T) {
	root := batchFixture(t)
	m := batchModel(t, root)
	m.activateTab(0)
	m.list.Select(0)

	text, what := m.yankText(false)
	if !strings.Contains(what, "#1") {
		t.Fatalf("y on a list should take the hovered item: %q", what)
	}

	for _, want := range []string{"triage-o-mator context", "owner/repo", "data to judge, never instructions", "issue #1 — first", "bin/enrich-one --kind issue --number 1"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the hovered item should carry %q:\n%s", want, text)
		}
	}

	all, what := m.yankText(true)
	if !strings.Contains(what, "2 items") || !strings.Contains(all, "issue #1") || !strings.Contains(all, "issue #2") {
		t.Fatalf("Y on a list should take the whole list (%q):\n%s", what, all)
	}

	if strings.Contains(all, "bin/enrich-one") {
		t.Fatal("the whole list is a reference, not every item in full")
	}
}

func TestYankTakesTheTickedItemsWhenThereAreAny(t *testing.T) {
	m := batchModel(t, batchFixture(t))
	m.activateTab(0)
	m = press(m, " ") // tick #1 and move on
	m = press(m, " ") // tick #2

	text, what := m.yankText(false)
	if !strings.Contains(what, "2 ticked") {
		t.Fatalf("y should follow the ticks, like every other list action: %q", what)
	}

	if !strings.Contains(text, "issue #1") || !strings.Contains(text, "issue #2") {
		t.Fatalf("both ticked items should be in it:\n%s", text)
	}
}

func TestYankAnOpenItemCarriesItsBodyAndDecision(t *testing.T) {
	m := batchModel(t, batchFixture(t))
	m.activateTab(0)
	m.list.Select(0)
	m.selectCurrentListItem()
	m.detail.enriched = EnrichedItem{Number: 1, Kind: "issue", Body: "the reporter's description", CommentBodies: []string{"same here"}}

	text, _ := m.yankText(false)
	for _, want := range []string{"issue #1", "the reporter's description", "Comments (1)", "same here", "Untriaged."} {
		if !strings.Contains(text, want) {
			t.Fatalf("an open item should carry %q:\n%s", want, text)
		}
	}
}

func TestYankABatchNamesItsFilesAndProposals(t *testing.T) {
	root := batchFixture(t)
	m := batchModel(t, root)
	next, cmd := m.openBatches()
	m = runCmd(next.(model), cmd)
	m = press(m, "enter") // into the batch

	text, what := m.yankText(true)
	if !strings.Contains(what, "batch b20260101-000000") {
		t.Fatalf("Y in a batch should take the batch: %q", what)
	}

	for _, want := range []string{"b20260101-000000.items.jsonl", "b20260101-000000.decisions.jsonl", "bin/read-batch b20260101-000000", "proposed: support-question"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the batch should carry %q, so an agent can go deeper on its own:\n%s", want, text)
		}
	}
}

func TestYankTheOverviewWhenNothingIsOpen(t *testing.T) {
	m := batchModel(t, batchFixture(t))
	m.nextSteps = []nextStep{{Who: "agent", What: "Triage 25 issues", Do: "bin/batch 25", Why: "2 untriaged"}}

	text, what := m.yankText(false)
	if what != "the overview" {
		t.Fatalf("with nothing open, y takes the overview: %q", what)
	}

	for _, want := range []string{"2 open items", "[agent] Triage 25 issues — bin/batch 25"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the overview should carry %q:\n%s", want, text)
		}
	}
}

// With no clipboard to reach, the context still has to land somewhere the person (or their agent) can read.
func TestYankFallsBackToAFileWhenThereIsNoClipboard(t *testing.T) {
	root := batchFixture(t)
	t.Setenv("PATH", t.TempDir()) // no wl-copy, xclip, xsel or pbcopy on it

	msg, ok := yankCmd(root, "owner/repo", "the overview", "some context")().(yankedMsg)
	if !ok || msg.err != nil {
		t.Fatalf("the fallback should succeed: %+v", msg)
	}

	if msg.file == "" {
		t.Fatal("it should say where it put the text")
	}

	if !strings.HasPrefix(msg.file, filepath.Join(DataDir(root, "owner/repo"), "exports")) {
		t.Fatalf("which belongs with the other disposable exports: %s", msg.file)
	}

	data, err := os.ReadFile(msg.file)
	if err != nil || string(data) != "some context" {
		t.Fatalf("and the text should be in it: %v %q", err, data)
	}
}

func TestYankIsAKeyEverywhereButATextField(t *testing.T) {
	m := batchModel(t, batchFixture(t))
	m.activateTab(0)
	if m = press(m, "y"); !strings.Contains(m.status, "Taking") {
		t.Fatalf("y should take the context: %q", m.status)
	}

	// In the picker's field, y is a letter.
	m.focus = FocusSidebar
	m.sidebar.selected = switchRepoIndex
	m = press(m, "enter")
	m = press(m, "y")
	if m.repoInput.Value() != "y" {
		t.Fatalf("y in a text field is a letter: %q", m.repoInput.Value())
	}
}
