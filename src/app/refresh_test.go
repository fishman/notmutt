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
	lastQuery atomic.Value
	queries   atomic.Int32
	emits     atomic.Int32 // ActQuery emit chunks - the chunk cadence
	threads   atomic.Int32 // ActThread calls - must stay zero in the load path
	countErr  atomic.Value // error: fails every ActCount when set
}

func (f *fakeWorker) set(uuid string, rev uint64) {
	f.uuid.Store(uuid)
	f.rev.Store(rev)
}

func (f *fakeWorker) setMsgs(msgs []core.Message) {
	f.msgs.Store(msgs)
}

// setStubs installs a query result; ActQuery serves it to Emit in
// chunks mirroring the backend cadence (100, then 5000 - the contract
// refresh_test pins), honoring a.Limit for the fast pre-query. The
// setMsgs path serves the changed-set cycle.
func (f *fakeWorker) setStubs(msgs []core.Message) {
	f.stubs.Store(msgs)
}

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
		if a.Emit == nil {
			break
		}
		var msgs []core.Message
		if stubs, ok := f.stubs.Load().([]core.Message); ok && len(stubs) > 0 {
			msgs = stubs
		} else if all, ok := f.msgs.Load().([]core.Message); ok {
			msgs = all
		}
		if a.Limit > 0 && len(msgs) > a.Limit {
			msgs = msgs[:a.Limit]
		}
		for i := 0; i < len(msgs); {
			f.emits.Add(1)
			size := 100
			if i > 0 {
				size = 5000
			}
			hi := min(i+size, len(msgs))
			if !a.Emit(msgs[i:hi]) {
				break
			}
			i = hi
		}
	case notmuch.ActThread:
		f.threads.Add(1)
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
	readProgress(t, ch) // the reload's page publishes progress first
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

// TestFullReloadPages pins the two-phase progressive fill: the fast
// pre-query (limit 100 - the first paint) then the full walk in ONE
// call, emitted as 100 then a 2400 steady chunk (no offset paging -
// each paged call re-walks the notmuch mset). The walk replaces the
// pre-query head (the snapshot restarts empty), so the view ends with
// exactly 2500 threads, no duplicates, still sorted. (ViewDiff events
// are not counted from the channel: 2500 progress events flood the
// 64-slot subscriber buffer and drops make that assertion flaky - the
// per-chunk publish is code-guaranteed by the emit shape.)
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

	if got := fw.queries.Load(); got != 2 {
		t.Fatalf("expected 2 query calls (pre-query + walk), got %d", got)
	}
	if got := fw.emits.Load(); got != 3 {
		t.Fatalf("expected 3 emit chunks (100 pre-query + 100/2400 walk), got %d", got)
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
	// no count query: cumulative done against itself - the bar grows
	// monotonically and never exceeds its total
	if p, ok := bus.LatestProgress("refresh", "inbox"); !ok || p.Done != 2500 || p.Total != 2500 {
		t.Fatalf("count failure must fall back to cumulative totals, got %+v ok=%v", p, ok)
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

	if got := fw.queries.Load(); got != 2 {
		t.Fatalf("expected the pre-query and the walk, got %d", got)
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

// TestLoadPathHasNoThreadFetch pins the content-free contract: the
// fill pages ActQuery (thread summaries - DB-side, zero file opens)
// and the changed-set cycle merges the lastmod summaries directly.
// ActThread (notmuch show - file opens) never runs in the load path;
// content loads only on open (R13).
func TestLoadPathHasNoThreadFetch(t *testing.T) {
	bus := core.NewBus()
	fw := &fakeWorker{}
	fw.set("u", 10)
	fw.setStubs([]core.Message{
		{ThreadID: "t1", Timestamp: 100, Author: "A", Subject: "s1", Tags: []string{"inbox"}},
	})
	view := core.NewView("inbox", "tag:inbox")
	r := newRefresher(bus, fw, view, 0)

	r.fullReload()

	if got := fw.threads.Load(); got != 0 {
		t.Fatalf("full reload must not fetch threads (show opens files), got %d ActThread calls", got)
	}
	if got := fw.queries.Load(); got != 2 {
		t.Fatalf("expected the pre-query and the walk, got %d", got)
	}
	// changed-set cycle: the lastmod summary merges directly, no show
	fw.set("u", 11)
	fw.setStubs(nil)
	fw.setMsgs([]core.Message{{ThreadID: "t2", Timestamp: 200, Author: "B", Subject: "s2"}})
	r.cycle()
	if got := fw.threads.Load(); got != 0 {
		t.Fatalf("the changed-set cycle must merge summaries, got %d ActThread calls", got)
	}
	if !hasThread(view.Threads, "t2") {
		t.Fatal("changed summary must land in the view")
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
