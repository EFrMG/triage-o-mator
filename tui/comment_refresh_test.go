package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestCommentHeaderLayoutAndColors(t *testing.T) {
	themeFixture(t)
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	m := commentEditorFixture(t)

	for _, preview := range []bool{false, true} {
		m.comment.previewing = preview
		title, color := "Compose comment", currentTheme.Info
		if preview {
			title, color = "Preview Markdown", currentTheme.Success
		}
		header := m.commentHeader(96)
		plain := ansi.Strip(header)
		if !strings.HasPrefix(plain, title) || !strings.HasSuffix(plain, m.comment.target) || ansi.StringWidth(header) != 96 || strings.Contains(header, "\n") {
			t.Fatalf("header is not a single line with right-aligned link: %q", plain)
		}
		styled := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true).Render(title)
		if !strings.HasPrefix(header, styled) {
			t.Fatal("header did not use the mode's color")
		}
		if strings.Contains(m.commentView(), "State change") {
			t.Fatal("redundant state label remains")
		}
	}
}

func TestPublishedCommentReloadsThroughScriptAndRejectsOlderResponse(t *testing.T) {
	m := commentEditorFixture(t)
	root := batchFixture(t)
	m.installRoot = root
	copyFixtureScripts(t, root, "enrich-one")
	fakeGH := "#!/bin/sh\nprintf '%s\\n' '{\"title\":\"first\",\"body\":\"body\",\"comments\":[{\"body\":\"newly published\",\"author\":{\"login\":\"tester\"}}]}'\n"
	if err := os.WriteFile(filepath.Join(root, "bin", "gh"), []byte(fakeGH), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	m.detail.populate(EnrichedItem{CommentBodies: []string{"old comment"}})
	m.detail.JumpSection(1)
	m.form.reason.SetValue("unsaved triage reason")
	m.form.dirty = true
	oldGeneration := m.detail.generation

	next, cmd := m.Update(commentMsg{root: root, repo: m.repo, publish: true, out: `{"comment":{"url":"https://github.com/owner/repo/issues/1#issuecomment-42"}}`})
	m = next.(model)
	if cmd == nil || m.comment.open {
		t.Fatal("publish did not dismiss composer and request reload")
	}
	m = send(m, cmd())
	if m.detail.enriched.CommentBodies[0] != "newly published" || !strings.Contains(m.status, "comments refreshed") {
		t.Fatalf("new comment not loaded: %+v, %s", m.detail.enriched, m.status)
	}
	if m.detail.active != 1 || !m.form.dirty || m.form.Reason() != "unsaved triage reason" {
		t.Fatal("reload changed selected tab or decision draft")
	}

	m = send(m, enrichedMsg{root: root, repo: m.repo, key: m.detail.key, generation: oldGeneration, data: EnrichedItem{CommentBodies: []string{"stale"}}})
	if m.detail.enriched.CommentBodies[0] != "newly published" {
		t.Fatal("old enrichment overwrote published comment")
	}
}

func TestRefreshClearsDetailCacheAndKeepsPinnedEvidence(t *testing.T) {
	for _, full := range []bool{false, true} {
		m := commentEditorFixture(t)
		m.comment.open = false
		m.detail.populate(EnrichedItem{CommentBodies: []string{"stale"}})
		m.detail.cache[Key{Kind: "issue", Number: 2}] = EnrichedItem{Body: "also stale"}
		generation := m.detail.generation
		next, cmd := m.startRefresh(full)
		m = next.(model)
		if cmd == nil {
			t.Fatal("refresh not started")
		}
		m = send(m, fetchSyncDoneMsg{repo: m.repo})
		if len(m.detail.cache) != 0 || !m.detail.loading || m.detail.generation <= generation {
			t.Fatal("refresh retained stale details")
		}
	}

	m := commentEditorFixture(t)
	m.detail.enriched = EnrichedItem{Body: "pinned", Evidence: &batchEvidence{SnapshotID: "fixed"}}
	m = send(m, fetchSyncDoneMsg{repo: m.repo})
	if m.detail.enriched.Body != "pinned" || m.refreshLiveDetail() != nil {
		t.Fatal("refresh replaced fixed evidence with live data")
	}
}

func TestCommentRefreshFailureKeepsPublicationSuccess(t *testing.T) {
	m := commentEditorFixture(t)
	m.comment.open = false
	m = send(m, enrichedMsg{root: m.installRoot, repo: m.repo, key: m.detail.key, generation: m.detail.generation, afterComment: true, err: errors.New("read failed")})
	if m.comment.open || !strings.Contains(m.status, "Comment published") || !strings.Contains(m.status, "reloading comments failed") {
		t.Fatal("read failure lost the successful publish outcome")
	}
}

func TestRefreshFromListDefersDetailReadUntilItemReopens(t *testing.T) {
	for _, full := range []bool{false, true} {
		m := commentEditorFixture(t)
		m.comment.open = false
		m.detail.populate(EnrichedItem{CommentBodies: []string{"stale"}})
		oldGeneration := m.detail.generation
		m.focus = FocusList

		next, _ := m.startRefresh(full)
		m = next.(model)
		next, cmd := m.Update(fetchSyncDoneMsg{repo: m.repo})
		m = next.(model)
		if cmd == nil {
			t.Fatal("list refresh must still reload the ledger")
		}
		if _, ok := cmd().(ledgerReloadedMsg); !ok {
			t.Fatal("list refresh scheduled work beyond reloading the ledger")
		}
		if m.detail.loading || len(m.detail.cache) != 0 {
			t.Fatal("list refresh must invalidate details without starting a detail read")
		}

		m = send(m, enrichedMsg{root: m.installRoot, repo: m.repo, key: m.detail.key, generation: oldGeneration, data: EnrichedItem{CommentBodies: []string{"late stale reply"}}})
		if len(m.detail.cache) != 0 {
			t.Fatal("an old in-flight read repopulated the invalidated cache")
		}
		if !m.detail.SetItem(m.items[0]) {
			t.Fatal("reopening the item must request fresh details")
		}
	}
}
