package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func baselineItems() []Item {
	return []Item{{Number: 1, Kind: "issue", State: "open", Title: "first"}, {Number: 2, Kind: "issue", State: "open", Title: "second"}}
}

func baselineTaxonomy() Taxonomy {
	return Taxonomy{Actions: []string{"none"}, ActionOperations: map[string]string{"none": "none"}, Confidence: []string{"low", "medium", "high"}}
}

func baselineSend(m model, msg tea.Msg) model {
	next, _ := m.Update(msg)
	return next.(model)
}

func baselineRow(number int) string {
	row := map[string]any{"number": number, "kind": "issue", "state": "open", "title": "issue", "url": "https://github.com/owner/repo/issues/1", "author": "author", "created_at": "2026-01-01T00:00:00Z", "updated_at": "", "labels": []string{}, "comments_count": 0, "action": "", "confidence": "", "reason": ""}
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
	files := append(modules, filepath.Join("..", "bin", "apply"), filepath.Join("..", "bin", "briefs"), filepath.Join("..", "bin", "taxonomy-settings"), filepath.Join("..", "bin", "item-labels"), filepath.Join("..", "bin", "action-policy"), filepath.Join("..", "bin", "action-proposals"), filepath.Join("..", "bin", "review-request"))
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

func TestBackClearsTicksBeforeLeavingSelections(t *testing.T) {
	for _, back := range []string{"esc", "h"} {
		for _, screen := range []string{"items", "group members", "batches", "briefs", "notifications"} {
			t.Run(screen+"/"+back, func(t *testing.T) {
				m := baselineModel(t, baselineRoot(t))
				itemKey := Key{Kind: "issue", Number: 1}
				switch screen {
				case "items":
					m.activateTab(0)
					m.ticked[itemKey] = true
					m.showList()
				case "group members":
					m.groups.open = true
					m.groups.detail = true
					m.groups.records = []Group{{Members: []GroupMember{{Kind: "issue", Number: 1}}}}
					m.groups.ticked = map[Key]bool{itemKey: true}
				case "batches":
					m.batches.open = true
					m.batches.ticked = map[string]bool{"batch": true}
				case "briefs":
					m.briefs.open = true
					m.briefs.ticked = map[string]bool{"brief": true}
				case "notifications":
					m.notifications.open = true
					m.notifications.ticked = map[int]bool{1: true}
				}

				m = baselineSend(m, mouseKey(back))
				if screen == "items" && m.focus != FocusList {
					t.Fatal("clearing item ticks left the list")
				}
				if screen == "group members" && (!m.groups.open || !m.groups.detail || len(m.groups.ticked) != 0) ||
					screen == "batches" && (!m.batches.open || len(m.batches.ticked) != 0) ||
					screen == "briefs" && (!m.briefs.open || len(m.briefs.ticked) != 0) ||
					screen == "notifications" && (!m.notifications.open || len(m.notifications.ticked) != 0) ||
					screen == "items" && (len(m.ticked) != 0 || strings.Contains(m.list.Title, "ticked")) {
					t.Fatal("first back did not clear ticks in place")
				}

				m = baselineSend(m, mouseKey(back))
				if screen == "items" && m.focus != FocusSidebar ||
					screen == "group members" && m.groups.detail ||
					screen == "batches" && m.batches.open ||
					screen == "briefs" && m.briefs.open ||
					screen == "notifications" && m.notifications.open {
					t.Fatal("second back did not leave the selection screen")
				}
			})
		}
	}
}

func TestBriefsMenuGroupsAndRendersMarkdownWithStaleReplyGuard(t *testing.T) {
	root := baselineRoot(t)
	reports := filepath.Join(root, "reports", "owner", "repo")
	if err := os.MkdirAll(reports, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"2026-10-06-master-brief.md":                 "# Master decisions\n\n**Recommendation:** Review the current cases.\n",
		"2026-10-06-pr-42-brief.md":                  "# PR 42\n\n[PR #42](https://github.com/owner/repo/pull/42) and [issue #1](https://github.com/owner/repo/issues/1) need review. [issue #1](https://github.com/owner/repo/issues/1) appears twice. Bare #2 is not a link. [other issue](https://github.com/elsewhere/repo/issues/3) and [source](https://example.com/note) stay external.\n\n```md\n[issue #9](https://github.com/owner/repo/issues/9)\nreturn true\n```\n" + strings.Repeat("\nMore context for scrolling.\n", 35),
		"2026-10-05-group-related-brief.md":          "# Related reports\n",
		"2026-10-06-batch-b20261006-000001-brief.md": "# Recent batch\n",
	} {
		if err := os.WriteFile(filepath.Join(reports, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	m := baselineModel(t, root)
	foundBriefsRow := false
	for y := 0; y < m.height; y++ {
		foundBriefsRow = foundBriefsRow || m.mouseSidebarRow(y) == briefsIndex
	}
	if !foundBriefsRow {
		t.Fatal("Briefs sidebar row has no mouse target")
	}

	next, cmd := m.openBriefs()
	m = next.(model)
	if cmd == nil {
		t.Fatal("Briefs did not request a list")
	}
	m = baselineSend(m, cmd())
	if len(m.briefs.records) != 4 || m.briefs.section != 0 || len(m.briefsRows(1)) != 1 || len(m.briefsRows(2)) != 2 ||
		m.briefsRows(2)[0].Type != "group" || m.briefsRows(2)[1].Type != "batch" {
		t.Fatalf("unexpected Briefs sections: %+v", m.briefs.records)
	}
	menu := ansi.Strip(m.briefsView())
	if !strings.Contains(menu, "Master (1)") || !strings.Contains(menu, "Items (1)") || !strings.Contains(menu, "Groups & batches (2)") {
		t.Fatalf("Briefs menu omitted a section: %q", menu)
	}
	m.briefs.section = 2
	work := ansi.Strip(m.briefsView())
	if !strings.Contains(work, "Related reports") || strings.Index(work, "Related reports") > strings.Index(work, "Recent batch") {
		t.Fatalf("group brief did not precede the batch brief: %q", work)
	}

	m = baselineSend(m, mouseKey("2"))
	next, cmd = m.Update(mouseKey("enter"))
	m = next.(model)
	if cmd == nil {
		t.Fatal("Item brief did not request Markdown")
	}
	m = baselineSend(m, cmd())
	if !m.briefs.reading || m.briefs.document == nil || !strings.Contains(ansi.Strip(m.briefs.viewport.View()), "return true") {
		t.Fatalf("item brief Markdown was not rendered: %q", m.briefs.viewport.View())
	}
	if !strings.Contains(ansi.Strip(m.briefsView()), "PR 42") {
		t.Fatal("Briefs reader lost the item heading")
	}
	view := ansi.Strip(m.briefs.viewport.View())
	if strings.Contains(view, "https://github.com/owner/repo/pull/42") || strings.Contains(view, "https://github.com/owner/repo/issues/1") || !strings.Contains(view, "https://example.com/note") || !strings.Contains(view, "https://github.com/owner/repo/issues/9") {
		t.Fatalf("Briefs link display changed source or code links: %q", view)
	}
	if len(m.briefs.items) != 2 || m.briefs.items[0] != (Key{Kind: "pr", Number: 42}) || m.briefs.items[1] != (Key{Kind: "issue", Number: 1}) || !strings.Contains(m.briefs.document.Content, "https://github.com/owner/repo/pull/42") {
		t.Fatalf("Brief item references lost order, deduplication, or source: %+v", m.briefs.items)
	}
	m.briefs.viewport.SetYOffset(3)
	offset := m.briefs.viewport.YOffset()
	m = baselineSend(m, mouseKey("enter"))
	if !m.briefs.cards || !strings.Contains(ansi.Strip(m.briefsView()), "Unavailable in the local ledger") {
		t.Fatal("Brief item cards omitted a missing ledger item")
	}
	m = baselineSend(m, mouseKey("enter"))
	if !m.briefs.cards || m.focus == FocusDetail {
		t.Fatal("Missing brief item opened an item detail")
	}
	m = baselineSend(m, mouseKey("esc"))
	if m.briefs.cards || m.briefs.viewport.YOffset() != offset {
		t.Fatal("Returning from brief cards lost the brief scroll position")
	}
	m = baselineSend(m, mouseKey("l"))
	m = baselineSend(m, mouseKey("down"))
	next, _ = m.Update(mouseKey("enter"))
	m = next.(model)
	if m.briefs.open || !m.briefs.returnToBrief || m.focus != FocusDetail || m.detail.key != (Key{Kind: "issue", Number: 1}) {
		t.Fatal("Brief card did not open its local item")
	}
	m = baselineSend(m, mouseKey("esc"))
	if !m.briefs.open || m.briefs.cards || m.briefs.viewport.YOffset() != offset {
		t.Fatal("Returning to the brief lost its scroll position")
	}
	m = baselineSend(m, mouseKey("esc"))
	m = baselineSend(m, mouseKey("space"))
	m = baselineSend(m, mouseKey("1"))
	m = baselineSend(m, mouseKey("space"))
	if len(m.briefs.ticked) != 2 || !strings.Contains(ansi.Strip(m.briefsView()), "2 ticked") {
		t.Fatal("Brief ticks did not survive a section change")
	}
	next, cmd = m.Update(mouseKey("d"))
	m = next.(model)
	if cmd == nil || !m.briefs.busy {
		t.Fatal("Mark read did not start")
	}
	next, cmd = m.Update(cmd())
	m = next.(model)
	if cmd == nil || !m.briefs.busy {
		t.Fatal("One keypress did not apply the checked brief renames")
	}
	next, reload := m.Update(cmd())
	m = next.(model)
	if reload == nil {
		t.Fatal("Brief rename did not reload the menu")
	}
	m = baselineSend(m, reload())
	if len(m.briefs.records) != 2 || len(m.briefs.ticked) != 0 {
		t.Fatalf("Marked briefs remain in the menu: %+v", m.briefs.records)
	}
	for _, name := range []string{"2026-10-06-master-brief.md", "2026-10-06-pr-42-brief.md"} {
		if _, err := os.Stat(filepath.Join(reports, strings.TrimSuffix(name, ".md")+"_READ.md")); err != nil {
			t.Fatalf("Brief was not preserved with _READ suffix: %v", err)
		}
	}
	m = baselineSend(m, mouseKey("3"))
	next, cmd = m.Update(mouseKey("d"))
	m = next.(model)
	next, cmd = m.Update(cmd())
	m = next.(model)
	if cmd == nil || len(m.briefs.markIDs) != 1 || m.briefs.markIDs[0] != "2026-10-05-group-related-brief.md" {
		t.Fatal("d without ticks did not apply to the hovered brief")
	}
	next, reload = m.Update(cmd())
	m = baselineSend(next.(model), reload())
	if len(m.briefs.records) != 1 {
		t.Fatal("Hovered brief remained after marking it read")
	}

	stale := briefsMsg{root: root, repo: "owner/repo", generation: m.briefsGeneration, read: m.briefs.document}
	staleMark := briefMarkMsg{root: root, repo: "owner/repo", generation: m.briefsGeneration, reply: &briefMarkReply{}}
	m.briefsGeneration++
	m.briefs.document = nil
	m = baselineSend(m, stale)
	m = baselineSend(m, staleMark)
	if m.briefs.document != nil {
		t.Fatal("late brief content replaced a newer read")
	}
	m.switchRepo("other/repo")
	m = baselineSend(m, stale)
	m = baselineSend(m, staleMark)
	if m.briefs.open || m.briefs.document != nil {
		t.Fatal("late brief content crossed the repository switch")
	}
}

func TestItemScoreAppearsOnCardsAndItemWithRevisionGuard(t *testing.T) {
	m := baselineModel(t, baselineRoot(t))
	m.ledgerSeen = ledgerStamp(m.installRoot, m.repo)
	if _, cmd := m.onStatusTick(); m.ledgerSeen == "" || cmd == nil {
		t.Fatal("the fixture ledger could not be stamped")
	}
	ledgerPath := filepath.Join(DataDir(m.installRoot, m.repo), "ledger.jsonl")
	saved, err := os.ReadFile(ledgerPath)
	if err != nil || os.WriteFile(ledgerPath, append(saved, '\n'), 0o644) != nil {
		t.Fatal("could not rewrite the fixture ledger")
	}
	ticked, _ := m.onStatusTick()
	if ticked.(model).ledgerSeen == m.ledgerSeen {
		t.Fatal("a ledger written outside the TUI, as an agent's scoring pass does, was not noticed")
	}
	reloaded := reloadLedgerCmd(m.installRoot, m.repo)().(ledgerReloadedMsg)
	if reloaded.err != nil || len(reloaded.items) != len(m.items) {
		t.Fatalf("the rewritten ledger did not reload: %+v", reloaded.err)
	}
	it := m.items[0]
	it.UpdatedAt = "2026-10-06T00:00:00Z"
	for _, marks := range []struct{ clarity, support, actionability int }{{0, 0, 0}, {1, 1, 0}, {1, 1, 1}, {2, 1, 1}, {2, 2, 1}} {
		value := marks.clarity + marks.support + marks.actionability
		score := &ItemScore{Rubric: "item-quality-v1", Value: &value, Dimensions: map[string]int{"clarity": marks.clarity, "support": marks.support, "actionability": marks.actionability},
			Reason:     fmt.Sprintf("Clarity %d: Symptom is described. Support %d: One terminal context is known. Actionability %d: Request exact resize steps. Comments complete; none recorded.", marks.clarity, marks.support, marks.actionability),
			Suggestion: "Try the narrow terminal case", SnapshotID: strings.Repeat("a", 64), AssessedBy: "agent:tester"}
		score.Revision.UpdatedAt = it.UpdatedAt
		it.ItemScore = score
		m.items[0] = it
		m.detail.SetItem(it)

		styled := lipgloss.NewStyle().Foreground(lipgloss.Color(itemScoreColor(value))).Bold(true).Render(fmt.Sprintf("Score %d/5", value))
		li := listItem{Item: it}
		card := markedCardWithRight(li.Title(), li.Description(), li.Mark(), itemScoreMark(it), false, 68)
		cardLines := strings.Split(card, "\n")
		if !strings.Contains(cardLines[1], styled) || !strings.HasSuffix(strings.TrimSpace(ansi.Strip(cardLines[1])), it.ScoreLabel()) ||
			ansi.StringWidth(cardLines[1]) != 68 || strings.Contains(cardLines[2], it.ScoreLabel()) {
			t.Fatalf("score %d was not right-aligned on the card title: %q", value, card)
		}
		list := newItemList([]Item{it}, "Scores", 68, 10)
		var rendered strings.Builder
		(cardDelegate{}).Render(&rendered, list, 0, list.Items()[0])
		if !strings.Contains(strings.Split(rendered.String(), "\n")[1], styled) {
			t.Fatal("item list delegate dropped the title-row score")
		}
		viewLines := strings.Split(m.itemView(), "\n")
		if !strings.HasSuffix(strings.TrimSpace(ansi.Strip(viewLines[0])), it.ScoreLabel()) || ansi.StringWidth(viewLines[0]) != m.detailInnerWidth() ||
			strings.Contains(viewLines[1], it.ScoreLabel()) || strings.Contains(m.itemFooter()[0].hints[0].desc, "Score") {
			t.Fatalf("score %d was not right-aligned on the item title, or was repeated in the footer", value)
		}

		comments := it.CommentsCount
		bound := it
		boundScore := *score
		boundScore.Revision.Comments = &comments
		bound.ItemScore = &boundScore
		bound.UpdatedAt = "2099-01-01T00:00:00Z"
		if _, current := bound.ScoreValue(); !current && bound.Kind == "issue" {
			t.Fatal("a later update time alone made an issue score stale")
		}
		bound.CommentsCount++
		if _, current := bound.ScoreValue(); bound.Kind == "issue" && (current || bound.ScoreLabel() != fmt.Sprintf("Score %d/5 (stale?)", value)) {
			t.Fatalf("a new comment did not mark the issue score stale: %q", bound.ScoreLabel())
		}
		pull := Item{Kind: "pr", HeadSHA: strings.Repeat("b", 40), UpdatedAt: "2099-01-01T00:00:00Z", ItemScore: &boundScore}
		pullScore := boundScore
		pullScore.Revision.HeadSHA = pull.HeadSHA
		pull.ItemScore = &pullScore
		if _, current := pull.ScoreValue(); !current {
			t.Fatal("an unchanged head commit made a PR score stale")
		}
		pull.HeadSHA = strings.Repeat("c", 40)
		if pull.ScoreLabel() != fmt.Sprintf("Score %d/5 (stale?)", value) {
			t.Fatalf("a new head commit did not mark the PR score stale: %q", pull.ScoreLabel())
		}
		form := m.formPanel(60)
		plain := ansi.Strip(form)
		if !strings.Contains(plain, "you: tester\n\nScore by agent:tester") ||
			!strings.Contains(plain, "Score by agent:tester\n\nClarity:") ||
			!strings.Contains(plain, "\n\nSupport:") || !strings.Contains(plain, "\n\nActionability:") || strings.Contains(plain, "Score reason:") ||
			strings.Contains(plain, "Suggested next check") || strings.Contains(plain, "Score source:") || strings.Contains(plain, it.ScoreLabel()) {
			t.Fatalf("score %d form details had the wrong order or extra fields: %q", value, plain)
		}
		if copied := m.itemBlock(it, EnrichedItem{}, false); !strings.Contains(copied, "Score reason: Clarity") || !strings.Contains(copied, score.SnapshotID) {
			t.Fatal("copied item context omitted the score basis or source")
		}
	}
	it.Title = strings.Repeat("Long item title ", 12)
	li := listItem{Item: it}
	for _, selected := range []bool{false, true} {
		card := markedCardWithRight(li.Title(), li.Description(), li.Mark(), itemScoreMark(it), selected, 35)
		row := strings.Split(card, "\n")[1]
		if !strings.HasSuffix(strings.TrimSpace(ansi.Strip(row)), it.ScoreLabel()) || ansi.StringWidth(row) != 35 || !strings.Contains(ansi.Strip(row), "#1") {
			t.Fatalf("long title displaced the right-aligned score: %q", row)
		}
	}
	m.items[0] = it
	m.detail.SetItem(it)
	itemHeading := strings.Split(m.itemView(), "\n")[0]
	if !strings.HasSuffix(strings.TrimSpace(ansi.Strip(itemHeading)), it.ScoreLabel()) || ansi.StringWidth(itemHeading) != m.detailInnerWidth() {
		t.Fatal("long item title displaced the score in the item menu")
	}
	prValue := 1
	pr := Item{Kind: "pr", Number: 18, UpdatedAt: it.UpdatedAt, ItemScore: &ItemScore{Rubric: "item-quality-v1", Value: &prValue,
		Dimensions: map[string]int{"correctness": 0, "safeguards": 0, "reviewability": 1}, AssessedBy: "agent:tester",
		Reason: "Correctness 0: the diff counts runes, which does not solve terminal cell width for wide or combining characters in issue #13. Safeguards 0: no matching edge-case handling or test is visible. Reviewability 1: the nine-line change is focused. Summary, comments, files and diff complete; no checks captured."}}
	pr.ItemScore.Revision.UpdatedAt = pr.UpdatedAt
	m.items = append(m.items, pr)
	m.detail.SetItem(pr)
	prForm := m.formPanel(60)
	prPlain := ansi.Strip(prForm)
	if !strings.Contains(prPlain, "Correctness: 0/2 · the diff counts runes") || !strings.Contains(prPlain, "\n\nSafeguards: 0/2") ||
		!strings.Contains(prPlain, "\n\nReviewability: 1/1 · the nine-line change is focused.") || strings.Contains(prPlain, "Summary, comments, files and diff complete") {
		t.Fatalf("PR score categories were not separated from coverage notes: %q", prPlain)
	}
	compact := m.formPanel(m.formPanelWidth())
	if lipgloss.Height(compact) > m.detailBodyHeight() || !strings.Contains(ansi.Strip(compact), "Correctness:") ||
		!strings.Contains(ansi.Strip(compact), "Safeguards:") || !strings.Contains(ansi.Strip(compact), "Reviewability:") || !strings.Contains(ansi.Strip(compact), "…") {
		t.Fatalf("long score reasons could not fit the visible form pane: %q", ansi.Strip(compact))
	}
	if !strings.Contains(prForm, lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Muted)).Bold(true).Render("Score by agent:tester")) ||
		!strings.Contains(prForm, lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Foreground)).Bold(true).Render("Correctness:")) {
		t.Fatal("score title or label lost its bold style")
	}

	it.UpdatedAt = "2026-10-07T00:00:00Z"
	if _, current := it.ScoreValue(); current || !strings.HasSuffix(itemScoreMark(it).text, "/5 (stale?)") {
		t.Fatal("a later item revision kept displaying the old score as current")
	}
}

