package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func datasetFixture(t *testing.T) model {
	t.Helper()
	m := corpusFixture(t)
	m.corpus.open = true
	m.corpus.id = strings.Repeat("b", 64)
	script := `#!/usr/bin/env python3
import json,sys
from pathlib import Path
a=sys.argv[1:]
assert a[:4]==['--host','github.com','--expected-repo','owner/repo']
with Path('offline-calls').open('a') as f: f.write(' '.join(a)+'\n')
command=a[4]
def val(k): return a[a.index(k)+1]
if command=='usage':
    print(json.dumps(dict(total_bytes=476212198,allocated_bytes=728915968,file_count=92505,limit_bytes=5000000000)))
elif command=='handoff':
    if '--corpus' in a: assert val('--corpus')=='b'*64
    print(json.dumps(dict(corpus_id='b'*64,repository=dict(full_name='owner/repo',host='github.com'),inventory_snapshot='a'*64,members=2,scope='open-items',profile='backlog',max_age=86400,status='finished',declared_counts=dict(complete=2))))
else: raise AssertionError(command)
`
	if err := os.WriteFile(filepath.Join(m.installRoot, "bin/cache"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return m
}

func datasetStep(t *testing.T, m model, k string) model {
	t.Helper()
	m, cmd := corpusKey(m, k)
	if cmd != nil {
		next, _ := m.Update(cmd())
		m = next.(model)
	}
	return m
}

func TestDatasetSizeAndRememberedHandoff(t *testing.T) {
	m := datasetFixture(t)
	m.height = 60
	m = datasetStep(t, m, "u")
	if m.corpus.usage == nil || m.corpus.usage.Total != 728915968 || !strings.Contains(m.corpusView(), "0.73 / 5.00 GB") {
		t.Fatal("size must display allocated bytes")
	}
	m.corpus.id = ""
	m = datasetStep(t, m, "y")
	if m.corpus.id != strings.Repeat("b", 64) || m.corpus.progress.Members != 2 {
		t.Fatal("remembered selection was not restored")
	}
}

func TestDatasetCopyUsesCurrentSelectionWithoutOpeningPrompt(t *testing.T) {
	m := datasetFixture(t)
	m.width, m.height = 120, 60
	m, cmd := corpusKey(m, "y")
	if cmd == nil {
		t.Fatal("copy did not verify the current selection")
	}
	next, copyCmd := m.Update(cmd())
	m = next.(model)
	if copyCmd == nil || !m.corpus.open || !strings.Contains(m.corpusView(), "Selected download") || strings.Contains(m.corpusView(), "Prompt to paste") {
		t.Fatal("copy opened a prompt screen or lost the dataset")
	}
	for _, want := range []string{m.installRoot, m.corpus.id, "prompts/prepare-analysis.md", "full inventory", "Do not fetch, save decisions"} {
		if !strings.Contains(m.datasetPrompt(), want) {
			t.Fatalf("copied prompt missing %q", want)
		}
	}

	calls, err := os.ReadFile(filepath.Join(m.installRoot, "offline-calls"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(calls), "handoff --corpus "+m.corpus.id) {
		t.Fatal("handoff did not bind loaded selection")
	}

	copyResult := copyCmd().(yankedMsg)
	if copyResult.err != nil || copyResult.what != "agent prompt" {
		t.Fatal("agent prompt copy failed", copyResult.err)
	}
}

func TestDatasetOpeningRestoresSelectionWithoutCopyOrAcquisition(t *testing.T) {
	m := datasetFixture(t)
	m.corpus = corpusUI{}
	m, cmd := corpusKey(m, "f")
	if cmd == nil || !m.corpus.open || m.corpus.busy {
		t.Fatal("opening should restore saved selection offline")
	}
	msg := cmd().(corpusMsg)
	next, cmd := m.Update(msg)
	m = next.(model)
	if cmd != nil || m.corpus.id != strings.Repeat("b", 64) || m.corpus.progress == nil {
		t.Fatal("opening did not quietly restore the selected dataset")
	}
	m, _ = corpusKey(m, "h")
	m, cmd = corpusKey(m, "f")
	if cmd == nil || m.corpus.busy {
		t.Fatal("reopening should read the current checkpoint offline")
	}
	second := cmd().(corpusMsg)
	next, _ = m.Update(second)
	m = next.(model)
	if m.corpus.id != strings.Repeat("b", 64) || m.corpus.progress.Members != 2 {
		t.Fatal("reopening lost the current selection")
	}
	calls, err := os.ReadFile(filepath.Join(m.installRoot, "offline-calls"))
	if err != nil || strings.Contains(string(calls), "handoff --corpus") {
		t.Fatal("restoration should read the repository's current corpus", err)
	}

	m.corpus.observation++
	m.corpus.id = ""
	next, _ = m.Update(second)
	if next.(model).corpus.id != "" {
		t.Fatal("late restore replaced a newer local state")
	}

	m.corpus.id = strings.Repeat("c", 64)
	m.corpus.operation++
	next, _ = m.Update(second)
	if next.(model).corpus.id != strings.Repeat("c", 64) {
		t.Fatal("late restore replaced newer selection")
	}
}

func TestDatasetSeparatesItemAndRunProgress(t *testing.T) {
	m := datasetFixture(t)
	m.width, m.height = 120, 60
	m.corpus.progress = &corpusProgress{}
	if err := json.Unmarshal([]byte(`{"members":10,"status":"stopped","declared_counts":{"complete":2,"pending":8},"last_run":{"started_at":"old","request_budget":100,"requests":100,"reason":"budget exhausted"}}`), m.corpus.progress); err != nil {
		t.Fatal(err)
	}
	view := m.datasetView()
	for _, want := range []string{"2/10 items processed", "Last run · request allowance", "100/100 requests"} {
		if !strings.Contains(view, want) {
			t.Fatalf("missing %q", want)
		}
	}

	m.corpus.budget = 1
	next, _ := m.startCorpus("run")
	m = next.(model)
	view = m.datasetView()
	if !strings.Contains(view, "This run · request allowance") || !strings.Contains(view, "0/65 requests") || strings.Contains(view, "100/100 requests") {
		t.Fatal("new run retained the old run's exhausted budget")
	}
}
