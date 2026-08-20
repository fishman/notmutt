// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"sync"

	"notmutt/core"
	"notmutt/notmuch"
)

// threadJob hydrates stub rows into real thread trees (R3): one ActThread
// per stub row, merged back into the view on arrival. The TUI repaints on
// the per-fetch Progress, so the trees fill in under the cursor as the
// fetches land. Hydration state IS the view - a thread is hydrated when
// its rows carry real message ids - so the only job state is the in-flight
// set: a stub row re-encountered (view reset, full reload) re-fetches by
// construction. Scans advance in scanPage waves over the whole view (the
// budget bounds in-flight fetches, not coverage); collapsed threads
// hydrate too - their root row is visible, C-expand becomes instant
// (MergeThread keeps the collapse state on the thread object).
type threadJob struct {
	bus    *core.Bus
	worker workerAPI
	view   *core.View
	// viewFor resolves the triggering event's view by its name (the
	// search tabs hydrate like the main view); nil = the main view only.
	viewFor  func(string) *core.View
	threaded func() bool // live config truth: Views[ActiveView].Threads
	gen      uint64      // the view generation the cursor belongs to
	next     int         // the scan cursor: each scan takes the next scanPage rows
	mu       sync.Mutex
	// pending dedupes in-flight fetches PER VIEW: thread ids span
	// views (one thread can hold messages in both inbox and a search
	// tab), so a wave started for one view must never suppress or block
	// another view's hydration of the same id.
	pending map[*core.View]map[string]bool
}

func newThreadJob(bus *core.Bus, w workerAPI, view *core.View, threaded func() bool, viewFor func(string) *core.View) *threadJob {
	return &threadJob{bus: bus, worker: w, view: view, viewFor: viewFor, threaded: threaded, pending: map[*core.View]map[string]bool{}}
}

func (t *threadJob) Run(ctx context.Context) {
	ch := t.bus.Subscribe()
	t.scanVisible(t.view) // startup scan covers whatever rows already exist; ViewDiff drives steady state
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-ch:
			if !scanTrigger(e) {
				continue
			}
			// one scan per wave, never one per merge: every fetched
			// thread publishes its own ViewDiff, and each event used to
			// spawn a fresh scan - 1 event -> 40 fetches -> 40 events ->
			// 40 scans, a self-sustaining storm that saturates the
			// worker and re-seeds the progress bar per scan. Draining
			// collapses the wave; events published during the scan
			// trigger the next one.
			drainEvents(ch)
			t.scanVisible(t.viewForEvent(e))
		}
	}
}

// viewForEvent resolves the triggering event's view: the events carry
// the view name (ViewDiff/QueryBatch), which maps through the registry
// to the search tabs' views. A name with no registry entry falls back
// to the main view.
func (t *threadJob) viewForEvent(e core.Event) *core.View {
	name := ""
	switch ev := e.(type) {
	case core.ViewDiff:
		name = ev.View
	case core.QueryBatch:
		name = ev.View
	}
	if name != "" && t.viewFor != nil {
		if v := t.viewFor(name); v != nil {
			return v
		}
	}
	return t.view
}

// scanTrigger reports the events that mean rows may need hydration.
func scanTrigger(e core.Event) bool {
	switch e.(type) {
	case core.ViewDiff, core.QueryBatch:
		return true
	}
	return false
}

// drainEvents consumes every queued event without handling it: the
// job reacts only to scan triggers, and this is a private subscriber
// channel, so the dropped events cost nothing outside this job.
func drainEvents(ch <-chan core.Event) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// threadWorthy picks rows that need hydration: real-row stubs (no
// message id). Ghost rows and hydrated rows are skipped; a pending
// thread is deduped by id, never by row position.
func threadWorthy(r core.Row) bool {
	m := r.Msg
	return m != nil && m.ID == "" && r.ThreadID != ""
}

func (t *threadJob) scanVisible(view *core.View) {
	if !t.threaded() {
		return
	}
	name := view.ViewName() // one locked read; the fetches publish it
	gen := view.Gen()
	if gen != t.gen {
		// a view switch: the scan starts at the top of the new view's
		// rows - ids in flight for the old view are refetched here
		// (their results are gated out of the merge below, so no double
		// work lands)
		t.gen = gen
		t.next = 0
	}
	rows := view.Rows()
	if len(rows) == 0 {
		return
	}
	// the cursor walks row positions, but hydration grows the flattened
	// blocks (1 stub row -> N message rows), so a fixed slice parks on
	// an already-hydrated block and the chain dies after wave 1. The
	// page is the next scanPage WORTHY rows; the cursor always advances.
	page := make([]core.Row, 0, scanPage)
	for t.next < len(rows) && len(page) < scanPage {
		if threadWorthy(rows[t.next]) {
			page = append(page, rows[t.next])
		}
		t.next++
	}
	if t.next >= len(rows) {
		t.next = 0
	}
	var wg sync.WaitGroup
	total := len(page)
	done := 0
	view.BeginMerge()
	for _, r := range page {
		tid := r.ThreadID
		t.mu.Lock()
		if t.pending[view][tid] {
			t.mu.Unlock()
			continue
		}
		if t.pending[view] == nil {
			t.pending[view] = map[string]bool{}
		}
		t.pending[view][tid] = true
		t.mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			rpl, err := t.worker.Call(notmuch.Action{Kind: notmuch.ActThread, ThreadID: tid})
			t.mu.Lock()
			// a failed fetch clears the gate: the next scan retries
			if set := t.pending[view]; set != nil {
				delete(set, tid)
				if len(set) == 0 {
					delete(t.pending, view)
				}
			}
			done++
			// the per-fetch progress is the paint trigger (the TUI re-reads
			// rows on Progress, not ViewDiff); the bar advances once per
			// thread (R15)
			t.bus.Publish(core.Progress{Job: "threads", View: name, Done: done, Total: total})
			t.mu.Unlock()
			if err != nil || rpl.Err != nil {
				return
			}
			if view.Gen() != gen {
				// the view switched while the fetch was in flight: the
				// result belongs to the old view's rows - dropping it
				// lets the new view's own scan refetch the id
				return
			}
			msgs := make([]*core.Message, len(rpl.Msgs))
			for i := range rpl.Msgs {
				msgs[i] = &rpl.Msgs[i]
			}
			view.MergeThread(core.NewThread(tid, msgs))
		}()
	}
	wg.Wait()
	view.EndMerge()
	// the wave deferred the dirty mark to EndMerge: a refresh merge
	// that landed inside the window needs this diff to repaint, or the
	// rows stay stale until the next event (the reported $-apply
	// reconcile). No-op waves publish nothing - a diff would re-trigger
	// the scan forever.
	if view.Dirty() {
		t.bus.Publish(core.ViewDiff{View: name})
	}
	if total > 0 && done == total {
		// scan end: the bar clears (R15 batch boundary) even when a fetch
		// failed - the failed thread retries on the next scan
		t.bus.Publish(core.Progress{Job: "threads", View: name, Done: total, Total: total})
	}
}
