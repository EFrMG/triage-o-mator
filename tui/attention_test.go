package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func attentionFixture(t *testing.T) model {
	t.Helper()
	m := corpusFixture(t)
	m.corpus = corpusUI{}
	m.focus = FocusSidebar
	m.sidebar.selected = notificationsIndex
	script := `#!/usr/bin/env python3
import json, sys
from pathlib import Path
a=sys.argv[1:]
assert a[:2]==['--expected-repo','owner/repo'], a
command=a[2]
assert command in ('attention-list','attention-read','attention-source'), a
Path('attention-call.json').write_text(json.dumps(a))
def arg(name, default=None): return a[a.index(name)+1] if name in a else default
def window(total, offset, n): return dict(total=total,offset=offset,returned=n,omitted_before=offset,omitted_after=total-offset-n,next_offset=offset+n if offset+n<total else None)
section='list' if command=='attention-list' else ('source' if command=='attention-source' else arg('--section'))
number=int(arg('--number','0'))
token='a'*64 if section=='list' else 'b'*64
if section!='list' or int(arg('--offset','0')): assert arg('--checkpoint')==token, a
out=dict(schema_version=1,policy='attention-reader-v1',repository=dict(host='github.com',full_name='owner/repo'),section=section,number=number,checkpoint=token,requests=0,entry=arg('--entry',''))
if section=='source':
    assert int(arg('--max-bytes'))==4096
    text='é' * 3000
    offset=int(arg('--byte-offset')); raw=text.encode(); part=raw[offset:offset+4096].decode()
    out.update(source_section=arg('--section'),reference=int(arg('--reference')),text=part,bytes=window(len(raw),offset,len(part.encode())))
else:
    offset=int(arg('--offset')); limit=int(arg('--limit')); assert limit==5
    total=7 if section in ('list','history') else 6
    n=min(limit,total-offset)
    rows=[]
    for i in range(offset,offset+n):
        row=dict(id=str(i+1),label='PR / closure / source '+str(i+1),preview='Original dissent; last successful check; unknown provenance; local hold',omitted_bytes=200 if i==0 else 0,label_omitted_bytes=0)
        if section=='list': row.update(number=i+1,selectable=i!=1,watch_checkpoint='b'*64)
        if section=='references': row.update(reference=i)
        rows.append(row)
    out.update(rows=rows,pagination=window(total,offset,n))
print(json.dumps(out))
`
	if err := os.WriteFile(filepath.Join(m.installRoot, "bin/cache"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	return m
}

func openAttention(t *testing.T, m model) model {
	t.Helper()
	cmd := openAttentionReader(&m)
	if cmd == nil || !m.attention.open {
		t.Fatal("sidebar did not open attention")
	}
	return finishAttentionCommand(t, m, cmd)
}

func openAttentionReader(m *model) tea.Cmd {
	next, cmd := m.readAttention(attentionLocation{section: "list"})
	*m = next.(model)
	return cmd
}

func finishAttentionCommand(t *testing.T, m model, cmd tea.Cmd) model {
	t.Helper()
	if cmd == nil {
		t.Fatal("missing reader command")
	}
	msg := cmd().(attentionMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	next, _ := m.Update(msg)
	return next.(model)
}

func attentionKey(t *testing.T, m model, k string) model {
	t.Helper()
	m, cmd := corpusKey(m, k)
	return finishAttentionCommand(t, m, cmd)
}

func TestAttentionDiscussionPagesPreserveDraft(t *testing.T) {
	m := attentionFixture(t)
	before := m.form.Snapshot()
	next, cmd := m.readAttention(attentionLocation{section: "history", number: 1, checkpoint: strings.Repeat("b", 64)})
	m = finishAttentionCommand(t, next.(model), cmd)
	for i := 0; i < 5; i++ {
		m, _ = corpusKey(m, "j")
	}
	m = attentionKey(t, m, "enter")
	if m.attention.page.Section != "history" || m.attention.page.Pagination.Offset != 5 || m.form.Snapshot() != before {
		t.Fatal("paged discussion changed draft or opened another level")
	}
	if !strings.Contains(m.attentionView(), "Previous comments") {
		t.Fatal("previous page is not reachable")
	}
}

func TestAttentionUnavailableRowAndNoGlobalWrites(t *testing.T) {
	m := openAttention(t, attentionFixture(t))
	m, _ = corpusKey(m, "tab")
	if m.attention.selected != 1 {
		t.Fatal("selection")
	}
	for _, k := range []string{"enter", "S"} {
		var cmd tea.Cmd
		m, cmd = corpusKey(m, k)
		if cmd != nil {
			t.Fatalf("%s issued unexpected work", k)
		}
	}
	m, _ = corpusKey(m, "?")
	if !m.showHelp {
		t.Fatal("help unavailable")
	}
}

func TestAttentionCancellationAndLateRepositoryReplies(t *testing.T) {
	m := attentionFixture(t)
	cmd := openAttentionReader(&m)
	process := m.attentionLifecycle.current
	msg := cmd().(attentionMsg)
	m, _ = corpusKey(m, "x")
	if !process.cancelled || m.attention.open {
		t.Fatal("cancel did not stop reader")
	}
	next, _ := m.Update(msg)
	if next.(model).attention.page != nil {
		t.Fatal("late completion shown")
	}
	cmd = openAttentionReader(&m)
	msg = cmd().(attentionMsg)
	for _, alter := range []func(*attentionMsg){func(x *attentionMsg) { x.root += "/other" }, func(x *attentionMsg) { x.repo = "other/repo" }, func(x *attentionMsg) { x.generation-- }} {
		stale := msg
		alter(&stale)
		next, _ = m.Update(stale)
		if next.(model).attention.page != nil {
			t.Fatal("foreign completion shown")
		}
	}
	m.switchRepo("other/repo")
	if m.attention.open || !m.attentionLifecycle.current.cancelled {
		t.Fatal("switch retained reader")
	}
	next, _ = m.Update(msg)
	if next.(model).attention.page != nil {
		t.Fatal("switched reply shown")
	}
}

func TestAttentionCancelBeforeDispatchAndForceQuit(t *testing.T) {
	m := attentionFixture(t)
	cmd := openAttentionReader(&m)
	m, _ = corpusKey(m, "esc")
	if msg := cmd().(attentionMsg); !errors.Is(msg.err, context.Canceled) {
		t.Fatal(msg.err)
	}
	cmd = openAttentionReader(&m)
	m, _ = corpusKey(m, "ctrl+c")
	if msg := cmd().(attentionMsg); !errors.Is(msg.err, context.Canceled) {
		t.Fatal(msg.err)
	}
}

func TestAttentionRejectsMalformedOrChangedReplies(t *testing.T) {
	m := attentionFixture(t)
	cmd := openAttentionReader(&m)
	msg := cmd().(attentionMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	for _, alter := range []func(*attentionPage){func(p *attentionPage) { p.Schema++ }, func(p *attentionPage) { p.Requests = 1 }, func(p *attentionPage) { p.Repository.Name = "foreign/repo" }, func(p *attentionPage) { p.Repository.Host = "other.host" }, func(p *attentionPage) { p.Pagination.Next = nil }, func(p *attentionPage) { p.Pagination.After++ }, func(p *attentionPage) { p.Number = 1 }, func(p *attentionPage) { p.Rows = nil }, func(p *attentionPage) { p.Checkpoint = "" }} {
		p := msg.page
		alter(&p)
		if validateAttentionPage(p, m.attention.location, m.repo) == nil {
			t.Fatal("invalid response accepted")
		}
	}
	m = finishAttentionCommand(t, m, cmd)
}

func TestAttentionErrorsClearOldPageAndNarrowViewsSanitizeText(t *testing.T) {
	m := openAttention(t, attentionFixture(t))
	m.attention.page.Rows[0].Preview = "\x1b[31mOriginal dissent\x1b[0m\x00 " + strings.Repeat("x", 300)
	for _, width := range []int{30, 60, 100} {
		m.width, m.height = width, 30
		view := m.attentionView()
		if strings.Contains(view, "\x00") || strings.Contains(view, "\x1b[31m") {
			t.Fatal("untrusted controls shown")
		}
		for _, line := range strings.Split(view, "\n") {
			if ansi.StringWidth(line) > width {
				t.Fatal("narrow overflow", line)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(m.installRoot, "bin/cache"), []byte("#!/bin/sh\necho 'changed watch' >&2\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	m, cmd := corpusKey(m, "h")
	if cmd != nil || m.attention.open {
		t.Fatal("back did not close the reader")
	}
	cmd = openAttentionReader(&m)
	next, _ := m.Update(cmd())
	m = next.(model)
	if m.attention.page != nil || m.attention.problem == "" || m.attention.busy {
		t.Fatal("failure retained stale page")
	}
}
