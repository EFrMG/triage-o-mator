package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func reopenFixture(t *testing.T) model {
	t.Helper()
	items := []Item{
		{Kind: "issue", Number: 1, State: "closed", Title: "First", URL: "https://github.com/owner/repo/issues/1", CreatedAt: "2026-01-01"},
		{Kind: "pr", Number: 2, State: "closed", Title: "Second", URL: "https://github.com/owner/repo/pull/2", CreatedAt: "2026-01-02"},
		{Kind: "issue", Number: 3, State: "open", Title: "Third", URL: "https://github.com/owner/repo/issues/3", CreatedAt: "2026-01-03"},
		{Kind: "issue", Number: 4, State: "closed", Title: "Fourth", URL: "https://github.com/owner/repo/issues/4", CreatedAt: "2026-01-04"},
	}
	m := newModel(t.TempDir(), "owner/repo", testTaxonomy(), "tester", items)
	m.refreshing = false
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 35})
	m.activateTab(allItemsTab)
	return m
}

func TestReopenBindingUsesTickedTargetsAndRequiresReview(t *testing.T) {
	m := reopenFixture(t)
	m.ticked[m.items[0].Key()] = true
	m.ticked[m.items[1].Key()] = true
	m = send(m, tea.KeyPressMsg{Text: "v"})
	if !m.comment.open || !m.comment.reopen || len(m.comment.targets) != 2 || m.comment.targets[0].key != m.items[0].Key() || m.comment.targets[1].key != m.items[1].Key() {
		t.Fatal("v did not open a composer for the ticked items in list order")
	}
	m.comment.text.SetValue("Both items are ready for another review.")
	next, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = next.(model)
	if cmd != nil || !m.comment.previewing || m.comment.busy {
		t.Fatal("bulk Ctrl-S must review targets before any script runs")
	}
	preview := strings.TrimSpace(ansi.Strip(m.comment.preview.View()))
	if !strings.Contains(preview, m.comment.targets[0].url) || !strings.Contains(preview, m.comment.targets[1].url) || !strings.Contains(preview, "Both items are ready") {
		t.Fatalf("bulk preview omits targets or text: %q", preview)
	}
	next, cmd = m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil || !next.(model).comment.busy {
		t.Fatal("approval from the review screen did not prepare the first write")
	}
}

