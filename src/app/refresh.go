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
	page     int
	uuid     string
	rPrev    uint64
	snapshot []*core.Thread
	running  bool
	mu       sync.Mutex
}

func newRefresher(bus *core.Bus, w workerAPI, view *core.View, rPrev uint64) *refresher {
	return &refresher{bus: bus, worker: w, view: view, page: 200, rPrev: rPrev}
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
	threads := r.fetchThreads(msgs)
	if len(threads) > 0 {
		r.merge(threads)
	}
	r.rPrev = rpl.Rev
}

// onConfig applies a runtime config change: a view-section change takes
// the query from the store (the single write path, R8), then a full
// reload re-fetches. Called from runRefresher's event loop only.
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
// thread's full state, budgeted to 3 concurrent calls. A failed thread
// fetch is silently dropped so a dead thread cannot kill the cycle.
func (r *refresher) fetchThreads(msgs []core.Message) []*core.Thread {
	ids := map[string]bool{}
	for _, m := range msgs {
		ids[m.ThreadID] = true
	}
	sem := make(chan struct{}, 3)
	threads := make([]*core.Thread, 0, len(ids))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			rpl, err := r.worker.Call(notmuch.Action{Kind: notmuch.ActThread, ThreadID: id})
			if err != nil || rpl.Err != nil {
				return
			}
			ptrs := make([]*core.Message, len(rpl.Msgs))
			for i := range rpl.Msgs {
				ptrs[i] = &rpl.Msgs[i]
			}
			mu.Lock()
			threads = append(threads, core.NewThread(id, ptrs))
			mu.Unlock()
		}(id)
	}
	wg.Wait()
	return threads
}

// fullReload re-fetches the view query and diffs the fresh page in as
// the full state: the view becomes exactly the query result, so threads
// that retagged out of the filter or were deleted are removed. Called
// for uuid changes, manual refresh, view config changes, and first load.
// The cursor survives via the merge walk.
func (r *refresher) fullReload() {
	rpl, err := r.worker.Call(notmuch.Action{Kind: notmuch.ActQuery, Query: r.view.Query, Limit: r.page})
	if err != nil || rpl.Err != nil {
		return
	}
	threads := r.fetchThreads(rpl.Msgs)
	sortThreads(threads)
	r.snapshot = threads
	r.view.MergeThreads(threads)
	r.bus.Publish(core.ViewDiff{View: r.view.Name})
}

func sortThreads(threads []*core.Thread) {
	sort.Slice(threads, func(i, j int) bool { return core.ThreadLess(threads[i], threads[j]) })
}
