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
	bus      *core.Bus
	worker   workerAPI
	view     *core.View
	threaded func() bool // live config truth: Views[ActiveView].Threads
	next     int         // the scan cursor: each scan takes the next scanPage rows
	mu       sync.Mutex
	pending  map[string]bool
}

func newThreadJob(bus *core.Bus, w workerAPI, view *core.View, threaded func() bool) *threadJob {
	return &threadJob{bus: bus, worker: w, view: view, threaded: threaded, pending: map[string]bool{}}
}

func (t *threadJob) Run(ctx context.Context) {
	ch := t.bus.Subscribe()
	t.scanVisible() // startup scan covers whatever rows already exist; ViewDiff drives steady state
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
			t.scanVisible()
		}
	}
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

func (t *threadJob) scanVisible() {
	if !t.threaded() {
		return
	}
	rows := t.view.Rows()
	if len(rows) == 0 {
		return
	}
	if t.next >= len(rows) {
		t.next = 0
	}
	end := min(t.next+scanPage, len(rows))
	page := rows[t.next:end]
	t.next = end
	var wg sync.WaitGroup
	total := 0
	for _, r := range page {
		if threadWorthy(r) {
			total++
		}
	}
	done := 0
	t.view.BeginMerge()
	for _, r := range page {
		if !threadWorthy(r) {
			continue
		}
		tid := r.ThreadID
		t.mu.Lock()
		if t.pending[tid] {
			t.mu.Unlock()
			continue
		}
		t.pending[tid] = true
		t.mu.Unlock()
		wg.Add(1)
		go func() {
			defer wg.Done()
			rpl, err := t.worker.Call(notmuch.Action{Kind: notmuch.ActThread, ThreadID: tid})
			t.mu.Lock()
			delete(t.pending, tid) // a failed fetch clears the gate: the next scan retries
			done++
			// the per-fetch progress is the paint trigger (the TUI re-reads
			// rows on Progress, not ViewDiff); the bar advances once per
			// thread (R15)
			t.bus.Publish(core.Progress{Job: "threads", View: t.view.Name, Done: done, Total: total})
			t.mu.Unlock()
			if err != nil || rpl.Err != nil {
				return
			}
			msgs := make([]*core.Message, len(rpl.Msgs))
			for i := range rpl.Msgs {
				msgs[i] = &rpl.Msgs[i]
			}
			t.view.MergeThread(core.NewThread(tid, msgs))
			t.bus.Publish(core.ViewDiff{View: t.view.Name})
		}()
	}
	wg.Wait()
	t.view.EndMerge()
	if total > 0 && done == total {
		// scan end: the bar clears (R15 batch boundary) even when a fetch
		// failed - the failed thread retries on the next scan
		t.bus.Publish(core.Progress{Job: "threads", View: t.view.Name, Done: total, Total: total})
	}
}
