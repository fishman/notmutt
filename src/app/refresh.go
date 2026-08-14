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

// firstPage is the initial fill page: 200 threads land fast so the first
// paint shows up immediately, then the fill continues at the steady page.
const firstPage = 200

// refresher owns the lastmod incremental cycle and the full-reload
// triggers. R_prev is the revision queried through - a change landing
// mid-cycle falls into the next one: one-cycle lag, deterministic.
type refresher struct {
	bus      *core.Bus
	worker   workerAPI
	view     *core.View
	page     int
	uuid     string
	rPrev    uint64
	snapshot []*core.Thread
	running  bool
	mu       sync.Mutex
}

func newRefresher(bus *core.Bus, w workerAPI, view *core.View, rPrev uint64) *refresher {
	return &refresher{bus: bus, worker: w, view: view, page: 1000, rPrev: rPrev}
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
	threads := r.fetchThreads(msgs, 0, 0, true)
	if len(threads) > 0 {
		r.merge(threads)
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
	rpl, err := r.worker.Call(notmuch.Action{
		Kind:  notmuch.ActQuery,
		Query: fmt.Sprintf("lastmod:%d..%d", prev, cur),
	})
	if err != nil || rpl.Err != nil {
		return nil, fmt.Errorf("changed query failed (err=%v, reply=%v)", err, rpl.Err)
	}
	return rpl.Msgs, nil
}

// fetchThreads maps changed messages to their threads and fetches each
// thread's full state, budgeted to 3 concurrent calls. The INCREMENTAL
// path (the lastmod changed set - small N) and the viewport hydrate
// (step two for the visible window) both use it; the full reload never
// does (search pages only). A failed thread fetch is silently dropped
// so a dead thread cannot kill the cycle. Progress falls back to the
// batch total when total <= 0 (this path has no count query). Failures
// count too, so the bar always completes. report=false silences the
// publishes: the viewport hydrate is bounded work that would otherwise
// clobber the fill's count-total bar.
func (r *refresher) fetchThreads(msgs []core.Message, base, total int, report bool) []*core.Thread {
	ids := map[string]bool{}
	for _, m := range msgs {
		ids[m.ThreadID] = true
	}
	if total <= 0 {
		total = len(ids)
		base = 0
	}
	sem := make(chan struct{}, 3)
	threads := make([]*core.Thread, 0, len(ids))
	var mu sync.Mutex
	done := 0
	var wg sync.WaitGroup
	for id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			rpl, err := r.worker.Call(notmuch.Action{Kind: notmuch.ActThread, ThreadID: id})
			mu.Lock()
			defer mu.Unlock()
			done++
			if report {
				r.bus.Publish(core.Progress{Job: "refresh", View: r.view.Name, Done: base + done, Total: total})
			}
			if err != nil || rpl.Err != nil {
				return
			}
			ptrs := make([]*core.Message, len(rpl.Msgs))
			for i := range rpl.Msgs {
				ptrs[i] = &rpl.Msgs[i]
			}
			threads = append(threads, core.NewThread(id, ptrs))
		}(id)
	}
	wg.Wait()
	return threads
}

// fullReload re-fetches the view query page by page and merges each
// page in as it lands (R3 progressive fill): one ActQuery per page.
// The search summary IS the index read - step one, DB-side thread
// data (thread id, date, authors, subject, tags) with zero file
// opens, so the whole list loads in seconds (per-thread round trips
// were the load wall). Full message data (ids, references, paths) is
// step two: fillViewport hydrates the visible window after the fill,
// and the rest loads on open (R13). One ViewDiff per page; a short
// page ends the query. The bar's total comes from a count query up
// front, so Done (threads accumulated) tracks the real result size
// instead of resetting per page; a count failure degrades to per-page
// totals. Threads that retagged out of the filter or were deleted are
// removed by the full snapshot replacement. Called for uuid changes,
// manual refresh, view config changes, and first load. The cursor
// survives via the merge walk.
func (r *refresher) fullReload() {
	total := 0
	if rpl, err := r.worker.Call(notmuch.Action{Kind: notmuch.ActCount, Query: r.view.Query}); err == nil && rpl.Err == nil {
		total = rpl.Count
	}
	var snapshot []*core.Thread
	done := 0
	limit := firstPage
	for offset := 0; ; {
		rpl, err := r.worker.Call(notmuch.Action{Kind: notmuch.ActQuery, Query: r.view.Query, Limit: limit, Offset: offset})
		if err != nil || rpl.Err != nil {
			return
		}
		page := groupThreads(rpl.Msgs)
		sortThreads(page)
		snapshot = mergeSorted(snapshot, page)
		r.snapshot = snapshot
		done += len(page)
		// progress first, then the diff: the page is reported as soon as
		// it lands. An empty page publishes nothing: a count/catalog
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
		offset += limit
		if len(rpl.Msgs) < limit {
			break
		}
		limit = r.page
	}
	r.fillViewport()
}

// fillViewport is the step-two hydrate: the visible window's stub
// rows (search summaries carry no message ids) get their full
// threads - ids, references, paths - through the budgeted per-thread
// fetch. Already-hydrated rows are untouched (the merge reconciles by
// id). The window mirrors the cache job's scanPage: the viewport at
// the top of the list; deep-scroll rows stay summaries until the
// viewport plumbing (cursor-following scans) lands.
func (r *refresher) fillViewport() {
	rows := r.view.Rows()
	if len(rows) > scanPage {
		rows = rows[:scanPage]
	}
	var stubs []core.Message
	for _, row := range rows {
		if row.Msg != nil && row.Msg.ID == "" {
			stubs = append(stubs, core.Message{ThreadID: row.ThreadID})
		}
	}
	if len(stubs) == 0 {
		return
	}
	threads := r.fetchThreads(stubs, 0, 0, false)
	if len(threads) > 0 {
		r.merge(threads)
	}
}

// groupThreads groups a page's search summaries into one stub thread
// per thread id (the summary row the view renders until the viewport
// hydrate replaces it with the full thread).
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
