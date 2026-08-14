package app

import (
	"fmt"
	"sort"
	"sync"

	"notmutt/config"
	"notmutt/core"
	"notmutt/notmuch"
)

type workerAPI interface {
	Call(a notmuch.Action) (notmuch.Reply, error)
}

// refresher owns the lastmod incremental cycle and the full-reload
// triggers. R_prev is the revision queried through - a change landing
// mid-cycle falls into the next one: one-cycle lag, deterministic.
type refresher struct {
	bus      *core.Bus
	worker   workerAPI
	view     *core.View
	uuid     string
	rPrev    uint64
	snapshot []*core.Thread
	running  bool
	mu       sync.Mutex
}

func newRefresher(bus *core.Bus, w workerAPI, view *core.View, rPrev uint64) *refresher {
	return &refresher{bus: bus, worker: w, view: view, rPrev: rPrev}
}

func (r *refresher) cycle() {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return
	}
	r.running = true
	r.mu.Unlock()
	defer func() { r.mu.Lock(); r.running = false; r.mu.Unlock() }()

	rpl, err := r.worker.Call(notmuch.Action{Kind: notmuch.ActRevision})
	if err != nil || rpl.Err != nil {
		return
	}
	if rpl.UUID != r.uuid {
		r.fullReload()
		r.uuid, r.rPrev = rpl.UUID, rpl.Rev
		return
	}
	if rpl.Rev == r.rPrev {
		return
	}
	// Drift is deliberate: messages retagged out of the view query still
	// bump lastmod, so their threads re-merge and can linger with stale
	// rows, while wholly deleted threads never appear in the changed set.
	// The reconcile soak timer (periodic full reload, future work) is the net.
	msgs, err := r.changed(r.rPrev, rpl.Rev)
	if err != nil {
		// Swallow is deliberate: a lock timeout already surfaced as
		// WorkerLockTimeout on the bus; the view self-heals next cycle.
		return
	}
	// The changed set IS the merge input: search summaries (thread-level
	// index data) need no further fetch, and per-message rows (cgo) build
	// real trees from references. No show in the refresh path - content
	// loads only on open (R13).
	page := groupThreads(msgs)
	sortThreads(page)
	if len(page) > 0 {
		r.merge(page)
	}
	r.rPrev = rpl.Rev
}

// onConfig applies a runtime config change: a view-section change takes
// the query from the store (the single write path, R8), then a full
// reload re-fetches. Runs in runRefresher's event-loop goroutine, which
// is unsynchronized against cycle(): an in-flight initial load touches
// the same r.view.Query and r.snapshot. ConfigChanged cannot fire in M1
// (the store has no mutation caller); when one lands, onConfig must be
// serialized against cycle - the running flag does not cover it.
func (r *refresher) onConfig(st *config.Store, e core.ConfigChanged) {
	if e.Section == "view" {
		if v, ok := st.Config().Views[r.view.Name]; ok && v.Query != r.view.Query {
			r.view.Query = v.Query
		}
	}
	r.fullReload()
}

// merge carries unchanged threads over from the last snapshot: the
// incremental feed names only changed threads, and MergeThreads replaces
// the view's thread set with its input, so a partial feed would evict
// every thread it does not mention. Full reloads bypass this path - they
// replace the snapshot with the fresh query page, which is how removals
// reconcile. The snapshot is content-only; cursor and collapse state
// live on the view's own thread objects.
func (r *refresher) merge(changed []*core.Thread) {
	snapshot := make([]*core.Thread, 0, len(r.snapshot)+len(changed))
	byID := make(map[string]bool, len(changed))
	for _, t := range changed {
		byID[t.ID] = true
	}
	for _, t := range r.snapshot {
		if !byID[t.ID] {
			snapshot = append(snapshot, t)
		}
	}
	snapshot = append(snapshot, changed...)
	sortThreads(snapshot)
	r.view.MergeThreads(snapshot)
	r.snapshot = snapshot
	r.bus.Publish(core.ViewDiff{View: r.view.Name})
}

