package app

import (
	"fmt"
	"sort"
	"strings"
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
		diag.Warn("refresh: revision", "err", fmt.Sprintf("%v %v", err, rpl.Err))
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
	msgs, err := r.changed(r.rPrev, rpl.Rev)
	if err != nil {
		// Swallow is deliberate: a lock timeout already surfaced as
		// WorkerLockTimeout on the bus; the view self-heals next cycle.
		// rPrev stays stale, so the next cycle retries the same range.
		diag.Warn("refresh: changed", "err", err.Error())
		return
	}
	// The changed set IS the merge input: search summaries (thread-level
	// index data), the same shape from both backends. No show in the
	// refresh path - content loads only on open (R13).
	page := groupThreads(msgs)
	sortThreads(page)
	if len(page) > 0 {
		kept, err := r.prune(page)
		if err != nil {
			// A failed prune must not advance rPrev: the un-pruned
			// changed set would merge threads back that the apply path
			// removed, and with rPrev advanced their lastmods are
			// consumed - the resurrection would be permanent.
			diag.Warn("refresh: prune", "err", err.Error())
			return
		}
		r.merge(kept)
	}
	r.rPrev = rpl.Rev
}

// prune answers view-query membership for the changed threads: a
// message retagged out of the view query still bumps lastmod, so the
// changed set can carry threads that no longer belong. The apply path
// removes them from the view at apply time; the refresh must not re-add
// them - and once their lastmod is consumed, no later changed set names
// them again, so the carry-over must forget them too. One batched
// intersect query over the changed thread ids - notmuch answers
// membership (R1), chunked so a mass retag cannot build an unbounded OR
// query. Survivors are the merge input; the pruned ids leave the
// snapshot now (a full reload reconciles any drift). A query failure
// surfaces as an error - the caller keeps rPrev stale and retries.
func (r *refresher) prune(changed []*core.Thread) ([]*core.Thread, error) {
	if len(changed) == 0 {
		return changed, nil
	}
	alive := make(map[string]bool, len(changed))
	for lo := 0; lo < len(changed); lo += pruneChunk {
		hi := min(lo+pruneChunk, len(changed))
		var q strings.Builder
		q.WriteByte('(')
		q.WriteString(r.view.Query)
		q.WriteString(") and (")
		for i := lo; i < hi; i++ {
			if i > lo {
				q.WriteString(" or ")
			}
			q.WriteString("thread:")
			q.WriteString(changed[i].ID)
		}
		q.WriteByte(')')
		rpl, err := r.worker.Call(notmuch.Action{
			Kind:  notmuch.ActQuery,
			Query: q.String(),
			Emit: func(chunk []core.Message) bool {
				for i := range chunk {
					alive[chunk[i].ThreadID] = true
				}
				return true
			},
		})
		if err != nil || rpl.Err != nil {
			return nil, fmt.Errorf("prune %s: %v %v", q.String(), err, rpl.Err)
		}
	}
	kept := make([]*core.Thread, 0, len(changed))
	for _, t := range changed {
		if alive[t.ID] {
			kept = append(kept, t)
		}
	}
	if len(kept) != len(changed) {
		gone := make(map[string]bool, len(changed)-len(kept))
		for _, t := range changed {
			if !alive[t.ID] {
				gone[t.ID] = true
			}
		}
		// the pruned threads leave the snapshot: the merge carry-over
		// would resurrect them for good (their lastmod is consumed)
		out := r.snapshot[:0]
		for _, t := range r.snapshot {
			if !gone[t.ID] {
				out = append(out, t)
			}
		}
		r.snapshot = out
	}
	return kept, nil
}

// pruneChunk bounds one intersect query: thread ids join an OR query,
// and notmuch chokes on unbounded queries (a mass retag can change
// thousands of threads at once).
const pruneChunk = 1000

