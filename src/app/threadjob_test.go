// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"testing"
	"time"

	"notmutt/core"
)

// TestThreadJobHydratesOnce pins the hydration wave discipline: every
// stub hydrates exactly once, and the loop quiesces - a ViewDiff burst
// collapses into one scan (the amplification regression: 1 event -> 40
// fetches -> 40 events -> 40 scans, the worker-saturating storm).
func TestThreadJobHydratesOnce(t *testing.T) {
	fw := &fakeWorker{}
	bus := core.NewBus()
	view := core.NewView("inbox", "tag:inbox")
	var stubs []*core.Thread
	for i := 0; i < scanPage*2; i++ {
		id := fmt.Sprintf("t%d", i)
		stubs = append(stubs, core.NewThread(id, []*core.Message{{ThreadID: id}}))
	}
	view.MergeThreads(stubs)
	tj := newThreadJob(bus, fw, view, func() bool { return true }, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tj.Run(ctx)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		bus.Publish(core.ViewDiff{View: "inbox"})
		all := true
		for i := 0; i < scanPage*2; i++ {
			if !view.Hydrated(fmt.Sprintf("t%d", i)) {
				all = false
				break
			}
		}
		if all {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for i := 0; i < scanPage*2; i++ {
		if !view.Hydrated(fmt.Sprintf("t%d", i)) {
			t.Fatalf("t%d never hydrated", i)
		}
	}
	if got := fw.threads.Load(); got > scanPage*2 {
		t.Fatalf("fetch amplification: %d fetches for %d stubs", got, scanPage*2)
	}
}

// TestThreadJobViewSwitchIsolation pins the per-view-generation wave:
// thread ids span folders, so a fetch held in flight for one view must
// neither suppress nor land in another view's hydration after a switch
// (the old bug: the bare-id pending set skipped the same id in the new
// view, and the stray merge dropped against rows it did not belong to
// - stubs stayed unhydrated until the scan cursor wrapped).
func TestThreadJobViewSwitchIsolation(t *testing.T) {
	fw := &fakeWorker{}
	bus := core.NewBus()
	view := core.NewView("deleted", "tag:deleted")
	var stubs []*core.Thread
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("t%d", i)
		stubs = append(stubs, core.NewThread(id, []*core.Message{{ThreadID: id}}))
	}
	view.MergeThreads(stubs)
	gate := make(chan struct{})
	fw.setBlock("t0", gate)
	tj := newThreadJob(bus, fw, view, func() bool { return true }, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go tj.Run(ctx)
	bus.Publish(core.ViewDiff{View: "deleted"})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && fw.threads.Load() < 4 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := fw.threads.Load(); got < 4 {
		t.Fatalf("wave never launched: %d fetches", got)
	}
	// the switch: the same view object, reset and reloaded with the
	// index query's stubs (the refresher's onConfig shape); t0's fetch
	// is still in flight under the old generation
	view.SetIdentity("index", "tag:inbox")
	view.Reset()
	index := []*core.Thread{
		core.NewThread("t0", []*core.Message{{ThreadID: "t0"}}),
		core.NewThread("t4", []*core.Message{{ThreadID: "t4"}}),
		core.NewThread("t5", []*core.Message{{ThreadID: "t5"}}),
	}
	view.MergeThreads(index)
	close(gate) // the old wave completes; its merges must drop
	bus.Publish(core.QueryBatch{View: "index"})
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		all := true
		for _, id := range []string{"t0", "t4", "t5"} {
			if !view.Hydrated(id) {
				all = false
				break
			}
		}
		if all {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, id := range []string{"t0", "t4", "t5"} {
		if !view.Hydrated(id) {
			t.Fatalf("%s never hydrated after the switch", id)
		}
	}
	// t0 fetched twice - once per view generation (the new view's scan
	// refetches the id the old wave held; the old code suppressed it:
	// 6 fetches total, t0 stuck until the cursor wrapped)
	if got := fw.threads.Load(); got != 7 {
		t.Fatalf("cross-view suppression: %d fetches, want 7 (4 old + 3 new)", got)
	}
}
