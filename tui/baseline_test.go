package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

func TestBaselineScriptsUseSelectedInstall(t *testing.T) {
	oldRoot := baselineRoot(t)
	selectedRoot := baselineRoot(t)
	t.Setenv("TRIAGE_ROOT", oldRoot)

	alias := filepath.Join(t.TempDir(), "selected")
	if err := os.Symlink(selectedRoot, alias); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	relativeRoot, err := filepath.Rel(cwd, alias)
	if err != nil {
		t.Fatal(err)
	}

	ledger := filepath.Join("data", "owner", "repo", "ledger.jsonl")
	oldBefore, err := os.ReadFile(filepath.Join(oldRoot, ledger))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := runScript(relativeRoot, "apply", "--number", "1", "--kind", "issue", "--category", "bug", "--action", "label-only", "--reason", "selected install", "--by", "tester"); err != nil {
		t.Fatal(err)
	}
	if got := baselineLedgerRow(t, selectedRoot)["reason"]; got != "selected install" {
		t.Fatalf("selected ledger reason = %v", got)
	}
	oldAfter, err := os.ReadFile(filepath.Join(oldRoot, ledger))
	if err != nil {
		t.Fatal(err)
	}
	if string(oldAfter) != string(oldBefore) {
		t.Fatal("inherited TRIAGE_ROOT changed the previous install's ledger")
	}

	script := "#!/usr/bin/env python3\nfrom _install import WORK_ROOT\nprint(WORK_ROOT)\n"
	if err := os.WriteFile(filepath.Join(selectedRoot, "bin", "selected-root"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := runReadScript(&readProcess{}, relativeRoot, "selected-root")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(out), filepath.Clean(alias); got != want {
		t.Fatalf("read script root = %q, want %q", got, want)
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
	m.detail.Resize(80, 20)
	m.detail.populate(EnrichedItem{
		CommentBodies: []string{"A saved comment"},
		CachedRead:    true,
		Evidence: &batchEvidence{Mode: "refresh", Components: map[string]*evidenceComponent{
			"comments": {Status: "complete", FetchedAt: "2026-09-27T21:33:07Z", Object: json.RawMessage(`[]`)},
		}},
	})
	if view := ansi.Strip(m.detail.View()); !strings.Contains(view, "A saved comment") || strings.Contains(view, "Item details refreshed") || strings.Contains(view, "comments: complete") {
		t.Fatalf("notification comments included redundant evidence notice: %q", view)
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
	m = baselineSend(m, ledgerReloadedMsg{root: m.installRoot, repo: m.repo, items: items})
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

func TestBaselineUnlistedLedgerDecisionRequiresExplicitCorrection(t *testing.T) {
	root := baselineRoot(t)
	path := filepath.Join(root, "data/owner/repo/ledger.jsonl")
	row := strings.Replace(baselineRow(1), `"category":""`, `"category":"retired"`, 1)
	row = strings.Replace(row, `"action":""`, `"action":"archive"`, 1)
	row = strings.Replace(row, `"confidence":""`, `"confidence":"obsolete"`, 1)
	if err := os.WriteFile(path, []byte(row+baselineRow(2)), 0o644); err != nil {
		t.Fatal(err)
	}

	m := baselineModel(t, root)
	m.activateTab(untriagedTab)
	m.openItem(m.items[0])
	if m.form.Category() != "retired" || m.form.Action() != "archive" || m.form.Confidence() != "obsolete" {
		t.Fatal("unlisted decision values changed on load")
	}
	if _, cmd := m.requestSave(); cmd != nil {
		t.Fatal("unlisted decision was accepted")
	}
	if got := baselineLedgerRow(t, root); got["category"] != "retired" || got["action"] != "archive" {
		t.Fatal("blocked save changed the ledger")
	}

	m.form.FocusField(fieldCategory)
	m.form.CycleValue(0)
	m.form.FocusField(fieldAction)
	m.form.CycleValue(0)
	m.commitDraftIfDirty()
	m.loadForm(m.items[0])
	if m.form.Category() != "bug" || m.form.Action() != "label-only" || m.form.Confidence() != "obsolete" {
		t.Fatal("draft lost corrected or unlisted values")
	}
	m.form.FocusField(fieldConfidence)
	m.form.CycleValue(0)
	m.commitDraftIfDirty()
	m.loadForm(m.items[0])
	if m.form.InvalidValues() != "" {
		t.Fatal("corrected draft retained an invalid flag")
	}
	m.form.reason.SetValue("Explicitly checked")
	_, cmd := m.requestSave()
	if cmd == nil {
		t.Fatal("corrected draft did not save")
	}
	if result := cmd().(applyDoneMsg); result.err != nil {
		t.Fatal(result.err)
	}
	if got := baselineLedgerRow(t, root); got["category"] != "bug" || got["action"] != "label-only" || got["confidence"] != "low" || got["reason"] != "Explicitly checked" {
		t.Fatalf("corrected decision = %v", got)
	}
}

func TestBaselineLongReasonSurvivesLoadProposalDraftAndSave(t *testing.T) {
	root := baselineRoot(t)
	reason := strings.Repeat("Review café 🦊 evidence. ", 25) + "\n" + strings.Repeat("Second line café 🦊. ", 25)
	path := filepath.Join(root, "data/owner/repo/ledger.jsonl")
	var row map[string]any
	if err := json.Unmarshal([]byte(baselineRow(1)), &row); err != nil {
		t.Fatal(err)
	}
	row["category"], row["action"], row["confidence"], row["reason"] = "bug", "label-only", "", reason
	row["reviewed"], row["reviewed_by"] = true, "tester"
	data, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(append(data, '\n'), []byte(baselineRow(2))...), 0o644); err != nil {
		t.Fatal(err)
	}

	m := baselineModel(t, root)
	m.activateTab(untriagedTab)
	m.openItem(m.items[0])
	if m.form.Reason() != reason {
		t.Fatal("loaded reason was truncated")
	}
	if m.form.Confidence() != "" {
		t.Fatal("blank saved confidence became a taxonomy default")
	}
	m.form.ApplyDraft(m.form.Snapshot())
	if m.form.Reason() != reason {
		t.Fatal("draft reason was truncated")
	}
	m.form.ApplyProposal(proposal{Category: "bug", Action: "label-only", Confidence: "", Reason: reason})
	if m.form.Reason() != reason || m.form.Confidence() != "" {
		t.Fatal("proposal reason or confidence changed")
	}
	_, cmd := m.requestSave()
	if cmd == nil {
		t.Fatal("save produced no command")
	}
	if result := cmd().(applyDoneMsg); result.err != nil {
		t.Fatal(result.err)
	}
	if got := baselineLedgerRow(t, root); got["reason"] != reason || got["confidence"] != "" || got["reviewed"] != true {
		t.Fatalf("saved long reason or original approval changed: %v", got)
	}
}

func TestBaselinePendingSavePinsRepositoryAndReleasesSwitch(t *testing.T) {
	root := baselineRoot(t)
	other := DataDir(root, "other/repo")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "ledger.jsonl"), []byte(baselineRow(1)), 0o644); err != nil {
		t.Fatal(err)
	}
	oldLedger := filepath.Join(root, "data/owner/repo/ledger.jsonl")
	newLedger := filepath.Join(other, "ledger.jsonl")
	oldBefore, err := os.ReadFile(oldLedger)
	if err != nil {
		t.Fatal(err)
	}
	newBefore, err := os.ReadFile(newLedger)
	if err != nil {
		t.Fatal(err)
	}

	m := baselineModel(t, root)
	m.activateTab(untriagedTab)
	m.selectCurrentListItem()
	m.detail.loading = false
	m.form.ApplyProposal(proposal{Category: "bug", Action: "label-only", Reason: "old repository decision"})
	next, cmd := m.Update(tea.KeyPressMsg{Text: "s"})
	m = next.(model)
	if cmd == nil || m.switchBusy() == "" {
		t.Fatal("queued save did not block switching")
	}
	m.editingRepo = true
	m.repoInput.SetValue("other/repo")
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.repo != "owner/repo" || !strings.Contains(m.status, "Can't switch yet") {
		t.Fatal("repository picker switched during a pending save")
	}

	if err := os.WriteFile(filepath.Join(root, "config", "repo"), []byte("other/repo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	msg := cmd().(applyDoneMsg)
	if msg.err == nil || !strings.Contains(msg.err.Error(), "repository changed") {
		t.Fatal("queued save accepted a changed config/repo")
	}
	m = baselineSend(m, msg)
	if m.switchBusy() != "" || m.form.Reason() != "old repository decision" {
		t.Fatal("failed save did not release switching or preserve the draft")
	}
	oldAfter, err := os.ReadFile(oldLedger)
	if err != nil || string(oldAfter) != string(oldBefore) {
		t.Fatal("original ledger changed", err)
	}
	newAfter, err := os.ReadFile(newLedger)
	if err != nil || string(newAfter) != string(newBefore) {
		t.Fatal("new repository ledger changed", err)
	}
}

func TestBaselineApplyRepliesRequireOrigin(t *testing.T) {
	root := baselineRoot(t)
	m := baselineModel(t, root)
	m.pendingApply = 1
	m.drafts[Key{Kind: "issue", Number: 1}] = decisionSnapshot{}
	key := Key{Kind: "issue", Number: 1}
	m = baselineSend(m, applyDoneMsg{root: "another install", repo: m.repo, key: key})
	m = baselineSend(m, applyDoneMsg{root: root, repo: "other/repo", key: key})
	if m.pendingApply != 1 || len(m.drafts) != 1 || m.status == "Saved." {
		t.Fatal("stale apply reply changed current state")
	}
	m = baselineSend(m, applyDoneMsg{root: root, repo: m.repo, key: key})
	if m.pendingApply != 0 || len(m.drafts) != 0 {
		t.Fatal("successful apply did not release switching or clear the draft")
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
	m = baselineSend(m, ledgerReloadedMsg{root: root, repo: "owner/repo", items: baselineItems()})
	if len(m.items) != 1 || m.items[0].Number != 7 {
		t.Fatal("late reply replaced the new repository's ledger")
	}
	m = baselineSend(m, ledgerReloadedMsg{root: "another install", repo: "other/repo", items: baselineItems()})
	if len(m.items) != 1 || m.items[0].Number != 7 {
		t.Fatal("reply from another install replaced the ledger")
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
args = sys.argv[1:]
with pathlib.Path('notification-calls.jsonl').open('a') as out:
    out.write(json.dumps(args) + '\n')
if 'reject' in args:
    if pathlib.Path('reject-fail').exists():
        sys.exit(1)
    print(json.dumps(dict(number=3, status='rejected', checkpoint='d' * 64,
                          rejection=dict(proposal_checkpoint='a' * 64, by='tester', reason=args[args.index('--reason') + 1]))))
else:
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
	if m.notifications.review != nil || m.notificationPR.open {
		t.Fatal("number key unexpectedly opened the notification")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.notifications.review == nil || !strings.Contains(m.notificationsView(), "Comment to publish") {
		t.Fatal("Enter did not open the closure proposal directly")
	}
	m = baselineSend(m, tea.KeyPressMsg{Text: "t"})
	if !m.attention.open {
		t.Fatal("saved discussion was not accessible from the proposal")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	m = baselineSend(m, tea.KeyPressMsg{Text: "i"})
	if !m.actionHistory.open {
		t.Fatal("closure history was not accessible from the proposal")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	m = baselineSend(m, tea.KeyPressMsg{Text: "l"})
	if !m.notificationPR.open {
		t.Fatal("l on the proposal did not open the PR")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.notifications.review == nil || m.notificationPR.open {
		t.Fatal("returning from the PR did not restore its proposal")
	}
	m = baselineSend(m, tea.KeyPressMsg{Text: "d"})
	if !m.comment.open || m.notifications.review == nil {
		t.Fatal("d in proposal review did not open the rejection composer")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.comment.open || m.notifications.review == nil {
		t.Fatal("canceling rejection did not restore proposal review")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.notificationPR.open {
		t.Fatal("Enter on the proposal did not open the PR")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.notifications.review != nil || !m.notifications.open {
		t.Fatal("Esc did not return from proposal to Notifications")
	}
	t.Setenv("TMPDIR", t.TempDir())
	next, cmd := m.handleNotificationsKey(tea.KeyPressMsg{Text: "D"})
	m = next.(model)
	if cmd == nil || !m.comment.open || !m.comment.busy || m.comment.rejectionCheckpoint != strings.Repeat("a", 64) {
		t.Fatal("D did not open the rejection reason in $EDITOR")
	}
	m = baselineSend(m, commentEditorMsg{root: root, repo: m.repo, key: Key{Kind: "pr", Number: 3}, body: "Reason from editor"})
	if !m.comment.previewing || m.comment.text.Value() != "Reason from editor" {
		t.Fatal("edited rejection reason did not return to proposal preview")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.comment.open || m.notifications.review != nil {
		t.Fatal("canceling the edited rejection left a modal open")
	}

	next, cmd = m.handleNotificationsKey(tea.KeyPressMsg{Text: "v"})
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

	next, cmd = m.handleNotificationsKey(tea.KeyPressMsg{Text: "d"})
	m = next.(model)
	if !m.comment.open || m.comment.rejectionCheckpoint != strings.Repeat("a", 64) {
		t.Fatal("d did not open the comment composer for rejection")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.comment.open {
		t.Fatal("Esc did not cancel rejection")
	}
	next, _ = m.handleNotificationsKey(tea.KeyPressMsg{Text: "d"})
	m = next.(model)
	m.comment.text.SetValue("Reason retained on failure")
	if err := os.WriteFile(filepath.Join(root, "reject-fail"), []byte("yes"), 0o644); err != nil {
		t.Fatal(err)
	}
	next, cmd = m.handleCommentKey(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = baselineSend(next.(model), cmd().(notificationRejectionDoneMsg))
	if !m.comment.open || m.comment.text.Value() != "Reason retained on failure" {
		t.Fatal("failed rejection lost the composer draft")
	}
	if err := os.Remove(filepath.Join(root, "reject-fail")); err != nil {
		t.Fatal(err)
	}
	m.comment.text.SetValue("")
	next, cmd = m.handleCommentKey(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	dismissed := cmd().(notificationRejectionDoneMsg)
	if dismissed.err != nil || !dismissed.rejected || dismissed.completed != 4 {
		t.Fatalf("d did not reject and dismiss every source: %+v", dismissed)
	}
	finished, _ := next.(model).finishNotificationRejection(dismissed)
	if finished.(model).comment.open || finished.(model).notifications.review != nil {
		t.Fatal("completed rejection left the composer or exact review open")
	}
	data, err = os.ReadFile(filepath.Join(root, "notification-calls.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"\"reject\"", "\"--reason\", \"\"", "\"dismiss\"", strings.Repeat("d", 64), "\"track-remove\"", "\"notification-dismiss\""} {
		if !strings.Contains(string(data), command) {
			t.Fatalf("missing %s in script calls: %s", command, data)
		}
	}

	m.comment.open = false
	m.notifications.proposals.Rows = nil
	_, cmd = m.handleNotificationsKey(tea.KeyPressMsg{Text: "d"})
	if cmd == nil {
		t.Fatal("d did not dismiss a notification without a pending proposal")
	}
	plainDismissal := cmd().(notificationItemDoneMsg)
	if plainDismissal.err != nil || plainDismissal.completed != 3 {
		t.Fatalf("presentation-only dismissal changed: %+v", plainDismissal)
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.notificationPR.open || m.notifications.review != nil {
		t.Fatal("notification without a closure proposal did not open the item directly")
	}
}

func TestCommentComposerEditorAndPreviewExit(t *testing.T) {
	root := baselineRoot(t)
	target := commentTarget{key: Key{Kind: "pr", Number: 3}, host: "github.com", url: "https://github.com/owner/repo/pull/3"}
	for _, flow := range []string{"comment", "close", "reopen", "rejection"} {
		for _, fromPreview := range []bool{false, true} {
			name := flow + "/composer"
			if fromPreview {
				name = flow + "/preview"
			}
			t.Run(name, func(t *testing.T) {
				t.Setenv("TMPDIR", t.TempDir())
				m := baselineModel(t, root)
				var targets []commentTarget
				if flow == "reopen" {
					targets = []commentTarget{target}
				}
				next, _ := m.openCommentComposer(target, flow == "close", flow == "reopen", targets)
				m = next.(model)
				if flow == "rejection" {
					m.comment.rejectionCheckpoint = strings.Repeat("a", 64)
				}
				m.comment.text.SetValue("Current draft")
				m.comment.previewing = fromPreview

				next, editorCmd := m.handleCommentKey(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
				m = next.(model)
				if editorCmd == nil || !m.comment.open || !m.comment.busy {
					t.Fatal("Ctrl-E did not open $EDITOR for the current composer")
				}
				m = baselineSend(m, commentEditorMsg{root: root, repo: m.repo, key: target.key, body: "Edited draft"})
				if !m.comment.open || !m.comment.previewing || m.comment.text.Value() != "Edited draft" {
					t.Fatal("edited draft did not return to preview")
				}
				m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEsc})
				if m.comment.open {
					t.Fatal("Esc from preview did not close the floating window")
				}
			})
		}
	}
}

func TestPendingProposalEditKeepsDraftAndReturnsToNewReview(t *testing.T) {
	root := baselineRoot(t)
	t.Setenv("TMPDIR", t.TempDir())
	script := `#!/usr/bin/env python3
import json, pathlib, sys
args = sys.argv[1:]
if 'edit' not in args:
    sys.exit('unexpected auto-close operation')
if pathlib.Path('edit-fail').exists():
    sys.exit('saved proposal changed')
comment = pathlib.Path(args[args.index('--comment-file') + 1]).read_text()
if '--rationale' in args:
    sys.exit('unexpected rationale')
print(json.dumps(dict(number=3, status='pending', checkpoint='b' * 64, comment=comment)))
`
	if err := os.WriteFile(filepath.Join(root, "bin", "auto-close"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	m := baselineModel(t, root)
	old := autoCloseRow{Number: 3, Title: "Fixture", Target: "https://github.com/owner/repo/pull/3", Status: "pending", Active: true,
		Checkpoint: strings.Repeat("a", 64), Comment: "Original comment", Inputs: &autoCloseInputs{ContextCheckpoint: "context"}}
	m.notifications = notificationsUI{open: true, proposals: autoCloseList{Rows: []autoCloseRow{old}}, ticked: map[int]bool{3: true}, review: &autoCloseReview{}}
	m.notifications.review.Plan.Proposals = []autoCloseRow{old}
	next, _ := m.handleNotificationsKey(tea.KeyPressMsg{Text: "e"})
	m = next.(model)
	if !m.comment.open || m.comment.text.Value() != old.Comment || strings.Contains(ansi.Strip(m.commentView()), "Rationale") {
		t.Fatal("proposal edit did not open a single comment draft")
	}
	next, editorCmd := m.handleCommentKey(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	m = next.(model)
	if editorCmd == nil || !m.comment.busy {
		t.Fatal("Ctrl-E did not open the proposal comment in $EDITOR")
	}
	m = baselineSend(m, commentEditorMsg{root: root, repo: m.repo, key: Key{Kind: "pr", Number: 3}, field: "comment", body: "Edited exact comment"})
	if m.comment.text.Value() != "Edited exact comment" || !m.comment.previewing {
		t.Fatal("external editor did not return the proposal comment for review")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if err := os.WriteFile(filepath.Join(root, "edit-fail"), []byte("yes"), 0o644); err != nil {
		t.Fatal(err)
	}
	next, saveCmd := m.handleCommentKey(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = baselineSend(next.(model), saveCmd().(proposalEditDoneMsg))
	if !m.comment.open || m.comment.text.Value() != "Edited exact comment" || m.notifications.review == nil {
		t.Fatal("failed proposal edit discarded its draft or old review")
	}
	if err := os.Remove(filepath.Join(root, "edit-fail")); err != nil {
		t.Fatal(err)
	}

	next, saveCmd = m.handleCommentKey(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = next.(model)
	saved := saveCmd().(proposalEditDoneMsg)
	late := saved
	late.generation++
	ignored, _ := m.finishProposalEdit(late)
	if !ignored.(model).comment.busy {
		t.Fatal("stale proposal edit reply changed the active composer")
	}
	next, reloadCmd := m.finishProposalEdit(saved)
	m = next.(model)
	if reloadCmd == nil || m.comment.open || m.notifications.review != nil || len(m.notifications.ticked) != 0 || m.notifications.openProposalAfter != (Key{Kind: "pr", Number: 3}) {
		t.Fatal("saved proposal edit kept an old review or selected-set plan")
	}
	fresh := old
	fresh.Checkpoint, fresh.Comment = strings.Repeat("b", 64), "Edited exact comment"
	loaded := notificationsMsg{root: root, repo: m.repo, generation: m.notificationsGeneration, proposals: autoCloseList{Rows: []autoCloseRow{fresh}}, tracked: trackedPage{}, attention: attentionPage{}, closures: actionHistoryPage{}}
	next, contextCmd := m.finishNotifications(loaded)
	m = next.(model)
	if contextCmd == nil || m.notifications.review == nil || m.notifications.review.Plan.Proposals[0].Checkpoint != fresh.Checkpoint {
		t.Fatal("edited proposal did not return to its new review")
	}
}

func TestGroupHandoffCopiesCurrentEditedContextForSelectedMembers(t *testing.T) {
	root := baselineRoot(t)
	script, err := os.ReadFile(filepath.Join("..", "bin", "group"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "group"), script, 0o755); err != nil {
		t.Fatal(err)
	}
	var pr map[string]any
	if err := json.Unmarshal([]byte(baselineRow(1)), &pr); err != nil {
		t.Fatal(err)
	}
	pr["kind"], pr["number"], pr["title"] = "pr", 3, "Reviewed candidate"
	pr["url"], pr["category"], pr["action"], pr["confidence"] = "https://github.com/owner/repo/pull/3", "enhancement", "keep-open", "high"
	pr["reason"], pr["reviewed"], pr["reviewed_by"], pr["reviewer_notes"] = "Maintainer guidance", true, "maintainer", "Check compatibility first"
	prJSON, err := json.Marshal(pr)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "data/owner/repo/ledger.jsonl"), append(append(prJSON, '\n'), []byte(baselineRow(2))...), 0o644); err != nil {
		t.Fatal(err)
	}

	call := func(args ...string) Group {
		t.Helper()
		out, err := runScript(root, "group", args...)
		if err != nil {
			t.Fatal(err)
		}
		var group Group
		if err := json.Unmarshal([]byte(out), &group); err != nil {
			t.Fatal(err)
		}
		return group
	}
	g := call("create", "--title", "Compare fixes", "--description", "Agent's draft guidance", "--by", "agent:helper")
	g = call("add", g.ID, "--kind", "pr", "--number", "3", "--notes", "Agent's candidate note", "--by", "agent:helper")
	g = call("add", g.ID, "--kind", "issue", "--number", "2", "--notes", "Original report", "--by", "agent:helper")
	g = call("update", g.ID, "--revision", strconv.Itoa(g.Revision), "--description", "Maintainer edited the group question", "--by", "maintainer")

	m := baselineModel(t, root)
	m.reviewer = "maintainer"
	m.groups = groupUI{open: true, detail: true, records: []Group{g}, member: 0, ticked: map[Key]bool{{Kind: "pr", Number: 3}: true}}
	next, cmd := m.Update(tea.KeyPressMsg{Text: "e"})
	m = next.(model)
	if cmd == nil || m.groups.editing != "notes" || m.groups.note.text.Value() != "Agent's candidate note" || !strings.Contains(ansi.Strip(m.viewContent()), "Member note") {
		t.Fatal("e did not open the member note in a floating editor")
	}
	longNote := "Maintainer wants the candidate reconsidered.\nKeep its unique behavior across all supported versions."
	m.groups.note.text.SetValue(longNote)
	m = baselineSend(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	preview := ansi.Strip(m.viewContent())
	if !m.groups.note.previewing || !strings.Contains(preview, "Preview member note") || !strings.Contains(preview, "Keep its unique behavior") {
		t.Fatal("multiline member note did not render in the floating preview")
	}
	t.Setenv("TMPDIR", t.TempDir())
	next, cmd = m.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	m = next.(model)
	if cmd == nil || !m.groups.busy {
		t.Fatal("Ctrl-E did not open the current member note in $EDITOR")
	}
	note := m.groups.note
	m = baselineSend(m, groupNoteEditorMsg{root: root, repo: m.repo, groupID: note.groupID, revision: note.revision,
		member: note.member, request: note.request, text: longNote})
	if m.groups.busy || !m.groups.note.previewing || m.groups.note.text.Value() != longNote {
		t.Fatal("external editor did not retain the multiline member note for review")
	}
	next, cmd = m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = next.(model)
	if cmd == nil || !m.groups.busy {
		t.Fatal("Ctrl-S did not save the member note through bin/group")
	}
	m = baselineSend(m, cmd().(groupsLoadedMsg))
	g = *m.selectedGroup()
	if m.groups.editing != "" || g.Members[0].Notes != longNote || g.Members[0].UpdatedBy != "maintainer" {
		t.Fatal("saved member note did not return to the group with attribution")
	}
	m.groups.member = 1
	next, cmd = m.Update(tea.KeyPressMsg{Text: "y"})
	m = next.(model)
	if cmd == nil || !m.groups.busy {
		t.Fatal("y did not start a fresh group handoff")
	}
	handoff := cmd().(groupHandoffMsg)
	if handoff.err != nil || len(handoff.selected) != 1 || handoff.selected[0] != (Key{Kind: "pr", Number: 3}) {
		t.Fatalf("y did not scope the handoff to the ticked member: %+v", handoff)
	}
	text := groupHandoffText(m.yankHeader("group handoff"), handoff)
	scope := strings.SplitN(text, "Assess these members", 2)[0]
	for _, expected := range []string{"- pr #3", "Maintainer edited the group question", "Maintainer wants the candidate reconsidered", "Check compatibility first", "Human review: confirmed by maintainer", "#### issue #2", "Current member checkpoints"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("edited context missing from group handoff: %s", expected)
		}
	}
	for _, excess := range []string{"```json", `"local_context"`, `"labels": []`, "- Assignee:", "- Agent note:"} {
		if strings.Contains(text, excess) {
			t.Fatalf("group handoff copied empty or raw fields: %s", excess)
		}
	}
	raw, err := runScript(root, "group", "--expected-repo", "owner/repo", "export", g.ID, "--format", "json")
	if err != nil || len(text) >= len(raw) {
		t.Fatal("group handoff did not reduce the agent context compared with the full packet")
	}
	if strings.Contains(scope, "- issue #2") {
		t.Fatal("y included an unticked member in the proposal scope")
	}
	next, copyCmd := m.finishGroupHandoff(handoff)
	m = next.(model)
	if copyCmd == nil || m.groups.busy {
		t.Fatal("completed handoff did not reach the existing copy path")
	}

	next, cmd = m.Update(tea.KeyPressMsg{Text: "Y"})
	m = next.(model)
	all := cmd().(groupHandoffMsg)
	if all.err != nil || len(all.selected) != 2 || all.selected[1] != (Key{Kind: "issue", Number: 2}) {
		t.Fatal("Y did not explicitly select all group members")
	}
	next, _ = m.finishGroupHandoff(all)
	m = next.(model)
	m.groups.ticked = map[Key]bool{}
	next, cmd = m.Update(tea.KeyPressMsg{Text: "y"})
	m = next.(model)
	hovered := cmd().(groupHandoffMsg)
	if hovered.err != nil || len(hovered.selected) != 1 || hovered.selected[0] != (Key{Kind: "issue", Number: 2}) {
		t.Fatal("y without ticks did not select the hovered member")
	}
	next, _ = m.finishGroupHandoff(hovered)
	m = next.(model)

	call("update", g.ID, "--revision", strconv.Itoa(g.Revision), "--description", "Changed again", "--by", "maintainer")
	next, cmd = m.Update(tea.KeyPressMsg{Text: "y"})
	m = next.(model)
	stale := cmd().(groupHandoffMsg)
	if stale.err == nil || stale.packet.Group.ID != "" {
		t.Fatal("changed group revision became an agent handoff")
	}
	next, copyCmd = m.finishGroupHandoff(stale)
	m = next.(model)
	if copyCmd != nil {
		t.Fatal("stale group handoff reached the clipboard path")
	}
	m.groups.member = 0
	next, _ = m.Update(tea.KeyPressMsg{Text: "e"})
	m = next.(model)
	m.groups.note.text.SetValue(longNote + "\nOne more requested check.")
	next, cmd = m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = next.(model)
	m = baselineSend(m, cmd().(groupsLoadedMsg))
	if m.groups.editing != "notes" || m.groups.note.text.Value() != longNote+"\nOne more requested check." || !m.statusIsError() {
		t.Fatal("stale member-note save did not retain the draft and report the error")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.groups.editing != "" {
		t.Fatal("Esc from member-note preview did not close the floating window")
	}
}

func TestGroupContextScrollStopsAtVisibleBoundary(t *testing.T) {
	m := baselineModel(t, baselineRoot(t))
	lines := make([]string, 40)
	for i := range lines {
		lines[i] = fmt.Sprintf("note line %d", i)
	}
	g := Group{ID: "focused", Title: "Review notes", Revision: 1, Members: []GroupMember{
		{Kind: "issue", Number: 1, Notes: strings.Join(lines, "\n")},
		{Kind: "issue", Number: 2, Notes: "Short note"},
	}}
	m.groups = groupUI{open: true, detail: true, records: []Group{g}}
	for range 20 {
		m = baselineSend(m, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	}
	vp := m.groupContextViewport(g, m.menuWidth(), m.mainHeight())
	if !vp.AtBottom() || m.groups.previewOffset != vp.YOffset() || !strings.Contains(ansi.Strip(vp.View()), "note line 39") {
		t.Fatal("group context stopped before its last visible note")
	}
	bottom := m.groups.previewOffset
	m = baselineSend(m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
	if m.groups.previewOffset >= bottom {
		t.Fatal("Ctrl-U did not move immediately after scrolling to the bottom")
	}
	m = baselineSend(m, tea.KeyPressMsg{Text: "j"})
	if m.groups.member != 1 || m.groups.previewOffset != 0 {
		t.Fatal("the next member inherited the previous note's scroll position")
	}
}

func TestCompletedClosureLeavesNotificationsWithoutTracking(t *testing.T) {
	m := baselineModel(t, baselineRoot(t))
	rejected := autoCloseRow{Number: 5, Title: "Keep this PR", Status: "rejected", Rejection: &autoCloseRejection{By: "maintainer", At: "2026-09-29T00:00:00Z", Reason: "Compatibility work remains useful"}}
	m.notifications = notificationsUI{open: true, proposals: autoCloseList{Rows: []autoCloseRow{{Number: 3, Title: "Completed fixture", Status: "executed"}, {Number: 4, Title: "Needs inspection", Status: "uncertain"}, rejected}}}
	choices := m.notifications.choices()
	if len(choices) != 2 || choices[0].key != (Key{Kind: "pr", Number: 4}) || choices[1].key != (Key{Kind: "pr", Number: 5}) || strings.Contains(m.notificationsView(), "PR #3") {
		t.Fatalf("completed closure remained a notification: %+v", choices)
	}
	view := ansi.Strip(m.notificationsView())
	if !strings.Contains(view, "Closure proposal rejected") || strings.Index(view, "PR #5") < strings.Index(view, "Past actions") {
		t.Fatal("rejected proposal was not shown under Past actions")
	}
	m.notifications.review = &autoCloseReview{}
	m.notifications.review.Plan.Proposals = []autoCloseRow{rejected}
	view = ansi.Strip(m.autoCloseReviewView())
	if !strings.Contains(view, "Compatibility work remains useful") || !strings.Contains(view, "maintainer") || strings.Contains(view, "Publish the comment below") {
		t.Fatal("rejected proposal reader hid the reason or offered publication")
	}
	_, command := m.handleNotificationsKey(tea.KeyPressMsg{Text: "a"})
	if command != nil {
		t.Fatal("rejected proposal started approval")
	}
}

func TestProposalReaderShowsCurrentGuidanceAndBlocksStaleApproval(t *testing.T) {
	root := baselineRoot(t)
	script := `#!/usr/bin/env python3
import json, pathlib, sys
args = sys.argv[1:]
row = dict(number=3, title="Fixture PR", target="https://github.com/owner/repo/pull/3", rationale="Superseded", comment="Exact closure comment", checkpoint="proposal-1", status="pending", active=True, inputs=dict(context_checkpoint="ctx-1", evidence=[dict(kind="pr", number=3, snapshot_id="snapshot-1", components={"summary": {"status": "complete"}})], evidence_gaps=["Gap one", "Gap two", "Gap three", "Gap four", "Gap five"]))
if "context" in args:
    stale = pathlib.Path("stale-context").exists()
    omitted = 30 if pathlib.Path("long-context").exists() else 0
    item = dict(repository="owner/repo", item=dict(kind="pr", number=3), checkpoint="ctx-2" if stale else "ctx-1", requests=0, pagination=dict(offset=0, next_offset=None), rows=[dict(kind="ledger", id="ledger", fields=dict(category=dict(preview="enhancement", omitted_bytes=0), action=dict(preview="keep-open", omitted_bytes=0), reviewer_notes=dict(preview="Check compatibility", omitted_bytes=omitted), reviewed=True)), dict(kind="group", id="group:g1", fields=dict(title=dict(preview="Compatibility review", omitted_bytes=0), status=dict(preview="draft", omitted_bytes=0), description=dict(preview="Compare alternatives", omitted_bytes=0))), dict(kind="member", id="member:g1:pr:3", selected=True, fields=dict(notes=dict(preview="Keep the old API", omitted_bytes=0)))])
    print(json.dumps(dict(repository="owner/repo", number=3, proposal_checkpoint="proposal-1", current=not stale, reason="local context changed" if stale else None, item_context=item, latest_rejection=dict(by="maintainer", at="2026-09-29T00:00:00Z", reason="Earlier objection", proposal_checkpoint="older"), requests=0)))
elif "review" in args:
    print(json.dumps(dict(plan=dict(repo="owner/repo", operation="comment-and-close-pr", proposals=[row]), approval="fresh-approval")))
else:
    sys.exit("unexpected auto-close command")
`
	if err := os.WriteFile(filepath.Join(root, "bin", "auto-close"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	sourceScript := `#!/usr/bin/env python3
import json
text = "Check compatibility across all supported versions"
print(json.dumps(dict(repository="owner/repo", item=dict(kind="pr", number=3), checkpoint="ctx-1", row="ledger", field="reviewer_notes", text=text, bytes=dict(offset=0, returned=len(text.encode())), continuation=None, requests=0)))
`
	if err := os.WriteFile(filepath.Join(root, "bin", "item-context"), []byte(sourceScript), 0o755); err != nil {
		t.Fatal(err)
	}
	var row autoCloseRow
	if err := json.Unmarshal([]byte(`{"number":3,"title":"Fixture PR","target":"https://github.com/owner/repo/pull/3","rationale":"Superseded","comment":"Exact closure comment","checkpoint":"proposal-1","status":"pending","active":true,"inputs":{"context_checkpoint":"ctx-1","evidence":[{"kind":"pr","number":3,"snapshot_id":"snapshot-1","components":{"summary":{"status":"complete"}}}],"evidence_gaps":["Gap one","Gap two","Gap three","Gap four","Gap five"]}}`), &row); err != nil {
		t.Fatal(err)
	}

	m := baselineModel(t, root)
	m.drafts[Key{Kind: "issue", Number: 1}] = decisionSnapshot{}
	m.notifications = notificationsUI{open: true, proposals: autoCloseList{Rows: []autoCloseRow{row}}}
	choice := m.notifications.choices()[0]
	next, cmd := m.openNotificationSource(choice, "proposal")
	m = next.(model)
	if cmd == nil || !m.notifications.contextBusy {
		t.Fatal("proposal did not start a local context read")
	}
	_, approval := m.handleNotificationsKey(tea.KeyPressMsg{Text: "a"})
	if approval != nil {
		t.Fatal("approval was available before context loaded")
	}
	current := cmd().(autoCloseContextMsg)
	if current.err != nil {
		t.Fatal(current.err)
	}
	m = baselineSend(m, current)
	m = baselineSend(m, tea.WindowSizeMsg{Width: 120, Height: 100})
	view := ansi.Strip(m.autoCloseReviewView())
	for _, expected := range []string{"Check compatibility", "Keep the old API", "Earlier objection", "snapshot-1", "Gap one", "Gap five", "Exact closure comment"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("proposal reader omitted %q: %s", expected, view)
		}
	}
	if strings.Contains(view, "Superseded") {
		t.Fatal("legacy rationale duplicated the proposed comment in review")
	}
	last := -1
	for _, section := range []string{"Proposed action", "Comment to publish", "Human context", "Selected evidence"} {
		at := strings.Index(view, section)
		if at <= last {
			t.Fatalf("proposal sections out of order near %q", section)
		}
		last = at
	}
	m = baselineSend(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	for range 200 {
		m = baselineSend(m, tea.KeyPressMsg{Text: "j"})
	}
	bottom := m.notifications.reviewScroll
	if bottom == 0 {
		t.Fatal("proposal never scrolled to its last line")
	}
	m = baselineSend(m, tea.KeyPressMsg{Text: "k"})
	if m.notifications.reviewScroll != bottom-1 {
		t.Fatal("proposal kept invisible scroll steps beyond its last line")
	}
	m.notifications.reviewScroll = 0
	_, reviewCmd := m.handleNotificationsKey(tea.KeyPressMsg{Text: "a"})
	if reviewCmd == nil {
		t.Fatal("current proposal did not prepare exact review")
	}
	review := reviewCmd().(autoCloseMsg)
	if review.err != nil || !review.review.Contexts[3].Current {
		t.Fatalf("exact review did not retain checked context: %+v", review.err)
	}
	missingContext := review
	missingContext.review.Contexts = nil
	_, publish := m.finishAutoClose(missingContext)
	if publish != nil {
		t.Fatal("exact review without a checked context offered publication")
	}
	next, shortNotesCmd := m.handleNotificationsKey(tea.KeyPressMsg{Text: "m"})
	if shortNotesCmd != nil || next.(model).notifications.notesOpen {
		t.Fatal("short local notes opened a duplicate reader")
	}
	if err := os.WriteFile(filepath.Join(root, "long-context"), []byte("yes"), 0o644); err != nil {
		t.Fatal(err)
	}
	next, cmd = m.openNotificationSource(choice, "proposal")
	m = next.(model)
	m = baselineSend(m, cmd().(autoCloseContextMsg))
	underlying := m.autoCloseReviewView()
	next, notesCmd := m.handleNotificationsKey(tea.KeyPressMsg{Text: "m"})
	m = next.(model)
	if notesCmd == nil || !m.notifications.notesOpen {
		t.Fatal("longer local guidance was not available from the proposal")
	}
	m = baselineSend(m, notesCmd().(autoCloseNotesMsg))
	if !strings.Contains(ansi.Strip(m.proposalNotesViewport().View()), "across all supported versions") || m.autoCloseReviewView() != underlying {
		t.Fatal("floating notes failed to show full text without reflowing the proposal")
	}
	m = baselineSend(m, tea.KeyPressMsg{Text: "m"})
	if m.notifications.notesOpen {
		t.Fatal("m did not close the floating notes window")
	}

	if err := os.WriteFile(filepath.Join(root, "stale-context"), []byte("yes"), 0o644); err != nil {
		t.Fatal(err)
	}
	next, cmd = m.openNotificationSource(choice, "proposal")
	m = next.(model)
	wrong := current
	wrong.generation++
	m = baselineSend(m, wrong)
	if !m.notifications.contextBusy {
		t.Fatal("late context reply replaced the pending read")
	}
	m = baselineSend(m, cmd().(autoCloseContextMsg))
	view = ansi.Strip(m.autoCloseReviewView())
	if !strings.Contains(view, "Changed context") || strings.Contains(view, "Publish the comment below") {
		t.Fatal("stale proposal appeared executable")
	}
	next, approval = m.handleNotificationsKey(tea.KeyPressMsg{Text: "a"})
	m = next.(model)
	if approval != nil || len(m.drafts) != 1 || !m.statusIsError() {
		t.Fatal("stale proposal gained approval, discarded a draft or failed to report the refusal")
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
