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

	openThread(fw, bus, "t1", false, core.RenderPlain, false, 0, false)

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

	openThread(fw, bus, "t1", true, core.RenderPlain, false, 0, false)

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

// TestOpenThreadTagFailureKeepsOpen pins the failure surface: a failed
// mark-read (lock timeout) must not lose the open - ThreadLoaded still
// publishes, the JobError reports the tag.
func TestOpenThreadTagFailureKeepsOpen(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	fw.setMsgs([]core.Message{{ID: "a", ThreadID: "t1"}})
	fw.setTagErr(errors.New("lock timeout"))

	openThread(fw, bus, "t1", false, core.RenderPlain, false, 0, false)

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