func TestItemCardMetadataLayout(t *testing.T) {
	for _, tc := range []struct {
		kind, typeLabel, color string
	}{
		{"issue", "Issue", currentTheme.Warning},
		{"pr", "PR", currentTheme.Info},
	} {
		item := Item{Kind: tc.kind, Number: 7, Title: "Example", UpdatedAt: "2026-10-07", Labels: []string{"bug", "needs info"}, CommentsCount: 2}
		li := listItem{Item: item}
		styledType := lipgloss.NewStyle().Foreground(lipgloss.Color(tc.color)).Bold(true).Render(tc.typeLabel)
		for _, selected := range []bool{false, true} {
			card := markedCardWithRightAndMeta(li.Title(), li.Description(), cardMark{}, itemScoreMark(item), cardMeta{li.Tags(), li.CommentsLabel()}, selected, 52)
			row := strings.Split(card, "\n")[2]
			plain := ansi.Strip(row)
			if !strings.Contains(row, styledType) || !strings.Contains(plain, "bug · needs info  2 comments") ||
				!strings.HasPrefix(strings.TrimSpace(plain), tc.typeLabel) ||
				!strings.HasSuffix(plain, "2 comments  ") || ansi.StringWidth(row) != 52 ||
				strings.Contains(plain, "triaged") || strings.Contains(plain, "updated") {
				t.Fatalf("%s selected=%v: unexpected card metadata: %q", tc.kind, selected, row)
			}
		}
	}
	decided := listItem{Item: Item{Kind: "pr", Number: 9, ProposedLabels: []string{"ready"}, Action: "none", TriagedBy: "agent"}}
	if plain := ansi.Strip(decided.Description()); plain != "PR · agent · none" || decided.Tags() != "ready" {
		t.Fatalf("decision marker displaced the item type: %q", plain)
	}
	if want := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Warning)).Render("agent"); !strings.Contains(decided.Description(), want) {
		t.Fatal("agent attribution lost its normal-weight color")
	}
	if want := lipgloss.NewStyle().Bold(true).Render("none"); !strings.Contains(decided.Description(), want) {
		t.Fatal("saved action lost its bold style")
	}
	proposed := listItem{Item: Item{Kind: "issue"}, proposalAction: "close"}
	if want := lipgloss.NewStyle().Bold(true).Render("close"); !strings.Contains(proposed.Description(), want) {
		t.Fatal("proposed action lost its bold style")
	}
	decided.Action = ""
	if plain := ansi.Strip(decided.Description()); plain != "PR · agent" || strings.Contains(plain, "/") {
		t.Fatalf("label-only decision rendered an empty action: %q", plain)
	}

	item := Item{Kind: "issue", Number: 8, Title: "Narrow", Labels: []string{"very long label", "another label"}, CommentsCount: 1,
		ReviewRequest: &ReviewRequest{By: "human"}}
	li := listItem{Item: item}
	card := markedCardWithRightAndMeta(li.Title(), li.Description(), cardMark{}, cardMark{}, cardMeta{li.Tags(), li.CommentsLabel()}, false, 28)
	row := strings.Split(card, "\n")[2]
	if !strings.Contains(ansi.Strip(row), "Issue") || !strings.HasSuffix(ansi.Strip(row), "1 comment  ") || ansi.StringWidth(row) != 28 {
		t.Fatalf("narrow card lost its type or right-aligned comment count: %q", row)
	}

	m := baselineModel(t, baselineRoot(t))
	m.items = []Item{{Kind: "pr", Number: 9, State: "open", Title: "Ready", ProposedLabels: []string{"ready"}, Confidence: "high", TriagedBy: "agent", CommentsCount: 2,
		ReviewRequest: &ReviewRequest{By: "maintainer"}}, {Kind: "issue", Number: 10, State: "open", Title: "Reviewed", Action: "close", TriagedBy: "maintainer", Labels: []string{"bug"}, CommentsCount: 1}}
	for _, tab := range []int{pendingReviewTab, mergeReadyTab, allItemsTab} {
		m.activateTab(tab)
		view := ansi.Strip(m.list.View())
		if !strings.Contains(view, "PR · agent") || !strings.Contains(view, "ready · pending review") || strings.Contains(view, "ready/") {
			t.Fatalf("tab %s did not render the shared card layout: %q", tabs[tab].Name, view)
		}
		if tab == allItemsTab && (!strings.Contains(view, "Issue · human · close") || !strings.Contains(view, "bug  1 comment")) {
			t.Fatalf("All Items lost human attribution or the action: %q", view)
		}
	}
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

