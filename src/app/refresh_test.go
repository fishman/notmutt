package app

import (
	"sync/atomic"
	"testing"
	"time"

	"notmutt/core"
	"notmutt/notmuch"
)

type fakeWorker struct {
	uuid atomic.Value
	rev  atomic.Uint64
}

func (f *fakeWorker) set(uuid string, rev uint64) {
	f.uuid.Store(uuid)
	f.rev.Store(rev)
}

func (f *fakeWorker) Call(a notmuch.Action) (notmuch.Reply, error) {
	r := notmuch.Reply{ID: a.ID}
	switch a.Kind {
	case notmuch.ActRevision:
		r.UUID, _ = f.uuid.Load().(string)
		r.Rev = f.rev.Load()
	case notmuch.ActQuery:
		r.Msgs = []core.Message{{ID: "changed", ThreadID: "t1"}}
	case notmuch.ActThread:
		r.Msgs = []core.Message{{ID: "changed", ThreadID: a.ThreadID}}
	}
	return r, nil
}

func TestCycleIncremental(t *testing.T) {
	bus := core.NewBus()
	fw := &fakeWorker{}
	fw.set("u", 10)
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{{ID: "changed", ThreadID: "t1"}})})
	r := newRefresher(bus, fw, view, 10)
	r.cycle()
	if r.rPrev != 10 || r.uuid != "u" {
		t.Fatalf("state wrong: %v %v", r.uuid, r.rPrev)
	}
	// no change: cycle is a no-op
	r.cycle()
	if len(view.Threads) != 1 {
		t.Fatalf("no-op cycle changed the view: %d threads", len(view.Threads))
	}
	// rev bump with no new ids: nothing to fetch
	fw.set("u", 11)
	r.cycle()
	// rev bump with a changed message: merged
	fw.set("u", 12)
	r.cycle()
	if len(view.Threads) != 1 {
		t.Fatalf("expected 1 thread after merge, got %d", len(view.Threads))
	}
}

func TestCycleUUIDFlipFullReload(t *testing.T) {
	bus := core.NewBus()
	fw := &fakeWorker{}
	fw.set("u1", 5)
	view := core.NewView("inbox", "tag:inbox")
	r := newRefresher(bus, fw, view, 5)
	r.cycle() // stores u1
	fw.set("u2", 6)
	ch := bus.Subscribe()
	r.cycle() // uuid mismatch: full reload path
	select {
	case e := <-ch:
		if _, ok := e.(core.ViewDiff); !ok {
			t.Fatalf("expected ViewDiff from full reload, got %T", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no ViewDiff after uuid flip")
	}
	if r.uuid != "u2" || r.rPrev != 6 {
		t.Fatalf("state not advanced: %v %v", r.uuid, r.rPrev)
	}
}

func TestCycleQuiet(t *testing.T) {
	bus := core.NewBus()
	fw := &fakeWorker{}
	fw.set("u", 10)
	view := core.NewView("inbox", "tag:inbox")
	r := newRefresher(bus, fw, view, 10)
	r.cycle() // seed uuid/rPrev (this is the initial full reload)
	ch := bus.Subscribe()
	r.cycle() // no rev change: no events
	select {
	case <-ch:
		t.Fatal("no events expected on a clean cycle")
	case <-time.After(50 * time.Millisecond):
	}
}
