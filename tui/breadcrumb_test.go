package main

import (
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestBreadcrumbFollowsTheFlow(t *testing.T) {
	root := batchFixture(t)
	m := batchModel(t, root)
	m = send(m, windowSize(120, 30))
	check := func(want string) {
		t.Helper()
		if got := strings.Join(m.breadcrumb(), " › "); got != want {
			t.Fatalf("breadcrumb = %q, want %q", got, want)
		}

		if !strings.Contains(ansi.Strip(m.viewContent()), "─ "+want+" ─") {
			t.Fatalf("the panel border should carry %q:\n%s", want, ansi.Strip(m.viewContent()))
		}
	}

	check("owner/repo › Overview")
	next, cmd := m.openBatches()
	m = runCmd(next.(model), cmd)
	check("owner/repo › Batches")
	m.startBatchForm()
	check("owner/repo › Batches › New batch")
	m.batches.editing = false
	m.openBatch("b20260101-000000")
	check("owner/repo › Batches › b20260101-000000")
	m.selectCurrentListItem()
	key := m.detail.key
	check("owner/repo › Batches › b20260101-000000 › #" + itoa(key.Number))

	m.similar[key] = []dupCandidate{{Number: 2, Kind: key.Kind, Title: "other"}}
	next, _ = m.openDuplicates()
	m = next.(model)
	check("owner/repo › Batches › b20260101-000000 › #" + itoa(key.Number) + " › Duplicates of #" + itoa(key.Number))

	m = press(m, "esc")
	m.groups = groupUI{open: true, originFocus: FocusDetail, sources: []Key{key}, records: []Group{{ID: "g", Title: "Wifi"}}}
	check("owner/repo › Batches › b20260101-000000 › #" + itoa(key.Number) + " › Groups")
	m.editGroup("add")
	check("owner/repo › Batches › b20260101-000000 › #" + itoa(key.Number) + " › Groups › Wifi › Add")
}

// A trail too wide for the border drops its leftmost crumbs first, and the border keeps its width.
func TestBreadcrumbDropsLeftmostCrumbsWhenNarrow(t *testing.T) {
	m := newModel("/tmp", "some-owner/some-long-repository-name", testTaxonomy(), "tester", testItems())
	m.groups = groupUI{open: true, originFocus: FocusSidebar, detail: true, records: []Group{{ID: "g", Title: "A rather long group title about wifi"}}}
	panel := panelStyle(true).Width(50).Height(3).Render("x")
	out := m.titled(panel, true)
	top := ansi.Strip(strings.SplitN(out, "\n", 2)[0])
	if lipgloss.Width(out) != lipgloss.Width(panel) || !strings.HasPrefix(top, "╭─ … › ") && !strings.HasPrefix(top, "╭ … › ") || !strings.Contains(top, "A rather long") || strings.Contains(top, "some-owner") {
		t.Fatalf("narrow border: %q", top)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// The trail sits in the middle of the border: the rules on either side differ by one cell at most.
func TestBreadcrumbIsCentered(t *testing.T) {
	m := newModel("/tmp", "owner/repo", testTaxonomy(), "tester", testItems())
	panel := panelStyle(true).Width(60).Height(3).Render("x")
	top := ansi.Strip(strings.SplitN(m.titled(panel, true), "\n", 2)[0])
	left := strings.Count(top[:strings.Index(top, " owner")], "─")
	right := strings.Count(top[strings.Index(top, "Overview"):], "─")
	if lipgloss.Width(top) != lipgloss.Width(panel) || left-right > 1 || right-left > 1 {
		t.Fatalf("not centered (%d left, %d right): %q", left, right, top)
	}
}
