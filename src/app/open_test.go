// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"notmutt/config"
	"notmutt/core"
	"notmutt/tui"
)

// TestOpenThreadHtmlOnlyDefaultsHTML: a thread whose first message has
// no plain part opens in the html view (RenderAuto), colored runs
// included - a hybrid plain view has no reason to win over the real
// html view.
func TestOpenThreadHtmlOnlyDefaultsHTML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "msg")
	msg := "From: sender@example.com\nTo: alpha@example.com\nSubject: html only\n" +
		"Date: Tue, 01 Jan 2019 00:00:00 +0000\nMIME-Version: 1.0\n" +
		"Content-Type: text/html; charset=utf-8\n\n" +
		"<html><body style=\"background:#111111\"><p style=\"color:#eeeeee\">Hello on dark</p></body></html>"
	if err := os.WriteFile(path, []byte(msg), 0o600); err != nil {
		t.Fatal(err)
	}
	fw := &fakeWorker{}
	fw.setMsgs([]core.Message{{ID: "a", ThreadID: "t1", Author: "sender@example.com", Subject: "html only", Paths: []string{path}}})

	tl := runOpen(t, fw, nil, tui.OpenReq{ThreadID: "t1", Mode: core.RenderAuto, Width: 80})
	if tl.RenderMode != core.RenderHTML {
		t.Fatalf("html-only thread must open in the html view, got %v", tl.RenderMode)
	}
	colored := false
	for _, l := range tl.Lines {
		for _, r := range l.Runs {
			if r.Fg != "" || r.Bg != "" {
				colored = true
			}
		}
	}
	if !colored {
		t.Fatal("the html view must carry colored runs")
	}
}

// TestOpenThreadMarksRead (R1): a full open loads the thread AND tags
// the opened message -unread (read is a tag; the refresh cycle
// reconciles it into the view), never the whole thread. ThreadLoaded
// publishes first.
func TestOpenThreadMarksRead(t *testing.T) {
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	fw.setMsgs([]core.Message{{ID: "a", ThreadID: "t1"}})

	tl := runOpen(t, fw, nil, tui.OpenReq{ThreadID: "t1"})
	if tl.ThreadID != "t1" || tl.Preview {
		t.Fatalf("open must publish a non-preview ThreadLoaded: %+v", tl)
	}
	calls := fw.tagCallsSnapshot()
	if len(calls) != 1 || calls[0].query != "id:a" || len(calls[0].tagOps) != 1 || calls[0].tagOps[0].Tag != "unread" || calls[0].tagOps[0].Add {
		t.Fatalf("open must tag the opened message -unread: %+v", calls)
	}

	// a mid-thread open names the opened message, not the thread
	fw.setMsgs([]core.Message{{ID: "a", ThreadID: "t1"}, {ID: "b", ThreadID: "t1"}})
	runOpen(t, fw, nil, tui.OpenReq{ThreadID: "t1", MsgID: "b"})
	calls = fw.tagCallsSnapshot()
	if len(calls) != 2 || calls[1].query != "id:b" {
		t.Fatalf("a mid-thread open must tag its own message only: %+v", calls)
	}
}

// TestOpenThreadMarksReadReflectsInViews: the direct -unread op must
// reflect in every view holding the message (a search tab included),
// or the flag stays stale until the next refresh.
func TestOpenThreadMarksReadReflectsInViews(t *testing.T) {
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	fw.setMsgs([]core.Message{{ID: "a", ThreadID: "t1", Tags: []string{"unread", "inbox"}}})

	main := core.NewView("inbox", "tag:inbox")
	main.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", ThreadID: "t1", Timestamp: 1, Tags: []string{"unread", "inbox"}},
	})})
	search := core.NewView("tag:x", "tag:x")
	search.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", ThreadID: "t1", Timestamp: 1, Tags: []string{"unread", "inbox"}},
	})})
	views := map[string]*core.View{"inbox": main, "tag:x": search}

	runOpen(t, fw, views, tui.OpenReq{ThreadID: "t1", MsgID: "a"})
	if tags := main.Tags("a"); slices.Contains(tags, "unread") {
		t.Fatalf("the mail surface must reflect -unread: %v", tags)
	}
	if tags := search.Tags("a"); slices.Contains(tags, "unread") {
		t.Fatalf("a search tab must reflect -unread: %v", tags)
	}
}

