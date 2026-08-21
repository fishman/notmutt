// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"slices"
	"strconv"
	"strings"
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
	countQ    atomic.Value // ActCount query - the scope-intersection pin
	queries   atomic.Int32
	emits     atomic.Int32 // ActQuery emit chunks - the chunk cadence
	threads   atomic.Int32 // ActThread calls - must stay zero in the load path
	countErr  atomic.Value // error: fails every ActCount when set
	pruneErr  atomic.Value // error: fails the prune intersect queries when set
	threadMap atomic.Value // ActThread content per thread id (the hydrated-thread re-fetch)
}

// setThreadMsgs installs the full thread content for ActThread fetches,
// keyed by thread id: the changed set may carry only summary stubs
// while the thread fetch returns the real messages.
func (f *fakeWorker) setThreadMsgs(byID map[string][]core.Message) {
	f.threadMap.Store(byID)
}

func (f *fakeWorker) setPruneErr(err error) { f.pruneErr.Store(&err) }

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
		f.countQ.Store(a.Query)
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
		if strings.Contains(a.Query, " and (thread:") {
			if v, ok := f.pruneErr.Load().(*error); ok && *v != nil {
				return notmuch.Reply{ID: a.ID}, *v
			}
		}
		if a.Emit == nil {
			break
		}
		var msgs []core.Message
		if stubs, ok := f.stubs.Load().([]core.Message); ok && len(stubs) > 0 {
			msgs = stubs
		} else if all, ok := f.msgs.Load().([]core.Message); ok {
			msgs = all
		}
		// membership queries carry the view query plus an identity
		// term: the apply-path eviction check (" and id:...", " and
		// thread:...") and the refresh prune's OR form (" and
		// (thread:... or ...)"). They match on the tag: terms,
		// mirroring notmuch for the tag-only subset. Plain refresh
		// queries pass through unfiltered.
		if strings.Contains(a.Query, " and id:") || strings.Contains(a.Query, " and (id:") || strings.Contains(a.Query, " and thread:") || strings.Contains(a.Query, " and (thread:") {
			msgs = matchTagQuery(msgs, a.Query)
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
		if byID, ok := f.threadMap.Load().(map[string][]core.Message); ok {
			if m, ok := byID[a.ThreadID]; ok {
				r.Msgs = m
				break
			}
		}
		msgs, _ := f.msgs.Load().([]core.Message)
		if len(msgs) == 0 {
			msgs = []core.Message{{ID: "changed", ThreadID: a.ThreadID}}
		}
		r.Msgs = msgs
	case notmuch.ActSnapshots:
		msgs, _ := f.msgs.Load().([]core.Message)
		r.Msgs = msgs
	}
	return r, nil
}

// matchTagQuery answers a membership query: the message must carry
// every tag:X term AND match an id:.../thread:... identity term. The
// refresh prune's OR form collects every thread: term (parens stripped
// from tokens).
func matchTagQuery(msgs []core.Message, q string) []core.Message {
	var terms []string
	want := map[string]bool{}
	for _, tok := range strings.Fields(q) {
		tok = strings.Trim(tok, "()")
		switch {
		case strings.HasPrefix(tok, "tag:"):
			terms = append(terms, strings.TrimPrefix(tok, "tag:"))
		case strings.HasPrefix(tok, "id:"):
			want[strings.Trim(tok[3:], "\"")] = true
		case strings.HasPrefix(tok, "thread:"):
			want["t:"+strings.TrimPrefix(tok, "thread:")] = true
		}
	}
	out := make([]core.Message, 0, len(msgs))
	for _, m := range msgs {
		if !hasAll(m.Tags, terms) {
			continue
		}
		if len(want) == 0 || want[m.ID] || want["t:"+m.ThreadID] {
			out = append(out, m)
		}
	}
	return out
}

