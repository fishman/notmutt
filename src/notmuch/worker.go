// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package notmuch

import (
	"context"
	"errors"
	"time"

	"notmutt/core"
)

// ErrUnsupported marks an action the build cannot run: the CLI
// backend's path updates (its own `notmuch new` reconciles moved files
// one poll later). Callers treat it as a silent no-op, never a failure.
var ErrUnsupported = errors.New("not supported by this backend")

type ActionKind int

const (
	ActOpen ActionKind = iota
	ActQuery
	ActQueryMsgs
	ActCount
	ActThread
	ActSnapshots
	ActTag
	ActAddPaths
	ActRemovePaths
	ActRevision
	ActNew
	ActAddresses
	ActClose
	ActReopen
)

type Action struct {
	ID       uint64
	Kind     ActionKind
	Query    string
	ThreadID string
	Limit    int
	// Flat marks the message-level walk (the flat views: unread,
	// deleted, search): one row per matched message for ActQuery, the
	// message count for ActCount.
	Flat    bool
	Emit    func([]core.Message) bool // ActQuery/ActQueryMsgs only: the consumer collects chunks as the backend walks
	TagOps  []TagOp
	Paths   []string // ActSnapshots: the message ids; ActAddPaths/ActRemovePaths: the files
	replyCh chan Reply
}

type Reply struct {
	ID    uint64
	Err   error
	Msgs  []core.Message
	Count int
	UUID  string
	Pre   uint64
	Rev   uint64
	Paths []string
	Addrs []core.AddressEntry
}

// Worker owns backend access, actions handled serially. The lock
// budget applies to writers only - a timeout becomes ErrLockTimeout
// plus a WorkerLockTimeout event, never a blocked UI. Start must run
// before any Call; Call waits on ready so the ctx install is synced.
type Worker struct {
	bus     *core.Bus
	backend Backend
	timeout time.Duration
	actions chan Action
	ctx     context.Context
	ready   chan struct{}
}

func NewWorker(bus *core.Bus, backend Backend, timeout time.Duration) *Worker {
	return &Worker{
		bus: bus, backend: backend, timeout: timeout,
		actions: make(chan Action, 16),
		ready:   make(chan struct{}),
	}
}

func (w *Worker) Start(ctx context.Context) {
	w.ctx = ctx
	close(w.ready)
	for {
		select {
		case <-ctx.Done():
			return
		case a := <-w.actions:
			w.handle(a)
		}
	}
}

// Call is synchronous request/response; safe from any goroutine.
func (w *Worker) Call(a Action) (Reply, error) {
	<-w.ready
	a.replyCh = make(chan Reply, 1)
	select {
	case w.actions <- a:
	case <-w.ctx.Done():
		return Reply{}, w.ctx.Err()
	}
	select {
	case r := <-a.replyCh:
		return r, nil
	case <-w.ctx.Done():
		return Reply{}, w.ctx.Err()
	}
}

func (w *Worker) handle(a Action) {
	// The lock budget applies to WRITERS only: tag/new hold the write
	// lock; reads run on the read handle, MVCC-safe - the fill must
	// never be cut off mid-walk by the budget.
	ctx, cancel := context.WithCancel(w.ctx)
	if a.Kind == ActTag || a.Kind == ActNew || a.Kind == ActAddPaths || a.Kind == ActRemovePaths {
		ctx, cancel = context.WithTimeout(w.ctx, w.timeout)
	}
	defer cancel()
	r := Reply{ID: a.ID}
	var err error
	switch a.Kind {
	case ActOpen:
		err = w.backend.Open(ctx, a.Query)
	case ActQuery, ActQueryMsgs, ActCount, ActThread, ActSnapshots, ActRevision, ActAddresses:
		r = w.read(ctx, a)
	case ActTag:
		err = w.backend.Tag(ctx, a.Query, a.TagOps)
		if err == nil {
			w.bus.Publish(core.WorkerDone{Job: "tag"})
		}
	case ActAddPaths:
		err = w.backend.AddPaths(ctx, a.Paths)
		if err == nil {
			w.bus.Publish(core.WorkerDone{Job: "tag"})
		}
	case ActRemovePaths:
		err = w.backend.RemovePaths(ctx, a.Paths)
		if err == nil {
			w.bus.Publish(core.WorkerDone{Job: "tag"})
		}
	case ActNew:
		r.Pre, r.Rev, err = w.backend.New(ctx)
		if err == nil {
			w.bus.Publish(core.WorkerDone{Job: "new"})
		}
	case ActClose:
		err = w.backend.Close(ctx)
	case ActReopen:
		err = w.backend.Reopen(ctx)
	}
	if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
		err = ErrLockTimeout
		w.bus.Publish(core.WorkerLockTimeout{Kind: actionName(a.Kind)})
	}
	if err != nil {
		// non-read actions deliver their error through the dispatch err;
		// reads set r.Err in read() and leave err nil
		r.Err = err
	}
	a.replyCh <- r
}

// read runs a read action on a fresh snapshot. A read-only cgo
// handle's Xapian revision is pinned at open: a commit landed on
// ANOTHER handle (a tag op's read-write reopen, a `notmuch new`
// subprocess, a foreign process) is invisible - and can invalidate -
// reads until reopened (a stale get_document throws
// OPERATION_INVALIDATED, message.cc). Callers used to reopen by hand
// per cycle (the walk worker); every read reopens here, so no caller
// can serve a stale snapshot. Writes reopen their own handle around
// the op and skip this; the CLI backend's Reopen is a no-op.
func (w *Worker) read(ctx context.Context, a Action) Reply {
	r := Reply{ID: a.ID}
	if err := w.backend.Reopen(ctx); err != nil {
		r.Err = err
		return r
	}
	switch a.Kind {
	case ActQuery:
		r.Err = w.backend.Query(ctx, a.Query, a.Limit, a.Flat, a.Emit)
	case ActQueryMsgs:
		r.Err = w.backend.QueryMsgs(ctx, a.Query, a.Emit)
	case ActCount:
		if a.Flat {
			r.Count, r.Err = w.backend.CountMsgs(ctx, a.Query)
		} else {
			r.Count, r.Err = w.backend.Count(ctx, a.Query)
		}
	case ActThread:
		r.Msgs, r.Err = w.backend.Thread(ctx, a.ThreadID)
	case ActSnapshots:
		r.Msgs, r.Err = w.backend.Snapshots(ctx, a.Paths)
	case ActRevision:
		r.UUID, r.Rev, r.Err = w.backend.Revision(ctx)
	case ActAddresses:
		r.Addrs, r.Err = w.backend.Addresses(ctx, a.Query)
	}
	return r
}

// actionNames is the canonical action-name table: the lock-timeout
// diagnostics render ActionKind through it.
var actionNames = map[ActionKind]string{
	ActOpen:        "open",
	ActQuery:       "query",
	ActQueryMsgs:   "querymsgs",
	ActCount:       "count",
	ActThread:      "thread",
	ActSnapshots:   "snapshots",
	ActTag:         "tag",
	ActAddPaths:    "addpaths",
	ActRemovePaths: "removepaths",
	ActRevision:    "revision",
	ActNew:         "new",
	ActAddresses:   "addresses",
	ActClose:       "close",
	ActReopen:      "reopen",
}

func actionName(k ActionKind) string {
	if n, ok := actionNames[k]; ok {
		return n
	}
	return "unknown"
}
