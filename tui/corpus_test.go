package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func corpusKey(m model, k string) (model, tea.Cmd) {
	if k == "enter" {
		next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		return next.(model), cmd
	}
	msg := tea.KeyPressMsg{Text: k}
	if k == "esc" {
		msg = tea.KeyPressMsg{Code: tea.KeyEsc}
	}
	next, cmd := m.Update(msg)
	return next.(model), cmd
}

func corpusFixture(t *testing.T) model {
	t.Helper()
	m := openFixtureItem(t)
	m.refreshing, m.detail.loading = false, false
	script := `#!/usr/bin/env python3
import json, sys
from pathlib import Path
a = sys.argv[1:]
p = Path('corpus-calls.json')
calls = json.loads(p.read_text()) if p.exists() else []
calls.append(a)
p.write_text(json.dumps(calls))
command = a[4]
plan = Path('fake-plan.json')
if command == 'corpus-create':
    plan.write_text(json.dumps(dict(scope=a[a.index('--scope')+1], profile=a[a.index('--profile')+1], inventory_snapshot=a[a.index('--snapshot')+1])))
    print(json.dumps(dict(corpus_id='b'*64)))
else:
    policy = json.loads(plan.read_text()) if plan.exists() else dict(scope='open-prs', profile='pr-comparison', inventory_snapshot='a'*64)
    result = dict(corpus_id=a[5], repository=dict(host='github.com', full_name='owner/repo'), max_age=86400, members=2, status='pending', declared_counts=dict(pending=2, running=0, complete=0, gaps=0, error=0), **policy)
    if command == 'corpus-run':
        result.update(status='stopped', last_run=dict(request_budget=int(a[a.index('--request-budget')+1]), requests=2, reason='fake budget stop'))
    print(json.dumps(result))
`
	if err := os.WriteFile(filepath.Join(m.installRoot, "bin/cache"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return m
}

func loadCorpusFixture(t *testing.T) model {
	t.Helper()
	m := corpusFixture(t)
	m.corpus.open = true
	m.corpus.id = strings.Repeat("b", 64)
	m.corpus.progress = &corpusProgress{ID: m.corpus.id, Inventory: strings.Repeat("a", 64), Scope: "open-prs", Profile: "pr-comparison", MaxAge: 86400, Members: 2, Status: "pending", Counts: map[string]int{"pending": 2}}
	m.corpus.progress.Repository.Host, m.corpus.progress.Repository.Name = evidenceHost, m.repo
	return m
}

func TestCorpusItemLimitFullChoiceAndWrap(t *testing.T) {
	m := loadCorpusFixture(t)
	for i := 0; i < 5; i++ {
		m, _ = corpusKey(m, "n")
	}
	if !strings.Contains(m.datasetText(), "full") {
		t.Fatal("full item choice is not visible")
	}
	m, cmd := corpusKey(m, "r")
	if cmd == nil {
		t.Fatal("upper budget did not start a run")
	}
	next, _ := m.Update(cmd())
	m = next.(model)
	if m.corpus.progress.LastRun.Budget != 11 {
		t.Fatalf("full allowance not derived from membership: %d", m.corpus.progress.LastRun.Budget)
	}
	data, err := os.ReadFile(filepath.Join(m.installRoot, "corpus-calls.json"))
	if err != nil || strings.Contains(string(data), "--item-limit") {
		t.Fatalf("full choice passed an item cap: %s, %v", data, err)
	}
	m, _ = corpusKey(m, "n")
	if m.corpus.budget != 0 {
		t.Fatal("item choices did not wrap to the 100-item default")
	}
	m, cmd = corpusKey(m, "r")
	next, _ = m.Update(cmd())
	m = next.(model)
	if m.corpus.progress.LastRun.Budget != 20 {
		t.Fatalf("100-item allowance was %d requests", m.corpus.progress.LastRun.Budget)
	}
	data, err = os.ReadFile(filepath.Join(m.installRoot, "corpus-calls.json"))
	if err != nil || !strings.Contains(string(data), `"--item-limit", "100"`) {
		t.Fatalf("default item limit not passed to runner: %s, %v", data, err)
	}
}

func TestCorpusCancellationNavigationAndRepoTokens(t *testing.T) {
	m := loadCorpusFixture(t)
	m, cmd := corpusKey(m, "r")
	before := m.form.Snapshot()
	m, _ = corpusKey(m, "esc")
	if !m.corpus.busy || m.corpusLifecycle.current.cancelled {
		t.Fatal("closing cancelled")
	}
	if m.switchBusy() != "" {
		t.Fatal("cancellable corpus blocked repo switching")
	}
	m, _ = corpusKey(m, "f")
	m, _ = corpusKey(m, "x")
	msg := cmd().(corpusMsg)
	if !errors.Is(msg.err, context.Canceled) {
		t.Fatal(msg.err)
	}
	next, _ := m.Update(msg)
	m = next.(model)
	if m.corpus.busy || m.form.Snapshot() != before || m.corpus.id == "" {
		t.Fatal("cancel lost resumable selection/draft")
	}
	m, cmd = corpusKey(m, "r")
	m.switchRepo(m.repo)
	next, _ = m.Update(cmd())
	m = next.(model)
	if m.corpus.progress != nil || m.corpus.busy || m.corpus.open {
		t.Fatal("old repo reply populated new session")
	}
}

func TestCorpusPreservesFixedBatch(t *testing.T) {
	m := loadCorpusFixture(t)
	m.activeBatch = "fixed"
	m.detail.enriched = EnrichedItem{Evidence: &batchEvidence{SnapshotID: "fixed"}}
	before := m.detail.enriched
	m, cmd := corpusKey(m, "r")
	next, _ := m.Update(cmd())
	if next.(model).detail.enriched.Evidence != before.Evidence {
		t.Fatal("corpus replaced frozen packet")
	}
}

func TestCorpusRejectsWrongRepositoryAndDisplaysSelection(t *testing.T) {
	m := loadCorpusFixture(t)
	m.width, m.height, m.ready = 60, 24, true
	for _, want := range []string{"owner/repo", "open-prs", "pr-comparison", "2 items"} {
		if !strings.Contains(m.viewContent(), want) {
			t.Fatalf("missing %q: %s", want, m.viewContent())
		}
	}
	path := filepath.Join(m.installRoot, "bin/cache")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(data), "full_name='owner/repo'", "full_name='foreign/repo'")), 0755); err != nil {
		t.Fatal(err)
	}
	m, cmd := corpusKey(m, "r")
	msg := cmd().(corpusMsg)
	if msg.err == nil || !strings.Contains(msg.err.Error(), "identity mismatch") {
		t.Fatalf("wrong repository accepted: %+v", msg)
	}
	next, _ := m.Update(msg)
	if next.(model).corpus.progress.Repository.Name != "owner/repo" {
		t.Fatal("failure replaced prior checkpoint")
	}
}

