package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestCommentCommandPreservesLeadingDashes(t *testing.T) {
	root := batchFixture(t)
	copyFixtureScripts(t, root, "comment")
	fakeGH := `#!/usr/bin/env python3
import json, sys
from pathlib import Path
if sys.argv[sys.argv.index("--method") + 1] == "GET":
    print(json.dumps(dict(number=1, html_url="https://github.com/owner/repo/issues/1")))
else:
    Path("posted.json").write_text(json.dumps(json.load(sys.stdin)))
    print(json.dumps(dict(id=42, html_url="https://github.com/owner/repo/issues/1#issuecomment-42")))
`
	if err := os.WriteFile(filepath.Join(root, "bin", "gh"), []byte(fakeGH), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Join(root, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))

	for _, body := range []string{"---\nComment", "---", "--", "-x"} {
		t.Run(body, func(t *testing.T) {
			m := newModel(root, "owner/repo", testTaxonomy(), "tester", testItems())
			m.comment = commentComposer{key: Key{Kind: "issue", Number: 1}, host: "github.com", text: textarea.New()}
			m.comment.text.SetValue(body)

			preview := m.commentCmd(false)().(commentMsg)
			if preview.err != nil {
				t.Fatal(preview.err)
			}
			var plan struct {
				Approval string `json:"approval"`
				Plan     struct {
					RequestID string `json:"request_id"`
					Body      string `json:"body"`
				} `json:"plan"`
			}
			if err := json.Unmarshal([]byte(preview.out), &plan); err != nil {
				t.Fatal(err)
			}
			if plan.Plan.Body != body {
				t.Fatalf("preview body = %q, want %q", plan.Plan.Body, body)
			}

			m.comment.requestID, m.comment.approval = plan.Plan.RequestID, plan.Approval
			if result := m.commentCmd(true)().(commentMsg); result.err != nil {
				t.Fatal(result.err)
			}
			data, err := os.ReadFile(filepath.Join(root, "posted.json"))
			if err != nil {
				t.Fatal(err)
			}
			var posted struct {
				Body string `json:"body"`
			}
			if err := json.Unmarshal(data, &posted); err != nil {
				t.Fatal(err)
			}
			if posted.Body != body {
				t.Fatalf("posted body = %q, want %q", posted.Body, body)
			}
		})
	}
}

func commentEditorFixture(t *testing.T) model {
	t.Helper()
	items := testItems()
	items[0].URL = "https://github.com/owner/repo/issues/1"
	m := newModel(t.TempDir(), "owner/repo", testTaxonomy(), "tester", items)
	m.refreshing = false
	m = send(m, tea.WindowSizeMsg{Width: 100, Height: 35})
	m.openItem(items[0])
	m.focus = FocusDetail
	next, _ := m.openComment()

	return next.(model)
}