// onConfig applies a runtime config change: a view-section change takes
// the query from the store (the single write path, R8), then a full
// reload re-fetches. Runs in runRefresher's event-loop goroutine, which
// is unsynchronized against cycle(): an in-flight initial load touches
// the same r.view.Query and r.snapshot. ConfigChanged cannot fire in M1
// (the store has no mutation caller); when one lands, onConfig must be
// serialized against cycle - the running flag does not cover it.
func (r *refresher) onConfig(st *config.Store, e core.ConfigChanged) {
	if e.Section != "view" {
		return
	}
	if v, ok := st.Config().Views[r.view.Name]; ok && v.Query != r.view.Query {
		r.view.Query = v.Query
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

// firstLoadRows is the fast pre-query size: enough threads for a
// meaningful first paint. The CLI writes the whole JSON write-at-end
// (measured 1.4s for a 33k-thread inbox), so chunk slicing alone cannot
// make the first paint early - every chunk still waits for the
// subprocess. A limit query lands in ~20ms.
const firstLoadRows = 100

// fullReload re-fetches the whole view query in TWO calls. Phase 1 is
// the fast pre-query (limit=firstLoadRows): the first 100 threads paint
// in milliseconds, so the UI shows content before the full walk
// finishes. Phase 2 is the full walk in ONE call (Backend.Query walks
// the result and emits chunks - no offset paging: every paged offset
// call re-walks the notmuch mset, measured ~40s for 33 pages of a
// 33k-thread inbox against ~5s for one call). The chunk IS the index
// read: content-free, DB-side data (thread summaries, the same shape
// from both backends), zero file opens - the whole list loads in
// seconds (per-thread show round trips were the load wall).
// Message content is step two, on open only (R13). Each chunk merges
// in as it lands (R3 progressive fill): progress then ViewDiff, so the
// paint tracks the walk (the backend emits the first 100 fast, then
// 5000s - the render-batching requirement). The walk's snapshot starts
// empty, so the re-walked head replaces the pre-query instead of
// duplicating it; the view then holds exactly the full result. The
// bar's total comes from a count query up front, so Done (threads
// accumulated) tracks the real result size instead of resetting per
// chunk; a count failure degrades to per-chunk totals. A chunkless
// result still merges once (empty query = empty view - removals
// reconcile via the full snapshot replacement). The emit closure runs
// on the worker goroutine inside the Call, which cycle() is blocked on,
// so the refresher state it touches is race-free. The cursor survives
// via the merge walk.
func (r *refresher) fullReload() {
	total := 0
	if rpl, err := r.worker.Call(notmuch.Action{Kind: notmuch.ActCount, Query: r.view.Query}); err == nil && rpl.Err == nil {
		total = rpl.Count
	}
	var snapshot []*core.Thread
	done := 0
	// phase 1: the fast pre-query paints the first rows immediately.
	// Failure degrades to the walk alone - the first paint just stays
	// empty for another second.
	if rpl, err := r.worker.Call(notmuch.Action{Kind: notmuch.ActQuery, Query: r.view.Query, Limit: firstLoadRows, Emit: func(msgs []core.Message) bool {
		snapshot = groupThreads(msgs)
		sortThreads(snapshot)
		if len(snapshot) > 0 {
			r.snapshot = snapshot
			r.paint(total, len(snapshot))
		}
		return true
	}}); err != nil || rpl.Err != nil {
		snapshot = nil
	}
	// phase 2: the full walk, from an empty snapshot - the pre-query's
	// threads are re-emitted at the head of the walk and replace it.
	snapshot = nil
	done = 0
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
			r.paint(total, done)
		}
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

// paint publishes the fill progress and the diff after a merge; the
// bar's total comes from the count query (or the batch when it failed).
// The merge runs inside a BeginMerge/EndMerge batch, so the view's
// dirty-mark lands once per emitted chunk: the flatten rebuilds once
// per batch end, never per intermediate keypress (the refresh window
// of the held-key lag round).
func (r *refresher) paint(total, done int) {
	if total <= 0 {
		r.bus.Publish(core.Progress{Job: "refresh", View: r.view.Name, Done: done, Total: done})
	} else {
		r.bus.Publish(core.Progress{Job: "refresh", View: r.view.Name, Done: done, Total: total})
	}
	r.view.BeginMerge()
	r.view.MergeThreads(r.snapshot)
	r.view.EndMerge()
	r.bus.Publish(core.ViewDiff{View: r.view.Name})
}

// groupThreads groups a page into one thread per thread id: the search
// summaries become stub threads (one summary row each - the index
// row), from both backends.
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