func TestBaselineSettingsGuidanceUsesScriptAndGuardsReplies(t *testing.T) {
	root := baselineRoot(t)
	m := baselineModel(t, root)
	for _, action := range m.taxonomy.Actions {
		if !slices.Contains(actionOperations, m.taxonomy.OperationFor(action)) {
			t.Fatalf("default action %q has no supported GitHub operation", action)
		}
	}
	if slices.Contains(m.taxonomy.Actions, "label-only") || slices.Contains(actionOperations, "label") || slices.Contains(m.taxonomy.Actions, "escalate-maintainer") || slices.Contains(m.taxonomy.Actions, "approve-merge-candidate") {
		t.Fatal("default actions still include labeling or unsupported advice")
	}
	custom := Taxonomy{Actions: []string{"request logs", "archive", "escalate-maintainer"}, ActionOperations: map[string]string{"request logs": "comment", "archive": "close"}}
	if !slices.Equal(custom.SelectableActions(), custom.Actions[:2]) || custom.OperationFor("archive") != "close" || custom.OperationFor("request logs") != "comment" {
		t.Fatal("custom titles did not retain their concrete GitHub operations")
	}
	m.sidebar.selected = settingsIndex
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	menu := ansi.Strip(m.settingsView())
	if !m.settings.open || !strings.Contains(menu, "Labels") || !strings.Contains(menu, "Actions") || strings.Contains(menu, "owner/repo") {
		t.Fatal("Settings did not show separate cards without repeating the repository")
	}

	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.settings.section != "label" || !strings.Contains(ansi.Strip(m.settingsView()), "Labels pending") {
		t.Fatal("Labels did not show the pending catalog")
	}
	next, cmd := m.Update(tea.KeyPressMsg{Text: "i"})
	m = next.(model)
	if cmd == nil || !m.settings.busy {
		t.Fatal("Initialize defaults did not request a live preview")
	}
	defaults := labelDefaultsPlan{Repository: m.repo, Operation: "initialize-defaults", Create: []GitHubLabel{{Name: "bug", Description: "Something is broken", Color: "d73a4a"}}, PreviewSHA256: "defaults-preview"}
	m = baselineSend(m, settingsDefaultsMsg{root: root, repo: "other/repo", operation: "initialize-defaults", request: m.settings.request, plan: defaults})
	if m.settings.defaults != nil || !m.settings.busy {
		t.Fatal("stale starter label preview opened Settings")
	}
	m = baselineSend(m, settingsDefaultsMsg{root: root, repo: m.repo, operation: "initialize-defaults", request: m.settings.request, plan: defaults})
	if m.settings.defaults == nil {
		t.Fatal("Initialize defaults did not open its preview")
	}
	defaultsContent := m.settingsDefaultsContent(m.settings.defaults.preview.Width())
	plainDefaults := ansi.Strip(defaultsContent)
	if !strings.Contains(ansi.Strip(m.viewContent()), "Preview starter labels") || !strings.Contains(plainDefaults, "GitHub repository\nowner/repo\n\n  Current\n  Existing labels stay unchanged.\n\n  After save\n  Title\n  bug") || !strings.Contains(plainDefaults, "  Description\n  Something is broken\n\n  Color\n  d73a4a") {
		t.Fatal("Initialize defaults did not show the exact label in the label preview layout")
	}
	accent := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Accent))
	if !strings.Contains(defaultsContent, accent.Bold(true).Render("After save")) || !strings.Contains(defaultsContent, accent.Render("Title")) || strings.Contains(defaultsContent, accent.Bold(true).Render("Title")) {
		t.Fatal("Initialize defaults did not use the label preview heading and field styles")
	}
	next, cmd = m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = next.(model)
	if cmd == nil || !m.settings.busy {
		t.Fatal("Initialize defaults did not require confirmation after preview")
	}
	m.settings.busy = false
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.settings.defaults != nil {
		t.Fatal("Initialize defaults preview did not close")
	}
	m.taxonomy.LabelCatalog = LabelCatalog{Repository: m.repo, Status: "observed", Labels: []GitHubLabel{{ID: 1, Name: "bug", Description: "Something is broken"}}}
	next, cmd = m.Update(tea.KeyPressMsg{Text: "I"})
	m = next.(model)
	if cmd == nil || !m.settings.busy {
		t.Fatal("capital I did not request a live comparison with the saved local catalog")
	}
	localPlan := labelDefaultsPlan{Repository: m.repo, Operation: "reconcile-local", Current: []GitHubLabel{{ID: 1, Name: "bug", Description: "Remote bug"}, {ID: 2, Name: "remote-only", Description: "Remove me", Color: "ff0000"}}, Create: []GitHubLabel{{Name: "local-custom", Description: "Keep me", Color: "abcdef"}}, Update: []labelDefinitionPlan{{Current: &GitHubLabel{ID: 1, Name: "bug", Description: "Remote bug", Color: "ff0000"}, Proposed: GitHubLabel{Name: "bug", Description: "Local bug", Color: "112233"}}}, Delete: []GitHubLabel{{ID: 2, Name: "remote-only", Description: "Remove me", Color: "ff0000"}}, PreviewSHA256: "local-preview"}
	m = baselineSend(m, settingsDefaultsMsg{root: root, repo: m.repo, operation: "reconcile-local", request: m.settings.request, plan: localPlan})
	if m.settings.defaults == nil || !strings.Contains(ansi.Strip(m.viewContent()), "Preview local label catalog") {
		t.Fatal("capital I did not open the local catalog preview")
	}
	localContent := ansi.Strip(m.settingsDefaultsContent(m.settings.defaults.preview.Width()))
	if !strings.Contains(localContent, "Deleting a GitHub label removes it from issues and PRs") || !strings.Contains(localContent, "Create") || !strings.Contains(localContent, "Edit bug") || !strings.Contains(localContent, "Delete from GitHub") || !strings.Contains(localContent, "remote-only") {
		t.Fatal("local catalog preview omitted an exact change or deletion consequence")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	labels := strings.Split(ansi.Strip(m.settingsView()), "\n")
	if len(labels) != m.mainHeight() || !strings.Contains(labels[len(labels)-1], "1 of 1") {
		t.Fatalf("Labels summary at wrong position: lines=%d height=%d last=%q", len(labels), m.mainHeight(), labels[len(labels)-1])
	}
	m = baselineSend(m, tea.KeyPressMsg{Text: "e"})
	if m.settings.editor == nil || !strings.Contains(ansi.Strip(m.viewContent()), "Edit label") {
		t.Fatal("e did not open the floating label editor")
	}
	m.settings.editor.title.SetValue("defect")
	m.settings.editor.description.SetValue("A reproducible defect")
	next, cmd = m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = next.(model)
	if cmd == nil || !m.settings.busy {
		t.Fatal("Ctrl-P did not request a live GitHub preview")
	}
	plan := labelDefinitionPlan{Repository: m.repo, Operation: "edit", Current: &GitHubLabel{ID: 1, Name: "bug", Description: "Something is broken", Color: "ff0000"}, Proposed: GitHubLabel{Name: "defect", Description: "A reproducible defect", Color: "ff0000"}, PreviewSHA256: "settings-preview"}
	m = baselineSend(m, settingsPreviewMsg{root: root, repo: m.repo, request: m.settings.request, name: "defect", description: "A reproducible defect", plan: plan})
	preview := m.settingsDraftPreview(m.settings.editor.preview.Width())
	plain := ansi.Strip(preview)
	if !m.settings.editor.previewing || !strings.Contains(plain, "GitHub repository\nowner/repo\n\n  Current") || !strings.Contains(plain, "  Current\n  Title\n  bug\n\n  Description\n  Something is broken") || !strings.Contains(plain, "  After save\n  Title\n  defect") || strings.Contains(plain, "(bug)") {
		t.Fatal("GitHub preview did not show current and next values together")
	}
	accentTitle := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Accent)).Render("Title")
	boldTitle := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Accent)).Bold(true).Render("Title")
	if !strings.Contains(preview, accentTitle) || strings.Contains(preview, boldTitle) {
		t.Fatal("preview field names did not use normal-weight accent styling")
	}
	next, cmd = m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = next.(model)
	if cmd == nil || !m.settings.busy {
		t.Fatal("one save from the live preview did not confirm the label change")
	}
	m.settings.busy = false
	m.settings.editor = nil
	m = baselineSend(m, tea.KeyPressMsg{Text: "n"})
	m.settings.editor.title.SetValue("triage")
	m.settings.editor.description.SetValue("Ready to triage")
	next, cmd = m.Update(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	m = next.(model)
	if cmd == nil || !m.settings.busy {
		t.Fatal("new label did not request a GitHub preview")
	}
	plan = labelDefinitionPlan{Repository: m.repo, Operation: "create", Proposed: GitHubLabel{Name: "triage", Description: "Ready to triage", Color: "ededed"}, PreviewSHA256: "new-preview"}
	m = baselineSend(m, settingsPreviewMsg{root: root, repo: m.repo, request: m.settings.request, name: "triage", description: "Ready to triage", plan: plan})
	if plain := ansi.Strip(m.settingsDraftPreview(m.settings.editor.preview.Width())); !strings.Contains(plain, "  Current\n  No existing label") || !strings.Contains(plain, "  After save\n  Title\n  triage") {
		t.Fatal("new label preview did not show current and proposed values together")
	}
	m.settings.editor = nil
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.settings.section != "action" || !strings.Contains(ansi.Strip(m.settingsView()), "No conversation or state change") {
		t.Fatal("Actions did not open their own description list")
	}

	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.settings.editor == nil || m.settings.editor.row.kind != "action" {
		t.Fatal("Settings did not open action guidance")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyTab})
	next, cmd = m.Update(tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl})
	m = next.(model)
	if cmd == nil || !m.settings.busy {
		t.Fatal("Ctrl-E did not open $EDITOR for the selected Settings field")
	}
	m = baselineSend(m, settingsEditorMsg{root: root, repo: m.repo, kind: "action", originalName: "none", field: 1, request: m.settings.request, text: "No conversation or state write needed"})
	if !m.settings.editor.previewing || m.settings.editor.description.Value() != "No conversation or state write needed" {
		t.Fatal("$EDITOR result did not return to the Settings preview")
	}
	next, cmd = m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = next.(model)
	if cmd == nil || !m.settings.busy || m.switchBusy() == "" {
		t.Fatal("Settings save did not protect the draft and repository")
	}
	message, ok := cmd().(settingsDoneMsg)
	if !ok || message.err != nil {
		t.Fatalf("Settings script failed: %+v", message)
	}
	m = baselineSend(m, message)
	if m.settings.editor != nil || m.taxonomy.ActionGuidance["none"] != "No conversation or state write needed" {
		t.Fatal("Settings did not reload saved guidance")
	}

	stale := settingsDoneMsg{root: root, repo: "other/repo", request: m.settings.request, operation: "save", taxonomy: Taxonomy{}}
	m = baselineSend(m, stale)
	if m.taxonomy.ActionGuidance["none"] != "No conversation or state write needed" {
		t.Fatal("stale Settings reply replaced the selected repository's guidance")
	}

	m = baselineSend(m, tea.KeyPressMsg{Text: "n"})
	if m.settings.editor == nil || !m.settings.editor.creating || !strings.Contains(ansi.Strip(m.viewContent()), "New action") {
		t.Fatal("n did not open the floating action editor")
	}
	m.settings.editor.title.SetValue("request-review")
	m.settings.editor.description.SetValue("Ask a maintainer to review")
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyTab})
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyTab})
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyRight})
	if m.settings.editor.operation != "close" {
		t.Fatal("action editor did not move to the next operation")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.settings.editor.operation != "comment" {
		t.Fatal("action editor did not select a GitHub operation")
	}
	next, cmd = m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = next.(model)
	if cmd == nil || !m.settings.busy {
		t.Fatal("new action was not sent to the settings script")
	}
	m = baselineSend(m, cmd().(settingsDoneMsg))
	if m.settings.editor != nil || m.taxonomy.ActionGuidance["request-review"] != "Ask a maintainer to review" || m.taxonomy.ActionOperations["request-review"] != "comment" {
		t.Fatal("new action was not saved and reloaded")
	}

	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyDown})
	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if cmd == nil || m.settings.section != "automations" {
		t.Fatal("Automations did not open from Settings")
	}
	m = baselineSend(m, cmd().(automationMsg))
	if !m.settings.automations.loaded || !m.settings.automations.labelingEnabled || !strings.Contains(ansi.Strip(m.automationsView()), "Scoring") {
		t.Fatal("Automations did not load default-on Labeling and local Scoring cards")
	}
	if prompt := m.labelingPrompt(); !strings.Contains(prompt, "prompts/label-items.md") || !strings.Contains(prompt, m.repo) || !strings.Contains(prompt, m.installRoot) {
		t.Fatal("Labeling prompt lacked the pinned install or agent instructions")
	}
	next, cmd = m.Update(tea.KeyPressMsg{Text: "y"})
	m = next.(model)
	if cmd == nil {
		t.Fatal("y did not copy the Labeling agent prompt")
	}
	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if cmd == nil || !m.settings.busy {
		t.Fatal("Labeling card did not toggle")
	}
	m = baselineSend(m, cmd().(automationMsg))
	if m.settings.automations.labelingEnabled {
		t.Fatal("Labeling did not turn off for the selected repository")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyDown})
	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if cmd != nil || m.settings.busy || !strings.Contains(m.status, "copy the bounded local scoring prompt") {
		t.Fatal("Scoring card changed automation settings")
	}
	if prompt := m.scoringPrompt(); !strings.Contains(prompt, "prompts/score-items.md") || !strings.Contains(prompt, m.repo) || !strings.Contains(prompt, m.installRoot) {
		t.Fatal("Scoring prompt lacked the pinned install or agent instructions")
	}
	next, cmd = m.Update(tea.KeyPressMsg{Text: "y"})
	m = next.(model)
	if cmd == nil || !strings.Contains(m.status, "Item scoring prompt") {
		t.Fatal("Scoring card did not copy its bounded local prompt")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyUp})
	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeySpace})
	m = next.(model)
	if cmd == nil {
		t.Fatal("Labeling card could not turn back on")
	}
	m = baselineSend(m, cmd().(automationMsg))
	if !m.settings.automations.labelingEnabled {
		t.Fatal("Labeling did not turn back on")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.settings.selected != 2 || m.settings.automations.pendingReviewHold {
		t.Fatal("Pending review action hold did not default to OFF after Scoring")
	}
	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if cmd == nil {
		t.Fatal("Pending review hold card could not be changed")
	}
	m = baselineSend(m, cmd().(automationMsg))
	if !m.settings.automations.pendingReviewHold {
		t.Fatal("Pending review action hold did not turn on for the selected repository")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.settings.selected != 3 || m.settings.automations.actions[0].Name != "comment" {
		t.Fatal("first writing action did not appear after Pending review hold")
	}
	if prompt := m.actionPrompt(m.settings.automations.actions[0]); !strings.Contains(prompt, "prompts/automated-actions.md") || !strings.Contains(prompt, "comment") || !strings.Contains(prompt, m.repo) {
		t.Fatal("action prompt lacked selected type, repository or playbook")
	}
	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if cmd == nil {
		t.Fatal("action card did not offer an explicit mode change")
	}
	m = baselineSend(m, cmd().(automationMsg))
	if m.settings.automations.actions[0].Mode != "execute" {
		t.Fatal("action mode did not update for the selected repository")
	}
	m = baselineSend(m, automationMsg{root: root, repo: "other/repo", request: m.settings.request, status: automationStatus{Repository: "other/repo"}, actions: actionPolicyStatus{Repository: "other/repo"}})
	if !m.settings.automations.labelingEnabled || m.settings.automations.actions[0].Mode != "execute" {
		t.Fatal("stale Automations response changed the selected repository's setting")
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

	if _, err := runScript(relativeRoot, "apply", "--number", "1", "--kind", "issue", "--action", "none", "--reason", "selected install", "--by", "tester"); err != nil {
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
	if m.detail.active != 1 || m.form.focused != fieldLabels {
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
	m = baselineSend(m, enrichedMsg{root: m.installRoot, repo: m.repo, generation: m.detail.generation, key: key, data: EnrichedItem{Kind: key.Kind, Number: key.Number, State: "closed", Body: "Live item"}})
	header := strings.SplitN(ansi.Strip(m.itemView()), "\n", 3)[1]
	if !strings.Contains(header, "closed") || m.items[0].State != "open" {
		t.Fatal("item header did not use the successful live read before ledger sync")
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
	m.detail.enriched.State = "open"
	m.detail.enriched.Evidence = &batchEvidence{SnapshotID: "fixed"}
	header = strings.SplitN(ansi.Strip(m.itemView()), "\n", 3)[1]
	if !strings.Contains(header, "closed") {
		t.Fatal("fixed packet state displaced the ledger state in the header")
	}
	m.detail.sections[m.detail.active].renderedFor = ""
	m.detail.renderActive()
	if detail := ansi.Strip(m.detail.View()); strings.Contains(detail, "snapshot: fixed") || !strings.Contains(detail, "Fixed evidence packet") {
		t.Fatal("fixed evidence exposed a machine identifier in the item view")
	}
}

func TestBaselineSaveAndExplicitPendingReviewAreSeparate(t *testing.T) {
	root := baselineRoot(t)
	m := baselineModel(t, root)
	m.activateTab(untriagedTab)
	m.selectCurrentListItem()
	m.form.ApplyProposal(proposal{Action: "none", Confidence: "medium", Reason: "Checked source", ProposedBy: "agent:triage"})
	next, cmd := m.Update(tea.KeyPressMsg{Text: "s"})
	if cmd == nil {
		t.Fatal("save produced no script command")
	}
	_ = baselineSend(next.(model), cmd())
	row := baselineLedgerRow(t, root)
	if row["action"] != "none" || row["review_request"] != nil {
		t.Fatalf("saved decision entered Pending review: %v", row)
	}
	if _, err := runScript(root, "review-request", "mark", "--expected-repo", "owner/repo", "--kind", "issue", "--number", "1", "--by", "agent:triage", "--reason", "Important design decision"); err != nil {
		t.Fatal(err)
	}
	items, err := LoadLedger(root, "owner/repo")
	if err != nil {
		t.Fatal(err)
	}
	if listed := tabs[pendingReviewTab].Filter(items, Taxonomy{}); len(listed) != 1 || listed[0].ReviewRequest.Reason != "Important design decision" {
		t.Fatalf("explicit request did not enter Pending review: %+v", listed)
	}
	m = baselineModel(t, root)
	m.activateTab(pendingReviewTab)
	next, cmd = m.requestReviewClear(m.listTargets())
	if cmd != nil || !strings.Contains(next.(model).status, "Press a again") {
		t.Fatal("clear did not require confirmation")
	}
	next, cmd = next.(model).requestReviewClear(next.(model).listTargets())
	if cmd == nil {
		t.Fatal("confirmed request did not use its owning script")
	}
	m = baselineSend(next.(model), cmd().(reviewClearDoneMsg))
	if m.items[0].ReviewRequest != nil || baselineLedgerRow(t, root)["action"] != "none" {
		t.Fatal("clearing Pending review changed the decision or left the request")
	}

	root = baselineRoot(t)
	taxonomy, err := LoadTaxonomy(root)
	if err != nil {
		t.Fatal(err)
	}
	taxonomy.LabelCatalog = LabelCatalog{Repository: "owner/repo", Status: "observed", Labels: []GitHubLabel{{ID: 7, Name: "bug", Color: "ff0000"}, {ID: 8, Name: "needs-info", Color: "ededed"}}}
	data, err := json.Marshal(taxonomy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config/taxonomy.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	m = baselineModel(t, root)
	m.activateTab(untriagedTab)
	m.selectCurrentListItem()
	m.form.FocusField(fieldLabels)
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyRight})
	if !m.form.pick.open {
		t.Fatal("Labels did not open the multi-select list")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeySpace})
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyDown})
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeySpace})
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !slices.Equal(m.form.ProposedLabels(), []string{"bug", "needs-info"}) || m.form.focused != fieldAction {
		t.Fatal("selected labels were not retained in the decision form")
	}
	m.form.CycleValue(1)
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.form.Action() != "" {
		t.Fatal("Backspace did not leave action assessment pending")
	}
	m.form.reason.SetValue("Confirmed bug")
	_, cmd = m.requestSave()
	if cmd == nil {
		t.Fatal("label decision produced no save command")
	}
	if result := cmd().(applyDoneMsg); result.err != nil {
		t.Fatal(result.err)
	}
	row = baselineLedgerRow(t, root)
	if row["action"] != "" || !slices.Equal(row["proposed_labels"].([]any), []any{"bug", "needs-info"}) || row["review_request"] != nil {
		t.Fatalf("new label decision did not stay separate from the action and explicit review request: %v", row)
	}
}

