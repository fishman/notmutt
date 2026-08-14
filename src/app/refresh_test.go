package app

import (
	"sync/atomic"
	"testing"
	"time"

	"notmutt/config"
	"notmutt/core"
	"notmutt/notmuch"
)

type fakeWorker struct {
	uuid      atomic.Value
	rev       atomic.Uint64
	msgs      atomic.Value
	lastQuery atomic.Value
}

func (f *fakeWorker) set(uuid string, rev uint64) {
	f.uuid.Store(uuid)
	f.rev.Store(rev)
}

func (f *fakeWorker) setMsgs(msgs []core.Message) {
	f.msgs.Store(msgs)
}

func (f *fakeWorker) Call(a notmuch.Action) (notmuch.Reply, error) {
	r := notmuch.Reply{ID: a.ID}
	switch a.Kind {
	case notmuch.ActRevision:
		r.UUID, _ = f.uuid.Load().(string)
		r.Rev = f.rev.Load()
	case notmuch.ActQuery:
		f.lastQuery.Store(a.Query)
		r.Msgs, _ = f.msgs.Load().([]core.Message)
	case notmuch.ActThread:
		stubs, _ := f.msgs.Load().([]core.Message)
		if len(stubs) == 0 {
			stubs = []core.Message{{ID: "changed", ThreadID: a.ThreadID}}
		}
		r.Msgs = stubs
	}
	return r, nil
}

type tagCall struct {
	query  string
	tagOps []core.TagOp
}

type fakeTagWorker struct {
	*fakeWorker
	tagErr   atomic.Value // error
	tagCalls atomic.Value // []tagCall
}

func (f *fakeTagWorker) setTagErr(err error) { f.tagErr.Store(err) }

func (f *fakeTagWorker) tagCallsSnapshot() []tagCall {
	v, _ := f.tagCalls.Load().([]tagCall)
	return v
}

func (f *fakeTagWorker) Call(a notmuch.Action) (notmuch.Reply, error) {
	if a.Kind == notmuch.ActTag {
		if err, _ := f.tagErr.Load().(error); err != nil {
			return notmuch.Reply{ID: a.ID}, err
		}
		var calls []tagCall
		if v, ok := f.tagCalls.Load().([]tagCall); ok {
			calls = v
		}
		f.tagCalls.Store(append(calls, tagCall{a.Query, a.TagOps}))
		return notmuch.Reply{ID: a.ID}, nil
	}
	return f.fakeWorker.Call(a)
}

func TestCycleIncremental(t *testing.T) {
	bus := core.NewBus()
	fw := &fakeWorker{}
	fw.set("u", 10)
	fw.setMsgs([]core.Message{{ID: "old", ThreadID: "t0"}})
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t0", []*core.Message{{ID: "old", ThreadID: "t0"}})})
	r := newRefresher(bus, fw, view, 10)
	// first cycle, no uuid: full reload path, t0 re-fetched and kept
	r.cycle()
	if r.rPrev != 10 || r.uuid != "u" {
		t.Fatalf("state wrong after full reload: %v %v", r.uuid, r.rPrev)
	}
	// no rev change: clean no-op
	r.cycle()
	if len(view.Threads) != 1 {
		t.Fatalf("no-op cycle changed the view: %d threads", len(view.Threads))
	}
	// rev bump with a changed message: thread fetched and merged
	fw.setMsgs([]core.Message{{ID: "m2", ThreadID: "t2"}})
	fw.set("u", 11)
	r.cycle()
	if len(view.Threads) != 2 {
		t.Fatalf("expected 2 threads after merge, got %d", len(view.Threads))
	}
	if !hasThread(view.Threads, "t2") {
		t.Fatal("thread t2 missing after merge")
	}
	// rev bump with an empty changed set: nothing fetched, nothing merged
	fw.setMsgs(nil)
	fw.set("u", 12)
	r.cycle()
	if len(view.Threads) != 2 {
		t.Fatalf("empty changed set merged something: %d threads", len(view.Threads))
	}
	if r.rPrev != 12 || r.uuid != "u" {
		t.Fatalf("state not advanced: %v %v", r.uuid, r.rPrev)
	}
}

func hasThread(threads []*core.Thread, id string) bool {
	for _, t := range threads {
		if t.ID == id {
			return true
		}
	}
	return false
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

func TestCycleFullReloadRemoves(t *testing.T) {
	bus := core.NewBus()
	fw := &fakeWorker{}
	fw.set("u1", 5)
	fw.setMsgs([]core.Message{{ID: "old", ThreadID: "t0"}})
	view := core.NewView("inbox", "tag:inbox")
	r := newRefresher(bus, fw, view, 5)
	r.cycle() // uuid flip from "": full reload loads t0
	if len(view.Threads) != 1 {
		t.Fatalf("expected t0 loaded, got %d threads", len(view.Threads))
	}
	fw.setMsgs(nil)
	fw.set("u2", 6)
	ch := bus.Subscribe()
	r.cycle() // uuid flip: full reload, empty result -> view empties
	select {
	case e := <-ch:
		if _, ok := e.(core.ViewDiff); !ok {
			t.Fatalf("expected ViewDiff from emptying reload, got %T", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no ViewDiff after emptying reload")
	}
	if len(view.Threads) != 0 {
		t.Fatalf("expected empty view after full reload, got %d threads", len(view.Threads))
	}
	if len(r.snapshot) != 0 {
		t.Fatalf("snapshot not reset: %d threads", len(r.snapshot))
	}
}

func TestOnConfig(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	fw := &fakeWorker{}
	fw.set("u", 1)
	fw.setMsgs([]core.Message{{ID: "m1", ThreadID: "t1"}})
	view := core.NewView("inbox", "tag:inbox")
	r := newRefresher(bus, fw, view, 0)
	st := config.NewStore(config.Default())
	if err := st.SetViewQuery("inbox", "tag:changed"); err != nil {
		t.Fatal(err)
	}
	r.onConfig(st, core.ConfigChanged{Section: "view"})
	if r.view.Query != "tag:changed" {
		t.Fatalf("view query not taken from the store: %q", r.view.Query)
	}
	if q, _ := fw.lastQuery.Load().(string); q != "tag:changed" {
		t.Fatalf("reload must query with the new query, got %q", q)
	}
	select {
	case e := <-ch:
		if _, ok := e.(core.ViewDiff); !ok {
			t.Fatalf("expected ViewDiff after config reload, got %T", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no ViewDiff after view-section config change")
	}
	r.onConfig(st, core.ConfigChanged{Section: "ui"})
	if r.view.Query != "tag:changed" {
		t.Fatalf("ui section must not change the query: %q", r.view.Query)
	}
	select {
	case e := <-ch:
		if _, ok := e.(core.ViewDiff); !ok {
			t.Fatalf("expected ViewDiff from ui-section reload, got %T", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no ViewDiff after ui-section config change")
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