// TestOpenThreadPreviewSkipsReadMarking: the fetch happens, the tag
// never does.
func TestOpenThreadPreviewSkipsReadMarking(t *testing.T) {
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	fw.setMsgs([]core.Message{{ID: "a", ThreadID: "t1"}})

	tl := runOpen(t, fw, nil, tui.OpenReq{ThreadID: "t1", Preview: true})
	if !tl.Preview {
		t.Fatalf("preview must publish a Preview ThreadLoaded: %+v", tl)
	}
	if calls := fw.tagCallsSnapshot(); len(calls) != 0 {
		t.Fatalf("preview must not tag: %+v", calls)
	}
}

// TestOpenViewMode: a sender domain mapped to html opens in html; plain
// and unmapped domains keep the plain default; the lookup is
// case-insensitive (the From string is display text); an unparseable
// From has no domain. An empty message set keeps the plain default -
// the domain is message data, only the fetch has it.
func TestOpenViewMode(t *testing.T) {
	defs := map[string]string{"alpha.example.com": "html", "atlas.example.com": "plain"}
	cases := []struct {
		name string
		from string
		want core.RenderMode
	}{
		{"mapped html", "Alpha <a@alpha.example.com>", core.RenderHTML},
		{"mapped plain", "Atlas <a@atlas.example.com>", core.RenderPlain},
		{"unknown domain", "Sender <sender@example.com>", core.RenderPlain},
		{"case-insensitive", "Alpha <a@ALPHA.EXAMPLE.COM>", core.RenderHTML},
		{"unparseable", "not an address", core.RenderPlain},
		{"bare address", "bare@alpha.example.com", core.RenderHTML},
	}
	for _, c := range cases {
		msgs := []core.Message{{ID: "a", ThreadID: "t1", Author: c.from}}
		if got := openViewMode(defs, msgs); got != c.want {
			t.Errorf("%s: openViewMode = %v, want %v", c.name, got, c.want)
		}
	}
	if got := openViewMode(defs, nil); got != core.RenderPlain {
		t.Fatalf("no messages must keep the plain default, got %v", got)
	}
	if got := openViewMode(nil, []core.Message{{Author: "a@alpha.example.com"}}); got != core.RenderPlain {
		t.Fatalf("no config must keep the plain default, got %v", got)
	}
}

// TestOpenThreadTagFailureKeepsOpen: a failed mark-read (lock timeout)
// must not lose the open - ThreadLoaded still publishes, the JobError
// reports the tag.
func TestOpenThreadTagFailureKeepsOpen(t *testing.T) {
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	fw.setMsgs([]core.Message{{ID: "a", ThreadID: "t1"}})
	fw.setTagErr(errors.New("lock timeout"))

	// two events ride one open (ThreadLoaded then JobError): keep the bus
	// explicit instead of runOpen's single read
	bus := core.NewBus()
	ch := bus.Subscribe()
	openThread(fw, bus, nil, tui.OpenReq{ThreadID: "t1"}, nil, config.Crypto{}, false, "")
	waitThread(t, ch)
	select {
	case e := <-ch:
		je, ok := e.(core.JobError)
		if !ok || je.Job != "open" {
			t.Fatalf("expected JobError{Job: open}, got %T %+v", e, je)
		}
	case <-time.After(time.Second):
		t.Fatal("no JobError")
	}
}

