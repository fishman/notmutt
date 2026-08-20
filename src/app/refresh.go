// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

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
	// refresh path - content loads only on open (R13); a hydrated
	// thread re-fetches below (R3 supersedes R13 there).
	page := groupThreads(msgs, r.view.ViewFlat())
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
		// a hydrated thread's changed stub carries summaries only: the
		// tree must show the new messages, so the content re-fetches
		// (R3 diff-and-insert - the stub guard in MergeThreads kept the
		// old tree until here). A fetch failure keeps rPrev stale like
		// the prune failure: the consumed lastmod would lose the new
		// messages, and the next cycle retries the same range. Flat
		// views skip the re-fetch entirely: a synthetic thread IS its
		// message, and re-fetching `thread:<msgid>` would merge the
		// whole conversation back into the flat list.
		for i, t := range kept {
			if r.view.ViewFlat() || !r.view.Hydrated(t.ID) {
				continue
			}
			ft, err := r.fetchThread(t.ID)
			if err != nil {
				diag.Warn("refresh: thread", "err", err.Error())
				return
			}
			kept[i] = ft
		}
		r.merge(kept)
	}
	r.rPrev = rpl.Rev
}

// fetchThread fetches a thread's full message set (the hydrated-thread
// re-fetch): ActThread -> NewThread, the same shape the hydrator merges.
func (r *refresher) fetchThread(id string) (*core.Thread, error) {
	rpl, err := r.worker.Call(notmuch.Action{Kind: notmuch.ActThread, ThreadID: id})
	if err != nil || rpl.Err != nil {
		return nil, fmt.Errorf("thread %s: %v %v", id, err, rpl.Err)
	}
	msgs := make([]*core.Message, len(rpl.Msgs))
	for i := range rpl.Msgs {
		msgs[i] = &rpl.Msgs[i]
	}
	return core.NewThread(id, msgs), nil
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
	// the flat prune is a message-level intersect: membership is decided
	// per MESSAGE id (the flat changed set names messages, and a read
	// sibling must not drag its conversation back in), the threaded
	// prune per thread id.
	flat := r.view.ViewFlat()
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
			if flat {
				q.WriteString("id:")
			} else {
				q.WriteString("thread:")
			}
			q.WriteString(changed[i].ID)
		}
		q.WriteByte(')')
		rpl, err := r.worker.Call(notmuch.Action{
			Kind:  notmuch.ActQuery,
			Query: q.String(),
			Flat:  flat,
			Emit: func(chunk []core.Message) bool {
				for i := range chunk {
					if flat {
						alive[chunk[i].ID] = true
					} else {
						alive[chunk[i].ThreadID] = true
					}
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

// onConfig applies a runtime config change: a view-section change moves
// the shared view pointer to the store's active view (the single write
// path, R8) and re-fetches. Reset drops the old query's rows - the
// switch renders empty until the reload's first chunk lands, never the
// previous view's rows. Runs in runRefresher's event-loop goroutine,
// which also owns the initial load - cycle and onConfig are serialized
// by construction.
func (r *refresher) onConfig(st *config.Store, e core.ConfigChanged) {
	if e.Section != "view" {
		return
	}
	name := st.Config().ActiveView
	v, ok := st.Config().Views[name]
	if !ok {
		return
	}
	r.view.SetIdentity(name, v.Query)
	r.view.SetThreaded(v.Threads)
	r.view.Reset()
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
		Flat:  r.view.ViewFlat(),
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
	flat := r.view.ViewFlat()
	total := 0
	if rpl, err := r.worker.Call(notmuch.Action{Kind: notmuch.ActCount, Query: r.view.Query, Flat: flat}); err == nil && rpl.Err == nil {
		total = rpl.Count
	}
	var snapshot []*core.Thread
	// phase 1: the fast pre-query paints the first rows immediately.
	// Failure degrades to the walk alone - the first paint just stays
	// empty for another second.
	if rpl, err := r.worker.Call(notmuch.Action{Kind: notmuch.ActQuery, Query: r.view.Query, Limit: firstLoadRows, Flat: flat, Emit: func(msgs []core.Message) bool {
		snapshot = groupThreads(msgs, flat)
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
	// mergeWalk publishes the fill progress per chunk; the hook merges
	// and reports the diff. An emptying reload publishes only the diff
	// (no progress - the old contract, the refresh tests pin the event
	// sequence).
	mergeWalk(r.worker, r.bus, r.view, total, "refresh", func(snapshot []*core.Thread, done int) {
		r.snapshot = snapshot
		mergeInto(r.bus, r.view, snapshot)
	})
}

// mergeWalk merges a query's emitted chunks into the view: each chunk
// groups and sorts, merges into the accumulated snapshot, publishes
// the fill progress, then runs onChunk with the snapshot (the merge
// and the diff) - the chunk is reported as soon as it lands. An empty
// chunk publishes nothing: a count/catalog race that empties the
// result must not leave a stuck bar at Done 0. An empty result still
// merges once (empty query = empty view - removals reconcile via the
// full snapshot replacement). The emit closure runs on the worker
// goroutine inside the Call, so the view state it touches is
// race-free. fullReload's phase 2 and the search tab (runSearchQuery)
// share this shape.
func mergeWalk(worker workerAPI, bus *core.Bus, view *core.View, total int, job string, onChunk func(snapshot []*core.Thread, done int)) {
	var snapshot []*core.Thread
	flat := view.ViewFlat()
	done := 0
	rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActQuery, Query: view.Query, Flat: flat, Emit: func(msgs []core.Message) bool {
		page := groupThreads(msgs, flat)
		sortThreads(page)
		snapshot = mergeSorted(snapshot, page)
		done += len(page)
		if len(page) > 0 {
			if total <= 0 {
				bus.Publish(core.Progress{Job: job, View: view.ViewName(), Done: done, Total: done})
			} else {
				bus.Publish(core.Progress{Job: job, View: view.ViewName(), Done: done, Total: total})
			}
			onChunk(snapshot, done)
		}
		return true
	}})
	if err != nil || rpl.Err != nil {
		return
	}
	if len(snapshot) == 0 {
		onChunk(nil, 0)
	}
}

// runSearchQuery loads one raw notmuch query into a fresh view (the
// ctrl+f search tab): the count + walk merge fill the tab in batches
// with progress and per-batch diffs - the fullReload shape without the
// phase-1 pre-query (the tab opens empty, the walk fills it). The view
// name is the query, so the events key per tab.
func runSearchQuery(worker workerAPI, bus *core.Bus, view *core.View) {
	total := 0
	if rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActCount, Query: view.Query, Flat: view.ViewFlat()}); err == nil && rpl.Err == nil {
		total = rpl.Count
	}
	mergeWalk(worker, bus, view, total, "search", func(snapshot []*core.Thread, done int) {
		mergeInto(bus, view, snapshot)
	})
}

// paint publishes the fill progress and the diff after a merge; the
// bar's total comes from the count query (or the batch when it failed).
func (r *refresher) paint(total, done int) {
	if total <= 0 {
		r.bus.Publish(core.Progress{Job: "refresh", View: r.view.Name, Done: done, Total: done})
	} else {
		r.bus.Publish(core.Progress{Job: "refresh", View: r.view.Name, Done: done, Total: total})
	}
	mergeInto(r.bus, r.view, r.snapshot)
}

// mergeInto merges the snapshot into the view in one Begin/End batch
// and reports the diff: the dirty-mark lands once per merge, so the
// flatten rebuilds once per batch end, never per intermediate keypress
// (the refresh window of the held-key lag round).
func mergeInto(bus *core.Bus, view *core.View, snapshot []*core.Thread) {
	view.BeginMerge()
	view.MergeThreads(snapshot)
	view.EndMerge()
	bus.Publish(core.ViewDiff{View: view.ViewName()})
}

// groupThreads groups a page into one thread per thread id: the full
// walk emits per-message rows, so each group becomes a thread tree.
// The flat views (unread, deleted, search) get one SYNTHETIC thread
// per message - keyed by the message id - so the list stays a plain
// chronological message list: every row is its own thread, merges
// reconcile per message, and the tree machinery never builds a
// hierarchy (the message keeps its real thread id in Msg.ThreadID, so
// open still finds the conversation).
func groupThreads(msgs []core.Message, flat bool) []*core.Thread {
	if flat {
		threads := make([]*core.Thread, 0, len(msgs))
		for i := range msgs {
			threads = append(threads, core.NewThread(msgs[i].ID, []*core.Message{&msgs[i]}))
		}
		return threads
	}
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
