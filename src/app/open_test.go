// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"notmutt/core"
)

// TestOpenThreadHtmlOnlyDefaultsHTML pins the html-only open default:
// a thread whose first message has no plain part opens in the html
// view (RenderAuto), colored runs included - the old default rendered
// the html structure with the colors stripped (the html-background
// with plain-font-colors report), a hybrid plain view had no reason
// to win over the real html view.
func TestOpenThreadHtmlOnlyDefaultsHTML(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
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

	openThread(fw, bus, "t1", "", false, core.RenderAuto, false, 80, false, nil, nil)

	select {
	case e := <-ch:
		tl, ok := e.(core.ThreadLoaded)
		if !ok {
			t.Fatalf("expected ThreadLoaded, got %T", e)
		}
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
	case <-time.After(time.Second):
		t.Fatal("no ThreadLoaded")
	}
}

// TestOpenThreadMarksRead pins the open-reads contract: a full open
// loads the thread AND tags the opened message -unread (R1 - read is a
// tag, the refresh cycle reconciles it into the view), never the whole
// thread - the other messages keep their unread state. ThreadLoaded
// publishes first.
func TestOpenThreadMarksRead(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	fw.setMsgs([]core.Message{{ID: "a", ThreadID: "t1"}})

	openThread(fw, bus, "t1", "", false, core.RenderPlain, false, 0, false, nil, nil)

	select {
	case e := <-ch:
		tl, ok := e.(core.ThreadLoaded)
		if !ok {
			t.Fatalf("expected ThreadLoaded, got %T", e)
		}
		if tl.ThreadID != "t1" || tl.Preview {
			t.Fatalf("open must publish a non-preview ThreadLoaded: %+v", tl)
		}
	case <-time.After(time.Second):
		t.Fatal("no ThreadLoaded")
	}
	calls := fw.tagCallsSnapshot()
	if len(calls) != 1 || calls[0].query != "id:a" || len(calls[0].tagOps) != 1 || calls[0].tagOps[0].Tag != "unread" || calls[0].tagOps[0].Add {
		t.Fatalf("open must tag the opened message -unread: %+v", calls)
	}

	// a mid-thread open names the opened message, not the thread
	fw.setMsgs([]core.Message{{ID: "a", ThreadID: "t1"}, {ID: "b", ThreadID: "t1"}})
	openThread(fw, bus, "t1", "b", false, core.RenderPlain, false, 0, false, nil, nil)
	select {
	case e := <-ch:
		if _, ok := e.(core.ThreadLoaded); !ok {
			t.Fatalf("expected ThreadLoaded, got %T", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no ThreadLoaded")
	}
	calls = fw.tagCallsSnapshot()
	if len(calls) != 2 || calls[1].query != "id:b" {
		t.Fatalf("a mid-thread open must tag its own message only: %+v", calls)
	}
}

// TestOpenThreadPreviewSkipsReadMarking pins the preview half: the
// fetch happens, the tag never does.
func TestOpenThreadPreviewSkipsReadMarking(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	fw.setMsgs([]core.Message{{ID: "a", ThreadID: "t1"}})

	openThread(fw, bus, "t1", "", true, core.RenderPlain, false, 0, false, nil, nil)

	select {
	case e := <-ch:
		tl, ok := e.(core.ThreadLoaded)
		if !ok || !tl.Preview {
			t.Fatalf("preview must publish a Preview ThreadLoaded: %T %+v", e, tl)
		}
	case <-time.After(time.Second):
		t.Fatal("no ThreadLoaded")
	}
	if calls := fw.tagCallsSnapshot(); len(calls) != 0 {
		t.Fatalf("preview must not tag: %+v", calls)
	}
}

// TestOpenViewMode pins the open key's per-domain default view: a
// sender domain mapped to html opens in the html view, plain and
// unmapped domains keep the plain default, the lookup is
// case-insensitive (the From string is display text), and an
// unparseable From has no domain. An empty message set keeps the
// plain default - the domain is message data, only the fetch has it.
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

// TestOpenThreadTagFailureKeepsOpen pins the failure surface: a failed
// mark-read (lock timeout) must not lose the open - ThreadLoaded still
// publishes, the JobError reports the tag.
func TestOpenThreadTagFailureKeepsOpen(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	fw.setMsgs([]core.Message{{ID: "a", ThreadID: "t1"}})
	fw.setTagErr(errors.New("lock timeout"))

	openThread(fw, bus, "t1", "", false, core.RenderPlain, false, 0, false, nil, nil)

	select {
	case e := <-ch:
		if _, ok := e.(core.ThreadLoaded); !ok {
			t.Fatalf("expected ThreadLoaded first, got %T", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no ThreadLoaded")
	}
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

// TestOpenThreadRendersOnlyOpenedMessage pins the message-scoped
// pager: the open carries the cursor message's id, the thread fetch
// narrows to that message, and the reply names it - the pager shows
// one email, never the whole thread (the thread-wide text stays
// queryable via the lua layer, not as the default view). A bare open
// (empty id) falls back to the thread's first.
func TestOpenThreadRendersOnlyOpenedMessage(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
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

	openThread(fw, bus, "t1", "b", false, core.RenderPlain, false, 80, false, nil, nil)
	loaded := func() core.ThreadLoaded {
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
	tl := loaded()
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

	openThread(fw, bus, "t1", "", false, core.RenderPlain, false, 80, false, nil, nil)
	tl = loaded()
	if tl.MsgID != "a" {
		t.Fatalf("a bare open must fall back to the thread's first, got %q", tl.MsgID)
	}
}
