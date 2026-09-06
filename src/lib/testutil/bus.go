// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"testing"
	"time"

	"notmutt/core"
)

// WaitEvent scans the bus until an event satisfies pred (unrelated events
// pass through) or the deadline - the async integration test idiom: wait
// for the event that proves a job finished, never for goroutine order.
func WaitEvent(t *testing.T, ch <-chan core.Event, pred func(core.Event) bool) core.Event {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		select {
		case e := <-ch:
			if pred(e) {
				return e
			}
		case <-deadline:
			t.Fatal("timed out waiting for the bus event")
			return nil
		}
	}
}

// Eventually polls cond until it returns true or the deadline - the
// condition-polling idiom over a short sleep, for state a goroutine
// writes without an event.
func Eventually(t *testing.T, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
