package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func baselineItems() []Item {
	return []Item{{Number: 1, Kind: "issue", State: "open", Title: "first"}, {Number: 2, Kind: "issue", State: "open", Title: "second"}}
}

func baselineTaxonomy() Taxonomy {
	return Taxonomy{IssueCategories: []string{"bug"}, PRCategories: []string{"merge-ready"}, Actions: []string{"label-only"}, Confidence: []string{"low", "medium", "high"}}
}

func baselineSend(m model, msg tea.Msg) model {
	next, _ := m.Update(msg)
	return next.(model)
}

func baselineRow(number int) string {
	row := map[string]any{"number": number, "kind": "issue", "state": "open", "title": "issue", "url": "https://github.com/owner/repo/issues/1", "author": "author", "created_at": "2026-01-01T00:00:00Z", "updated_at": "", "labels": []string{}, "comments_count": 0, "category": "", "action": "", "confidence": "", "reason": "", "reviewed": false}
	data, _ := json.Marshal(row)
	return string(data) + "\n"
}

func baselineRoot(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	for _, dir := range []string{"bin", "config", "data/owner/repo/batches"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	modules, err := filepath.Glob(filepath.Join("..", "bin", "_*.py"))
	if err != nil {
		t.Fatal(err)
	}
	files := append(modules, filepath.Join("..", "bin", "apply"))
	for _, source := range files {
		data, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "bin", filepath.Base(source)), data, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	taxonomy, err := os.ReadFile(filepath.Join("..", "config", "taxonomy.json"))
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{MarkerName: []byte("{}\n"), "config/repo": []byte("owner/repo\n"), "config/taxonomy.json": taxonomy, "data/owner/repo/ledger.jsonl": []byte(baselineRow(1) + baselineRow(2))} {
		if err := os.WriteFile(filepath.Join(root, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func baselineModel(t *testing.T, root string) model {
	t.Helper()
	taxonomy, err := LoadTaxonomy(root)
	if err != nil {
		t.Fatal(err)
	}
	items, err := LoadLedger(root, "owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(root, "owner/repo", taxonomy, "tester", items)
	m = baselineSend(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	return baselineSend(m, fetchSyncDoneMsg{})
}

func TestBaselineResizePromptCentered(t *testing.T) {
	m := baselineModel(t, baselineRoot(t))
	check := func(width, height int) {
		t.Helper()
		m = baselineSend(m, tea.WindowSizeMsg{Width: width, Height: height})
		if !m.needsResize() {
			t.Fatal("small terminal did not show a resize prompt")
		}

		prompt := "Please resize to at least 60 × 24."
		lines := strings.Split(ansi.Strip(m.viewContent()), "\n")
		for y, line := range lines {
			if x := strings.Index(line, prompt); x >= 0 {
				if x != (width-ansi.StringWidth(prompt))/2 || y != (height-1)/2 {
					t.Fatalf("resize prompt at (%d, %d), want center of %d × %d", x, y, width, height)
				}
				return
			}
		}
		t.Fatal("resize prompt missing")
	}

	check(50, 20)
	m = baselineSend(m, tea.WindowSizeMsg{Width: 30, Height: 20})
	wantLines := []string{"Please resize to at least", "60 × 24."}
	seen := 0
	for y, line := range strings.Split(ansi.Strip(m.viewContent()), "\n") {
		if seen < len(wantLines) && strings.Contains(line, wantLines[seen]) {
			if x := strings.Index(line, wantLines[seen]); x != (30-ansi.StringWidth(wantLines[seen]))/2 || y != 9+seen {
				t.Fatalf("wrapped resize line %q is not centered at row %d", wantLines[seen], y)
			}
			seen++
		}
	}
	if seen != len(wantLines) {
		t.Fatal("wrapped resize prompt is incomplete")
	}
	m = baselineSend(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.activateTab(untriagedTab)
	m.selectCurrentListItem()
	m = baselineSend(m, tea.KeyPressMsg{Text: "c"})
	if !m.comment.open {
		t.Fatal("comment composer did not open")
	}
	check(80, 18)
}

func baselineLedgerRow(t *testing.T, root string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "data/owner/repo/ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var row map[string]any
	if err := json.Unmarshal([]byte(strings.SplitN(string(data), "\n", 2)[0]), &row); err != nil {
		t.Fatal(err)
	}
	return row
}

func TestBaselineInstallRootAndRepoBoundary(t *testing.T) {
	root := baselineRoot(t)
	t.Setenv("TRIAGE_ROOT", root)
	got, err := FindInstallRoot()
	if err != nil || got != root {
		t.Fatalf("install root = %q, %v", got, err)
	}
	if validRepo("../escape") || DataDir(root, "owner/repo") != filepath.Join(root, "data", "owner", "repo") {
		t.Fatal("repository path escaped its install")
	}
}

func TestBaselineDraftAndQuitConfirmation(t *testing.T) {
	m := newModel(t.TempDir(), "owner/repo", baselineTaxonomy(), "tester", baselineItems())
	m.activateTab(untriagedTab)
	m.selectCurrentListItem()
	m.form.NextField()
	m.form.CycleValue(1)
	if !m.form.dirty {
		t.Fatal("editing should create a draft")
	}
	m.list.Select(1)
	m.selectCurrentListItem()
	m.list.Select(0)
	m.selectCurrentListItem()
	if !m.form.dirty {
		t.Fatal("switching items lost the draft")
	}
	_, cmd := m.requestQuit()
	if cmd != nil {
		t.Fatal("unsaved draft allowed immediate quit")
	}
}

func TestBaselineViewDeclaresTerminalState(t *testing.T) {
	m := newModel(t.TempDir(), "owner/repo", baselineTaxonomy(), "tester", baselineItems())
	view := m.View()
	if !view.AltScreen || view.MouseMode != tea.MouseModeCellMotion || view.ForegroundColor == nil || view.BackgroundColor == nil {
		t.Fatal("view omitted terminal state")
	}
	if view.Content != m.viewContent() {
		t.Fatal("view and rendered model disagree")
	}
}

func TestBaselineErrorShortcutMatchesFooterAcrossViews(t *testing.T) {
	for _, view := range []string{"sidebar", "list", "item", "notifications"} {
		t.Run(view, func(t *testing.T) {
			m := baselineModel(t, baselineRoot(t))
			m.lastError = errorDetails{what: "Fixture error", text: "full details"}
			switch view {
			case "list":
				m.activateTab(untriagedTab)
			case "item":
				m.activateTab(untriagedTab)
				m.selectCurrentListItem()
			case "notifications":
				m.notifications.open = true
			}

			shown := false
			for _, group := range m.footerGroups() {
				for _, hint := range group.hints {
					shown = shown || hint.keys == "!"
				}
			}
			if !shown {
				t.Fatal("footer omitted the error shortcut")
			}

			m = baselineSend(m, tea.KeyPressMsg{Text: "!"})
			if !m.lastError.open {
				t.Fatal("error shortcut did not open the saved error")
			}
		})
	}

	m := baselineModel(t, baselineRoot(t))
	m.lastError = errorDetails{what: "Fixture error", text: "full details"}
	m.activateTab(untriagedTab)
	m.selectCurrentListItem()
	m.form.FocusField(fieldReason)
	m = baselineSend(m, tea.KeyPressMsg{Text: "!"})
	if m.lastError.open || m.form.Reason() != "!" {
		t.Fatal("error shortcut intercepted typing in the reason field")
	}
}

func TestBaselineNotificationItemTabNavigation(t *testing.T) {
	m := baselineModel(t, baselineRoot(t))
	m.notifications.open = true
	next, _ := m.openNotificationItemAt(Key{Kind: "pr", Number: 3}, 1)
	m = next.(model)
	if !m.notificationPR.open || m.detail.active != 1 || len(m.detail.sections) != 3 {
		t.Fatal("notification PR did not open on its comments tab")
	}

	for _, step := range []struct {
		key  tea.KeyPressMsg
		want int
	}{
		{tea.KeyPressMsg{Text: "L"}, 2},
		{tea.KeyPressMsg{Text: "H"}, 1},
		{tea.KeyPressMsg{Text: "H"}, 0},
		{tea.KeyPressMsg{Code: tea.KeyTab}, 1},
		{tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, 0},
		{tea.KeyPressMsg{Code: 'l', Mod: tea.ModCtrl}, 0},
	} {
		m = baselineSend(m, step.key)
		if m.detail.active != step.want {
			t.Fatalf("%q selected tab %d, want %d", step.key.String(), m.detail.active, step.want)
		}
	}

	for _, click := range []struct {
		label string
		x     int
		want  int
	}{
		{"L", 0, 1},
		{"H", 0, 0},
	} {
		key := mouseHintActionAt(click.label, click.x)
		next, _ := m.mousePress(key)
		m = next.(model)
		if m.detail.active != click.want {
			t.Fatalf("footer %q selected tab %d, want %d", key, m.detail.active, click.want)
		}
	}

	m = baselineSend(m, tea.KeyPressMsg{Text: "h"})
	if m.notificationPR.open {
		t.Fatal("h did not return from the notification item")
	}
}

func TestBaselineItemTabsLeadIntoForm(t *testing.T) {
	m := baselineModel(t, baselineRoot(t))
	m.activateTab(untriagedTab)
	m.selectCurrentListItem()
	if !m.sideBySide() || m.form.focused != fieldContent || len(m.detail.sections) != 2 {
		t.Fatal("item did not open with tabs beside its form")
	}

	m = baselineSend(m, tea.KeyPressMsg{Text: "L"})
	if m.detail.active != 1 || m.form.focused != fieldContent {
		t.Fatal("L did not select the last tab")
	}
	m = baselineSend(m, tea.KeyPressMsg{Text: "L"})
	if m.detail.active != 1 || m.form.focused != fieldCategory {
		t.Fatal("L on the last tab did not focus the form")
	}
	m.form.FocusField(fieldAction)
	m = baselineSend(m, tea.KeyPressMsg{Text: "H"})
	if m.detail.active != 1 || m.form.focused != fieldContent {
		t.Fatal("H in the form did not return focus to the active tab")
	}
	m = baselineSend(m, tea.KeyPressMsg{Text: "H"})
	if m.detail.active != 0 || m.form.focused != fieldContent {
		t.Fatal("H did not select the previous tab")
	}
}

func TestBaselineNotificationItemCommentActions(t *testing.T) {
	m := baselineModel(t, baselineRoot(t))
	m.notifications.open = true
	key := Key{Kind: "issue", Number: 9}
	next, _ := m.openNotificationItem(key)
	m = next.(model)
	if _, found := m.findItem(key); found {
		t.Fatal("fixture item unexpectedly exists in the ledger")
	}

	m = baselineSend(m, tea.KeyPressMsg{Text: "c"})
	if m.comment.open {
		t.Fatal("comment opened before the item read completed")
	}

	read := func(state, url string) {
		m = baselineSend(m, evidenceReadMsg{root: m.installRoot, repo: m.repo, key: key, request: m.evidenceRequest, generation: m.detail.generation, data: EnrichedItem{Kind: key.Kind, Number: key.Number, State: state, URL: url, Evidence: &batchEvidence{Mode: "refresh"}}})
	}
	read("open", "https://elsewhere.example/owner/repo/issues/9")
	m = baselineSend(m, tea.KeyPressMsg{Text: "c"})
	if m.comment.open {
		t.Fatal("comment opened for a URL outside the pinned host")
	}

	read("open", "https://github.com/owner/repo/issues/9")
	for _, action := range []struct {
		key   string
		close bool
	}{
		{"c", false},
		{"x", true},
	} {
		m = baselineSend(m, tea.KeyPressMsg{Text: action.key})
		if !m.comment.open || m.comment.close != action.close || m.comment.target != "https://github.com/owner/repo/issues/9" {
			t.Fatalf("%s did not open the approved comment composer for the selected item", action.key)
		}
		m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	}

	m = baselineSend(m, tea.KeyPressMsg{Text: "v"})
	if m.comment.open {
		t.Fatal("reopen composer opened for an open item")
	}

	m.refreshNotificationItem()
	read("closed", "https://github.com/owner/repo/issues/9")
	m = baselineSend(m, tea.KeyPressMsg{Text: "v"})
	if !m.comment.open || !m.comment.reopen || len(m.comment.targets) != 1 || m.comment.targets[0].key != key {
		t.Fatal("reopen composer did not target the closed notification item")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEsc})

	m.refreshNotificationItem()
	read("open", "https://github.com/owner/repo/issues/9")
	m = baselineSend(m, tea.KeyPressMsg{Text: "c"})
	request := m.evidenceRequest
	out := `{"comment":{"status":"succeeded","url":"https://github.com/owner/repo/issues/9#issuecomment-1"}}`
	m = baselineSend(m, commentMsg{root: m.installRoot, repo: m.repo, publish: true, out: out})
	if m.comment.open || !m.detail.loading || m.evidenceRequest <= request {
		t.Fatal("published comment did not refresh the notification item")
	}

	read("open", "https://github.com/owner/repo/issues/9")
	m = baselineSend(m, tea.KeyPressMsg{Text: "x"})
	request = m.evidenceRequest
	closeOut := `{"comment":{"status":"succeeded","url":"https://github.com/owner/repo/issues/9#issuecomment-2"},"state_change":{"status":"succeeded","state":"closed"}}`
	m = baselineSend(m, commentMsg{root: m.installRoot, repo: m.repo, publish: true, out: closeOut})
	if m.comment.open || m.detail.item.State != "open" || !m.detail.loading || m.evidenceRequest <= request || !m.refreshing {
		t.Fatal("close outcome did not schedule an upstream item and ledger refresh")
	}

	m.refreshing = false
	read("closed", "https://github.com/owner/repo/issues/9")
	m = baselineSend(m, tea.KeyPressMsg{Text: "v"})
	request = m.evidenceRequest
	reopenOut := `{"comment":{"status":"succeeded","url":"https://github.com/owner/repo/issues/9#issuecomment-3"},"state_change":{"status":"succeeded","state":"open","url":"https://github.com/owner/repo/issues/9"}}`
	m = baselineSend(m, commentMsg{root: m.installRoot, repo: m.repo, publish: true, out: reopenOut})
	if m.comment.open || m.detail.item.State != "closed" || !m.detail.loading || m.evidenceRequest <= request || !m.refreshing {
		t.Fatal("reopen outcome did not schedule an upstream item and ledger refresh")
	}
}

func TestBaselineCommentStateComesFromLedgerRefresh(t *testing.T) {
	m := baselineModel(t, baselineRoot(t))
	m.activateTab(untriagedTab)
	m.selectCurrentListItem()
	key := m.detail.key
	m = baselineSend(m, tea.KeyPressMsg{Text: "x"})
	if !m.comment.open || !m.comment.close {
		t.Fatal("close composer did not open")
	}

	out := `{"comment":{"status":"succeeded","url":"https://github.com/owner/repo/issues/1#issuecomment-1"},"state_change":{"status":"succeeded","state":"closed"}}`
	m = baselineSend(m, commentMsg{root: m.installRoot, repo: m.repo, publish: true, out: out})
	if m.detail.item.State != "open" || m.items[0].State != "open" || !m.refreshing {
		t.Fatal("close response changed state before the direct item fetch")
	}

	items := append([]Item(nil), m.items...)
	for i := range items {
		if items[i].Key() == key {
			items[i].State = "closed"
		}
	}
	m = baselineSend(m, ledgerReloadedMsg{repo: m.repo, items: items})
	if m.detail.item.State != "closed" {
		t.Fatal("refreshed ledger state did not reach the open item")
	}
}

func TestBaselineSaveAndHumanApprovalAreSeparate(t *testing.T) {
	for _, approve := range []bool{false, true} {
		t.Run(map[bool]string{false: "proposal", true: "approval"}[approve], func(t *testing.T) {
			root := baselineRoot(t)
			m := baselineModel(t, root)
			m.activateTab(untriagedTab)
			m.selectCurrentListItem()
			m.form.ApplyProposal(proposal{Category: "bug", Action: "label-only", Confidence: "medium", Reason: "Reviewed source", ProposedBy: "agent:triage"})
			shortcut := "s"
			if approve {
				shortcut = "S"
			}
			next, cmd := m.Update(tea.KeyPressMsg{Text: shortcut})
			if cmd == nil {
				t.Fatal("save produced no script command")
			}
			_ = baselineSend(next.(model), cmd())
			row := baselineLedgerRow(t, root)
			if row["reviewed"] != approve || row["category"] != "bug" {
				t.Fatalf("saved decision = %v", row)
			}
			if approve && row["reviewed_by"] != "tester" {
				t.Fatal("human approval lost its reviewer")
			}
		})
	}
}

func TestBaselineSwitchRepoClearsLocalStateAndLateReply(t *testing.T) {
	root := baselineRoot(t)
	dir := DataDir(root, "other/repo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ledger.jsonl"), []byte(baselineRow(7)), 0o644); err != nil {
		t.Fatal(err)
	}
	m := baselineModel(t, root)
	m.drafts[Key{Kind: "issue", Number: 1}] = decisionSnapshot{}
	m.similar[Key{Kind: "issue", Number: 1}] = []dupCandidate{{Number: 2}}
	m.switchRepo("other/repo")
	if m.repo != "other/repo" || len(m.items) != 1 || m.items[0].Number != 7 || len(m.drafts) != 0 || len(m.similar) != 0 {
		t.Fatal("repository switch kept prior repository state")
	}
	m = baselineSend(m, ledgerReloadedMsg{repo: "owner/repo", items: baselineItems()})
	if len(m.items) != 1 || m.items[0].Number != 7 {
		t.Fatal("late reply replaced the new repository's ledger")
	}
}

func TestBaselineRepoPickerWarnsBeforeDiscardingDraft(t *testing.T) {
	root := baselineRoot(t)
	m := baselineModel(t, root)
	m.activateTab(untriagedTab)
	m.selectCurrentListItem()
	m.form.NextField()
	m.form.CycleValue(1)
	m.detail.loading = false
	m.focus = FocusSidebar
	m.sidebar.selected = switchRepoIndex
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m.repoInput.SetValue("other/repo")
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.repo != "owner/repo" || !m.confirmSwitch || len(m.drafts) == 0 {
		t.Fatal("switching repositories discarded an unsaved decision without confirmation")
	}
}

func TestBaselineStaleItemReadCannotReplaceCurrentItem(t *testing.T) {
	m := newModel(t.TempDir(), "owner/repo", baselineTaxonomy(), "tester", baselineItems())
	m.openItem(m.items[0])
	m = baselineSend(m, enrichedMsg{root: m.installRoot, repo: "other/repo", generation: m.detail.generation, key: m.detail.key, data: EnrichedItem{Body: "foreign body"}})
	if m.detail.enriched.Body == "foreign body" {
		t.Fatal("foreign repository read replaced item content")
	}
}

func TestBaselineFixedBatchDoesNotMixLiveEvidence(t *testing.T) {
	root := baselineRoot(t)
	batchDir := filepath.Join(root, "data/owner/repo/batches")
	items := `{"number":1,"kind":"issue","body":"frozen body","evidence":{"snapshot_id":"fixed-id","problems":{"summary":[]}}}` + "\n"
	for name, content := range map[string]string{"b20260101-000000.items.jsonl": items, "b20260101-000000.decisions.jsonl": ""} {
		if err := os.WriteFile(filepath.Join(batchDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	records, err := loadBatches(root, "owner/repo")
	if err != nil || len(records) != 1 {
		t.Fatalf("load batches: %v, %d records", err, len(records))
	}
	m := baselineModel(t, root)
	m.batches.records = records
	m.detail.cache[Key{Kind: "issue", Number: 1}] = EnrichedItem{Body: "live body"}
	m.openBatch(records[0].ID)
	if m.detail.SetItem(Item{Kind: "issue", Number: 1}) || m.detail.enriched.Body != "frozen body" {
		t.Fatal("fixed batch requested or displayed live evidence")
	}
}

func TestBaselineCommentPlanMustMatchDraft(t *testing.T) {
	items := baselineItems()
	items[0].URL = "https://github.com/owner/repo/issues/1"
	m := newModel(t.TempDir(), "owner/repo", baselineTaxonomy(), "tester", items)
	m.openItem(items[0])
	m.focus = FocusDetail
	next, _ := m.openComment()
	m = next.(model)
	if !m.comment.open {
		t.Fatal("comment composer did not open")
	}
	m.comment.text.SetValue("approved text")
	m.comment.busy = true
	preview, err := json.Marshal(map[string]any{"approval": "digest", "plan": map[string]any{"request_id": "id", "target": m.comment.target, "body": "different text", "operation": "comment", "state_change": "none"}})
	if err != nil {
		t.Fatal(err)
	}
	next, cmd := m.Update(commentMsg{root: m.installRoot, repo: m.repo, out: string(preview)})
	if cmd != nil || next.(model).comment.busy {
		t.Fatal("changed comment plan reached publication")
	}
}

func TestBaselineEmptyInventoryKeepsPriorCorpus(t *testing.T) {
	m := baselineModel(t, baselineRoot(t))
	m.corpus.id = strings.Repeat("a", 64)
	m.corpus.progress = &corpusProgress{ID: m.corpus.id, Members: 2}
	m.corpus.preparing, m.corpus.busy = true, true
	result := inventoryResult{Status: "empty", Scope: "full"}
	result.Repository.Host, result.Repository.Name = evidenceHost, m.repo
	next, cmd := m.Update(corpusMsg{root: m.installRoot, epoch: m.corpusEpoch, operation: m.corpus.operation, action: "capture", inventory: result})
	m = next.(model)
	if cmd != nil || m.corpus.preparing || m.corpus.id != strings.Repeat("a", 64) || m.corpus.progress.Members != 2 {
		t.Fatal("empty inventory replaced the prior corpus")
	}
}

func TestBaselineAttentionDropsForeignReply(t *testing.T) {
	m := baselineModel(t, baselineRoot(t))
	next, cmd := m.readAttention(attentionLocation{section: "history", number: 1})
	m = next.(model)
	if cmd == nil || !m.attention.busy {
		t.Fatal("attention read did not start")
	}
	m = baselineSend(m, attentionMsg{root: m.installRoot, repo: "other/repo", generation: m.attentionGeneration})
	if !m.attention.busy || m.attention.page != nil {
		t.Fatal("foreign attention reply replaced the local read")
	}
}

func TestNotificationCountIncludesProposalsAfterSync(t *testing.T) {
	root := baselineRoot(t)
	for name, output := range map[string]string{
		"fetch":      "{}",
		"sync":       "{}",
		"auto-close": `{"repository":"owner/repo","requests":0,"rows":[{"number":3,"needs_attention":true},{"number":4,"needs_attention":true},{"number":5,"needs_attention":false}]}`,
	} {
		script := "#!/usr/bin/env python3\nprint('" + output + "')\n"
		if err := os.WriteFile(filepath.Join(root, "bin", name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cache := `#!/usr/bin/env python3
import json, sys
if 'track-check' in sys.argv:
    print(json.dumps({'unread_total': 1}))
else:
    print(json.dumps({'repository': {'full_name': 'owner/repo'}, 'rows': [{'identity': {'kind': 'pr', 'number': 3}, 'new_count': 2}], 'unread_total': 1, 'offset': 0, 'next': None}))
`
	if err := os.WriteFile(filepath.Join(root, "bin", "cache"), []byte(cache), 0o755); err != nil {
		t.Fatal(err)
	}

	m := baselineModel(t, root)
	result := fetchSyncCmd(root, m.repo, false)().(fetchSyncDoneMsg)
	if result.err != nil || result.trackingErr != nil || result.proposalErr != nil || result.unreadTotal != 2 {
		t.Fatalf("sync notification count = %+v", result)
	}
	m = baselineSend(m, result)
	if m.sidebar.notificationCount != 2 || m.notifications.open {
		t.Fatal("sync did not update the closed Notifications menu count")
	}
}

func TestClosureReviewShowsTargetAndRefreshPinsHost(t *testing.T) {
	root := baselineRoot(t)
	m := baselineModel(t, root)
	target := "https://ghe.example/owner/repo/pull/3"
	m.notifications = notificationsUI{open: true, review: &autoCloseReview{}}
	m.notifications.review.Plan.Proposals = []autoCloseRow{{Number: 3, Title: "Enterprise PR", Target: target, Comment: "Close with explanation"}}
	if !strings.Contains(ansi.Strip(m.autoCloseReviewView()), target) {
		t.Fatal("closure approval did not show the exact target URL")
	}

	for name, script := range map[string]string{
		"fetch":      "#!/usr/bin/env python3\nimport json, pathlib, sys\npathlib.Path('fetch-args.json').write_text(json.dumps(sys.argv[1:]))\n",
		"sync":       "#!/usr/bin/env python3\nprint('{}')\n",
		"cache":      "#!/usr/bin/env python3\nprint('{\"unread_total\":0}')\n",
		"auto-close": "#!/usr/bin/env python3\nprint('{\"repository\":\"owner/repo\",\"requests\":0,\"rows\":[]}')\n",
	} {
		if err := os.WriteFile(filepath.Join(root, "bin", name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	result := fetchSyncCmdAtHost(root, m.repo, false, "ghe.example", Key{Kind: "pr", Number: 3})().(fetchSyncDoneMsg)
	if result.err != nil || result.trackingErr != nil || result.proposalErr != nil {
		t.Fatalf("host-pinned refresh failed: %+v", result)
	}
	data, err := os.ReadFile(filepath.Join(root, "fetch-args.json"))
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	if err := json.Unmarshal(data, &args); err != nil {
		t.Fatal(err)
	}
	if strings.Join(args, " ") != "--expected-repo owner/repo --host ghe.example --include-item pr:3" {
		t.Fatalf("refresh used unexpected target arguments: %v", args)
	}
}

func TestProposalReaderTracksComments(t *testing.T) {
	root := baselineRoot(t)
	if err := os.WriteFile(filepath.Join(root, "bin", "cache"), []byte("#!/usr/bin/env python3\nprint('{\"already_tracking\": false}')\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	key := Key{Kind: "pr", Number: 3}
	m := baselineModel(t, root)
	m.notifications = notificationsUI{open: true, proposals: autoCloseList{Rows: []autoCloseRow{{Number: 3, Status: "pending", Active: true}}}, review: &autoCloseReview{}}
	m.notifications.review.Plan.Proposals = m.notifications.proposals.Rows
	next, cmd := m.handleNotificationsKey(tea.KeyPressMsg{Text: "w"})
	if cmd == nil {
		t.Fatal("w in a proposal reader did not start tracking")
	}
	result := cmd().(trackDoneMsg)
	if result.err != nil || result.key != key {
		t.Fatalf("w tracked the wrong item: %+v", result)
	}
	updated, reload := next.(model).finishTracking(result)
	if reload == nil || updated.(model).notifications.review == nil {
		t.Fatal("tracking did not refresh Notifications while keeping the proposal reader")
	}
}

func TestNotificationsShowOneCardPerItemAndViewEachSource(t *testing.T) {
	root := baselineRoot(t)
	logScript := `#!/usr/bin/env python3
import json, pathlib, sys
with pathlib.Path('notification-calls.jsonl').open('a') as out:
    out.write(json.dumps(sys.argv[1:]) + '\n')
print('{}')
`
	for _, name := range []string{"cache", "auto-close"} {
		if err := os.WriteFile(filepath.Join(root, "bin", name), []byte(logScript), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	m := baselineModel(t, root)
	tracked := trackedRow{Title: "Fixture", NewCount: 2, CheckedAt: "2026-09-26T20:00:00Z"}
	tracked.Identity.Kind, tracked.Identity.Number = "pr", 3
	m.notifications = notificationsUI{
		open:      true,
		tracked:   &trackedPage{Rows: []trackedRow{tracked}, Total: 1, UnreadTotal: 1},
		proposals: autoCloseList{Rows: []autoCloseRow{{Number: 3, Title: "Fixture", Status: "pending", Active: true, Needs: true, Checkpoint: strings.Repeat("a", 64)}}},
		attention: &attentionPage{Rows: []attentionRow{{Number: 3, Selectable: true, Attention: true, WatchCheckpoint: strings.Repeat("b", 64)}}},
		closures:  &actionHistoryPage{Rows: []actionHistoryRow{{Number: 3, Selectable: true, HistoryCheckpoint: strings.Repeat("c", 64)}}},
	}
	choices := m.notifications.choices()
	if len(choices) != 1 || choices[0].kind != "item" || choices[0].key != (Key{Kind: "pr", Number: 3}) || !m.notifications.choiceNeeds(choices[0]) {
		t.Fatalf("notification sources were not grouped: %+v", choices)
	}
	if strings.Count(m.notificationsView(), "PR #3: Fixture") != 1 {
		t.Fatal("PR appears more than once in Notifications")
	}
	m = baselineSend(m, tea.KeyPressMsg{Text: "1"})
	if m.notifications.review != nil || m.notifications.sourceOpen {
		t.Fatal("number key unexpectedly opened a notification source")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.notifications.sourceOpen || !strings.Contains(m.notificationsView(), "Current item") {
		t.Fatal("Enter did not open the item's source list")
	}
	m = baselineSend(m, tea.KeyPressMsg{Text: "j"})
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.notificationPR.open {
		t.Fatal("source list did not open the selected item")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if !m.notifications.sourceOpen || m.notificationPR.open {
		t.Fatal("returning from the item did not restore its source list")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.notifications.sourceOpen || !m.notifications.open {
		t.Fatal("Esc did not return from sources to Notifications")
	}

	next, cmd := m.handleNotificationsKey(tea.KeyPressMsg{Text: "v"})
	if cmd == nil {
		t.Fatal("v did not act on the grouped item")
	}
	result := cmd().(notificationItemDoneMsg)
	if result.err != nil || result.completed != 4 || result.total != 4 || !next.(model).trackingBusy {
		t.Fatalf("v did not update every source: %+v", result)
	}
	data, err := os.ReadFile(filepath.Join(root, "notification-calls.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"\"view\"", "\"track-read\"", "\"notification-view\""} {
		if !strings.Contains(string(data), command) {
			t.Fatalf("missing %s in script calls: %s", command, data)
		}
	}

	_, cmd = m.handleNotificationsKey(tea.KeyPressMsg{Text: "d"})
	dismissed := cmd().(notificationItemDoneMsg)
	if dismissed.err != nil || dismissed.completed != 4 {
		t.Fatalf("d did not dismiss every source: %+v", dismissed)
	}
	data, err = os.ReadFile(filepath.Join(root, "notification-calls.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"\"dismiss\"", "\"track-remove\"", "\"notification-dismiss\""} {
		if !strings.Contains(string(data), command) {
			t.Fatalf("missing %s in script calls: %s", command, data)
		}
	}
}

func TestCompletedClosureLeavesNotificationsWithoutTracking(t *testing.T) {
	m := baselineModel(t, baselineRoot(t))
	m.notifications = notificationsUI{open: true, proposals: autoCloseList{Rows: []autoCloseRow{{Number: 3, Title: "Completed fixture", Status: "executed"}, {Number: 4, Title: "Needs inspection", Status: "uncertain"}}}}
	choices := m.notifications.choices()
	if len(choices) != 1 || choices[0].key != (Key{Kind: "pr", Number: 4}) || strings.Contains(m.notificationsView(), "PR #3") {
		t.Fatalf("completed closure remained a notification: %+v", choices)
	}
}

func TestBaselineReadCancellationStopsChildProcess(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/usr/bin/env python3\nimport pathlib, time\npathlib.Path('started').write_text('yes')\ntime.sleep(30)\n"
	if err := os.WriteFile(filepath.Join(root, "bin", "enrich-one"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	p := &readProcess{}
	done := make(chan error, 1)
	go func() { _, err := runReadScript(p, root, "enrich-one"); done <- err }()
	t.Cleanup(p.stop)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "started")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("read script did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.stop()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled read kept running")
	}
}
