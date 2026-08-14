package app

import (
	"errors"
	"strconv"
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
	stubs     atomic.Value
	full      atomic.Value // map[string][]core.Message: per-thread full data for ActThread
	lastQuery atomic.Value
	queries   atomic.Int32
	threadErr atomic.Value // error: fails every ActThread when set
	countErr  atomic.Value // error: fails every ActCount when set
}

func (f *fakeWorker) set(uuid string, rev uint64) {
	f.uuid.Store(uuid)
	f.rev.Store(rev)
}

func (f *fakeWorker) setMsgs(msgs []core.Message) {
	f.msgs.Store(msgs)
}

// setStubs installs a paged query result; ActQuery serves it sliced by
// Limit/Offset (the legacy setMsgs path serves the whole set at offset 0
// and nothing after - the fill loop needs both to terminate).
func (f *fakeWorker) setStubs(msgs []core.Message) {
	f.stubs.Store(msgs)
}

// setThreadFull installs the step-two full data for one thread; ActThread
// serves it before falling back to the stubs/msgs paths.
func (f *fakeWorker) setThreadFull(id string, msgs []core.Message) {
	full, _ := f.full.Load().(map[string][]core.Message)
	if full == nil {
		full = map[string][]core.Message{}
	}
	full[id] = msgs
	f.full.Store(full)
}

func (f *fakeWorker) setThreadErr(err error) { f.threadErr.Store(err) }

func (f *fakeWorker) setCountErr(err error) { f.countErr.Store(err) }

func (f *fakeWorker) Call(a notmuch.Action) (notmuch.Reply, error) {
	r := notmuch.Reply{ID: a.ID}
	switch a.Kind {
	case notmuch.ActRevision:
		r.UUID, _ = f.uuid.Load().(string)
		r.Rev = f.rev.Load()
	case notmuch.ActCount:
		if err, _ := f.countErr.Load().(error); err != nil {
			return notmuch.Reply{ID: a.ID}, err
		}
		if stubs, ok := f.stubs.Load().([]core.Message); ok && len(stubs) > 0 {
			r.Count = len(stubs)
		} else if msgs, ok := f.msgs.Load().([]core.Message); ok {
			r.Count = len(msgs)
		}
	case notmuch.ActQuery:
		f.lastQuery.Store(a.Query)
		f.queries.Add(1)
		if stubs, ok := f.stubs.Load().([]core.Message); ok && len(stubs) > 0 {
			lo := a.Offset
			if lo > len(stubs) {
				lo = len(stubs)
			}
			hi := lo + a.Limit
			if hi > len(stubs) {
				hi = len(stubs)
			}
			r.Msgs = stubs[lo:hi]
			break
		}
		if a.Offset == 0 {
			r.Msgs, _ = f.msgs.Load().([]core.Message)
		}
	case notmuch.ActThread:
		if err, _ := f.threadErr.Load().(error); err != nil {
			return notmuch.Reply{ID: a.ID}, err
		}
		if full, ok := f.full.Load().(map[string][]core.Message); ok {
			if msgs, ok := full[a.ThreadID]; ok {
				r.Msgs = msgs
				return r, nil
			}
		}
		if stubs, ok := f.stubs.Load().([]core.Message); ok && len(stubs) > 0 {
			for _, m := range stubs {
				if m.ThreadID == a.ThreadID {
					r.Msgs = []core.Message{m}
					return r, nil
				}
			}
		}
		msgs, _ := f.msgs.Load().([]core.Message)
		if len(msgs) == 0 {
			msgs = []core.Message{{ID: "changed", ThreadID: a.ThreadID}}
		}
		r.Msgs = msgs
	}
	return r, nil
}

type tagCall struct {
	query  string
	tagOps []core.TagOp
}

type fakeTagWorker struct {
	*fakeWorker
	tagErr    atomic.Value // error
	replyErr  atomic.Value // error
	failQuery atomic.Value // string: only this query fails; "" fails all
	tagCalls  atomic.Value // []tagCall
}

func (f *fakeTagWorker) setTagErr(err error)   { f.tagErr.Store(err) }
func (f *fakeTagWorker) setReplyErr(err error) { f.replyErr.Store(err) }
func (f *fakeTagWorker) setFailQuery(q string) { f.failQuery.Store(q) }

func (f *fakeTagWorker) tagCallsSnapshot() []tagCall {
	v, _ := f.tagCalls.Load().([]tagCall)
	return v
}