func (r *refresher) changed(prev, cur uint64) ([]core.Message, error) {
	var msgs []core.Message
	rpl, err := r.worker.Call(notmuch.Action{
		Kind:  notmuch.ActQuery,
		Query: fmt.Sprintf("lastmod:%d..%d", prev, cur),
		Emit: func(chunk []core.Message) bool {
			msgs = append(msgs, chunk...)
			return true
		},
	})
	if err != nil || rpl.Err != nil {
		return nil, fmt.Errorf("changed query failed (err=%v, reply=%v)", err, rpl.Err)
	}
	return msgs, nil
}

// fullReload re-fetches the whole view query in ONE call (Backend.Query
// walks the result and emits chunks - no offset paging: every paged
// offset call re-walks the notmuch mset, measured ~40s for 33 pages of
// a 33k-thread inbox against ~5s for one call). The chunk IS the index
// read: content-free, DB-side data (thread summaries on the CLI,
// per-message DB-header rows on cgo), zero file opens - the whole list
// loads in seconds (per-thread show round trips were the load wall).
// Message content is step two, on open only (R13). Each chunk merges
// in as it lands (R3 progressive fill): progress then ViewDiff, so the
// paint tracks the walk (the backend emits the first 200 fast, then
// 1000s - the render-batching requirement). The bar's total comes from
// a count query up front, so Done (threads accumulated) tracks the
// real result size instead of resetting per chunk; a count failure
// degrades to per-chunk totals. A chunkless result still merges once
// (empty query = empty view - removals reconcile via the full snapshot
// replacement). The emit closure runs on the worker goroutine inside
// the Call, which cycle() is blocked on, so the refresher state it
// touches is race-free. The cursor survives via the merge walk.
func (r *refresher) fullReload() {
	total := 0
	if rpl, err := r.worker.Call(notmuch.Action{Kind: notmuch.ActCount, Query: r.view.Query}); err == nil && rpl.Err == nil {
		total = rpl.Count
	}
	var snapshot []*core.Thread
	done := 0
	rpl, err := r.worker.Call(notmuch.Action{Kind: notmuch.ActQuery, Query: r.view.Query, Emit: func(msgs []core.Message) bool {
		page := groupThreads(msgs)
		sortThreads(page)
		snapshot = mergeSorted(snapshot, page)
		r.snapshot = snapshot
		done += len(page)
		// progress first, then the diff: the chunk is reported as soon as
		// it lands. An empty chunk publishes nothing: a count/catalog
		// race that empties the result must not leave a stuck bar at
		// Done 0.
		if len(page) > 0 {
			if total <= 0 {
				r.bus.Publish(core.Progress{Job: "refresh", View: r.view.Name, Done: len(page), Total: len(page)})
			} else {
				r.bus.Publish(core.Progress{Job: "refresh", View: r.view.Name, Done: done, Total: total})
			}
		}
		r.view.MergeThreads(snapshot)
		r.bus.Publish(core.ViewDiff{View: r.view.Name})
		return true
	}})
	if err != nil || rpl.Err != nil {
		return
	}
	if len(snapshot) == 0 {
		r.snapshot = nil
		r.view.MergeThreads(nil)
		r.bus.Publish(core.ViewDiff{View: r.view.Name})
	}
}

// groupThreads groups a page into one thread per thread id: the CLI
// page's search summaries become stub threads (one summary row each -
// the index row), the cgo page's per-message rows become real threads
// (the tree builds from references).
func groupThreads(msgs []core.Message) []*core.Thread {
	byID := map[string][]*core.Message{}
	for i := range msgs {
		id := msgs[i].ThreadID
		byID[id] = append(byID[id], &msgs[i])
	}
	threads := make([]*core.Thread, 0, len(byID))
	for id, m := range byID {
		threads = append(threads, core.NewThread(id, m))
	}
	return threads
}

// mergeSorted merges two ThreadLess-sorted slices; the fill pages in
// sorted pages, and re-sorting the whole snapshot per page is quadratic
// in the result size.
func mergeSorted(a, b []*core.Thread) []*core.Thread {
	out := make([]*core.Thread, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if core.ThreadLess(b[j], a[i]) {
			out = append(out, b[j])
			j++
		} else {
			out = append(out, a[i])
			i++
		}
	}
	out = append(out, a[i:]...)
	out = append(out, b[j:]...)
	return out
}

func sortThreads(threads []*core.Thread) {
	sort.Slice(threads, func(i, j int) bool { return core.ThreadLess(threads[i], threads[j]) })
}
