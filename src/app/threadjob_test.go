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
	tj := newThreadJob(bus, fw, view, func() bool { return true })
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
