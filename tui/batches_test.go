package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// batchFixture builds a throwaway repo root with the real bin/apply, a two-issue ledger, and one batch whose decisions file proposes a decision for issue #1 only.
func batchFixture(t *testing.T) string {
	t.Helper()
	// An empty installs registry, not the machine's own.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root := t.TempDir()
	for _, dir := range []string{"bin", "config", "data/owner/repo/batches"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	copyFixtureScripts(t, root, "apply", "batch")

	taxonomy, err := os.ReadFile(filepath.Join("..", "config", "taxonomy.json"))
	if err != nil {
		t.Fatal(err)
	}

	files := map[string]string{
		// What bin/install-to writes to mark an install; every script refuses to run without it.
		MarkerName:                     "{}\n",
		"config/repo":                  "owner/repo\n",
		"config/taxonomy.json":         string(taxonomy),
		"data/owner/repo/ledger.jsonl": ledgerFixtureRow(1, "first") + ledgerFixtureRow(2, "second"),
		"data/owner/repo/batches/b20260101-000000.items.jsonl": `{"number":1,"kind":"issue","body":"first body","comment_bodies":["hello"]}
{"number":2,"kind":"issue","body":"second body","comment_bodies":[]}
`,
		"data/owner/repo/batches/b20260101-000000.decisions.jsonl": `{"number":1,"kind":"issue","category":"support-question","action":"comment-request-info","confidence":"medium","reason":"asks for help"}
{"number":2,"kind":"issue","category":"","action":"","confidence":"","reason":""}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// ledgerFixtureRow mirrors a complete row as bin/sync writes it, since bin/apply rewrites any missing field as "".
func ledgerFixtureRow(number int, title string) string {
	row := map[string]any{
		"number": number, "kind": "issue", "state": "open", "title": title, "url": "", "author": "", "created_at": fmt.Sprintf("2026-01-0%d", number), "updated_at": "",
		"labels": []string{}, "comments_count": 0, "category": "", "action": "", "confidence": "", "reason": "", "triaged_at": "", "triaged_by": "", "batch_id": "",
		"reviewed": false, "reviewed_by": "", "reviewed_at": "", "reviewer_notes": "", "first_seen_at": "", "last_synced_at": "",
	}

	data, _ := json.Marshal(row)

	return string(data) + "\n"
}

func batchModel(t *testing.T, root string) model {
	t.Helper()
	tax, err := LoadTaxonomy(root)
	if err != nil {
		t.Fatal(err)
	}

	items, err := LoadLedger(root, "owner/repo")
	if err != nil {
		t.Fatal(err)
	}

	m := newModel(root, "owner/repo", tax, "tester", items)
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m = send(m, fetchSyncDoneMsg{}) // the startup fetch
	records, err := loadBatches(root, "owner/repo")
	if err != nil {
		t.Fatal(err)
	}

	return send(m, batchesLoadedMsg{records: records})
}

func TestFixedBatchKeepsItsEvidenceAndMissingDiffOffline(t *testing.T) {
	root := batchFixture(t)
	path := filepath.Join(batchesDir(root, "owner/repo"), "b20260101-000000.items.jsonl")
	data := `{"number":1,"kind":"pr","body":"frozen body","evidence":{"snapshot_id":"fixed-id","problems":{"summary":[],"diff":["missing"]}}}` + "\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(strings.Replace(path, ".items.", ".decisions.", 1), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	records, err := loadBatches(root, "owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	m := batchModel(t, root)
	m.batches.records = records
	k := Key{Kind: "pr", Number: 1}
	m.detail.cache[k] = EnrichedItem{Body: "live body", DiffText: "live diff", DiffLoaded: true}
	m.openBatch(records[0].ID)
	if m.detail.SetItem(Item{Kind: "pr", Number: 1}) {
		t.Fatal("fixed packet requested online enrichment")
	}
	if m.detail.enriched.Body != "frozen body" || m.detail.enriched.DiffText != "" || !m.detail.enriched.DiffLoaded {
		t.Fatalf("fixed packet mixed with live evidence: %+v", m.detail.enriched)
	}
	m.detail.OnEnriched(enrichedMsg{key: k, data: EnrichedItem{Body: "late live body"}})
	if m.detail.enriched.Body != "frozen body" {
		t.Fatal("late live response replaced fixed evidence")
	}
	m.detail.width = 80
	m.detail.renderActive()
	if !strings.Contains(m.detail.sections[0].viewport.View(), "diff: missing") {
		t.Fatal("missing evidence diagnostics")
	}
}

func TestFixedBatchDistinguishesMissingAndEmptyDiff(t *testing.T) {
	d := newDetailModel()
	d.SetItem(Item{Kind: "pr", Number: 1})
	d.Resize(100, 30)
	d.populate(EnrichedItem{CachedRead: true, DiffLoaded: true, Evidence: &batchEvidence{Mode: "offline", SnapshotID: "test", Problems: map[string][]string{"diff": {"missing"}}}})
	d.JumpSection(2)
	if !strings.Contains(d.View(), "No cached payload") || d.NeedsActiveDiff() {
		t.Fatal("missing evidence looks empty or starts a fetch")
	}
	d.populate(EnrichedItem{CachedRead: true, DiffLoaded: true, Evidence: &batchEvidence{Mode: "offline", SnapshotID: "empty", Components: map[string]*evidenceComponent{"diff": {Status: "complete", FetchedAt: "2026-09-22", Object: json.RawMessage(`{"sha256":"test"}`)}}}})
	if !strings.Contains(d.View(), "(empty diff)") || !strings.Contains(d.View(), "observed 2026-09-22") {
		t.Fatalf("verified empty diff lost distinction: %s", d.View())
	}
}

func ledgerRow(t *testing.T, root string, number int) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "data", "owner", "repo", "ledger.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}

		if int(row["number"].(float64)) == number {
			return row
		}
	}

	t.Fatalf("#%d missing from ledger", number)

	return nil
}

func TestBatchOpensWithCachedContextAndProposals(t *testing.T) {
	m := batchModel(t, batchFixture(t))
	if len(m.batches.records) != 1 || len(m.batches.records[0].Proposals) != 1 {
		t.Fatalf("expected one batch with one filled proposal, got %+v", m.batches.records)
	}

	m.openBatch("b20260101-000000")
	if !strings.Contains(m.list.Title, "0/2 triaged") {
		t.Fatalf("batch list title = %q", m.list.Title)
	}

	m.selectCurrentListItem()
	if m.detail.loading {
		t.Fatal("opening a batch item re-fetched context bin/batch already saved")
	}

	if !strings.Contains(m.detail.sections[0].source, "first body") {
		t.Fatal("batch body not shown")
	}

	if m.form.Category() != "support-question" || m.form.Reason() != "asks for help" || !m.form.proposed || m.form.dirty {
		t.Fatalf("proposal not prefilled as an unsaved, non-dirty proposal: %+v", m.form.Snapshot())
	}

	m.list.Select(1)
	m.selectCurrentListItem()
	if m.form.proposed || m.form.touched {
		t.Fatal("item without a proposal should show untouched defaults")
	}
}

func TestSaveWarnsOnDefaultsAndEmptyReason(t *testing.T) {
	root := batchFixture(t)
	m := batchModel(t, root)
	m.activateTab(0)
	m.list.Select(1) // #2, no proposal outside a batch either
	m.selectCurrentListItem()
	next, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = next.(model)
	if cmd != nil || !m.confirmSave || !strings.Contains(m.status, "Defaults unchanged") || !strings.Contains(m.status, "no reason") {
		t.Fatalf("first Ctrl-S on untouched defaults should only warn; status %q", m.status)
	}

	m = press(m, "j") // any other key disarms
	next, cmd = m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = next.(model)
	if cmd != nil {
		t.Fatal("a disarmed warning must warn again rather than save")
	}

	next, cmd = m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = next.(model)
	if cmd == nil {
		t.Fatal("second consecutive Ctrl-S should save anyway")
	}

	m = send(m, cmd())
	if row := ledgerRow(t, root, 2); row["category"] != "bug" || row["batch_id"] != "tui" {
		t.Fatalf("forced default save not recorded: %v", row)
	}
}

func TestBatchSaveStampsBatchAndApproveWarnsOnUnsavedEdits(t *testing.T) {
	root := batchFixture(t)
	m := batchModel(t, root)
	m.openBatch("b20260101-000000")
	m.selectCurrentListItem()
	next, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("a complete proposal should save on the first Ctrl-S")
	}

	m = send(next.(model), cmd())
	m = send(m, reloadLedgerCmd(root, "owner/repo")())
	row := ledgerRow(t, root, 1)
	if row["category"] != "support-question" || row["batch_id"] != "b20260101-000000" || row["triaged_by"] != "tester" || row["reviewed"] == true {
		t.Fatalf("batch save not stamped correctly: %v", row)
	}

	if !strings.Contains(m.list.Title, "1/2 triaged") {
		t.Fatalf("batch progress not refreshed: %q", m.list.Title)
	}

	if m.focus != FocusList || m.list.SelectedItem().(listItem).Number != 2 {
		t.Fatal("a save should go back to the batch, on its next item")
	}

	m.list.Select(0)
	m.selectCurrentListItem()
	m = press(m, "tab")
	m = press(m, "down") // unsaved category edit
	next, cmd = m.Update(tea.KeyPressMsg{Text: "a"})
	m = next.(model)
	if cmd != nil || !m.confirmApprove {
		t.Fatal("approving with unsaved edits should warn first")
	}

	next, cmd = m.Update(tea.KeyPressMsg{Text: "a"})
	if cmd == nil {
		t.Fatal("second a should approve the saved decision")
	}

	send(next.(model), cmd())
	if row := ledgerRow(t, root, 1); row["reviewed"] != true || row["category"] != "support-question" {
		t.Fatalf("approval should confirm the saved decision, not the unsaved edit: %v", row)
	}
}

func TestApplyBatchProposalsNeedsConfirmAndKeepsExistingDecisions(t *testing.T) {
	root := batchFixture(t)
	if _, err := runScript(root, "apply", "--number", "1", "--kind", "issue", "--category", "bug", "--action", "label-only", "--reason", "human call", "--by", "human"); err != nil {
		t.Fatal(err)
	}

	m := batchModel(t, root)
	m.batches.open = true
	m = press(m, "A")
	if !strings.Contains(m.status, "No proposals to apply") {
		t.Fatalf("already-triaged proposals should not be offered: %q", m.status)
	}

	out, err := runScript(root, "apply", filepath.Join(root, "data", "owner", "repo", "batches", "b20260101-000000.decisions.jsonl"), "--only-untriaged")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out, "Kept 1 existing") || ledgerRow(t, root, 1)["reason"] != "human call" {
		t.Fatalf("--only-untriaged overwrote a human decision: %s", out)
	}

	root = batchFixture(t)
	m = batchModel(t, root)
	m.batches.open = true
	next, cmd := m.Update(tea.KeyPressMsg{Text: "A"})
	m = next.(model)
	if cmd != nil || m.batches.confirm != "A" {
		t.Fatal("applying proposals should ask for confirmation first")
	}

	next, cmd = m.Update(tea.KeyPressMsg{Text: "A"})
	if cmd == nil {
		t.Fatal("second A should apply")
	}

	send(next.(model), cmd())
	if row := ledgerRow(t, root, 1); row["triaged_by"] != "agent" || row["reviewed"] == true || row["batch_id"] != "b20260101-000000" {
		t.Fatalf("applied proposal should be an unreviewed agent decision: %v", row)
	}
}

func TestBatchFormBuildsBatchArgs(t *testing.T) {
	m := batchModel(t, batchFixture(t))
	m.batches.open = true
	m.batches.groups = []Group{{ID: "g1", Title: "Group one"}}
	m = press(m, "n")
	m = press(m, "enter") // Enter confirms the size and moves on
	m = press(m, "j")     // kind: issue
	m = press(m, "tab")
	m = press(m, "down") // order: newest
	m = press(m, "tab")
	m = press(m, "j") // group: g1
	args, err := m.batchFormArgs()
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.Join(args, " "); got != "25 --order newest --kind issue --group g1" {
		t.Fatalf("bin/batch args = %q", got)
	}

	m.batches.size.SetValue("0")
	if _, err := m.batchFormArgs(); err == nil {
		t.Fatal("size 0 should be rejected")
	}

	assertBounds(t, m.viewContent(), 100, 30)
}

func TestDeleteBatchNeedsConfirmAndLeavesLedger(t *testing.T) {
	root := batchFixture(t)
	m := batchModel(t, root)
	m.openBatch("b20260101-000000")
	before := ledgerRow(t, root, 1)
	m = press(m, "esc") // list -> sidebar
	m.batches.open = true

	m = press(m, "d")
	if m.batches.confirm != "d" || !strings.Contains(m.status, "1 unapplied proposals") {
		t.Fatalf("first d should warn about the unapplied proposal: %q", m.status)
	}

	m = press(m, "j")
	if m.batches.confirm != "" {
		t.Fatal("any other key should disarm the delete")
	}

	m = press(m, "d")
	next, cmd := m.Update(tea.KeyPressMsg{Text: "d"})
	if cmd == nil {
		t.Fatal("second d should delete")
	}

	m = send(next.(model), cmd())
	if len(m.batches.records) != 0 || !strings.Contains(m.status, "Deleted batch") {
		t.Fatalf("batch still listed: %q", m.status)
	}

	if m.activeBatch != "" || m.listReady {
		t.Fatal("the deleted batch should no longer be the active list")
	}

	for _, name := range []string{"b20260101-000000.items.jsonl", "b20260101-000000.decisions.jsonl"} {
		if _, err := os.Stat(filepath.Join(root, "data", "owner", "repo", "batches", name)); !os.IsNotExist(err) {
			t.Fatalf("%s not removed", name)
		}
	}

	if after := ledgerRow(t, root, 1); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatal("deleting a batch changed the ledger")
	}
}

func TestAgentNotesShowFromProposalAndSurviveSave(t *testing.T) {
	root := batchFixture(t)
	decisions := filepath.Join(root, "data/owner/repo/batches/b20260101-000000.decisions.jsonl")
	if err := os.WriteFile(decisions, []byte(`{"number":1,"kind":"issue","category":"support-question","action":"comment-request-info","confidence":"medium","reason":"asks for help","agent_notes":"Comment 1 has the fix.","proposed_by":"agent:alice"}
{"number":2,"kind":"issue","category":"","action":"","confidence":"","reason":""}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	m := batchModel(t, root)
	m.openBatch("b20260101-000000")
	m.selectCurrentListItem()
	if len(m.detail.sections) != 3 || m.detail.sections[1].name != agentNotesSection || m.detail.sections[1].source != "Comment 1 has the fix." {
		t.Fatalf("proposal notes should show as the second section: %+v", m.detail.sections)
	}

	next, cmd := m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = send(next.(model), cmd())
	m = send(m, reloadLedgerCmd(root, "owner/repo")())
	if row := ledgerRow(t, root, 1); row["agent_notes"] != "Comment 1 has the fix." || row["triaged_by"] != "tester" {
		t.Fatalf("accepting a proposal should keep its notes and credit the reviewer: %v", row)
	}

	m.activateTab(len(tabs) - 1) // All Items: the notes now come from the ledger
	m.selectCurrentListItem()
	if m.detail.sections[1].name != agentNotesSection {
		t.Fatalf("ledger agent_notes should show outside the batch too: %+v", m.detail.sections)
	}

	m.list.Select(1)
	m.selectCurrentListItem()
	for _, s := range m.detail.sections {
		if s.name == agentNotesSection {
			t.Fatal("an item without notes should have no Agent notes section")
		}
	}
}

// copyFixtureScripts includes shared modules so throwaway installs follow the scripts' evolving dependencies.
func copyFixtureScripts(t *testing.T, root string, scripts ...string) {
	t.Helper()
	modules, err := filepath.Glob(filepath.Join("..", "bin", "_*.py"))
	if err != nil {
		t.Fatal(err)
	}

	for _, path := range modules {
		scripts = append(scripts, filepath.Base(path))
	}

	for _, name := range scripts {
		data, err := os.ReadFile(filepath.Join("..", "bin", name))
		if err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(root, "bin", name), data, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}