// TestOpenThreadRendersOnlyOpenedMessage: the open carries the cursor
// message's id, the fetch narrows to it, and the reply names it - the
// pager shows one email, never the whole thread (thread-wide text
// stays queryable via the lua layer). A bare open (empty id) falls
// back to the thread's first.
func TestOpenThreadRendersOnlyOpenedMessage(t *testing.T) {
	dir := t.TempDir()
	writeMsg := func(id, subject string) string {
		path := filepath.Join(dir, id)
		msg := "From: sender@example.com\nTo: alpha@example.com\nSubject: " + subject + "\n" +
			"Date: Tue, 01 Jan 2019 00:00:00 +0000\n\nbody of " + subject + "\n"
		if err := os.WriteFile(path, []byte(msg), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	p1, p2 := writeMsg("a", "first message"), writeMsg("b", "second message")
	fw := &fakeWorker{}
	fw.setMsgs([]core.Message{
		{ID: "a", ThreadID: "t1", Paths: []string{p1}},
		{ID: "b", ThreadID: "t1", Paths: []string{p2}},
	})

	tl := runOpen(t, fw, nil, tui.OpenReq{ThreadID: "t1", MsgID: "b", Width: 80})
	if tl.MsgID != "b" {
		t.Fatalf("the reply must name the opened message, got %q", tl.MsgID)
	}
	var text strings.Builder
	for _, l := range tl.Lines {
		text.WriteString(l.Text)
	}
	if got := text.String(); strings.Contains(got, "first") || !strings.Contains(got, "second") {
		t.Fatalf("the pager must render the opened message only:\n%s", got)
	}

	tl = runOpen(t, fw, nil, tui.OpenReq{ThreadID: "t1", Width: 80})
	if tl.MsgID != "a" {
		t.Fatalf("a bare open must fall back to the thread's first, got %q", tl.MsgID)
	}
}

// TestOpenThreadRowsFirst: the full walk owns the worker for seconds,
// so an open must not queue behind it. A thread resident in a
// registered view opens from the view's rows - zero ActThread calls,
// the walk already loaded headers and paths; a thread in no view falls
// back to the worker fetch.
func TestOpenThreadRowsFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "msg")
	raw := "From: sender@example.com\nTo: alpha@example.com\nSubject: rows first\n" +
		"Date: Tue, 01 Jan 2019 00:00:00 +0000\n\nbody\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	msg := core.Message{ID: "a", ThreadID: "t1", Author: "sender@example.com", Subject: "rows first", Paths: []string{path}}
	v := core.NewView("inbox", "tag:inbox")
	v.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{&msg})})
	views := map[string]*core.View{v.ViewName(): v}

	fw := &fakeWorker{}
	fw.setMsgs([]core.Message{msg})
	tl := runOpen(t, fw, views, tui.OpenReq{ThreadID: "t1", MsgID: "a", Preview: true, Width: 80})
	if tl.Err != nil {
		t.Fatalf("rows-first open failed: %v", tl.Err)
	}
	if tl.MsgID != "a" {
		t.Fatalf("open must narrow to the opened message, got %q", tl.MsgID)
	}
	if len(tl.Lines) == 0 {
		t.Fatal("rows-first open rendered no lines")
	}
	if n := fw.threads.Load(); n != 0 {
		t.Fatalf("a view-resident thread must not fetch through the worker, got %d ActThread calls", n)
	}

	// the fallback: the thread is in no view (a closed tab's pager, a
	// view reset race) - the worker fetch serves it
	tl = runOpen(t, fw, map[string]*core.View{}, tui.OpenReq{ThreadID: "t1", MsgID: "a", Preview: true, Width: 80})
	if tl.Err != nil {
		t.Fatalf("fallback open failed: %v", tl.Err)
	}
	if n := fw.threads.Load(); n != 1 {
		t.Fatalf("a thread in no view must fall back to the fetch, got %d ActThread calls", n)
	}
}

// waitThread fails unless the next bus event is the ThreadLoaded a full
// open publishes, returning it.
func waitThread(t *testing.T, ch <-chan core.Event) core.ThreadLoaded {
	t.Helper()
	select {
	case e := <-ch:
		tl, ok := e.(core.ThreadLoaded)
		if !ok {
			t.Fatalf("expected ThreadLoaded, got %T", e)
		}
		return tl
	case <-time.After(time.Second):
		t.Fatal("no ThreadLoaded")
		return core.ThreadLoaded{}
	}
}

// runOpen drives openThread on a fresh subscription under the fixture's
// standard open environment (no domain map, no crypto config, light
// theme) and returns the ThreadLoaded it publishes. req carries only
// what a test varies - a bare {ThreadID} is the plain full open of the
// thread's first message (RenderPlain is the zero mode).
func runOpen(t *testing.T, fw workerAPI, views map[string]*core.View, req tui.OpenReq) core.ThreadLoaded {
	t.Helper()
	bus := core.NewBus()
	ch := bus.Subscribe()
	openThread(fw, bus, views, req, nil, config.Crypto{}, false, "")
	return waitThread(t, ch)
}