func TestCommentSingleSubmitPublishesFromEditorOrPreview(t *testing.T) {
	for _, preview := range []bool{false, true} {
		t.Run(fmt.Sprint("preview=", preview), func(t *testing.T) {
			m := commentEditorFixture(t)
			m = send(m, tea.KeyPressMsg{Text: "qrac? a comment"})
			if m.comment.text.Value() != "qrac? a comment" || m.refreshing || m.form.dirty {
				t.Fatal("composer leaked keys into other actions")
			}

			if preview {
				next, cmd := m.handleCommentKey(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
				m = next.(model)
				if cmd != nil || !m.comment.previewing || m.comment.busy {
					t.Fatal("preview must render locally without running a script")
				}
			}

			next, cmd := m.handleCommentKey(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
			m = next.(model)
			if cmd == nil || !m.comment.busy {
				t.Fatal("one submit must start publishing")
			}

			body := m.comment.text.Value()
			m = send(m, tea.KeyPressMsg{Text: "ignored while busy"})
			if m.comment.text.Value() != body {
				t.Fatal("approved text changed during publishing")
			}

			data, _ := json.Marshal(map[string]any{"approval": "digest", "plan": map[string]any{"request_id": "id", "target": m.comment.target, "body": body}})
			next, cmd = m.Update(commentMsg{root: m.installRoot, repo: m.repo, out: string(data)})
			m = next.(model)
			if cmd == nil || !m.comment.busy || m.comment.approval != "digest" {
				t.Fatal("validated plan must publish without a second keypress")
			}

			next, cmd = m.Update(commentMsg{root: m.installRoot, repo: m.repo, publish: true, out: `{"comment":{"url":"https://github.com/owner/repo/issues/1#issuecomment-42"}}`})
			m = next.(model)
			if m.comment.open || cmd == nil || !m.detail.loading || m.status != "Comment published." {
				t.Fatal("publish must return to item, report success, and reload comments")
			}

		})
	}
}

func TestCommentPreviewRendersMarkdownAndPreservesDraft(t *testing.T) {
	m := commentEditorFixture(t)
	body := "# Heading\n\n---\n\n```go\nfmt.Println(42)\n```"
	m.comment.text.SetValue(body)
	prompt := m.comment.text.Prompt
	line := m.comment.text.Line()
	m = send(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	rendered := ansi.Strip(m.comment.preview.View())
	if !strings.Contains(rendered, "Heading") || !strings.Contains(rendered, "fmt.Println(42)") || strings.Contains(rendered, "```") || strings.Contains(rendered, "# Heading") {
		t.Fatalf("preview did not render Markdown: %q", rendered)
	}
	if !strings.Contains(m.viewContent(), m.comment.target) {
		t.Fatal("preview must show the publishing target")
	}

	m = send(m, tea.WindowSizeMsg{Width: 65, Height: 24})
	for _, row := range strings.Split(m.comment.preview.View(), "\n") {
		if ansi.StringWidth(row) > m.comment.preview.Width() {
			t.Fatal("preview overflowed after resize")
		}
	}

	m = send(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if m.comment.previewing || !m.comment.text.Focused() || m.comment.text.Value() != body || m.comment.text.Line() != line || m.comment.text.Prompt != prompt || prompt == "" {
		t.Fatal("toggling preview changed the draft, cursor or editor marker")
	}
	next, _ := m.handleCommentKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if m.comment.busy || m.comment.text.Value() == body {
		t.Fatal("Enter must insert a newline, not publish")
	}
}

func TestCommentRejectsChangedPlanBeforePublishing(t *testing.T) {
	m := commentEditorFixture(t)
	m.comment.text.SetValue("approved text")
	m.comment.busy = true
	data, _ := json.Marshal(map[string]any{"approval": "digest", "plan": map[string]any{"request_id": "id", "target": m.comment.target, "body": "different text"}})
	next, cmd := m.Update(commentMsg{root: m.installRoot, repo: m.repo, out: string(data)})
	if cmd != nil || next.(model).comment.busy {
		t.Fatal("changed plan must not publish")
	}
}

func TestCommentRejectsMismatchedTarget(t *testing.T) {
	items := testItems()
	items[0].URL = "https://github.com/other/repo/issues/1"
	m := newModel(t.TempDir(), "owner/repo", testTaxonomy(), "tester", items)
	m.openItem(items[0])
	next, cmd := m.openComment()
	if next.(model).comment.open || cmd != nil {
		t.Fatal("mismatched item URL must not open composer")
	}
}

func TestCommentPreviewEscapeAndStaleReply(t *testing.T) {
	m := commentEditorFixture(t)
	m = send(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = send(m, commentMsg{root: "other", repo: m.repo, publish: true, out: `{}`})
	if !m.comment.open {
		t.Fatal("stale reply changed composer")
	}

	m = send(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.comment.previewing || !m.comment.text.Focused() {
		t.Fatal("Esc from preview must return to editing")
	}

	m = send(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.comment.open {
		t.Fatal("Esc from editing must discard composer")
	}
}
