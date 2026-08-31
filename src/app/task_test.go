// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"notmutt/core"
)

// TestTaskCancel: the task loop turns a CancelTask event into the task
// context's cancel, and the lifecycle events carry the state the task
// view renders.
func TestTaskCancel(t *testing.T) {
	bus := core.NewBus()
	loopCh := bus.Subscribe() // the loop's subscription, in place before any event
	watch := bus.Subscribe()  // the test's assertion view
	loopCtx, stop := context.WithCancel(context.Background())
	defer stop()
	go taskLoop(loopCtx, loopCh)
	ctx, cancel := context.WithCancel(context.Background())
	tk := registerTask(bus, "sync gmail", cancel)
	select {
	case e := <-watch:
		te, ok := e.(core.TaskChanged)
		if !ok || !te.Active || te.ID != tk.ID {
			t.Fatalf("start event = %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no TaskChanged start")
	}
	bus.Publish(core.CancelTask{ID: tk.ID})
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("CancelTask did not reach the task loop")
	}
	// the CancelTask event rides watch too (broadcast); drain it before
	// asserting the complete event that follows
	select {
	case e := <-watch:
		if _, ok := e.(core.CancelTask); !ok {
			t.Fatalf("expected the CancelTask echo, got %T %+v", e, e)
		}
	case <-time.After(time.Second):
		t.Fatal("no CancelTask echo on watch")
	}
	completeTask(bus, tk.ID, true, nil)
	select {
	case e := <-watch:
		te, ok := e.(core.TaskChanged)
		if !ok || te.Active || !te.Cancelled || te.ID != tk.ID {
			t.Fatalf("complete event = %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no TaskChanged complete")
	}
}

// TestTaskKillsSyncCommand: the poll's wiring - the sync runs under a
// context the task's cancel shares, so a CancelTask kills the process.
func TestTaskKillsSyncCommand(t *testing.T) {
	bus := core.NewBus()
	loopCtx, stop := context.WithCancel(context.Background())
	defer stop()
	go taskLoop(loopCtx, bus.Subscribe())
	tctx, cancel := context.WithCancel(context.Background())
	tk := registerTask(bus, "sync gmail", cancel)
	done := make(chan error, 1)
	go func() {
		done <- exec.CommandContext(tctx, "sleep", "30").Run()
	}()
	bus.Publish(core.CancelTask{ID: tk.ID})
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the cancel must kill the sync command")
	}
	if tctx.Err() == nil {
		t.Fatal("the sync context must be cancelled")
	}
}