func (f *fakeTagWorker) Call(a notmuch.Action) (notmuch.Reply, error) {
	if a.Kind == notmuch.ActTag {
		if q, _ := f.failQuery.Load().(string); q == "" || a.Query == q {
			if err, _ := f.tagErr.Load().(error); err != nil {
				return notmuch.Reply{ID: a.ID}, err
			}
			if err, _ := f.replyErr.Load().(error); err != nil {
				return notmuch.Reply{ID: a.ID, Err: err}, nil
			}
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
	readProgress(t, ch) // the reload's thread fetches report progress first
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
	readProgress(t, ch)
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

func TestFetchThreadsPublishesProgress(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	fw := &fakeWorker{}
	fw.setMsgs([]core.Message{{ID: "m1", ThreadID: "t1"}, {ID: "m2", ThreadID: "t2"}})
	view := core.NewView("inbox", "tag:inbox")
	r := newRefresher(bus, fw, view, 0)
	threads := r.fetchThreads([]core.Message{{ID: "m1", ThreadID: "t1"}, {ID: "m2", ThreadID: "t2"}}, 0, 0, true)
	if len(threads) != 2 {
		t.Fatalf("expected 2 threads, got %d", len(threads))
	}
	seen := map[int]bool{}
	for i := 0; i < 2; i++ {
		select {
		case e := <-ch:
			p, ok := e.(core.Progress)
			if !ok {
				t.Fatalf("expected Progress, got %T", e)
			}
			if p.Job != "refresh" || p.Total != 2 || p.Done < 1 || p.Done > 2 {
				t.Fatalf("bad progress: %+v", p)
			}
			seen[p.Done] = true
		case <-time.After(time.Second):
			t.Fatal("missing progress event")
		}
	}
	if !seen[1] || !seen[2] {
		t.Fatalf("progress must cover both fetches: %v", seen)
	}
}

func TestFetchThreadsFailurePublishesProgress(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	fw := &fakeWorker{}
	fw.setMsgs([]core.Message{{ID: "m1", ThreadID: "t1"}})
	fw.setThreadErr(errors.New("boom"))
	view := core.NewView("inbox", "tag:inbox")
	r := newRefresher(bus, fw, view, 0)
	threads := r.fetchThreads([]core.Message{{ID: "m1", ThreadID: "t1"}}, 0, 0, true)
	if len(threads) != 0 {
		t.Fatalf("failed fetch must drop the thread, got %d", len(threads))
	}
	select {
	case e := <-ch:
		p, ok := e.(core.Progress)
		if !ok {
			t.Fatalf("expected Progress, got %T", e)
		}
		if p.Job != "refresh" || p.Done != 1 || p.Total != 1 {
			t.Fatalf("failed fetch must still complete progress: %+v", p)
		}
	case <-time.After(time.Second):
		t.Fatal("missing progress event on failed fetch")
	}
}

// TestFullReloadPages pins the progressive fill: 2500 stubs served in
// four pages (200/1000/1000/300) - the first page is the fast 200 so
// the first paint lands immediately, then the steady page of 1000 takes
// over. One ActQuery per page, and the view ends with the complete
// merged set, still sorted. (ViewDiff events are not counted from the
// channel: 2500 progress events flood the 64-slot subscriber buffer and
// drops make that assertion flaky - the per-page publish is
// code-guaranteed by the loop shape.)
func TestFullReloadPages(t *testing.T) {
	bus := core.NewBus()
	fw := &fakeWorker{}
	stubs := make([]core.Message, 2500)
	for i := range stubs {
		stubs[i] = core.Message{ID: "m" + strconv.Itoa(i), ThreadID: "t" + strconv.Itoa(i)}
	}
	fw.setStubs(stubs)
	view := core.NewView("inbox", "tag:inbox")
	r := newRefresher(bus, fw, view, 0)

	r.fullReload()

	if got := fw.queries.Load(); got != 4 {
		t.Fatalf("expected 4 query page fetches for 2500 stubs at 200/1000/1000/300, got %d", got)
	}
	if len(view.Threads) != 2500 {
		t.Fatalf("view must hold all 2500 threads after the fill, got %d", len(view.Threads))
	}
	if len(r.snapshot) != 2500 {
		t.Fatalf("snapshot must hold all 2500 threads, got %d", len(r.snapshot))
	}
	// every page merged before the next fetch: snapshot stays sorted
	for i := 1; i < len(r.snapshot); i++ {
		if core.ThreadLess(r.snapshot[i], r.snapshot[i-1]) {
			t.Fatalf("snapshot out of order at %d", i)
		}
	}
	// the count query fixes the bar's total: Done accumulates against
	// 2500, never a per-page reset
	if p, ok := bus.LatestProgress("refresh", "inbox"); !ok || p.Done != 2500 || p.Total != 2500 {
		t.Fatalf("bar must reflect the query total, got %+v ok=%v", p, ok)
	}
}

// TestFullReloadCountFailure pins the fallback: when the count query
// fails, progress degrades to per-batch totals instead of a wrong one.
func TestFullReloadCountFailure(t *testing.T) {
	bus := core.NewBus()
	fw := &fakeWorker{}
	stubs := make([]core.Message, 2500)
	for i := range stubs {
		stubs[i] = core.Message{ID: "m" + strconv.Itoa(i), ThreadID: "t" + strconv.Itoa(i)}
	}
	fw.setStubs(stubs)
	fw.setCountErr(errors.New("count failed"))
	view := core.NewView("inbox", "tag:inbox")
	r := newRefresher(bus, fw, view, 0)

	r.fullReload()

	if len(view.Threads) != 2500 {
		t.Fatalf("fill must still complete when the count fails, got %d threads", len(view.Threads))
	}
	// last batch: 300 threads, per-batch total, base reset so the bar
	// never exceeds its total
	if p, ok := bus.LatestProgress("refresh", "inbox"); !ok || p.Done != 300 || p.Total != 300 {
		t.Fatalf("count failure must fall back to batch totals, got %+v ok=%v", p, ok)
	}
}

// TestFullReloadStubThreads pins the step-one fill: search summaries
// (empty message ids - the index read, zero file opens) group into one
// stub thread per thread id. No per-thread fallback exists in the
// fill: ActQuery pages are the whole ingestion.
func TestFullReloadStubThreads(t *testing.T) {
	bus := core.NewBus()
	fw := &fakeWorker{}
	fw.setStubs([]core.Message{
		{ThreadID: "t1", Timestamp: 100, Author: "A", Subject: "s1", Tags: []string{"inbox"}},
		{ThreadID: "t2", Timestamp: 200, Author: "B", Subject: "s2"},
	})
	view := core.NewView("inbox", "tag:inbox")
	r := newRefresher(bus, fw, view, 0)

	r.fullReload()

	if got := fw.queries.Load(); got != 1 {
		t.Fatalf("one query page must cover the whole fill, got %d", got)
	}
	if len(view.Threads) != 2 {
		t.Fatalf("expected 2 threads, got %d", len(view.Threads))
	}
	t1 := findThread(view.Threads, "t1")
	if t1 == nil || t1.Count() != 1 {
		t.Fatalf("t1 must be a single stub row: %+v", t1)
	}
	if t1.Root == nil || t1.Root.Msg == nil || t1.Root.Msg.ID != "" || t1.Root.Msg.Subject != "s1" {
		t.Fatalf("stub must carry the search summary with an empty id: %+v", t1.Root)
	}
}

// TestFullReloadViewportHydrates pins step two: after the fill, the
// visible window's stub rows are replaced by their full threads
// (per-thread fetch - the only file opens in the load path), while
// out-of-window stubs stay summaries. The fill itself never queries
// per-thread.
func TestFullReloadViewportHydrates(t *testing.T) {
	bus := core.NewBus()
	fw := &fakeWorker{}
	fw.setStubs([]core.Message{
		{ThreadID: "t1", Timestamp: 100, Author: "A", Subject: "s1"},
		{ThreadID: "t2", Timestamp: 200, Author: "B", Subject: "s2"},
	})
	fw.setThreadFull("t1", []core.Message{
		{ID: "m1", ThreadID: "t1", Timestamp: 100, Author: "A", Subject: "s1", Paths: []string{"/m/Mail/x/1"}},
		{ID: "m2", ThreadID: "t1", Timestamp: 90, Author: "A", Subject: "s1", References: []string{"m1"}},
	})
	view := core.NewView("inbox", "tag:inbox")
	r := newRefresher(bus, fw, view, 0)

	r.fullReload()

	if got := fw.queries.Load(); got != 1 {
		t.Fatalf("the hydrate must use per-thread fetches, not query pages: got %d ActQuery calls", got)
	}
	t1 := findThread(view.Threads, "t1")
	if t1 == nil || t1.Root == nil || t1.Root.Msg == nil || t1.Root.Msg.ID != "m1" || t1.Count() != 2 {
		t.Fatalf("viewport hydrate must replace the stub with the full thread: %+v", t1)
	}
	t2 := findThread(view.Threads, "t2")
	if t2 == nil || t2.Root == nil || t2.Root.Msg == nil || t2.Root.Msg.ID != "" {
		t.Fatalf("unhydrated stub must stay a summary: %+v", t2)
	}
	if len(view.Threads) != 2 {
		t.Fatalf("expected 2 threads, got %d", len(view.Threads))
	}
}

func findThread(threads []*core.Thread, id string) *core.Thread {
	for _, t := range threads {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// TestFullReloadEmpty pins the loop termination: an empty result ends
// the fill after one empty merge.
func TestFullReloadEmpty(t *testing.T) {
	bus := core.NewBus()
	fw := &fakeWorker{}
	fw.setStubs(nil)
	view := core.NewView("inbox", "tag:inbox")
	r := newRefresher(bus, fw, view, 0)
	ch := bus.Subscribe()
	r.fullReload()
	select {
	case e := <-ch:
		if _, ok := e.(core.ViewDiff); !ok {
			t.Fatalf("expected one ViewDiff from the empty reload, got %T", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no ViewDiff from empty reload")
	}
	if len(view.Threads) != 0 {
		t.Fatalf("empty reload left %d threads", len(view.Threads))
	}
}
