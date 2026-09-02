// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

// The walk-worker split (bug: categorize hung behind a full-walk). The
// long read walks (refresh full-reloads, search tabs) run on their own
// notmuch worker, so an interactive op on the main worker - categorize,
// open, tag, apply - never queues behind one. This test models the two
// workers with two backends: the walk backend blocks ActQuery until
// released, the interactive backend serves instantly. categorize must
// complete while the walk still holds the walk worker. A reversion to
// one shared worker (everything on the blocked backend) fails this test.

import (
	"context"
	"sync"
	"testing"
	"time"

	"notmutt/config"
	"notmutt/core"
	"notmutt/notmuch"
)

// blockedBackend blocks ActQuery until release; everything else canned.
// Reopen is instant (the walk handle's staleness gate must not itself
// block).
type blockedBackend struct {
	mu       sync.Mutex
	released bool
	block    chan struct{}
	entered  chan struct{}
}

func (b *blockedBackend) release() {
	b.mu.Lock()
	b.released = true
	if b.block != nil {
		close(b.block)
		b.block = nil
	}
	b.mu.Unlock()
}

// walkEntered reports that the walk's ActQuery is blocking the worker.
func (b *blockedBackend) walkEntered() {
	b.mu.Lock()
	if b.entered != nil {
		close(b.entered)
		b.entered = nil
	}
	b.mu.Unlock()
}

func (b *blockedBackend) Open(ctx context.Context, q string) error { return nil }
func (b *blockedBackend) Close(ctx context.Context) error          { return nil }
func (b *blockedBackend) Reopen(ctx context.Context) error         { return nil }
func (b *blockedBackend) Count(ctx context.Context, q string) (int, error) {
	return 1000, nil
}
func (b *blockedBackend) CountMsgs(ctx context.Context, q string) (int, error) {
	return 1000, nil
}
func (b *blockedBackend) Revision(ctx context.Context) (string, uint64, error) {
	return "u", 1, nil
}
func (b *blockedBackend) AddPaths(ctx context.Context, p []string) error { return nil }
func (b *blockedBackend) RemovePaths(ctx context.Context, p []string) error {
	return nil
}
func (b *blockedBackend) New(ctx context.Context) (uint64, uint64, error) {
	return 1, 1, nil
}
func (b *blockedBackend) Addresses(ctx context.Context, q string) ([]core.AddressEntry, error) {
	return nil, nil
}
func (b *blockedBackend) Tag(ctx context.Context, q string, ops []core.TagOp) error {
	return nil
}

// Query blocks until release: the walk owns the worker for a
// controllable window.
func (b *blockedBackend) Query(ctx context.Context, q string, limit int, flat bool, emit func([]core.Message) bool) error {
	b.mu.Lock()
	if !b.released {
		block := b.block
		if block == nil {
			block = make(chan struct{})
			b.block = block
		}
		if b.entered != nil {
			close(b.entered)
			b.entered = nil
		}
		b.mu.Unlock()
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	} else {
		b.mu.Unlock()
	}
	if emit != nil {
		emit([]core.Message{{ID: "t1", ThreadID: "t1", Timestamp: 100, Author: "Ann", Subject: "hello"}})
	}
	return nil
}

func (b *blockedBackend) QueryMsgs(ctx context.Context, q string, emit func([]core.Message) bool) error {
	if emit != nil {
		emit([]core.Message{{ID: "m1", ThreadID: "t1"}})
	}
	return nil
}

func (b *blockedBackend) Snapshots(ctx context.Context, ids []string) ([]core.Message, error) {
	return []core.Message{{ID: "m1", Timestamp: 100, Author: "Ann", Subject: "hello", Tags: []string{"inbox"}, Paths: []string{"/nonexistent/m1"}}}, nil
}

func (b *blockedBackend) Thread(ctx context.Context, id string) ([]core.Message, error) {
	return []core.Message{{ID: "m1", Timestamp: 100, Author: "Ann", Subject: "hello", Tags: []string{"inbox"}, Paths: []string{"/nonexistent/m1"}}}, nil
}

func TestWalkWorkerSplit(t *testing.T) {
	walkBe := &blockedBackend{}
	intBe := &blockedBackend{released: true}
	bus := core.NewBus()
	walkWorker := notmuch.NewWorker(bus, walkBe, 10*time.Second)
	worker := notmuch.NewWorker(bus, intBe, 10*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go walkWorker.Start(ctx)
	go worker.Start(ctx)
	if _, err := walkWorker.Call(notmuch.Action{Kind: notmuch.ActOpen, Query: ""}); err != nil {
		t.Fatal(err)
	}
	if _, err := worker.Call(notmuch.Action{Kind: notmuch.ActOpen, Query: ""}); err != nil {
		t.Fatal(err)
	}

	// the search walk starts and blocks the walk worker
	walkBe.entered = make(chan struct{})
	searchView := core.NewView("tag:x", "tag:x")
	searchDone := make(chan struct{})
	go func() {
		runSearchQuery(walkWorker, bus, searchView)
		close(searchDone)
	}()

	// categorize on the interactive worker: must NOT queue behind the
	// walk (the bug this split fixes)
	select {
	case <-walkBe.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("walk never blocked the walk worker")
	}
	cfg := config.Default()
	categorizeDone := make(chan struct{})
	go func() {
		categorizeThread(worker, bus, "t1", &cfg)
		close(categorizeDone)
	}()
	select {
	case <-categorizeDone:
	case <-time.After(time.Second):
		t.Fatal("categorize queued behind the search walk - the split failed")
	}
	// the walk must still be running: categorize completed on the OTHER
	// worker, not by draining the walk
	select {
	case <-searchDone:
		t.Fatal("the walk must still hold the walk worker while categorize ran")
	case <-time.After(50 * time.Millisecond):
	}

	// release the walk; the search fills its view
	walkBe.release()
	select {
	case <-searchDone:
	case <-time.After(5 * time.Second):
		t.Fatal("the walk never completed after release")
	}
	if got := searchView.Threads; len(got) != 1 || got[0].ID != "t1" {
		t.Fatalf("search view = %d threads, want the walked t1", len(got))
	}
}