func TestDatasetDownloadChainsCaptureCreateAndRun(t *testing.T) {
	m := corpusFixture(t)
	before := m.form.Snapshot()
	script := `#!/usr/bin/env python3
import json
print(json.dumps(dict(status='imported', scope='full', items=2, snapshot_id='a'*64, repository=dict(host='github.com', full_name='owner/repo'))))
`
	if err := os.WriteFile(filepath.Join(m.installRoot, "bin/fetch"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	m, _ = corpusKey(m, "f")
	m, _ = corpusKey(m, "n")
	m, cmd := corpusKey(m, "d")
	if cmd == nil || !m.corpus.busy {
		t.Fatal("a single d must start downloading")
	}
	if _, duplicate := corpusKey(m, "d"); duplicate != nil {
		t.Fatal("repeated d started an overlapping download")
	}
	for _, action := range []string{"capture", "create", "run"} {
		if cmd == nil || !m.corpus.busy {
			t.Fatalf("missing %s stage", action)
		}
		msg := cmd().(corpusMsg)
		if msg.action != action {
			t.Fatalf("wanted %s, got %s", action, msg.action)
		}
		next, nextCmd := m.Update(msg)
		m, cmd = next.(model), nextCmd
	}
	if cmd != nil || m.corpus.preparing || m.corpus.busy || m.form.Snapshot() != before {
		t.Fatal("download failed to stop or changed the decision draft")
	}
	if m.corpus.progress.Scope != "open-items" || m.corpus.progress.Profile != "backlog" || m.corpus.progress.LastRun.Budget != 65 {
		t.Fatalf("wrong dataset scope or budget: %+v", m.corpus.progress)
	}
	previousID, previousProgress := m.corpus.id, m.corpus.progress
	m, cmd = corpusKey(m, "d")
	next, cmd := m.Update(cmd())
	m = next.(model)
	if m.corpus.id != previousID || m.corpus.progress != previousProgress || cmd == nil {
		t.Fatal("new listing replaced the current download before its plan was ready")
	}
	next, cmd = m.Update(cmd())
	m = next.(model)
	data, _ := os.ReadFile(filepath.Join(m.installRoot, "corpus-calls.json"))
	if strings.Contains(string(data), "--keep-complete") {
		t.Fatal("download/update must retain refresh semantics")
	}
	if !strings.Contains(string(data), "--reuse-corpus") {
		t.Fatal("update lost previous dataset references")
	}
	m, _ = corpusKey(m, "x")
	next, cmd = m.Update(cmd())
	m = next.(model)
	if cmd != nil || m.corpus.preparing || m.corpus.busy {
		t.Fatal("cancelled download continued")
	}
}

func TestDatasetEmptyCaptureDoesNotCreateOrRun(t *testing.T) {
	m := corpusFixture(t)
	m.corpus.id = strings.Repeat("b", 64)
	m.corpus.progress = &corpusProgress{ID: m.corpus.id, Members: 2}
	m.corpus.preparing, m.corpus.busy = true, true
	result := inventoryResult{Status: "empty", Scope: "full"}
	result.Repository.Host, result.Repository.Name = evidenceHost, m.repo
	next, cmd := m.Update(corpusMsg{root: m.installRoot, epoch: m.corpusEpoch, operation: m.corpus.operation, action: "capture", inventory: result})
	m = next.(model)
	if cmd != nil || m.corpus.preparing || m.corpus.busy || m.corpus.id != strings.Repeat("b", 64) || m.corpus.progress.Members != 2 {
		t.Fatal("empty listing must keep the previous download")
	}
}

func TestDatasetCheckpointProgressCannotReplaceCompletedRun(t *testing.T) {
	m := loadCorpusFixture(t)
	m, run := corpusKey(m, "r")
	next, tick := m.onStatusTick()
	m = next.(model)
	if !m.corpus.observing {
		t.Fatal("running download did not schedule a checkpoint read")
	}
	commands := tick().(tea.BatchMsg)
	checkpoint := commands[1]().(corpusMsg)
	if checkpoint.err != nil || checkpoint.action != "observe" {
		t.Fatalf("checkpoint read: %+v", checkpoint)
	}
	status := m.status
	next, _ = m.Update(checkpoint)
	m = next.(model)
	if !m.corpus.busy || m.corpus.observing || m.status != status {
		t.Fatal("checkpoint read changed download lifecycle or status")
	}

	next, _ = m.Update(run())
	m = next.(model)
	next, _ = m.Update(checkpoint)
	m = next.(model)
	if m.corpus.busy || m.corpus.progress.Status != "stopped" {
		t.Fatal("late checkpoint replaced final result")
	}
}
