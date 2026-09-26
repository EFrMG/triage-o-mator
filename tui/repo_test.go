package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func writeLedger(t *testing.T, root, repo, rows string) {
	t.Helper()
	dir := DataDir(root, repo)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "ledger.jsonl"), []byte(rows), 0o644); err != nil {
		t.Fatal(err)
	}
}

func typeRepo(m model, repo string) model {
	m.focus = FocusSidebar
	m.sidebar.selected = switchRepoIndex
	m = press(m, "enter")
	m.repoInput.SetValue(repo)

	return press(m, "enter")
}

func TestSwitchRepoLoadsThatRepoAndDropsPerRepoState(t *testing.T) {
	root := batchFixture(t)
	writeLedger(t, root, "other/repo", ledgerFixtureRow(7, "only in other/repo"))
	m := batchModel(t, root)
	m.activateTab(0)
	m.selectCurrentListItem()
	m.form.NextField()
	m.form.CycleValue(1)
	m.similar[Key{Kind: "issue", Number: 1}] = []dupCandidate{{Number: 2, Kind: "issue"}}

	m = typeRepo(m, "other/repo")
	if m.repo != "owner/repo" || !strings.Contains(m.status, "wait for the current fetch") {
		t.Fatalf("switching while the item's content is loading should wait: repo %s, status %q", m.repo, m.status)
	}

	m.detail.loading = false // the item's content fetch has finished
	m = press(m, "enter")
	if m.repo != "owner/repo" || !m.confirmSwitch || !strings.Contains(m.status, "1 unsaved decision(s) will be discarded") {
		t.Fatalf("the first Enter with an unsaved draft should only warn: repo %s, status %q", m.repo, m.status)
	}

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if cmd == nil || m.repo != "other/repo" || len(m.drafts) != 0 {
		t.Fatalf("the second Enter should discard the draft and switch: %q", m.status)
	}

	if len(m.items) != 1 || m.items[0].Number != 7 || len(m.similar) != 0 || m.listReady || !m.refreshing {
		t.Fatal("switching should load only other/repo's ledger, clear caches, and start a fetch")
	}

	got, err := ReadRepo(root)
	if err != nil || got != "other/repo" {
		t.Fatalf("config/repo = %q, %v", got, err)
	}

	late := send(m, ledgerReloadedMsg{repo: "owner/repo", items: testItems()})
	if len(late.items) != 1 {
		t.Fatal("a ledger reload for the previous repo must be ignored")
	}
}

func TestSwitchRepoPromptValidatesAndDropsStaleMessages(t *testing.T) {
	m := batchModel(t, batchFixture(t))
	m = typeRepo(m, "foo/")
	if m.repo != "owner/repo" || !strings.Contains(m.status, "neither a repo nor an install") {
		t.Fatalf("a malformed repo should be rejected in the prompt: %q", m.status)
	}

	m = press(m, "x")
	if m.status != "" || m.repoInput.Value() != "foo/x" {
		t.Fatalf("typing should clear the complaint about the old value: status %q, value %q", m.status, m.repoInput.Value())
	}
}

func TestInvalidRepoNamesAreRejected(t *testing.T) {
	for _, repo := range []string{"../evil", "owner/..", "./x", "a/b/c", "owner", "owner/repo name"} {
		if validRepo(repo) {
			t.Errorf("%q should be invalid", repo)
		}
	}

	if !validRepo("omacom/omarchy") || !validRepo("my.org/some-repo_2") {
		t.Error("real repo names should be valid")
	}

	if got := DataDir("/r", "omacom/omarchy"); got != filepath.Join("/r", "data", "omacom", "omarchy") {
		t.Errorf("DataDir = %q", got)
	}
}