func hasAll(tags, want []string) bool {
	for _, w := range want {
		if !slices.Contains(tags, w) {
			return false
		}
	}
	return true
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
	fw.setMsgs([]core.Message{{ID: "m2", ThreadID: "t2", Tags: []string{"inbox"}}})
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

// TestCycleRefetchesHydratedThread pins the R3 diff-and-insert: a
// hydrated thread's changed set carries only summary stubs (the
// refresh feed shape), so the cycle re-fetches the thread content -
// the new message appears in the existing tree, no full reload.
func TestCycleRefetchesHydratedThread(t *testing.T) {
	bus := core.NewBus()
	fw := &fakeWorker{}
	fw.set("u", 10)
	fw.setMsgs([]core.Message{{ID: "m1", ThreadID: "t1", Tags: []string{"inbox"}}})
	view := core.NewView("inbox", "tag:inbox")
	r := newRefresher(bus, fw, view, 10)
	// first cycle: the full reload path, t1 loads as real content
	r.cycle()
	if !r.view.Hydrated("t1") {
		t.Fatal("t1 must be hydrated after the load")
	}
	// a reply lands: the changed set is a stub (no message id), the
	// thread fetch returns the full tree
	fw.setMsgs([]core.Message{{ID: "", ThreadID: "t1", Tags: []string{"inbox"}}})
	fw.setThreadMsgs(map[string][]core.Message{"t1": {
		{ID: "m1", ThreadID: "t1"},
		{ID: "m2", ThreadID: "t1", References: []string{"m1"}},
	}})
	fw.set("u", 11)
	r.cycle()
	rows := view.Rows()
	if len(rows) != 2 {
		t.Fatalf("the reply must appear in the tree: %d rows", len(rows))
	}
	if rows[0].Msg.ID != "m1" || !rows[0].Root || rows[1].Msg.ID != "m2" || rows[1].Depth != 1 {
		t.Fatalf("tree content wrong: %+v", rows)
	}
	// a failed thread fetch keeps rPrev stale: the consumed lastmod
	// would lose the new message
	fw.setMsgs([]core.Message{{ID: "", ThreadID: "t1", Tags: []string{"inbox"}}})
	fw.setThreadMsgs(map[string][]core.Message{}) // empty map: the fallback serves f.msgs
	fw.set("u", 12)
	fw.setPruneErr(errors.New("boom"))
	r.cycle()
	if r.rPrev != 11 {
		t.Fatalf("rPrev advanced past the failed fetch: %d", r.rPrev)
	}
	if len(view.Rows()) != 2 {
		t.Fatalf("the failed cycle must not touch the tree: %d rows", len(view.Rows()))
	}
}

// TestCyclePrunesRetaggedOut pins the resurrection fix: a message
// retagged out of the view query (the apply path archives it) still
// bumps lastmod, so the changed set carries its thread - the prune
// intersect drops it from the merge AND from the snapshot carry-over
// (once its lastmod is consumed, no later changed set names it again).
func TestCyclePrunesRetaggedOut(t *testing.T) {
	bus := core.NewBus()
	fw := &fakeWorker{}
	fw.set("u", 10)
	fw.setMsgs([]core.Message{{ID: "m2", ThreadID: "t2", Tags: []string{"inbox"}}})
	view := core.NewView("inbox", "tag:inbox")
	r := newRefresher(bus, fw, view, 0)
	r.cycle() // full reload: t2 loads
	if !hasThread(view.Threads, "t2") {
		t.Fatal("full reload must load t2")
	}
	// m2 changed: retagged out of the view query
	fw.setMsgs([]core.Message{{ID: "m2", ThreadID: "t2", Tags: []string{"archive"}}})
	fw.set("u", 11)
	r.cycle()
	if hasThread(view.Threads, "t2") {
		t.Fatal("a thread retagged out of the view query must not re-merge")
	}
	if len(r.snapshot) != 0 {
		t.Fatalf("the pruned thread must leave the snapshot too: %d", len(r.snapshot))
	}
	if q, _ := fw.lastQuery.Load().(string); q != "(tag:inbox) and (thread:t2)" {
		t.Fatalf("prune query = %q", q)
	}
}

// TestCyclePruneKeepsMatching pins the positive prune: a changed
// thread that still matches the view query merges with its reconciled
// tags.
func TestCyclePruneKeepsMatching(t *testing.T) {
	bus := core.NewBus()
	fw := &fakeWorker{}
	fw.set("u", 10)
	fw.setMsgs([]core.Message{{ID: "m2", ThreadID: "t2", Tags: []string{"inbox"}}})
	view := core.NewView("inbox", "tag:inbox")
	r := newRefresher(bus, fw, view, 0)
	r.cycle()
	// soft change: the thread still matches the query
	fw.setMsgs([]core.Message{{ID: "m2", ThreadID: "t2", Tags: []string{"inbox", "unread"}}})
	fw.set("u", 11)
	r.cycle()
	if !hasThread(view.Threads, "t2") {
		t.Fatal("a still-matching thread must stay")
	}
	if tags := view.Tags("m2"); !slices.Equal(tags, []string{"inbox", "unread"}) {
		t.Fatalf("reconciled tags = %v", tags)
	}
}

// TestCyclePruneFailureKeepsStale pins the prune-failure path: a
// failed intersect must not advance rPrev (an un-pruned changed set
// would merge the removed thread back, and with rPrev advanced its
// lastmod is consumed - permanent resurrection); the next cycle retries.
func TestCyclePruneFailureKeepsStale(t *testing.T) {
	bus := core.NewBus()
	fw := &fakeWorker{}
	fw.set("u", 10)
	fw.setMsgs([]core.Message{{ID: "m2", ThreadID: "t2", Tags: []string{"inbox"}}})
	view := core.NewView("inbox", "tag:inbox")
	r := newRefresher(bus, fw, view, 0)
	r.cycle()
	fw.setMsgs([]core.Message{{ID: "m2", ThreadID: "t2", Tags: []string{"archive"}}})
	fw.set("u", 11)
	fw.setPruneErr(errors.New("lock timeout"))
	r.cycle()
	if r.rPrev != 10 {
		t.Fatalf("rPrev advanced past a failed prune: %d", r.rPrev)
	}
	if !hasThread(view.Threads, "t2") {
		t.Fatal("a failed cycle must not merge anything")
	}
	fw.setPruneErr(nil)
	r.cycle()
	if hasThread(view.Threads, "t2") {
		t.Fatal("the retried prune must drop the thread")
	}
	if r.rPrev != 11 {
		t.Fatalf("rPrev = %d after the retry", r.rPrev)
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
	if r.view.ViewQuery() != "tag:changed" {
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
	if r.view.ViewQuery() != "tag:changed" {
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

func TestOnConfigSwitchesView(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	fw := &fakeWorker{}
	fw.set("u", 1)
	fw.setMsgs([]core.Message{{ID: "m1", ThreadID: "t1"}})
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{{ID: "m1", ThreadID: "t1"}})})
	r := newRefresher(bus, fw, view, 0)
	view.Reset()
	if len(view.Threads) != 0 {
		t.Fatalf("Reset must drop the view rows, %d threads left", len(view.Threads))
	}
	st := config.NewStore(config.Default())
	if err := st.SetActiveView("archive"); err != nil {
		t.Fatal(err)
	}
	r.onConfig(st, core.ConfigChanged{Section: "view"})
	if r.view.ViewName() != "archive" || r.view.ViewQuery() != "tag:archive" {
		t.Fatalf("view not switched: name %q query %q", r.view.Name, r.view.Query)
	}
	if q, _ := fw.lastQuery.Load().(string); q != "tag:archive" {
		t.Fatalf("reload must query the new view, got %q", q)
	}
	readProgress(t, ch)
	select {
	case e := <-ch:
		if _, ok := e.(core.ViewDiff); !ok {
			t.Fatalf("expected ViewDiff after view switch, got %T", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no ViewDiff after view switch")
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

// TestRunSearchQuery pins the search-tab loader (the ctrl+f seam's
// app side): the count + walk fill the fresh view in one merged batch,
// publishing progress first and the diff keyed by the query name - the
// event order the search tab renders against.
func TestRunSearchQuery(t *testing.T) {
	bus := core.NewBus()
	fw := &fakeWorker{}
	fw.setMsgs([]core.Message{
		{ID: "m1", ThreadID: "t1", Timestamp: 3, Author: "Ann", Subject: "alpha"},
		{ID: "m2", ThreadID: "t2", Timestamp: 2, Author: "Bob", Subject: "beta"},
		{ID: "m3", ThreadID: "t2", Timestamp: 1, Author: "Bob", Subject: "re: beta"},
	})
	view := core.NewView("tag:acme", "tag:acme")
	ch := bus.Subscribe()
	runSearchQuery(fw, bus, view)
	if len(view.Threads) != 2 {
		t.Fatalf("the walk must merge 2 threads, got %d", len(view.Threads))
	}
	if !view.Hydrated("t1") {
		t.Fatal("the merged rows must carry real message ids")
	}
	var progress, diff bool
	for {
		select {
		case e := <-ch:
			switch ev := e.(type) {
			case core.Progress:
				progress = true
			case core.ViewDiff:
				if ev.View != "tag:acme" {
					t.Fatalf("the diff must key the query name, got %q", ev.View)
				}
				diff = true
			}
		case <-time.After(2 * time.Second):
			t.Fatal("no diff from the search load")
		}
		if diff {
			break
		}
	}
	if !progress {
		t.Fatal("the fill must publish progress before the diff")
	}
}

// TestFlatRefresh pins the flat-view refresh: unread is a
// chronological message list - one row per matched message, no thread
// drag. A cycle whose changed set names ONE message of a conversation
// must keep the other's synthetic thread: the prune decides
// membership per message id, so a read sibling never drags its
// conversation back in.
func TestFlatRefresh(t *testing.T) {
	bus := core.NewBus()
	w := &fakeWorker{}
	view := core.NewView("unread", "tag:unread")
	view.SetThreaded(false)
	r := newRefresher(bus, w, view, 0)
	// two messages of the same conversation, both unread
	w.set("uuid", 10)
	w.setMsgs([]core.Message{
		{ID: "a@example.com", ThreadID: "conv1", Timestamp: 1, Tags: []string{"unread"}},
		{ID: "b@example.com", ThreadID: "conv1", Timestamp: 2, Tags: []string{"unread"}},
	})
	r.cycle()
	rows := view.Rows()
	if len(rows) != 2 {
		t.Fatalf("flat view rows = %d (want 2, one per message)", len(rows))
	}
	for _, row := range rows {
		if row.Msg == nil || !slices.Contains(row.Msg.Tags, "unread") {
			t.Fatalf("flat row must be its own message: %+v", row)
		}
	}
	// b read: the toggle bumps BOTH messages' lastmod, so the changed
	// set names both; the prune answers membership per message and
	// drops b - a read sibling never drags its conversation back in
	w.set("uuid", 20)
	w.setMsgs([]core.Message{
		{ID: "a@example.com", ThreadID: "conv1", Timestamp: 1, Tags: []string{"unread"}},
		{ID: "b@example.com", ThreadID: "conv1", Timestamp: 2, Tags: []string{"read"}},
	})
	r.cycle()
	rows = view.Rows()
	if len(rows) != 1 || rows[0].Msg == nil || rows[0].Msg.ID != "a@example.com" {
		t.Fatalf("after read: rows = %v (want just a)", rowIDs(rows))
	}
}

func rowIDs(rows []core.Row) []string {
	var ids []string
	for _, r := range rows {
		if r.Msg != nil {
			ids = append(ids, r.Msg.ID)
		}
	}
	return ids
}