func TestSingleReopenPreviewShowsCommentWithoutTargetWrapper(t *testing.T) {
	m := reopenFixture(t)
	next, _ := m.openReopen(m.items[:1])
	m = next.(model)
	m.comment.text.SetValue("Please review this again.")
	m = send(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	preview := strings.TrimSpace(ansi.Strip(m.comment.preview.View()))
	if preview != "Please review this again." {
		t.Fatalf("single-item preview contains redundant reopening text: %q", preview)
	}
	if !strings.Contains(ansi.Strip(m.commentHeader(100)), m.items[0].URL) {
		t.Fatal("single-item target is missing from the preview header")
	}
}

func TestReopenCommandUsesTheSelectedTargetAndStateChange(t *testing.T) {
	m := reopenFixture(t)
	root := batchFixture(t)
	copyFixtureScripts(t, root, "comment")
	m.installRoot = root
	next, _ := m.openReopen(m.items[:2])
	m = next.(model)
	m.comment.text.SetValue("Reconsider this item")
	for index, target := range m.comment.targets {
		m.comment.index = index
		preview := m.commentCmd(false)().(commentMsg)
		if preview.err != nil {
			t.Fatal(preview.err)
		}
		var plan struct {
			Plan struct {
				Operation   string `json:"operation"`
				StateChange string `json:"state_change"`
				Target      string `json:"target"`
			} `json:"plan"`
		}
		if err := json.Unmarshal([]byte(preview.out), &plan); err != nil || plan.Plan.Operation != "reopen" || plan.Plan.StateChange != "open" || plan.Plan.Target != target.url {
			t.Fatalf("target %d got wrong plan: %s, %v", index, preview.out, err)
		}
	}
}

func TestReopenWorksFromItemAndOtherItemLists(t *testing.T) {
	m := reopenFixture(t)
	m.activeBatch = "batch-id"
	m = send(m, tea.KeyPressMsg{Text: "v"})
	if !m.comment.open || len(m.comment.targets) != 1 || m.comment.targets[0].key != m.items[0].Key() {
		t.Fatal("v did not use the hovered item in a batch list")
	}
	m.comment.open = false
	m.activeBatch = ""
	m.openItem(m.items[1])
	m.focus = FocusDetail
	m = send(m, tea.KeyPressMsg{Text: "v"})
	if !m.comment.open || !m.comment.reopen || m.comment.targets[0].key != m.items[1].Key() {
		t.Fatal("v did not open the current PR from item view")
	}
	m.comment.open = false
	m.items[1].State = "open"
	next, cmd := m.openReopen([]Item{m.items[1]})
	if cmd != nil || next.(model).comment.open {
		t.Fatal("already open item entered the reopen composer")
	}
}

func TestCapitalOStillChangesUntriagedAgeOrder(t *testing.T) {
	m := reopenFixture(t)
	m.activateTab(untriagedTab)
	m = send(m, tea.KeyPressMsg{Text: "O"})
	if !m.untriagedNewest || m.comment.open {
		t.Fatal("O must reverse untriaged age order without opening a composer")
	}
}

func TestReopenUsesTickedGroupMembersAndDuplicateCandidates(t *testing.T) {
	m := reopenFixture(t)
	first, second := m.items[0].Key(), m.items[1].Key()
	m.groups = groupUI{open: true, detail: true, records: []Group{{ID: "g", Members: []GroupMember{{Kind: first.Kind, Number: first.Number}, {Kind: second.Kind, Number: second.Number}}}}, ticked: map[Key]bool{first: true, second: true}}
	m = send(m, tea.KeyPressMsg{Text: "v"})
	if !m.comment.open || len(m.comment.targets) != 2 || m.comment.targets[0].key != first || m.comment.targets[1].key != second {
		t.Fatal("group member ticks were not used for reopening")
	}

	m = reopenFixture(t)
	second, fourth := m.items[1].Key(), m.items[3].Key()
	m.dups = dupUI{open: true, source: first, checked: map[Key]bool{second: true, fourth: true}}
	m.similar[first] = []dupCandidate{{Kind: second.Kind, Number: second.Number}, {Kind: fourth.Kind, Number: fourth.Number}}
	m = send(m, tea.KeyPressMsg{Text: "v"})
	if !m.comment.open || len(m.comment.targets) != 2 || m.comment.targets[0].key != second || m.comment.targets[1].key != fourth {
		t.Fatal("duplicate candidate ticks were not used for reopening")
	}
}

func TestReopenBatchStopsOnUncertainOutcomeAndKeepsCompletedState(t *testing.T) {
	m := reopenFixture(t)
	next, _ := m.openReopen(m.items[:2])
	m = next.(model)
	m.comment.text.SetValue("Reopen for another review")
	m.comment.busy = true
	first := m.comment.targets[0]
	plan := `{"approval":"digest","plan":{"request_id":"id","target":"` + first.url + `","body":"Reopen for another review","operation":"reopen","state_change":"open"}}`
	next, cmd := m.Update(commentMsg{root: m.installRoot, repo: m.repo, index: 0, out: plan})
	m = next.(model)
	if cmd == nil || !m.comment.busy {
		t.Fatal("exact first plan did not authorize publishing")
	}
	result := `{"comment":{"status":"succeeded","url":"` + first.url + `#issuecomment-42"},"state_change":{"status":"succeeded","state":"open","url":"` + first.url + `"}}`
	next, cmd = m.Update(commentMsg{root: m.installRoot, repo: m.repo, index: 0, publish: true, out: result})
	m = next.(model)
	if cmd == nil || m.items[0].State != "open" || m.items[1].State != "closed" || m.comment.index != 1 || !m.comment.busy {
		t.Fatal("first success did not advance to the second target")
	}
	m = send(m, commentMsg{root: m.installRoot, repo: m.repo, index: 1, publish: true, err: errors.New("connection lost")})
	if m.comment.open || m.items[0].State != "open" || m.items[1].State != "closed" || !strings.Contains(m.status, "1/2") || !strings.Contains(m.status, "uncertain") {
		t.Fatalf("partial reopen outcome was hidden: %s", m.status)
	}
}

func TestReopenRejectsCommentOnlyPlanAndCompletesEachTargetOnce(t *testing.T) {
	m := reopenFixture(t)
	next, _ := m.openReopen(m.items[:2])
	m = next.(model)
	m.comment.text.SetValue("Please review these again")
	m.comment.busy = true
	first, second := m.comment.targets[0], m.comment.targets[1]
	wrong := `{"approval":"digest","plan":{"request_id":"id","target":"` + first.url + `","body":"Please review these again","operation":"comment","state_change":"none"}}`
	next, cmd := m.Update(commentMsg{root: m.installRoot, repo: m.repo, index: 0, out: wrong})
	if cmd != nil || next.(model).items[0].State != "closed" {
		t.Fatal("comment-only plan was accepted for reopening")
	}

	m = reopenFixture(t)
	next, _ = m.openReopen(m.items[:2])
	m = next.(model)
	m.comment.text.SetValue("Please review these again")
	for index, target := range []commentTarget{first, second} {
		m.comment.busy = true
		plan := `{"approval":"digest","plan":{"request_id":"id","target":"` + target.url + `","body":"Please review these again","operation":"reopen","state_change":"open"}}`
		next, cmd = m.Update(commentMsg{root: m.installRoot, repo: m.repo, index: index, out: plan})
		m = next.(model)
		if cmd == nil || !m.comment.busy {
			t.Fatalf("plan %d did not advance to publishing", index)
		}
		result := `{"comment":{"status":"succeeded","url":"` + target.url + `#issuecomment-42"},"state_change":{"status":"succeeded","state":"open","url":"` + target.url + `"}}`
		next, cmd = m.Update(commentMsg{root: m.installRoot, repo: m.repo, index: index, publish: true, out: result})
		m = next.(model)
		if cmd == nil || m.items[index].State != "open" {
			t.Fatalf("success %d did not update the target", index)
		}
	}
	if m.comment.open || !m.refreshing || len(m.comment.completed) != 2 || !strings.Contains(m.status, "Reopened 2 items") {
		t.Fatalf("batch did not finish with two confirmed reopenings: %s", m.status)
	}
}
