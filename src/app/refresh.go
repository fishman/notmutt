package app

import (
	"fmt"
	"sort"
	"sync"

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
	bus     *core.Bus
	worker  workerAPI
	view    *core.View
	page    int
	uuid    string
	rPrev   uint64
	running bool
	mu      sync.Mutex
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
	msgs, err := r.changed(r.rPrev, rpl.Rev)
	if err != nil {
		return
	}
	threads := r.fetchThreads(msgs)
	if len(threads) > 0 {
		sortThreads(threads)
		r.view.MergeThreads(threads)
		r.bus.Publish(core.ViewDiff{View: r.view.Name})
	}
	r.rPrev = rpl.Rev
}

func (r *refresher) changed(prev, cur uint64) ([]core.Message, error) {
	rpl, err := r.worker.Call(notmuch.Action{
		Kind:  notmuch.ActQuery,
		Query: fmt.Sprintf("lastmod:%d..%d", prev, cur),
	})
	if err != nil || rpl.Err != nil {
		return nil, fmt.Errorf("changed query: %v %v", err, rpl.Err)
	}
	return rpl.Msgs, nil
}

// fetchThreads maps changed messages to their threads and fetches each
// thread's full state, budgeted to 3 concurrent calls.
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

// fullReload re-fetches the view query and merges; cursor survives via
// the merge walk. Called for uuid changes, manual refresh, view config
// changes, and first load.
func (r *refresher) fullReload() {
	rpl, err := r.worker.Call(notmuch.Action{Kind: notmuch.ActQuery, Query: r.view.Query, Limit: r.page})
	if err != nil || rpl.Err != nil {
		return
	}
	threads := r.fetchThreads(rpl.Msgs)
	sortThreads(threads)
	r.view.MergeThreads(threads)
	r.bus.Publish(core.ViewDiff{View: r.view.Name})
}

func sortThreads(threads []*core.Thread) {
	sort.Slice(threads, func(i, j int) bool { return core.ThreadLess(threads[i], threads[j]) })
}
