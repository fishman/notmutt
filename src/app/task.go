// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"sync"

	"notmutt/core"
)

// task.go: the background task registry behind the TUI's task view.
// A cancellable background job (the refresh hook's sync command)
// registers before it starts and completes when it returns; the
// registry publishes TaskChanged on the bus so the task view stays
// current. Cancellation flows back on CancelTask - the app's task loop
// owns the cancel, never the UI.

// Task is one running background job: its display identity and the
// cancel func the task loop invokes.
type Task struct {
	ID     string
	Label  string
	Active bool
	cancel context.CancelFunc
}

var (
	taskMu  sync.Mutex
	tasks   = map[string]*Task{}
	taskSeq uint64
)

// registerTask adds a running task and publishes its start. cancel
// kills the job (a sync task's exec.CommandContext).
func registerTask(bus *core.Bus, label string, cancel context.CancelFunc) *Task {
	taskMu.Lock()
	taskSeq++
	t := &Task{ID: fmt.Sprintf("t%d", taskSeq), Label: label, Active: true, cancel: cancel}
	tasks[t.ID] = t
	taskMu.Unlock()
	bus.Publish(core.TaskChanged{ID: t.ID, Label: label, Active: true})
	return t
}

// completeTask removes a task and publishes its end; a user cancel is
// reported as cancelled, never as a failure.
func completeTask(bus *core.Bus, id string, cancelled bool, err error) {
	taskMu.Lock()
	t := tasks[id]
	if t != nil {
		t.Active = false
		delete(tasks, id)
	}
	taskMu.Unlock()
	if t == nil {
		return
	}
	e := core.TaskChanged{ID: id, Label: t.Label, Cancelled: cancelled}
	if err != nil {
		e.Err = err.Error()
	}
	bus.Publish(e)
}

// cancelTask kills the named task; it is a no-op for an unknown id.
func cancelTask(id string) {
	taskMu.Lock()
	t := tasks[id]
	taskMu.Unlock()
	if t != nil && t.cancel != nil {
		t.cancel()
	}
}

// taskLoop owns task cancellation: CancelTask events kill the matching
// task's context (the exec.CommandContext kills the process). It runs
// in its own goroutine so a blocking poll can never stall a cancel.
// The subscription is passed in so a caller (or test) can subscribe
// deterministically before any event is published.
func taskLoop(ctx context.Context, ch <-chan core.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case e := <-ch:
			if c, ok := e.(core.CancelTask); ok {
				cancelTask(c.ID)
			}
		}
	}
}
