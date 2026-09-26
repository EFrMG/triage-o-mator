package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func notificationsFixture(t *testing.T) model {
	t.Helper()
	m := corpusFixture(t)
	m.width, m.height, m.ready = 120, 36, true
	m.focus = FocusSidebar
	m.sidebar.selected = notificationsIndex
	script := `#!/usr/bin/env python3
import json,sys
a=sys.argv[1:]
assert a[:2]==['--expected-repo','owner/repo'],a
command=a[2]
assert command in ('track-list','attention-list','action-list','notification-state','notification-view','notification-dismiss'),a
if command in ('notification-view','notification-dismiss'):
    print('{}')
    sys.exit(0)
if command=='notification-state':
    print(json.dumps({'repository':{'full_name':'owner/repo'},'rows':{},'requests':0}))
    sys.exit(0)
assert a[-4:]==['--offset','0','--limit','5'],a
if command=='track-list':
    print(json.dumps({'repository':{'full_name':'owner/repo'},'rows':[],'total':0,'offset':0,'next':None,'requests':0}))
    sys.exit(0)
attention=command=='attention-list'
row={'id':'8028','label':'PR #8028: retained activity','preview':json.dumps({'response_comments':2,'coverage':{'latest_complete':False,'latest_gap_count':1,'last_successful_check':None}}),'omitted_bytes':0,'label_omitted_bytes':0,'number':8028,'selectable':True,'attention':True}
row.update({'watch_checkpoint':'b'*64} if attention else {'history_checkpoint':'c'*64})
page={'schema_version':1,'policy':'attention-reader-v1' if attention else 'action-history-reader-v1','repository':{'host':'github.com','full_name':'owner/repo'},'section':'list','number':0,'checkpoint':'a'*64,'entry':'','requests':0,'rows':[row],'pagination':{'total':1,'offset':0,'returned':1,'omitted_before':0,'omitted_after':0,'next_offset':None}}
print(json.dumps(page))
`
	if err := os.WriteFile(filepath.Join(m.installRoot, "bin/cache"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestNotificationsMenuHasTwoSections(t *testing.T) {
	m := notificationsFixture(t)
	before := m.form.Snapshot()
	cmd := m.enterSidebarSelection()
	if !m.notifications.open || cmd == nil {
		t.Fatal("sidebar did not open notifications")
	}
	msg := cmd().(notificationsMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	next, _ := m.Update(msg)
	m = next.(model)
	view := m.viewContent()
	attention, actions := strings.Index(view, "Needs attention"), strings.Index(view, "Past actions")
	if attention < 0 || actions <= attention || !strings.Contains(view, "2 response comment(s)") || !strings.Contains(view, "coverage incomplete") || !strings.Contains(view, "Press w on an issue or PR") || strings.Contains(view, "Past closures") {
		t.Fatalf("notification content or order: %s", view)
	}
	if strings.Contains(m.sidebar.View(false), "Imported actions") || strings.Contains(m.sidebar.View(false), "Needs attention") {
		t.Fatal("old sidebar entries remain")
	}
	if m.form.Snapshot() != before {
		t.Fatal("reading notifications changed the decision draft")
	}

	next, cmd = m.handleNotificationsKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if cmd == nil || !m.attention.open || m.attention.location.section != "history" || m.attention.location.number != 8028 || m.attention.location.checkpoint != strings.Repeat("b", 64) {
		t.Fatal("attention row lost its watch binding")
	}
	m, _ = corpusKey(m, "esc")
	if !m.notifications.open || m.attention.open {
		t.Fatal("reader did not return to Notifications")
	}
	m, _ = corpusKey(m, "j")
	next, cmd = m.handleNotificationsKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = next.(model)
	if cmd == nil || !m.actionHistory.open || m.actionHistory.location.section != "entries" || m.actionHistory.location.checkpoint != strings.Repeat("c", 64) {
		t.Fatal("closure row lost its independent history binding")
	}
}

func TestTrackedNotificationCountAndMarkRead(t *testing.T) {
	m := notificationsFixture(t)
	cmd := m.enterSidebarSelection()
	msg := cmd().(notificationsMsg)
	msg.tracked.Rows = []trackedRow{{Title: "Changed", NewCount: 2, CheckedAt: "2026-09-25T00:00:00Z"}, {Title: "Quiet", CheckedAt: "2026-09-25T00:00:00Z"}}
	msg.tracked.Rows[0].Identity.Kind, msg.tracked.Rows[0].Identity.Number = "issue", 1
	msg.tracked.Rows[1].Identity.Kind, msg.tracked.Rows[1].Identity.Number = "pr", 2
	msg.tracked.Total, msg.tracked.UnreadTotal = 2, 1
	next, _ := m.Update(msg)
	m = next.(model)
	if !strings.Contains(m.sidebar.View(false), "Notifications (1)") {
		t.Fatal("sidebar did not count unread tracked items")
	}
	view := m.viewContent()
	if strings.Index(view, "Changed") > strings.Index(view, "Past actions") || !strings.Contains(view, "Quiet") {
		t.Fatalf("tracked order or sections missing: %s", view)
	}
	next, cmd = m.handleNotificationsKey(tea.KeyPressMsg{Text: "v"})
	m = next.(model)
	if !m.trackingBusy || cmd == nil {
		t.Fatal("mark read did not start for unread tracked item")
	}
	m.trackingBusy = false
	next, cmd = m.handleNotificationsKey(tea.KeyPressMsg{Text: "d"})
	m = next.(model)
	if cmd == nil || !m.trackingBusy || cmd().(trackDoneMsg).action != "remove" {
		t.Fatal("one d did not stop tracking the selected item")
	}
}

func TestNotificationsViewAndDismissOnePress(t *testing.T) {
	m := notificationsFixture(t)
	cmd := m.enterSidebarSelection()
	next, _ := m.Update(cmd())
	m = next.(model)
	next, cmd = m.handleNotificationsKey(tea.KeyPressMsg{Text: "v"})
	m = next.(model)
	if cmd == nil || !m.trackingBusy {
		t.Fatal("v did not mark watched activity viewed")
	}
	view := cmd().(notificationDoneMsg)
	if view.action != "view" || view.source != "watch" || view.number != 8028 {
		t.Fatalf("wrong view command: %+v", view)
	}
	m.trackingBusy = false
	m.notifications.state.Rows = map[string]notificationStatus{"watch:pr:8028": {ViewedCheckpoint: &m.notifications.attention.Rows[0].WatchCheckpoint}}
	choices := m.notifications.choices()
	if len(choices) != 2 || choices[1].kind != "attention" || m.notifications.choiceNeeds(choices[1]) {
		t.Fatalf("viewed watch did not move to Past actions: %+v", choices)
	}
	m.notifications.selected = 1
	next, cmd = m.handleNotificationsKey(tea.KeyPressMsg{Text: "d"})
	m = next.(model)
	if cmd == nil || !m.trackingBusy {
		t.Fatal("one d did not dismiss the selected row")
	}
	dismiss := cmd().(notificationDoneMsg)
	if dismiss.action != "dismiss" || dismiss.source != "watch" {
		t.Fatalf("wrong dismiss command: %+v", dismiss)
	}
	m.trackingBusy = false
	m.notifications.selected = 0
	next, cmd = m.handleNotificationsKey(tea.KeyPressMsg{Text: "v"})
	m = next.(model)
	if cmd == nil || !m.trackingBusy {
		t.Fatal("v did not mark imported action viewed")
	}
	action := cmd().(notificationDoneMsg)
	if action.action != "view" || action.source != "action" || action.number != 8028 {
		t.Fatalf("wrong imported-action view command: %+v", action)
	}
	m.notifications.state.Rows["action:pr:8028"] = notificationStatus{ViewedCheckpoint: &m.notifications.closures.Rows[0].HistoryCheckpoint}
	if m.notifications.actionNeeds(m.notifications.closures.Rows[0]) || m.notifications.choiceNeeds(m.notifications.choices()[0]) {
		t.Fatal("viewed imported action stayed in Needs attention")
	}
}

func TestNotificationsStaleReplies(t *testing.T) {
	m := notificationsFixture(t)
	cmd := m.enterSidebarSelection()
	process := m.notificationsLifecycle.current
	m, _ = corpusKey(m, "esc")
	if !process.cancelled || m.notifications.open {
		t.Fatal("cancel did not stop the offline read")
	}
	if msg := cmd().(notificationsMsg); msg.err == nil {
		t.Fatal("cancelled read succeeded")
	}
}

func TestNotificationsSmallScreenKeepsPastClosureReachable(t *testing.T) {
	m := notificationsFixture(t)
	m.width, m.height = 60, 24
	attention := attentionPage{}
	for i := 1; i <= 5; i++ {
		attention.Rows = append(attention.Rows, attentionRow{Number: i, Label: "PR activity", Preview: `{}`})
	}
	attention.Pagination.Total = 5
	closures := actionHistoryPage{Rows: []actionHistoryRow{{Number: 8028, Label: "PR #8028 closure"}}}
	closures.Pagination.Total = 1
	m.notifications = notificationsUI{open: true, attention: &attention, closures: &closures, selected: 0}
	view := m.viewContent()
	if !strings.Contains(view, "PR #8028 closure") {
		t.Fatalf("selected closure clipped at 60×24: %s", view)
	}
}

func TestNotificationDirectionalOpenAndStyledReaders(t *testing.T) {
	for _, open := range []string{"l", "right"} {
		m := notificationsFixture(t)
		cmd := m.enterSidebarSelection()
		next, _ := m.Update(cmd())
		m = next.(model)
		m, cmd = corpusKey(m, open)
		if cmd == nil || !m.attention.open || m.attention.location.section != "history" {
			t.Fatalf("%s did not open the selected notification", open)
		}
	}

	attention := attentionFixture(t)
	attention.width, attention.height = 60, 24
	next, cmd := attention.readAttention(attentionLocation{section: "history", number: 1, checkpoint: strings.Repeat("b", 64)})
	attention = finishAttentionCommand(t, next.(model), cmd)
	if view := attention.attentionView(); !strings.Contains(view, "▔") || !strings.Contains(view, "Discussion") {
		t.Fatalf("attention records lack selected cards: %s", view)
	}
	if view := attention.viewContent(); !strings.Contains(view, "▔") || !strings.Contains(view, "Saved discussion entry") {
		t.Fatalf("attention card clipped by the 60×24 layout: %s", view)
	}
	attention, _ = corpusKey(attention, "j")
	if attention.attention.selected != 1 {
		t.Fatal("j did not select the next attention record")
	}
	attention, _ = corpusKey(attention, "shift+tab")
	if attention.attention.selected != 0 {
		t.Fatal("Shift-Tab did not select the previous attention record")
	}
	attention, _ = corpusKey(attention, "j")
	attention, cmd = corpusKey(attention, "right")
	if cmd == nil || !attention.notificationPR.open || attention.notificationPR.key.Number != 1 {
		t.Fatal("comment selection did not open its PR")
	}

	action := actionHistoryFixture(t)
	action.width, action.height = 60, 24
	next, cmd = action.readActionHistory(actionHistoryLocation{section: "entries", number: 1, checkpoint: strings.Repeat("b", 64)})
	action = finishActionCommand(t, next.(model), cmd)
	if view := action.actionHistoryView(); !strings.Contains(view, "▔") || !strings.Contains(view, "Closure explanations") {
		t.Fatalf("closure records lack selected cards: %s", view)
	}
	if view := action.viewContent(); !strings.Contains(view, "▔") || !strings.Contains(view, "record 1") {
		t.Fatalf("closure card clipped by the 60×24 layout: %s", view)
	}
	action, _ = corpusKey(action, "j")
	if action.actionHistory.selected != 1 {
		t.Fatal("j did not select the next closure record")
	}
	action, cmd = corpusKey(action, "l")
	if cmd == nil || !action.notificationPR.open || action.notificationPR.key.Number != 1 {
		t.Fatal("closure selection did not open its PR")
	}
}

func TestNotificationDiscussionExplainsResponse(t *testing.T) {
	m := notificationsFixture(t)
	m.width, m.height = 100, 36
	m.attention = attentionUI{open: true, location: attentionLocation{section: "history", number: 8028}}
	page := attentionPage{Section: "history", Number: 8028}
	page.Pagination = attentionWindow{Total: 2, Returned: 2}
	page.Rows = []attentionRow{
		{ID: "revision", Label: "comment:5672562917 · 2026-09-15T00:01:47Z", Preview: `{"source_id":"comment:5672562917","relation":"at-or-after-closure","source_times":{"created_at":"2026-09-15T00:01:47Z"},"comment_author":"joelclark","comment_excerpt":"Please reopen. These are different bugs.","reference_count":1}`},
		{ID: "later", Label: "comment:2 · 2026-09-16T00:01:47Z", Preview: `{"source_id":"comment:2","relation":"at-or-after-closure","source_times":{"created_at":"2026-09-16T00:01:47Z"},"comment_author":"author","comment_excerpt":"Second response clarifies the fix.","reference_count":1}`},
	}
	m.attention.page = &page
	view := m.attentionView()
	if !strings.Contains(view, "Response after closure") || !strings.Contains(view, "Please reopen") || strings.Contains(view, "comment:5672562917") {
		t.Fatalf("discussion still reads like raw evidence machinery: %s", view)
	}
	m.attention.selected = 1
	nextView := m.attentionView()
	firstAt, secondAt := strings.Index(view, "Second response"), strings.Index(nextView, "Second response")
	if firstAt < 0 || secondAt < 0 || strings.Contains(view, "Details") || strings.Count(view[:firstAt], "\n") != strings.Count(nextView[:secondAt], "\n") {
		t.Fatal("focusing a comment shifted the related cards")
	}
}

func TestNotificationRefreshesPRAndReturnsToComment(t *testing.T) {
	m := attentionFixture(t)
	m.width, m.height, m.ready = 100, 36, true
	reader, cmd := m.readAttention(attentionLocation{section: "history", number: 1, checkpoint: strings.Repeat("b", 64)})
	m = finishAttentionCommand(t, reader.(model), cmd)
	m, _ = corpusKey(m, "j")
	selected := m.attention.selected
	before := m.form.Snapshot()

	script := `#!/usr/bin/env python3
import json,sys
a=sys.argv[1:]
assert a[a.index('--cache-mode')+1]=='refresh',a
assert a[a.index('--number')+1]=='1',a
assert a[a.index('--expected-repo')+1]=='owner/repo',a
assert '--diff' in a,a
print(json.dumps({'number':1,'kind':'pr','title':'A current PR','state':'closed','url':'https://github.com/owner/repo/pull/1','body':'Current description','comment_bodies':['Closure explanation','New contributor response'],'comment_authors':['maintainer','contributor'],'comment_dates':['2026-09-01','2026-09-02'],'evidence':{'mode':'refresh','snapshot_id':'saved','stats':{'requests':2},'components':{'summary':{'status':'complete','object':{}},'comments':{'status':'complete','object':{}},'diff':{'status':'missing'}},'problems':{}}}))
`
	if err := os.WriteFile(filepath.Join(m.installRoot, "bin/enrich-one"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	m, cmd = corpusKey(m, "enter")
	if cmd == nil || !m.notificationPR.open || !m.detail.notificationOnly || m.detail.item.Number != 1 {
		t.Fatal("selected comment did not open a read-only PR item")
	}
	read := cmd().(evidenceReadMsg)
	if read.err != nil {
		t.Fatal(read.err)
	}
	next, _ := m.Update(read)
	m = next.(model)
	if m.detail.item.Title != "A current PR" || m.detail.enriched.CommentBodies[1] != "New contributor response" || !strings.Contains(ansi.Strip(m.viewContent()), "New contributor response") || strings.Contains(m.viewContent(), "offline") {
		t.Fatal("refreshed PR item did not show the current title and discussion")
	}
	for _, k := range []string{"S", "a"} {
		var action tea.Cmd
		m, action = corpusKey(m, k)
		if action != nil {
			t.Fatalf("%s issued work from the read-only PR item", k)
		}
	}
	m, _ = corpusKey(m, "h")
	if m.notificationPR.open || !m.attention.open || m.attention.selected != selected || m.form.Snapshot() != before || m.status != "" {
		t.Fatal("back did not restore the selected notification and draft")
	}
	next, _ = m.Update(read)
	if next.(model).notificationPR.open {
		t.Fatal("late refresh result reopened the PR")
	}
}

func TestNotificationsMoreRowsPageInPlace(t *testing.T) {
	m := notificationsFixture(t)
	script := `#!/usr/bin/env python3
import json,sys
a=sys.argv[1:]
command=a[2]
if command=='notification-state':
    print(json.dumps({'repository':{'full_name':'owner/repo'},'rows':{},'requests':0}))
    sys.exit(0)
offset=int(a[a.index('--offset')+1])
limit=int(a[a.index('--limit')+1])
assert limit==5
if command=='track-list':
    print(json.dumps({'repository':{'full_name':'owner/repo'},'rows':[],'total':0,'offset':0,'next':None,'requests':0}))
    sys.exit(0)
assert offset==0 or a[a.index('--checkpoint')+1]=='a'*64
attention=command=='attention-list'
total=7 if attention else 1
rows=[]
for number in range(offset+1,min(offset+limit,total)+1):
    row=dict(id=str(number),label='PR #'+str(number),preview='{}',omitted_bytes=0,label_omitted_bytes=0,number=number,selectable=True,attention=attention)
    row.update({'watch_checkpoint':'b'*64} if attention else {'history_checkpoint':'c'*64})
    rows.append(row)
page=dict(schema_version=1,policy='attention-reader-v1' if attention else 'action-history-reader-v1',repository=dict(host='github.com',full_name='owner/repo'),section='list',number=0,checkpoint='a'*64,entry='',requests=0,rows=rows,pagination=dict(total=total,offset=offset,returned=len(rows),omitted_before=offset,omitted_after=total-offset-len(rows),next_offset=offset+len(rows) if offset+len(rows)<total else None))
print(json.dumps(page))
`
	if err := os.WriteFile(filepath.Join(m.installRoot, "bin/cache"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	cmd := m.enterSidebarSelection()
	next, _ := m.Update(cmd())
	m = next.(model)
	for i := 0; i < 5; i++ {
		m, _ = corpusKey(m, "j")
	}
	m, cmd = corpusKey(m, "enter")
	if cmd == nil || !m.notifications.open || m.attention.open {
		t.Fatal("more row opened another screen")
	}
	next, _ = m.Update(cmd())
	m = next.(model)
	if m.notifications.attention.Pagination.Offset != 5 || !strings.Contains(m.notificationsView(), "Previous saved items") {
		t.Fatal("next saved page unavailable in Notifications")
	}
	m, cmd = corpusKey(m, "enter")
	next, _ = m.Update(cmd())
	m = next.(model)
	if m.notifications.attention.Pagination.Offset != 0 {
		t.Fatal("previous saved page unavailable")
	}
}
