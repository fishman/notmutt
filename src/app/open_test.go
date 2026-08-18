// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"testing"
	"time"

	"notmutt/core"
)

// TestOpenThreadMarksRead pins the open-reads contract: a full open
// loads the thread AND tags it -unread (R1 - read is a tag, the
// refresh cycle reconciles it into the view), ThreadLoaded first.
func TestOpenThreadMarksRead(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	fw.setMsgs([]core.Message{{ID: "a", ThreadID: "t1"}})

	openThread(fw, bus, "t1", false, core.RenderPlain, false, 0, false, nil)

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
	if len(calls) != 1 || calls[0].query != "thread:t1" || len(calls[0].tagOps) != 1 || calls[0].tagOps[0].Tag != "unread" || calls[0].tagOps[0].Add {
		t.Fatalf("open must tag the thread -unread: %+v", calls)
	}
}

// TestOpenThreadPreviewSkipsReadMarking pins the preview half: the
// fetch happens, the tag never does.
func TestOpenThreadPreviewSkipsReadMarking(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	fw.setMsgs([]core.Message{{ID: "a", ThreadID: "t1"}})

	openThread(fw, bus, "t1", true, core.RenderPlain, false, 0, false, nil)

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

	openThread(fw, bus, "t1", false, core.RenderPlain, false, 0, false, nil)

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
