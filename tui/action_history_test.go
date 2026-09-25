package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func actionHistoryFixture(t *testing.T) model {
	t.Helper()
	m := corpusFixture(t)
	script := `#!/usr/bin/env python3
import json,sys
a=sys.argv[1:]
assert a[:2]==['--expected-repo','owner/repo']
def arg(name,default=None): return a[a.index(name)+1] if name in a else default
cmd=a[2]
section='list' if cmd=='action-list' else ('source' if cmd=='action-source' else arg('--section'))
number=int(arg('--number','0'))
token='a'*64 if section=='list' else 'b'*64
def window(total,offset,count): return dict(total=total,offset=offset,returned=count,omitted_before=offset,omitted_after=total-offset-count,next_offset=offset+count if offset+count<total else None)
out=dict(schema_version=1,policy='action-history-reader-v1',repository=dict(host='github.com',full_name='owner/repo'),section=section,number=number,checkpoint=token,entry=arg('--entry',''),requests=0)
if section=='source':
    offset=int(arg('--byte-offset'))
    text='source content' if offset==0 else ''
    out.update(reference=int(arg('--reference')),rows=[],text=text,bytes=window(len(text.encode()),offset,len(text.encode())))
else:
    offset=int(arg('--offset')); limit=int(arg('--limit'))
    total=2 if section=='list' else (1 if section=='context' else 7)
    n=min(limit,total-offset)
    rows=[]
    for i in range(offset,offset+n):
        row=dict(id=str(i+1),label='record '+str(i+1),preview='attributed claim',omitted_bytes=0,label_omitted_bytes=0)
        if section=='list': row.update(number=i+1,selectable=i==0,history_checkpoint='b'*64)
        if section=='context': row.update(id='watch',watch=dict(status='linked',checksum='c'*64))
        if section=='sources': row.update(reference=i)
        rows.append(row)
    out.update(rows=rows,pagination=window(total,offset,n))
print(json.dumps(out))
`
	if err := os.WriteFile(filepath.Join(m.installRoot, "bin/cache"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return m
}

func finishActionCommand(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	msg := cmd().(actionHistoryMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	next, _ := m.finishActionHistory(msg)
	return next.(model)
}

func TestActionHistoryNavigationAndStaleReplies(t *testing.T) {
	m := actionHistoryFixture(t)
	before := m.form.Snapshot()
	next, cmd := m.readActionHistory(actionHistoryLocation{section: "entries", number: 1, checkpoint: strings.Repeat("b", 64)})
	m = finishActionCommand(t, next.(model), cmd)
	for i := 0; i < 5; i++ {
		m, _ = corpusKey(m, "j")
	}
	next, cmd = m.handleActionHistoryKey(tea.KeyMsg{Type: tea.KeyEnter})
	m = finishActionCommand(t, next.(model), cmd)
	if m.actionHistory.page.Section != "entries" || m.actionHistory.page.Pagination.Offset != 5 || m.form.Snapshot() != before {
		t.Fatal("paged explanations changed the draft or opened another level")
	}
	stale := actionHistoryMsg{root: m.installRoot, repo: m.repo, generation: m.actionHistoryGeneration - 1, page: *m.actionHistory.page}
	next, _ = m.finishActionHistory(stale)
	m = next.(model)
	if m.actionHistory.page.Pagination.Offset != 5 {
		t.Fatal("late response accepted")
	}
	if !strings.Contains(m.actionHistoryView(), "Previous explanations") {
		t.Fatal("previous page is not reachable")
	}
}

func TestActionHistoryRejectsForeignAndOversizeResponses(t *testing.T) {
	m := actionHistoryFixture(t)
	next, cmd := m.readActionHistory(actionHistoryLocation{section: "list"})
	m = next.(model)
	msg := cmd().(actionHistoryMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	msg.page.Rows[0].Preview = strings.Repeat("x", 1201)
	if validateActionHistoryPage(msg.page, m.actionHistory.location, m.repo) == nil {
		t.Fatal("oversized preview accepted")
	}
	msg.repo = "foreign/repo"
	next, _ = m.finishActionHistory(msg)
	m = next.(model)
	if m.actionHistory.page != nil {
		t.Fatal("foreign response accepted")
	}
}

func TestActionHistoryCancelSwitchAndLateReplies(t *testing.T) {
	m := actionHistoryFixture(t)
	next, cmd := m.readActionHistory(actionHistoryLocation{section: "list"})
	m = next.(model)
	oldGeneration := m.actionHistoryGeneration
	next, _ = m.handleActionHistoryKey(tea.KeyMsg{Type: tea.KeyEsc})
	m = next.(model)
	reply := cmd().(actionHistoryMsg)
	if reply.err == nil || m.actionHistory.open || m.actionHistoryGeneration == oldGeneration {
		t.Fatal("cancel did not stop pending read or invalidate replies")
	}

	next, _ = m.finishActionHistory(reply)
	m = next.(model)
	if m.actionHistory.open || m.actionHistory.page != nil {
		t.Fatal("late cancelled reply reopened history")
	}

	next, cmd = m.readActionHistory(actionHistoryLocation{section: "list"})
	m = next.(model)
	oldGeneration = m.actionHistoryGeneration
	_ = m.switchRepo("other/repo")
	reply = cmd().(actionHistoryMsg)
	if reply.err == nil || m.actionHistory.open || m.actionHistoryGeneration == oldGeneration {
		t.Fatal("repository switch did not cancel history")
	}

	next, _ = m.finishActionHistory(reply)
	m = next.(model)
	if m.actionHistory.page != nil {
		t.Fatal("foreign reply survived repository switch")
	}
}