func TestBaselineUnlistedLedgerDecisionRequiresExplicitCorrection(t *testing.T) {
	root := baselineRoot(t)
	path := filepath.Join(root, "data/owner/repo/ledger.jsonl")
	row := strings.Replace(baselineRow(1), `"action":""`, `"action":"archive"`, 1)
	row = strings.Replace(row, `"confidence":""`, `"confidence":"obsolete"`, 1)
	if err := os.WriteFile(path, []byte(row+baselineRow(2)), 0o644); err != nil {
		t.Fatal(err)
	}

	m := baselineModel(t, root)
	m.activateTab(untriagedTab)
	m.openItem(m.items[0])
	if m.form.Action() != "archive" || m.form.Confidence() != "obsolete" {
		t.Fatal("unlisted decision values changed on load")
	}
	if _, cmd := m.requestSave(); cmd != nil {
		t.Fatal("unlisted decision was accepted")
	}
	if got := baselineLedgerRow(t, root); got["action"] != "archive" {
		t.Fatal("blocked save changed the ledger")
	}

	m.form.FocusField(fieldAction)
	m.form.CycleValue(0)
	m.commitDraftIfDirty()
	m.loadForm(m.items[0])
	if m.form.Action() != "none" || m.form.Confidence() != "obsolete" {
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
	if got := baselineLedgerRow(t, root); got["action"] != "none" || got["confidence"] != "low" || got["reason"] != "Explicitly checked" {
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
	row["action"], row["confidence"], row["reason"] = "none", "", reason
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
	m.form.ApplyProposal(proposal{Action: "none", Confidence: "", Reason: reason})
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
	if got := baselineLedgerRow(t, root); got["reason"] != reason || got["confidence"] != "" {
		t.Fatalf("saved long reason changed: %v", got)
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
	m.form.ApplyProposal(proposal{Action: "none", Reason: "old repository decision"})
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

func TestBaselineBatchCreationUsesFloatingEditor(t *testing.T) {
	m := baselineModel(t, baselineRoot(t))
	m.batches.open = true
	m.sidebar.selected = batchesIndex
	m = baselineSend(m, mouseKey("n"))
	if !m.batches.editing || m.batches.size.Prompt != "" {
		t.Fatal("n did not open the batch editor")
	}
	view := ansi.Strip(m.bodyView())
	if strings.Contains(view, "> 25") {
		t.Fatal("batch size field kept the default input prompt")
	}
	for _, field := range []string{"New batch", "Size", "Kind", "Order", "Group"} {
		if !strings.Contains(view, field) {
			t.Fatalf("floating batch editor omitted %s", field)
		}
	}
	if lipgloss.Width(m.bodyView()) != m.width {
		t.Fatal("floating batch editor overflowed the terminal")
	}

	m = baselineSend(m, mouseKey("tab"))
	m = baselineSend(m, mouseKey("l"))
	if !m.batches.pick.open || m.batches.field != 1 || !strings.Contains(ansi.Strip(m.bodyView()), "issue") {
		t.Fatal("kind choices did not open in the floating editor")
	}
	m = baselineSend(m, mouseKey("down"))
	m = baselineSend(m, mouseKey("enter"))
	if !m.batches.editing || m.batches.pick.open || m.batches.kindIdx != 1 {
		t.Fatal("picking a kind created the batch or lost the selected value")
	}
	m = baselineSend(m, mouseKey("tab"))
	m = baselineSend(m, mouseKey("tab"))
	next, cmd := m.Update(mouseKey("enter"))
	if cmd == nil || next.(model).batches.editing || !next.(model).batches.busy {
		t.Fatal("Enter on the final Group field did not create the batch")
	}

	next, cmd = m.Update(mouseKey("ctrl+s"))
	m = next.(model)
	if cmd == nil || m.batches.editing || !m.batches.busy || m.batches.kindIdx != 1 {
		t.Fatal("Ctrl-S did not submit the floating batch editor")
	}
}

func TestBaselineBatchItemsReturnToBatches(t *testing.T) {
	m := baselineModel(t, baselineRoot(t))
	id := "b20261008-000001"
	m.batches.records = []batchRecord{{ID: id, Keys: []Key{{Kind: "issue", Number: 1}}}}
	m.batches.selected = 0
	m.openBatch(id)
	if m.activeBatch != id || !m.listReady || m.focus != FocusList {
		t.Fatal("batch did not open its item list")
	}

	m = baselineSend(m, mouseKey("enter"))
	if m.focus != FocusDetail {
		t.Fatal("batch item did not open")
	}
	m = baselineSend(m, mouseKey("esc"))
	if m.focus != FocusList || m.activeBatch != id {
		t.Fatal("Esc from a batch item did not return to its list")
	}
	m = baselineSend(m, mouseKey("esc"))
	if !m.batches.open || m.activeBatch != "" || m.batches.selected != 0 || m.listReady || m.focus != FocusSidebar || m.sidebar.selected != batchesIndex {
		t.Fatal("Esc from a batch item list did not return to the selected batch")
	}
	m = baselineSend(m, mouseKey("esc"))
	if m.batches.open || !m.overview {
		t.Fatal("Esc from Batches did not return to the overview")
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

func TestBaselineAutomaticDatasetPreferenceAndContinuation(t *testing.T) {
	root := baselineRoot(t)
	m := baselineModel(t, root)
	m.corpus.open = true
	next, cmd := m.handleCorpusKey(tea.KeyPressMsg{Text: "o"})
	m = next.(model)
	if cmd == nil || !m.corpus.automatic || !m.corpus.busy || m.corpus.action != "restore" || !m.corpus.repositionRetry {
		t.Fatal("turning automatic download on did not start a dataset check")
	}
	if enabled, err := loadCorpusAuto(root, m.repo); err != nil || !enabled {
		t.Fatalf("automatic download preference was not saved: %v", err)
	}
	if lines := strings.Split(ansi.Strip(datasetAutoToggle(true, 30)), "\n"); len(lines) != 3 || strings.TrimSpace(lines[0]) != "" || strings.TrimSpace(lines[2]) != "" || strings.TrimSpace(lines[1]) != "ON   OFF" || strings.Index(lines[1], "ON") < 10 {
		t.Fatalf("automatic download choices were not padded and centered: %q", lines)
	}

	fallback := &datasetUsage{}
	fallback.Reposition.State, fallback.Reposition.Fallback, fallback.Reposition.Reason = "off", true, "pinned Reposition installation timed out"
	next, cmd = m.finishCorpus(corpusMsg{root: root, epoch: m.corpusEpoch, operation: m.corpus.operation,
		observation: m.corpus.observation, action: "restore", usage: fallback})
	m = next.(model)
	if cmd == nil || m.corpus.action != "capture" || !m.corpus.preparing || m.corpus.repositionRetry || !strings.Contains(ansi.Strip(m.datasetText()), "pinned Reposition installation timed out") || !strings.Contains(m.lastError.text, "pinned Reposition installation timed out") {
		t.Fatal("Reposition fallback interrupted automatic inventory capture")
	}

	fresh := baselineModel(t, root)
	if !fresh.corpus.automatic {
		t.Fatal("automatic preference was not restored at startup")
	}
	next, cmd = fresh.Update(fetchSyncDoneMsg{root: root, repo: fresh.repo, backlog: true})
	fresh = next.(model)
	if cmd == nil || fresh.corpus.action != "restore" || !fresh.corpus.busy {
		t.Fatal("backlog refresh did not start automatic dataset work")
	}

	selectedID := strings.Repeat("a", 64)
	limited := corpusProgress{ID: selectedID, Members: 200, Status: "stopped", Counts: map[string]int{"pending": 200},
		LastRun: &struct {
			StartedAt string `json:"started_at"`
			Budget    int    `json:"request_budget"`
			Requests  int    `json:"requests"`
			Reason    string `json:"reason"`
		}{Reason: "request budget exhausted"}}
	next, cmd = fresh.finishCorpus(corpusMsg{root: root, epoch: fresh.corpusEpoch, operation: fresh.corpus.operation,
		observation: fresh.corpus.observation, action: "restore", id: selectedID, progress: limited})
	fresh = next.(model)
	if cmd == nil || !fresh.corpus.busy || fresh.corpus.action != "run" {
		t.Fatal("automatic download did not resume the selected dataset")
	}
	limited.Counts = map[string]int{"complete": 100}
	next, cmd = fresh.finishCorpus(corpusMsg{root: root, epoch: fresh.corpusEpoch, operation: fresh.corpus.operation,
		action: "run", id: selectedID, progress: limited})
	fresh = next.(model)
	if cmd == nil || !fresh.corpus.busy || fresh.corpus.action != "run" {
		t.Fatal("automatic download did not continue after its request budget")
	}
	limited.Counts = map[string]int{"complete": 100, "error": 80}
	limited.LastRun.Reason = "GitHub read returned HTTP 504"
	next, cmd = fresh.finishCorpus(corpusMsg{root: root, epoch: fresh.corpusEpoch, operation: fresh.corpus.operation,
		action: "run", id: selectedID, progress: limited})
	fresh = next.(model)
	if cmd == nil || !fresh.corpus.busy || fresh.corpus.action != "run" {
		t.Fatal("automatic download did not retry a transient gateway failure")
	}
	limited.Counts["error"] = 160
	next, cmd = fresh.finishCorpus(corpusMsg{root: root, epoch: fresh.corpusEpoch, operation: fresh.corpus.operation,
		action: "run", id: selectedID, progress: limited})
	fresh = next.(model)
	if cmd != nil || fresh.corpus.busy || fresh.corpus.autoStalls != 2 {
		t.Fatal("failed items counted as acquired evidence or a repeated gateway failure kept retrying")
	}
	next, _ = fresh.handleCorpusKey(tea.KeyPressMsg{Text: "o"})
	fresh = next.(model)
	next, cmd = fresh.handleCorpusKey(tea.KeyPressMsg{Text: "o"})
	fresh = next.(model)
	if cmd == nil || fresh.corpus.action != "restore" || fresh.corpus.autoStalls != 0 {
		t.Fatal("explicit automatic restart did not check the selected dataset")
	}
	limited.Status = "interrupted"
	limited.LastRun.Reason = "runner exited without a final checkpoint; resume explicitly"
	next, cmd = fresh.finishCorpus(corpusMsg{root: root, epoch: fresh.corpusEpoch, operation: fresh.corpus.operation,
		observation: fresh.corpus.observation, action: "restore", id: selectedID, progress: limited})
	fresh = next.(model)
	if cmd == nil || !fresh.corpus.busy || fresh.corpus.action != "run" {
		t.Fatal("explicit automatic restart did not resume an interrupted download")
	}
	limited.Status = "stopped"
	limited.LastRun.Reason = "Git batch fetch failed; PR code remains incomplete"
	next, cmd = fresh.finishCorpus(corpusMsg{root: root, epoch: fresh.corpusEpoch, operation: fresh.corpus.operation,
		action: "run", id: selectedID, progress: limited})
	fresh = next.(model)
	if cmd != nil || fresh.corpus.busy {
		t.Fatal("automatic download retried a hard Git failure")
	}
	if handoff := fresh.datasetPrompt(); !strings.Contains(handoff, "Saved coverage: 100 complete") || !strings.Contains(handoff, "Last stop: Git batch fetch failed") {
		t.Fatal("agent handoff omitted the saved incomplete coverage or stop reason")
	}

	next, _ = fresh.handleCorpusKey(tea.KeyPressMsg{Text: "o"})
	fresh = next.(model)
	if fresh.corpus.automatic {
		t.Fatal("turning automatic download off did not update the menu")
	}
	if enabled, err := loadCorpusAuto(root, fresh.repo); err != nil || enabled {
		t.Fatalf("automatic OFF preference was not saved: %v", err)
	}
	for _, oldKey := range []string{"d", "r", "n"} {
		next, cmd = fresh.handleCorpusKey(tea.KeyPressMsg{Text: oldKey})
		fresh = next.(model)
		if cmd != nil || fresh.corpus.busy {
			t.Fatalf("retired dataset key %q still started work", oldKey)
		}
	}
}

func TestBaselineDatasetMenuAndNotificationShortcut(t *testing.T) {
	root := baselineRoot(t)
	m := baselineModel(t, root)
	m.corpus.progress = &corpusProgress{ID: strings.Repeat("a", 64), Members: 1, Status: "finished", Counts: map[string]int{"complete": 1}}
	m.corpus.inventoryNotice = "Listed 1 open items; preparing the download."
	m.corpus.busy, m.corpus.action = true, "run"
	next, _ := m.finishCorpus(corpusMsg{root: root, epoch: m.corpusEpoch, operation: m.corpus.operation, action: "run", id: m.corpus.progress.ID, progress: *m.corpus.progress})
	m = next.(model)
	if strings.Contains(m.datasetText(), "preparing the download") {
		t.Fatal("completed download retained its preparation notice")
	}
	menuText := ansi.Strip(m.datasetText())
	if strings.Contains(menuText, "ON starts at startup and after a backlog refresh.") || strings.Contains(menuText, "Press o to toggle.") || strings.Contains(menuText, "· Full cache management") {
		t.Fatal("dataset toggle spacing, guidance, or title did not match the menu")
	}
	menuLines := strings.Split(ansi.Strip(m.datasetView()), "\n")
	for _, label := range []string{"Automatic download", "Starts after each backlog refresh."} {
		found := false
		for i, line := range menuLines {
			if strings.TrimSpace(line) != label {
				continue
			}
			if strings.Index(line, label) != (m.menuWidth()-ansi.StringWidth(label))/2+1 {
				t.Fatalf("dataset label %q was not centered: %q", label, line)
			}
			if label == "Automatic download" && (i+1 >= len(menuLines) || menuLines[i+1] != "") {
				t.Fatal("dataset title was not followed by an empty line")
			}
			if label == "Starts after each backlog refresh." && (i+2 >= len(menuLines) || menuLines[i+1] != "" || menuLines[i+2] != "") {
				t.Fatal("dataset guidance was not followed by two empty lines")
			}
			found = true
			break
		}
		if !found {
			t.Fatalf("dataset label %q was missing", label)
		}
	}
	for _, removed := range []string{"update reuse within", "Updates at saved item checkpoints", "Analyze with an agent", "Download limits", "request allowance", "Local data:"} {
		if strings.Contains(menuText, removed) {
			t.Fatalf("dataset menu retained removed guidance: %q", removed)
		}
	}
	if len(menuLines) != m.mainHeight() || !strings.Contains(menuLines[len(menuLines)-1], "Reuse eligible data within one day") || !strings.Contains(menuLines[len(menuLines)-1], "storage ceiling 5 GB") {
		t.Fatalf("dataset footer was not fixed to the pane bottom: %q", menuLines)
	}
	m.corpus.usage = &datasetUsage{Total: 300000000, Limit: 5000000000, Categories: map[string]int64{"reposition": 100000000}}
	m.corpus.usage.Reposition.State = "on"
	if !strings.Contains(ansi.Strip(m.datasetText()), "evidence 200.0 MB · indexes 100.0 MB") || strings.Contains(ansi.Strip(m.datasetText()), "Ranked search") || strings.Contains(ansi.Strip(m.datasetText()), "Reposition ON") {
		t.Fatal("dataset menu did not show combined cache storage")
	}
	next, command := m.handleCorpusKey(tea.KeyPressMsg{Text: "p"})
	m = next.(model)
	if command != nil || m.corpus.busy {
		t.Fatal("retired Reposition key started an operation")
	}

	next, _ = m.openNotifications()
	m = next.(model)
	m = baselineSend(m, tea.KeyPressMsg{Text: "f"})
	if !m.corpus.open || m.notifications.open == true || !m.corpus.returnToNotifications {
		t.Fatal("f did not open the dataset from Notifications")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.corpus.open || !m.notifications.open {
		t.Fatal("closing the dataset did not return to Notifications")
	}
}

func TestBaselineAutomaticRepositionSetupErrorKeepsNativeRestore(t *testing.T) {
	root := baselineRoot(t)
	setup := "#!/bin/sh\nprintf attempted > '" + filepath.Join(root, "reposition-attempted") + "'\nexit 2\n"
	cache := `#!/bin/sh
case "$5" in
  handoff) printf '{"corpus_id":""}\n' ;;
  usage) printf '{"allocated_bytes":8192,"limit_bytes":5000000000,"file_count":2,"allocated_categories":{"reposition":4096},"reposition_environment":{"enabled":false,"state":"off","fallback":true}}\n' ;;
  *) exit 2 ;;
esac
`
	for name, content := range map[string]string{"reposition-env": setup, "cache": cache} {
		if err := os.WriteFile(filepath.Join(root, "bin", name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	ui := corpusUI{automatic: true, autoRestore: true, repositionRetry: true}
	msg := corpusCommand(root, "owner/repo", 0, ui, "restore", &readProcess{})().(corpusMsg)
	if msg.err != nil || msg.repositionError == nil || msg.usage == nil || !msg.usage.Reposition.Fallback {
		t.Fatalf("Reposition setup error blocked native restore: %#v", msg)
	}
	if _, err := os.Stat(filepath.Join(root, "reposition-attempted")); err != nil {
		t.Fatalf("automatic restore did not try Reposition setup: %v", err)
	}
	m := baselineModel(t, root)
	m.corpus.automatic, m.corpus.autoRestore, m.corpus.busy = true, true, true
	next, command := m.finishCorpus(msg)
	m = next.(model)
	if command == nil || m.corpus.action != "capture" || m.lastError.text == "" || !strings.Contains(ansi.Strip(m.datasetText()), "Reposition setup unavailable") {
		t.Fatal("Reposition setup command failure was not visible while native capture continued")
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
		"fetch":            "{}",
		"sync":             "{}",
		"action-proposals": `{"repository":"owner/repo","requests":0,"rows":[{"kind":"pr","number":3,"operation":"close","status":"pending","needs_attention":true},{"kind":"pr","number":4,"operation":"close","status":"pending","needs_attention":true},{"kind":"pr","number":5,"operation":"close","status":"executed","needs_attention":false}]}`,
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
	m.notifications = notificationsUI{open: true, review: &closureReview{Approval: "exact-approval"}, reviewKey: "a"}
	m.notifications.review.Plan.Proposals = []actionProposalRow{{Kind: "pr", Operation: "close", Number: 3, Title: "Enterprise PR", Target: target, Comment: "Close with explanation"}, {Kind: "pr", Operation: "close", Number: 4, Title: "Second PR", Target: "https://ghe.example/owner/repo/pull/4", Comment: "Close with explanation"}}
	if !strings.Contains(ansi.Strip(m.closureReviewView()), target) {
		t.Fatal("closure approval did not show the exact target URL")
	}

	for name, script := range map[string]string{
		"fetch":            "#!/usr/bin/env python3\nimport json, pathlib, sys\npathlib.Path('fetch-args.json').write_text(json.dumps(sys.argv[1:]))\n",
		"sync":             "#!/usr/bin/env python3\nprint('{}')\n",
		"cache":            "#!/usr/bin/env python3\nprint('{\"unread_total\":0}')\n",
		"action-proposals": "#!/usr/bin/env python3\nprint('{\"repository\":\"owner/repo\",\"requests\":0,\"rows\":[]}')\n",
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
	row := actionProposalRow{Kind: "pr", Number: 3, Operation: "close", Status: "pending", Active: true}
	m.notifications = notificationsUI{open: true, actions: actionProposalList{Rows: []actionProposalRow{row}}, actionReview: &actionReviewUI{row: row}}
	next, cmd := m.handleNotificationsKey(tea.KeyPressMsg{Text: "w"})
	if cmd == nil {
		t.Fatal("w in a proposal reader did not start tracking")
	}
	result := cmd().(trackDoneMsg)
	if result.err != nil || result.key != key {
		t.Fatalf("w tracked the wrong item: %+v", result)
	}
	updated, reload := next.(model).finishTracking(result)
	if reload == nil || updated.(model).notifications.actionReview == nil {
		t.Fatal("tracking did not refresh Notifications while keeping the proposal reader")
	}

	ticked := []Item{{Kind: "issue", Number: 1}, {Kind: "pr", Number: 3}}
	list := baselineModel(t, root)
	next, cmd = list.requestTracking(ticked)
	if cmd != nil || next.(model).listConfirm != "w" {
		t.Fatal("tracking several ticked items did not ask for w again")
	}
	next, cmd = next.(model).requestTracking(ticked)
	if cmd == nil {
		t.Fatal("the second w did not track the ticked items")
	}
	many := cmd().(trackManyDoneMsg)
	if many.err != nil || many.added != 2 {
		t.Fatalf("w tracked only part of the ticked selection: %+v", many)
	}
	updated, _ = next.(model).finishTrackingMany(many)
	if updated.(model).trackingBusy || !strings.Contains(updated.(model).status, "Tracking 2 new item(s)") {
		t.Fatalf("bulk tracking did not report its result: %q", updated.(model).status)
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
	for _, name := range []string{"cache", "action-proposals"} {
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
		actions:   actionProposalList{Rows: []actionProposalRow{{Kind: "pr", Number: 3, Title: "Fixture", Target: "https://github.com/owner/repo/pull/3", Comment: "Publish this explanation", Operation: "close", Status: "pending", Active: true, Needs: true, Checkpoint: strings.Repeat("a", 64), Inputs: proposalInputs{ContextCheckpoint: "context"}}}},
		attention: &attentionPage{Rows: []attentionRow{{Number: 3, Selectable: true, Attention: true, WatchCheckpoint: strings.Repeat("b", 64)}}},
		closures:  &actionHistoryPage{Rows: []actionHistoryRow{{Number: 3, Selectable: true, HistoryCheckpoint: strings.Repeat("c", 64)}}},
	}
	choices := m.notifications.choices()
	if len(choices) != 1 || choices[0].kind != "item" || choices[0].key != (Key{Kind: "pr", Number: 3}) || !m.notifications.choiceNeeds(choices[0]) {
		t.Fatalf("notification sources were not grouped: %+v", choices)
	}
	if strings.Count(ansi.Strip(m.notificationsView()), "PR #3 Fixture") != 1 {
		t.Fatal("PR appears more than once in Notifications")
	}
	m = baselineSend(m, tea.KeyPressMsg{Text: "1"})
	if m.notifications.actionReview != nil || m.notificationPR.open {
		t.Fatal("number key unexpectedly opened the notification")
	}
	next, openCmd := m.handleNotificationsKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = baselineSend(next.(model), openCmd().(actionReviewMsg))
	if m.notifications.actionReview == nil || !strings.Contains(m.notificationsView(), "Comment to publish") {
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
	if m.notifications.actionReview == nil || m.notificationPR.open {
		t.Fatal("returning from the PR did not restore its proposal")
	}
	m = baselineSend(m, tea.KeyPressMsg{Text: "d"})
	if !m.comment.open || m.notifications.actionReview == nil {
		t.Fatal("d in proposal review did not open the rejection composer")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.comment.open || m.notifications.actionReview == nil {
		t.Fatal("canceling rejection did not restore proposal review")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.notificationPR.open {
		t.Fatal("Enter on the proposal did not open the PR")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.notifications.actionReview != nil || !m.notifications.open {
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
	if m.comment.open || m.notifications.actionReview != nil {
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

	next, _ = m.handleNotificationsKey(tea.KeyPressMsg{Text: "d"})
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
	if finished.(model).comment.open || finished.(model).notifications.actionReview != nil {
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
	m.notifications.actions.Rows = nil
	_, cmd = m.handleNotificationsKey(tea.KeyPressMsg{Text: "d"})
	if cmd == nil {
		t.Fatal("d did not dismiss a notification without a pending proposal")
	}
	plainDismissal := cmd().(notificationItemDoneMsg)
	if plainDismissal.err != nil || plainDismissal.completed != 3 {
		t.Fatalf("presentation-only dismissal changed: %+v", plainDismissal)
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.notificationPR.open || m.notifications.actionReview != nil {
		t.Fatal("notification without a closure proposal did not open the item directly")
	}
}

func TestNotificationsReviewSelectionUsesSharedReaderForOneClosure(t *testing.T) {
	m := baselineModel(t, baselineRoot(t))
	m.notifications = notificationsUI{open: true, ticked: map[int]bool{1: true}}
	m.notifications.actions = actionProposalList{Rows: []actionProposalRow{
		{Kind: "pr", Number: 1, Operation: "close", Status: "pending", Active: true, Checkpoint: "first"},
		{Kind: "pr", Number: 2, Operation: "close", Status: "pending", Active: true, Checkpoint: "second"},
		{Kind: "issue", Number: 3, Operation: "comment", Status: "pending", Active: true, Checkpoint: "issue"},
	}}
	for index, choice := range m.notifications.choices() {
		if choice.key == (Key{Kind: "issue", Number: 3}) {
			m.notifications.selected = index
		}
	}

	next, cmd := m.handleNotificationsKey(tea.KeyPressMsg{Text: "a"})
	m = next.(model)
	if cmd == nil || m.notifications.actionReview == nil || m.notifications.actionReview.row.Number != 1 || m.notifications.review != nil {
		t.Fatal("one ticked PR closure did not use the shared action reader ahead of the cursor")
	}

	m.notifications.actionReview = nil
	m.notifications.ticked[2] = true
	next, cmd = m.handleNotificationsKey(tea.KeyPressMsg{Text: "a"})
	m = next.(model)
	if cmd == nil || !m.notifications.reviewBusy || m.notifications.actionReview != nil {
		t.Fatal("two ticked PR closures did not enter batch review")
	}

	m.notifications.reviewBusy = false
	m.notifications.ticked = map[int]bool{}
	m.notifications.actions.Rows[1].Active = false
	next, cmd = m.handleNotificationsKey(tea.KeyPressMsg{Text: "A"})
	m = next.(model)
	if cmd == nil || m.notifications.actionReview == nil || m.notifications.actionReview.row.Number != 1 || m.notifications.review != nil {
		t.Fatal("one active PR closure under A did not use the shared action reader")
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
    sys.exit('unexpected action proposal operation')
if pathlib.Path('edit-fail').exists():
    sys.exit('saved proposal changed')
comment = pathlib.Path(args[args.index('--comment-file') + 1]).read_text()
print(json.dumps(dict(kind='pr', number=3, operation='close', status='pending', checkpoint='b' * 64, comment=comment)))
`
	if err := os.WriteFile(filepath.Join(root, "bin", "action-proposals"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	m := baselineModel(t, root)
	old := actionProposalRow{Kind: "pr", Number: 3, Operation: "close", Title: "Fixture", Target: "https://github.com/owner/repo/pull/3", Status: "pending", Active: true,
		Checkpoint: strings.Repeat("a", 64), Comment: "Original comment", Inputs: proposalInputs{ContextCheckpoint: "context"}}
	m.notifications = notificationsUI{open: true, actions: actionProposalList{Rows: []actionProposalRow{old}},
		ticked: map[int]bool{3: true}, actionReview: &actionReviewUI{row: old}}
	next, _ := m.handleNotificationsKey(tea.KeyPressMsg{Text: "e"})
	m = next.(model)
	if !m.comment.open || m.comment.text.Value() != old.Comment {
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
	if !m.comment.open || m.comment.text.Value() != "Edited exact comment" || m.notifications.actionReview == nil {
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
	if reloadCmd == nil || m.comment.open || m.notifications.actionReview != nil || len(m.notifications.ticked) != 0 || m.notifications.openActionAfter != (Key{Kind: "pr", Number: 3}) {
		t.Fatal("saved proposal edit kept an old review or selected-set plan")
	}
	fresh := old
	fresh.Checkpoint, fresh.Comment = strings.Repeat("b", 64), "Edited exact comment"
	loaded := notificationsMsg{root: root, repo: m.repo, generation: m.notificationsGeneration,
		actions: actionProposalList{Repository: m.repo, Rows: []actionProposalRow{fresh}},
		tracked: trackedPage{}, attention: attentionPage{}, closures: actionHistoryPage{}}
	next, contextCmd := m.finishNotifications(loaded)
	m = next.(model)
	if contextCmd == nil || m.notifications.actionReview == nil || m.notifications.actionReview.row.Checkpoint != fresh.Checkpoint {
		t.Fatal("edited proposal did not return to its new review")
	}
}

func TestGroupEditFloatingFieldsStayBoundedAndSave(t *testing.T) {
	root := baselineRoot(t)
	script, err := os.ReadFile(filepath.Join("..", "bin", "group"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "group"), script, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := runScript(root, "group", "create", "--title", "Draft", "--description", "Initial guidance", "--by", "agent:helper")
	if err != nil {
		t.Fatal(err)
	}
	var g Group
	if err := json.Unmarshal([]byte(out), &g); err != nil {
		t.Fatal(err)
	}

	m := baselineModel(t, root)
	m.contributor = "maintainer"
	m.groups = groupUI{open: true, records: []Group{g}}
	m = baselineSend(m, tea.WindowSizeMsg{Width: 80, Height: 28})
	next, cmd := m.Update(tea.KeyPressMsg{Text: "e"})
	m = next.(model)
	if cmd == nil || m.groups.editing != "edit" || !strings.Contains(ansi.Strip(m.viewContent()), "Edit group") {
		t.Fatal("e did not open the floating group editor")
	}

	title := strings.Repeat("Long group title ", 12)
	description := strings.Repeat("Long maintainer guidance that should wrap in the editor. ", 12)
	m.groups.edit.title.SetValue(title)
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyTab})
	if m.groups.edit.field != 1 {
		t.Fatal("Tab did not focus Description")
	}
	m.groups.edit.description.SetValue(description)
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyTab})
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyRight})
	if m.groups.edit.field != groupStatusField || m.groups.edit.status != "ready" {
		t.Fatal("Status did not remain a focused choice")
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: tea.KeyTab})
	m.groups.edit.assignee.SetValue("maintainer")
	editView := ansi.Strip(m.viewContent())
	for _, label := range []string{"Title", "Description", "Status", "Assignee"} {
		if !strings.Contains(editView, label) {
			t.Fatalf("floating editor omitted %s", label)
		}
	}
	for _, line := range strings.Split(editView, "\n") {
		if ansi.StringWidth(line) > m.width {
			t.Fatal("floating group editor overflowed the terminal")
		}
	}
	m = baselineSend(m, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl})
	if !m.groups.edit.previewing || !strings.Contains(ansi.Strip(m.viewContent()), "Preview group") {
		t.Fatal("Ctrl-P did not preview the edited group")
	}
	if !strings.Contains(m.groupEditPreviewText(m.groups.edit.preview.Width()), lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Accent)).Bold(true).Render("Title")) {
		t.Fatal("group preview title did not use bold accent styling")
	}
	next, cmd = m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = next.(model)
	if cmd == nil || !m.groups.busy {
		t.Fatal("Ctrl-S did not save through bin/group")
	}
	m = baselineSend(m, cmd().(groupsLoadedMsg))
	g = *m.selectedGroup()
	if m.groups.editing != "" || g.Title != title || g.Description != description || g.Status != "ready" || g.Assignee != "maintainer" || g.UpdatedBy != "maintainer" {
		t.Fatal("floating editor did not save all four fields with attribution")
	}

	m = baselineSend(m, tea.KeyPressMsg{Text: "n"})
	if m.groups.editing != "new" || !strings.Contains(ansi.Strip(m.viewContent()), "New group") {
		t.Fatal("n did not open the floating group creator")
	}
	m.groups.edit.title.SetValue("Follow-up")
	m.groups.edit.description.SetValue("Collect related reports")
	next, cmd = m.Update(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = next.(model)
	if cmd == nil || !m.groups.busy {
		t.Fatal("floating group creator did not call bin/group")
	}
	m = baselineSend(m, cmd().(groupsLoadedMsg))
	if m.groups.editing != "" || len(m.groups.records) != 2 || m.selectedGroup().Title != "Follow-up" {
		t.Fatal("floating group creator did not save and select the new group")
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
	pr["kind"], pr["number"], pr["title"] = "pr", 3, "Selected candidate"
	pr["url"], pr["action"], pr["confidence"] = "https://github.com/owner/repo/pull/3", "keep-open", "high"
	pr["reason"], pr["maintainer_notes"] = "Maintainer guidance", "Check compatibility first"
	pr["review_request"] = map[string]string{"by": "agent:helper", "at": "2026-09-29T00:00:00Z", "reason": "Check project compatibility"}
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
	m.contributor = "maintainer"
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
	for _, expected := range []string{"- pr #3", "Maintainer edited the group question", "Maintainer wants the candidate reconsidered", "Check compatibility first", "Pending review requested by agent:helper", "#### issue #2", "Current member checkpoints"} {
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

func TestCompletedOutcomesStayVisibleAndRejectedProposalsLeaveNotifications(t *testing.T) {
	m := baselineModel(t, baselineRoot(t))
	rejected := actionProposalRow{Number: 5, Title: "Keep this PR", Status: "rejected", Checkpoint: "rejected-5", Rejection: &proposalRejection{By: "maintainer", At: "2026-09-29T00:00:00Z", Reason: "Compatibility work remains useful"}}
	var completed actionProposalRow
	if err := json.Unmarshal([]byte(`{"number":3,"title":"Completed fixture","target":"https://github.com/owner/repo/pull/3","comment":"Published explanation","status":"executed","needs_attention":true,"outcome":{"comment":{"status":"succeeded"},"state_change":{"status":"succeeded"}}}`), &completed); err != nil {
		t.Fatal(err)
	}
	m.notifications = notificationsUI{open: true, actions: actionProposalList{Rows: []actionProposalRow{{Kind: "pr", Number: 3, Title: completed.Title, Target: completed.Target, Operation: "close", Comment: completed.Comment, Status: "executed", Needs: true},
		{Kind: "pr", Number: 4, Title: "Needs inspection", Operation: "close", Status: "uncertain", Needs: true},
		{Kind: "pr", Number: 5, Title: rejected.Title, Operation: "close", Status: "rejected"}}}}
	choices := m.notifications.choices()
	if len(choices) != 2 || choices[0].key != (Key{Kind: "pr", Number: 4}) || choices[1].key != (Key{Kind: "pr", Number: 3}) || m.notifications.choiceNeeds(choices[1]) || notificationCount(nil, m.notifications.actions) != 1 {
		t.Fatalf("completed outcome or uncertain closure was unavailable, or rejection remained: %+v", choices)
	}
	view := ansi.Strip(m.notificationsView())
	if !strings.Contains(view, "PR #3") || strings.Contains(view, "PR #5") || strings.Index(view, "Past actions") > strings.Index(view, "PR #3") || strings.Contains(strings.Split(view, "\n")[0], "owner/repo") {
		t.Fatal("completed outcome was not under Past actions or Notifications repeated the repository")
	}
	m.notifications.selected = 1
	for _, action := range m.contextFooterGroups()[0].hints {
		if action.keys == "a" || action.keys == "a/A" || action.keys == "Space" {
			t.Fatal("completed closure card still offered approval")
		}
	}
	pressed, approvalCmd := m.handleNotificationsKey(tea.KeyPressMsg{Text: "a"})
	if approvalCmd != nil || pressed.(model).status != "This action already ran." || pressed.(model).statusIsError() || pressed.(model).status == pressed.(model).statusWarning {
		t.Fatal("completed action card did not show a green already-ran status")
	}
	completed.Kind, completed.Operation = "pr", "close"
	m.notifications.actionReview = &actionReviewUI{row: completed, context: &actionProposalContext{Reason: "proposal is executed"}}
	view = ansi.Strip(m.actionReviewView())
	if !strings.Contains(view, "Published comment") || !strings.Contains(view, "Saved outcome") || strings.Contains(view, "Changed context") || strings.Contains(view, "Approval unavailable") || strings.Contains(strings.Split(view, "\n")[0], "owner/repo") {
		t.Fatal("completed PR closure appeared stale or offered a new approval")
	}
	if groups := m.contextFooterGroups(); groups[0].name != "Action outcome" {
		t.Fatal("completed PR closure kept proposal controls")
	}
	var completedAction actionProposalRow
	if err := json.Unmarshal([]byte(`{"kind":"issue","number":2,"title":"Completed fixture","target":"https://github.com/owner/repo/issues/2","operation":"reopen","comment":"Published reopen explanation","status":"executed","outcome":{"comment":{"status":"succeeded"},"state_change":{"status":"succeeded"}}}`), &completedAction); err != nil {
		t.Fatal(err)
	}
	m.notifications.actionReview = &actionReviewUI{row: completedAction, context: &actionProposalContext{Reason: "proposal is executed"}}
	view = ansi.Strip(m.actionReviewView())
	if !strings.Contains(view, "Published comment") || !strings.Contains(view, "Saved outcome") || strings.Contains(view, "Changed context") || strings.Contains(view, "Why: proposal is executed") || strings.Contains(strings.Split(view, "\n")[0], "owner/repo") {
		t.Fatal("completed issue action appeared stale or repeated the repository")
	}
	if groups := m.contextFooterGroups(); groups[0].name != "Action outcome" {
		t.Fatal("completed issue action kept proposal controls")
	}
	pressed, approvalCmd = m.handleNotificationsKey(tea.KeyPressMsg{Text: "a"})
	if approvalCmd != nil || pressed.(model).status != "This action already ran." || pressed.(model).statusIsError() || pressed.(model).status == pressed.(model).statusWarning {
		t.Fatal("completed action reader did not show a green already-ran status")
	}
	rejected.Kind, rejected.Operation = "pr", "close"
	context := actionProposalContext{Number: 5, ProposalCheckpoint: "rejected-5", Reason: "proposal is rejected", LatestRejection: rejected.Rejection}
	if err := json.Unmarshal([]byte(`{"rows":[{"kind":"ledger","fields":{"action":{"preview":""}}}]}`), &context.ItemContext); err != nil {
		t.Fatal(err)
	}
	m.notifications.actionReview = &actionReviewUI{row: rejected, context: &context}
	view = ansi.Strip(m.actionReviewView())
	if !strings.Contains(view, "Compatibility work remains useful") || !strings.Contains(view, "By: maintainer") || !strings.Contains(view, "No local triage decision yet") ||
		strings.Contains(view, "Publish the comment below") || strings.Contains(view, "Why: proposal is rejected") || strings.Contains(view, "Earlier objection") {
		t.Fatal("rejected proposal reader hid the reason or offered publication")
	}
	_, command := m.handleNotificationsKey(tea.KeyPressMsg{Text: "a"})
	if command != nil {
		t.Fatal("rejected proposal started approval")
	}
}

func TestSuggestedActionsAndExplicitPendingReviewHaveSeparateTabs(t *testing.T) {
	if len(tabs) != 4 || tabs[0].Name != "Untriaged" || tabs[1].Name != "Pending review" || tabs[2].Name != "Merge-Ready PRs" || tabs[3].Name != "All Items" {
		t.Fatal("the explicit Pending review tab is missing or an action tab returned")
	}
	flagged := []Item{{Kind: "issue", Number: 7, State: "closed", ReviewRequest: &ReviewRequest{By: "agent:triage", At: "2026-10-07T00:00:00Z", Reason: "Check impact"}},
		{Kind: "issue", Number: 8, State: "open", Action: "close"}}
	if listed := tabs[pendingReviewTab].Filter(flagged, Taxonomy{}); len(listed) != 1 || listed[0].Number != 7 {
		t.Fatalf("Pending review did not follow the explicit flag independent of state or triage: %+v", listed)
	}
	ready := []Item{{Kind: "pr", Number: 1, State: "open", ProposedLabels: []string{"ready"}, Confidence: "high"}, {Kind: "issue", Number: 2, State: "open", ProposedLabels: []string{"ready"}, Confidence: "high"},
		{Kind: "pr", Number: 4, State: "open", ProposedLabels: []string{"ready"}, Confidence: "medium"}}
	if listed := tabs[mergeReadyTab].Filter(ready, Taxonomy{}); len(listed) != 1 || listed[0].Number != 1 {
		t.Fatalf("Merge-Ready PRs did not list exactly the high-confidence ready PRs: %+v", listed)
	}

	m := baselineModel(t, baselineRoot(t))
	m.taxonomy = Taxonomy{ActionOperations: map[string]string{"comment": "comment", "close": "close", "reopen": "reopen"}}
	for number := 1; number <= 23; number++ {
		m.items = append(m.items, Item{Kind: "issue", Number: number, State: "open", Action: "close", Title: fmt.Sprintf("Fixture %d", number), CreatedAt: fmt.Sprintf("2026-01-%02dT00:00:00Z", number)})
	}
	m.items = append(m.items, Item{Kind: "issue", Number: 24, State: "closed", Action: "reopen", Title: "Reopen fixture"})
	m.items = append(m.items, Item{Kind: "issue", Number: 25, State: "closed", Action: "close", Title: "Already closed"})
	m.notifications = notificationsUI{open: true, actions: actionProposalList{Rows: []actionProposalRow{{Kind: "issue", Number: 1, Status: "pending", Active: true}}}}
	m.notifications.suggestions = suggestedActions(m.items, m.taxonomy, m.notifications.actions)
	if len(m.notifications.suggestions) != 23 {
		t.Fatalf("saved action suggestions were lost or duplicated: %d", len(m.notifications.suggestions))
	}
	allItems := append([]Item(nil), m.items...)
	m = baselineSend(m, ledgerReloadedMsg{root: m.installRoot, repo: m.repo, items: []Item{{Kind: "issue", Number: 2, State: "open", Action: "close"}}})
	if len(m.notifications.suggestions) != 1 {
		t.Fatalf("Notifications kept suggestions from an older ledger: %d", len(m.notifications.suggestions))
	}
	m.items = allItems
	m.notifications.suggestions = suggestedActions(m.items, m.taxonomy, m.notifications.actions)
	choices := m.notifications.choices()
	suggestionIndex, moreIndex := -1, -1
	for i, choice := range choices {
		if choice.kind == "item" && choice.suggestion >= 0 && m.notifications.suggestions[choice.suggestion].Action == "close" && suggestionIndex < 0 {
			suggestionIndex = i
		}
		if choice.kind == "suggestion-more" {
			moreIndex = i
		}
	}
	if suggestionIndex < 0 || moreIndex < 0 {
		t.Fatal("Notifications did not page suggested actions")
	}
	m.notifications.selected = suggestionIndex
	var cardSummary, cardMarkText string
	renderNotificationChoice(m.notifications, choices[suggestionIndex], func(_, summary string, mark cardMark) { cardSummary, cardMarkText = summary, mark.text })
	if !strings.Contains(cardSummary, "Suggested close · exact action needed") || cardMarkText != "" {
		t.Fatal("saved suggestion looked like an approved exact proposal")
	}
	stale := m.notifications
	stale.actions = actionProposalList{Rows: []actionProposalRow{{Kind: "issue", Number: 1, Operation: "close", Status: "pending", OutOfDate: &proposalOutOfDate{Reason: "item activity changed"}}}}
	staleChoice := stale.choices()[0]
	var staleSummary string
	renderNotificationChoice(stale, staleChoice, func(_, summary string, _ cardMark) { staleSummary = summary })
	if staleChoice.actionProposal != 0 || stale.choiceNeeds(staleChoice) || !stale.choiceSuggested(staleChoice) || !strings.Contains(staleSummary, "out of date") {
		t.Fatalf("a proposal GitHub refused as out of date still looked approvable: %q", staleSummary)
	}
	if choices[0].actionProposal != 0 || suggestionIndex == 0 || m.notifications.choiceNeeds(choices[suggestionIndex]) || !m.notifications.choiceSuggested(choices[suggestionIndex]) || !m.notifications.choiceSuggested(choices[moreIndex]) {
		t.Fatal("suggested actions were mixed with the cards that need attention")
	}
	next, cmd := m.handleNotificationsKey(tea.KeyPressMsg{Text: "y"})
	if cmd == nil || !strings.Contains(next.(model).status, "action suggestion") {
		t.Fatal("suggested action did not offer a focused agent handoff")
	}
	next, cmd = m.handleNotificationsKey(tea.KeyPressMsg{Text: "a"})
	if cmd != nil || !strings.Contains(next.(model).status, "exact action proposal") {
		t.Fatal("suggested action offered publication without an exact proposal")
	}
	m.notifications.selected = moreIndex
	next, cmd = m.handleNotificationsKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if cmd != nil || m.notifications.suggestionOffset != suggestedActionPageSize || m.notifications.choices()[m.notifications.selected].suggestion != suggestedActionPageSize {
		t.Fatal("suggested action page did not advance to the next bounded set")
	}

	proposalList := notificationsUI{actions: actionProposalList{Rows: []actionProposalRow{{Kind: "pr", Number: 3, Action: "close", Operation: "close", Status: "pending", Active: true, Needs: true},
		{Kind: "issue", Number: 2, Action: "close", Operation: "close", Status: "pending", Active: true, Needs: true}}}}
	var summaries []string
	var marks []string
	for _, choice := range proposalList.choices() {
		renderNotificationChoice(proposalList, choice, func(_, summary string, mark cardMark) {
			summaries = append(summaries, summary)
			marks = append(marks, mark.text)
		})
	}
	if len(summaries) != 2 || summaries[0] != "Close proposed" || summaries[1] != "Close proposed" || marks[0] != marks[1] {
		t.Fatalf("issue and PR closures had different notification cards: %v, %v", summaries, marks)
	}
	proposalList.actions.Rows[0].Needs = false
	proposalList.actions.Rows[1].Needs = false
	marks = nil
	for _, choice := range proposalList.choices() {
		renderNotificationChoice(proposalList, choice, func(_, _ string, mark cardMark) { marks = append(marks, mark.text) })
	}
	if len(marks) != 2 || marks[0] != "Close" || marks[1] != "Close" {
		t.Fatalf("viewed issue and PR proposals had different card marks: %v", marks)
	}
	actionCards := notificationsUI{actions: actionProposalList{Rows: []actionProposalRow{
		{Kind: "issue", Number: 1, Operation: "reopen", Status: "executed"},
		{Kind: "issue", Number: 2, Operation: "comment", Status: "executed"},
		{Kind: "issue", Number: 3, Operation: "close", Status: "executed"},
	}}}
	var actionMarks []cardMark
	for _, choice := range actionCards.choices() {
		renderNotificationChoice(actionCards, choice, func(_, _ string, mark cardMark) { actionMarks = append(actionMarks, mark) })
	}
	if len(actionMarks) != 3 || actionMarks[0] != (cardMark{"Reopen", currentTheme.Success}) ||
		actionMarks[1] != (cardMark{"Comment", currentTheme.Info}) || actionMarks[2] != (cardMark{"Close", currentTheme.Error}) {
		t.Fatalf("past action cards did not show the operation in its color: %+v", actionMarks)
	}
	empty := baselineModel(t, baselineRoot(t))
	empty.notifications = notificationsUI{open: true}
	if view := ansi.Strip(empty.notificationsView()); !strings.Contains(view, "No new Notifications.") || strings.Contains(view, "Nothing needs attention.") {
		t.Fatal("empty Notifications message was not updated")
	}
}

func TestProposalReaderShowsCurrentGuidanceAndBlocksStaleApproval(t *testing.T) {
	root := baselineRoot(t)
	script := `#!/usr/bin/env python3
import json, pathlib, sys
args = sys.argv[1:]
row = dict(kind="pr", operation="close", number=3, title="Fixture PR", target="https://github.com/owner/repo/pull/3", comment="Exact closure comment", checkpoint="proposal-1", status="pending", active=True, inputs=dict(context_checkpoint="ctx-1", evidence=[dict(kind="pr", number=3, snapshot_id="snapshot-1", components={"summary": {"status": "complete"}}), dict(kind="issue", number=4, snapshot_id="snapshot-2", components={"summary": {"status": "complete"}, "comments": {"status": "partial"}})], evidence_gaps=["Gap one", "Gap two", "Gap three", "Gap four", "Gap five"]))
if "context" in args:
    stale = pathlib.Path("stale-context").exists()
    omitted = 30 if pathlib.Path("long-context").exists() else 0
    item = dict(repository="owner/repo", item=dict(kind="pr", number=3), checkpoint="ctx-2" if stale else "ctx-1", requests=0, pagination=dict(offset=0, next_offset=None), rows=[dict(kind="ledger", id="ledger", fields=dict(action=dict(preview="keep-open", omitted_bytes=0), maintainer_notes=dict(preview="Check compatibility", omitted_bytes=omitted))), dict(kind="group", id="group:g1", fields=dict(title=dict(preview="Compatibility review", omitted_bytes=0), status=dict(preview="draft", omitted_bytes=0), description=dict(preview="Compare alternatives", omitted_bytes=0))), dict(kind="member", id="member:g1:pr:3", selected=True, fields=dict(notes=dict(preview="Keep the old API", omitted_bytes=0)))])
    print(json.dumps(dict(repository="owner/repo", kind="pr", number=3, proposal_checkpoint="proposal-1", current=not stale, reason="local context changed" if stale else None, item_context=item, latest_rejection=dict(by="maintainer", at="2026-09-29T00:00:00Z", reason="Earlier objection", proposal_checkpoint="older"), requests=0)))
elif "review" in args:
    print(json.dumps(dict(plan=dict(repo="owner/repo", operation="conversation-or-state-action", proposals=[row]), approval="fresh-approval")))
else:
    sys.exit("unexpected action proposal command")
`
	if err := os.WriteFile(filepath.Join(root, "bin", "action-proposals"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	sourceScript := `#!/usr/bin/env python3
import json
text = "Check compatibility across all supported versions"
print(json.dumps(dict(repository="owner/repo", item=dict(kind="pr", number=3), checkpoint="ctx-1", row="ledger", field="maintainer_notes", text=text, bytes=dict(offset=0, returned=len(text.encode())), continuation=None, requests=0)))
`
	if err := os.WriteFile(filepath.Join(root, "bin", "item-context"), []byte(sourceScript), 0o755); err != nil {
		t.Fatal(err)
	}
	var row actionProposalRow
	if err := json.Unmarshal([]byte(`{"kind":"pr","operation":"close","number":3,"title":"Fixture PR","target":"https://github.com/owner/repo/pull/3","comment":"Exact closure comment","checkpoint":"proposal-1","status":"pending","active":true,"inputs":{"context_checkpoint":"ctx-1","evidence":[{"kind":"pr","number":3,"snapshot_id":"snapshot-1","components":{"summary":{"status":"complete"}}},{"kind":"issue","number":4,"snapshot_id":"snapshot-2","components":{"summary":{"status":"complete"},"comments":{"status":"partial"}}}],"evidence_gaps":["Gap one","Gap two","Gap three","Gap four","Gap five"]}}`), &row); err != nil {
		t.Fatal(err)
	}
	row.HeadSHA = strings.Repeat("a", 40)
	row.UpdatedAt = "2026-09-30T12:34:56Z"

	m := baselineModel(t, root)
	m.drafts[Key{Kind: "issue", Number: 1}] = decisionSnapshot{}
	m.notifications = notificationsUI{open: true, actions: actionProposalList{Rows: []actionProposalRow{row}}}
	choice, ok := m.notifications.actionProposalChoice(Key{Kind: "pr", Number: 3})
	if !ok {
		t.Fatal("PR closure was missing from the unified proposal list")
	}
	next, cmd := m.openActionReview(choice)
	m = next.(model)
	if cmd == nil || !m.notifications.actionReview.busy {
		t.Fatal("proposal did not start a local context read")
	}
	_, approval := m.handleNotificationsKey(tea.KeyPressMsg{Text: "a"})
	if approval != nil {
		t.Fatal("approval was available before context loaded")
	}
	current := cmd().(actionReviewMsg)
	if current.err != nil {
		t.Fatal(current.err)
	}
	m = baselineSend(m, current)
	m = baselineSend(m, tea.WindowSizeMsg{Width: 120, Height: 100})
	view := ansi.Strip(m.actionReviewView())
	for _, expected := range []string{"Check compatibility", "Keep the old API", "Earlier objection · By: maintainer", "PR #3 Fixture PR", "PR #3 Complete", "Issue #4 Not found: comments", "Gap Not found: Gap one", "Gap Not found: Gap five", "Exact closure comment"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("proposal reader omitted %q: %s", expected, view)
		}
	}
	if strings.Contains(view, "snapshot-1") || strings.Contains(view, "summary: complete") {
		t.Fatal("proposal reader displayed technical evidence identifiers or component states")
	}
	viewLines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	footer := strings.TrimSpace(viewLines[len(viewLines)-1])
	if strings.Contains(view, "Observed PR revision") || !strings.Contains(footer, "Updated: 2026-09-30 12:34 UTC") || !strings.HasSuffix(footer, "Head: "+row.HeadSHA) {
		t.Fatalf("proposal revision was not shown as a bottom footer: %q", footer)
	}
	if narrow := proposalRevisionFooter(row, 45); ansi.StringWidth(narrow) != 45 || !strings.Contains(narrow, "…") {
		t.Fatalf("narrow proposal footer did not preserve both aligned fields: %q", narrow)
	}
	copyText, _ := m.yankActionProposal(row)
	for _, expected := range []string{"snapshot-1", "snapshot-2", "--number 3 --checkpoint proposal-1", "Not found: Issue #4 · comments", "Gap one", "Current guidance and feedback:"} {
		if !strings.Contains(copyText, expected) {
			t.Fatalf("proposal handoff omitted %q: %s", expected, copyText)
		}
	}
	if strings.Contains(copyText, "summary: complete") {
		t.Fatal("proposal handoff repeated complete component details")
	}
	if _, cmd := m.handleNotificationsKey(tea.KeyPressMsg{Text: "y"}); cmd == nil {
		t.Fatal("y did not offer the proposal handoff from the reader")
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
	bottom := m.notifications.actionReview.scroll
	if bottom == 0 {
		t.Fatal("proposal never scrolled to its last line")
	}
	m = baselineSend(m, tea.KeyPressMsg{Text: "k"})
	if m.notifications.actionReview.scroll != bottom-1 {
		t.Fatal("proposal kept invisible scroll steps beyond its last line")
	}
	m.notifications.actionReview.scroll = 0
	_, reviewCmd := m.handleNotificationsKey(tea.KeyPressMsg{Text: "a"})
	if reviewCmd == nil {
		t.Fatal("current proposal did not prepare exact review")
	}
	if exact := reviewCmd().(actionReviewMsg); exact.err != nil || exact.approval != "fresh-approval" {
		t.Fatalf("exact review did not return its approval: %+v", exact.err)
	}
	review := closureReviewCmd(root, m.repo, m.notificationsGeneration, false, []int{3})().(closureReviewMsg)
	if review.err != nil || !review.review.Contexts[3].Current {
		t.Fatalf("batch review did not retain checked context: %+v", review.err)
	}
	missingContext := review
	missingContext.review.Contexts = nil
	_, publish := m.finishClosureReview(missingContext)
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
	next, cmd = m.openActionReview(choice)
	m = baselineSend(next.(model), cmd().(actionReviewMsg))
	underlying := m.actionReviewView()
	next, notesCmd := m.handleNotificationsKey(tea.KeyPressMsg{Text: "m"})
	m = next.(model)
	if notesCmd == nil || !m.notifications.notesOpen {
		t.Fatal("longer local guidance was not available from the proposal")
	}
	m = baselineSend(m, notesCmd().(proposalNotesMsg))
	notesView := ansi.Strip(m.proposalNotesViewport().View())
	if !strings.Contains(notesView, "across all supported versions") || !strings.Contains(notesView, "Group guidance") || !strings.Contains(notesView, "Member note") || m.actionReviewView() != underlying {
		t.Fatal("floating notes failed to show full text without reflowing the proposal")
	}
	m = baselineSend(m, tea.KeyPressMsg{Text: "m"})
	if m.notifications.notesOpen {
		t.Fatal("m did not close the floating notes window")
	}

	if err := os.WriteFile(filepath.Join(root, "stale-context"), []byte("yes"), 0o644); err != nil {
		t.Fatal(err)
	}
	next, cmd = m.openActionReview(choice)
	m = next.(model)
	wrong := current
	wrong.generation++
	m = baselineSend(m, wrong)
	if !m.notifications.actionReview.busy {
		t.Fatal("late context reply replaced the pending read")
	}
	m = baselineSend(m, cmd().(actionReviewMsg))
	view = ansi.Strip(m.actionReviewView())
	if !strings.Contains(view, "Changed context") || strings.Contains(view, "Publish the comment below") {
		t.Fatal("stale proposal appeared executable")
	}
	next, approval = m.handleNotificationsKey(tea.KeyPressMsg{Text: "a"})
	m = next.(model)
	if approval != nil || len(m.drafts) != 1 || m.status == "" {
		t.Fatal("stale proposal gained approval, discarded a draft or failed to report the refusal")
	}
}

func TestStagedActionReviewChecksContextBeforeExactApproval(t *testing.T) {
	root := baselineRoot(t)
	script := `#!/usr/bin/env python3
import json, pathlib, sys
args = sys.argv[1:]
answer = pathlib.Path("answered-action").read_text() if pathlib.Path("answered-action").exists() else None
row = dict(kind="issue", number=1, title="Needs reproduction", target="https://github.com/owner/repo/issues/1", action="comment", operation="comment", comment="Could you share steps to reproduce?", updated_at="2026-10-04T01:00:00Z", checkpoint="action-2" if answer else "action-1", status="pending", active=True, decision_question="Should this request be sent?", inputs=dict(context_checkpoint="ctx-a", evidence=[], evidence_gaps=["Discussion not acquired"]))
if answer:
    row["decision_resolution"] = dict(by="maintainer", at="2026-10-04T02:00:00Z", reason=answer, held_checkpoint="action-1")
if "context" in args:
    stale = pathlib.Path("stale-action").exists()
    context = dict(repository="owner/repo", item=dict(kind="issue", number=1), checkpoint="ctx-a", requests=0, pagination=dict(offset=0, next_offset=None), rows=[dict(kind="ledger", id="ledger", fields=dict(action="comment", reason="Missing reproduction"))])
    print(json.dumps(dict(repository="owner/repo", kind="issue", number=1, proposal_checkpoint=row["checkpoint"], current=not stale, reason="local guidance changed" if stale else None, item_context=context, requests=0)))
elif "answer" in args:
    answer = args[args.index("--answer") + 1]
    pathlib.Path("answered-action").write_text(answer)
    row["checkpoint"] = "action-2"
    row["decision_resolution"] = dict(by=args[args.index("--by") + 1], at="2026-10-04T02:00:00Z", reason=answer, held_checkpoint="action-1")
    print(json.dumps(row))
elif "review" in args:
    print(json.dumps(dict(plan=dict(repo="owner/repo", operation="conversation-or-state-action", proposals=[row]), approval="exact-approval")))
elif "execute" in args:
    pathlib.Path("published-action").write_text("yes")
    print(json.dumps(dict(results=[dict(kind="issue", number=1, status="executed")])))
else:
    sys.exit("unexpected action command")
`
	if err := os.WriteFile(filepath.Join(root, "bin", "action-proposals"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	row := actionProposalRow{Kind: "issue", Number: 1, Title: "Needs reproduction", Target: "https://github.com/owner/repo/issues/1", Action: "comment", Operation: "comment", Comment: "Could you share steps to reproduce?", UpdatedAt: "2026-10-04T01:00:00Z", Checkpoint: "action-1", Status: "pending", Active: true, Needs: true, Inputs: proposalInputs{ContextCheckpoint: "ctx-a"}, DecisionQuestion: "Should this request be sent?"}
	if err := json.Unmarshal([]byte(`{"context_checkpoint":"ctx-a","evidence":[{"kind":"issue","number":1,"snapshot_id":"snapshot-1","components":{"summary":{"status":"complete"}}}]}`), &row.Inputs); err != nil {
		t.Fatal(err)
	}
	m := baselineModel(t, root)
	m.height = 80
	m.contributor = "maintainer"
	m.notifications = notificationsUI{open: true, actions: actionProposalList{Rows: []actionProposalRow{row}}}
	choice := m.notifications.choices()[0]
	if choice.actionProposal != 0 || choice.key != (Key{Kind: "issue", Number: 1}) {
		t.Fatal("staged issue action did not appear in Notifications")
	}
	if err := os.WriteFile(filepath.Join(root, "stale-action"), []byte("yes"), 0o644); err != nil {
		t.Fatal(err)
	}
	next, cmd := m.openActionReview(choice)
	m = baselineSend(next.(model), cmd().(actionReviewMsg))
	if m.notifications.actionReview == nil || m.notifications.actionReview.context == nil || m.notifications.actionReview.context.Current {
		t.Fatal("stale action context was accepted")
	}
	next, cmd = m.handleNotificationsKey(tea.KeyPressMsg{Text: "a"})
	m = next.(model)
	if cmd == nil || !m.comment.open || m.notifications.actionReview.approval != "" {
		t.Fatal("questioned action entered approval before an answer")
	}
	next, _ = m.handleCommentKey(tea.KeyPressMsg{Text: "esc"})
	m = next.(model)
	if err := os.Remove(filepath.Join(root, "stale-action")); err != nil {
		t.Fatal(err)
	}
	next, cmd = m.openActionReview(choice)
	m = baselineSend(next.(model), cmd().(actionReviewMsg))
	styledReview := m.actionReviewView()
	if view := ansi.Strip(styledReview); !strings.Contains(view, "Could you share steps to reproduce?") || !strings.Contains(view, "Should this request be sent?") || strings.Contains(view, "snapshot-1") || strings.Contains(view, "summary: complete") {
		t.Fatal("exact proposed comment or question was absent from the action review")
	}
	if !strings.Contains(styledReview, newProposalReviewStyles().section.Render("Proposed action")) || !strings.Contains(styledReview, newProposalReviewStyles().section.Render("Comment to publish")) {
		t.Fatal("issue action did not use the shared proposal review styling")
	}
	next, cmd = m.handleNotificationsKey(tea.KeyPressMsg{Text: "r"})
	m = next.(model)
	if cmd != nil || m.comment.open {
		t.Fatal("r still opened the action answer composer")
	}
	next, cmd = m.handleNotificationsKey(tea.KeyPressMsg{Text: "a"})
	m = next.(model)
	if cmd == nil || !m.comment.open || m.comment.answerCheckpoint != "action-1" {
		t.Fatal("a did not open the attributed answer composer")
	}
	m.height = 30
	m.layoutComment()
	panel := ansi.Strip(m.commentView())
	if lipgloss.Height(panel) > m.commentHeight() || !strings.Contains(strings.Split(panel, "\n")[lipgloss.Height(panel)-1], "╯") {
		t.Fatal("action answer editor overflowed or lost its bottom border")
	}
	m.comment.answerQuestion = strings.Repeat("Long question text ", 100)
	m.layoutComment()
	if lipgloss.Height(m.commentView()) > m.commentHeight() {
		t.Fatal("long action question overflowed the answer editor")
	}
	m.comment.text.SetValue("Send a focused request")
	next, cmd = m.handleCommentKey(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = next.(model)
	if cmd == nil || !m.comment.busy {
		t.Fatal("answer composer did not save through the owning script")
	}
	answerMsg := cmd().(proposalAnswerDoneMsg)
	if answerMsg.err != nil || !answerMsg.saved || answerMsg.row.DecisionResolution == nil || answerMsg.row.DecisionResolution.By != "maintainer" {
		t.Fatalf("answer was not saved and attributed: %+v", answerMsg)
	}
	updated, _ := m.finishProposalAnswer(answerMsg)
	m = updated.(model)
	m.notifications.actions = actionProposalList{Rows: []actionProposalRow{answerMsg.row}}
	choice = m.notifications.choices()[0]
	next, cmd = m.openActionReview(choice)
	m = baselineSend(next.(model), cmd().(actionReviewMsg))
	if view := ansi.Strip(m.actionReviewView()); !strings.Contains(view, "Could you share steps to reproduce?") || !strings.Contains(view, "Send a focused request") {
		t.Fatal("exact comment and attributed answer were absent from the approval review")
	}
	next, cmd = m.handleNotificationsKey(tea.KeyPressMsg{Text: "a"})
	m = baselineSend(next.(model), cmd().(actionReviewMsg))
	if m.notifications.actionReview.approval != "exact-approval" {
		t.Fatal("exact action review did not retain its approval")
	}
	if _, err := os.Stat(filepath.Join(root, "published-action")); !os.IsNotExist(err) {
		t.Fatal("first approval press published the action")
	}
	next, cmd = m.handleNotificationsKey(tea.KeyPressMsg{Text: "a"})
	if cmd == nil || !next.(model).notifications.actionReview.busy {
		t.Fatal("second approval press did not start the selected write")
	}
	if result := cmd().(actionReviewMsg); result.err != nil {
		t.Fatal(result.err)
	}
	if _, err := os.Stat(filepath.Join(root, "published-action")); err != nil {
		t.Fatal("approved action was not sent to its owning script")
	}
}

func TestQuestionedClosureAnswerRequiresExactReviewBeforePublish(t *testing.T) {
	root := baselineRoot(t)
	script := `#!/usr/bin/env python3
import json, pathlib, sys
args = sys.argv[1:]
answer = pathlib.Path("closure-answer").read_text() if pathlib.Path("closure-answer").exists() else None
row = dict(kind="pr", number=3, operation="close", title="Older change", target="https://github.com/owner/repo/pull/3", comment="This PR is superseded by #4.", head_sha="b" * 40, updated_at="2026-10-04T01:00:00Z", checkpoint="closure-2" if answer else "closure-1", status="pending", active=True, decision_question="Does #4 replace this PR?", inputs=dict(context_checkpoint="ctx-a", evidence=[], evidence_gaps=["Comparison pending"]))
if answer:
    row["decision_resolution"] = dict(by="maintainer", at="2026-10-04T02:00:00Z", reason=answer, held_checkpoint="closure-1")
if "context" in args:
    context = dict(repository="owner/repo", item=dict(kind="pr", number=3), checkpoint="ctx-a", requests=0, pagination=dict(offset=0, next_offset=None), rows=[])
    print(json.dumps(dict(repository="owner/repo", kind="pr", number=3, proposal_checkpoint=row["checkpoint"], current=True, reason=None, item_context=context, requests=0)))
elif "answer" in args:
    text = args[args.index("--answer") + 1]
    pathlib.Path("closure-answer").write_text(text)
    row["checkpoint"] = "closure-2"
    row["decision_resolution"] = dict(by=args[args.index("--by") + 1], at="2026-10-04T02:00:00Z", reason=text, held_checkpoint="closure-1")
    print(json.dumps(row))
elif "review" in args:
    print(json.dumps(dict(plan=dict(repo="owner/repo", operation="conversation-or-state-action", proposals=[row]), approval="exact-closure-approval")))
elif "execute" in args:
    pathlib.Path("published-closure").write_text("yes")
    print(json.dumps(dict(results=[dict(kind="pr", number=3, status="executed")])))
else:
    sys.exit("unexpected action proposal operation")
`
	if err := os.WriteFile(filepath.Join(root, "bin", "action-proposals"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	row := actionProposalRow{Kind: "pr", Number: 3, Operation: "close", Title: "Older change", Target: "https://github.com/owner/repo/pull/3", Comment: "This PR is superseded by #4.", HeadSHA: strings.Repeat("b", 40), UpdatedAt: "2026-10-04T01:00:00Z", Checkpoint: "closure-1", Status: "pending", Active: true, DecisionQuestion: "Does #4 replace this PR?", Inputs: proposalInputs{ContextCheckpoint: "ctx-a"}}
	m := baselineModel(t, root)
	m.contributor = "maintainer"
	m.notifications = notificationsUI{open: true, actions: actionProposalList{Rows: []actionProposalRow{row}}}
	choice, ok := m.notifications.actionProposalChoice(Key{Kind: "pr", Number: 3})
	if !ok {
		t.Fatal("questioned PR closure was missing from unified proposals")
	}
	next, cmd := m.openActionReview(choice)
	m = baselineSend(next.(model), cmd().(actionReviewMsg))
	if m.notifications.actionReview == nil || m.notifications.actionReview.context == nil {
		t.Fatal("PR closure did not open the shared action reader")
	}
	next, cmd = m.handleNotificationsKey(tea.KeyPressMsg{Text: "a"})
	m = next.(model)
	if cmd == nil || !m.comment.open || m.comment.answerCheckpoint != "closure-1" {
		t.Fatal("a did not open the PR closure answer composer")
	}
	m.comment.text.SetValue("Yes, #4 retains the behavior")
	next, cmd = m.handleCommentKey(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	m = next.(model)
	answerMsg := cmd().(proposalAnswerDoneMsg)
	if answerMsg.err != nil || !answerMsg.saved {
		t.Fatalf("PR closure answer was not saved on the unified proposal path: %+v", answerMsg)
	}
	updated, _ := m.finishProposalAnswer(answerMsg)
	m = updated.(model)
	row.Checkpoint = "closure-2"
	row.DecisionResolution = &actionDecisionResolution{By: "maintainer", At: "2026-10-04T02:00:00Z", Reason: "Yes, #4 retains the behavior", HeldCheckpoint: "closure-1"}
	m.notifications.actions = actionProposalList{Rows: []actionProposalRow{row}}
	choice, ok = m.notifications.actionProposalChoice(Key{Kind: "pr", Number: 3})
	if !ok {
		t.Fatal("answered PR closure was missing from unified proposals")
	}
	next, cmd = m.openActionReview(choice)
	m = baselineSend(next.(model), cmd().(actionReviewMsg))
	if view := ansi.Strip(m.actionReviewView()); !strings.Contains(view, row.Target) || !strings.Contains(view, row.Comment) || !strings.Contains(view, row.DecisionResolution.Reason) {
		t.Fatal("PR closure review omitted exact target, public comment or answer")
	}
	next, cmd = m.handleNotificationsKey(tea.KeyPressMsg{Text: "a"})
	m = baselineSend(next.(model), cmd().(actionReviewMsg))
	if m.notifications.actionReview == nil || m.notifications.actionReview.approval != "exact-closure-approval" {
		t.Fatal("answered closure did not enter exact approval review")
	}
	if _, err := os.Stat(filepath.Join(root, "published-closure")); !os.IsNotExist(err) {
		t.Fatal("first approval press published the closure")
	}
	_, cmd = m.handleNotificationsKey(tea.KeyPressMsg{Text: "a"})
	if cmd == nil {
		t.Fatal("second approval press did not start the closure write")
	}
	if result := cmd().(actionReviewMsg); result.err != nil {
		t.Fatal(result.err)
	}
	if _, err := os.Stat(filepath.Join(root, "published-closure")); err != nil {
		t.Fatal("approved closure was not sent to its owning script")
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
