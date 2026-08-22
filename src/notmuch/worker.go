// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package notmuch

import (
	"context"
	"errors"
	"time"

	"notmutt/core"
)

// ErrUnsupported marks a backend action the build cannot run: the CLI
// backend's path updates (there is no add/remove-file command; its own
// `notmuch new` reconciles moved files one poll later). Callers treat
// it as a silent no-op, never as a failure to report.
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

// Worker owns backend access. Actions are handled serially; every op runs
// under a lock budget - a timeout becomes ErrLockTimeout plus a
// WorkerLockTimeout event, never a blocked UI. Start must run before
// any Call; Call waits on ready so the ctx install is synchronized.
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
	// The lock budget applies to WRITERS only: tag/new hold notmuch's
	// write lock; reads (query, count, thread, revision) run on the
	// read handle and are MVCC-safe, so the fill must never be cut off
	// mid-walk by the budget.
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
	case ActQuery:
		err = w.backend.Query(ctx, a.Query, a.Limit, a.Flat, a.Emit)
	case ActQueryMsgs:
		err = w.backend.QueryMsgs(ctx, a.Query, a.Emit)
	case ActCount:
		if a.Flat {
			r.Count, err = w.backend.CountMsgs(ctx, a.Query)
		} else {
			r.Count, err = w.backend.Count(ctx, a.Query)
		}
	case ActThread:
		r.Msgs, err = w.backend.Thread(ctx, a.ThreadID)
	case ActSnapshots:
		r.Msgs, err = w.backend.Snapshots(ctx, a.Paths)
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
	case ActRevision:
		r.UUID, r.Rev, err = w.backend.Revision(ctx)
	case ActAddresses:
		r.Addrs, err = w.backend.Addresses(ctx, a.Query)
	case ActNew:
		r.Pre, r.Rev, err = w.backend.New(ctx)
		if err == nil {
			w.bus.Publish(core.WorkerDone{Job: "new"})
		}
	case ActClose:
		err = w.backend.Close(ctx)
	}
	if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
		err = ErrLockTimeout
		w.bus.Publish(core.WorkerLockTimeout{Kind: actionName(a.Kind)})
	}
	r.Err = err
	a.replyCh <- r
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
}

func actionName(k ActionKind) string {
	if n, ok := actionNames[k]; ok {
		return n
	}
	return "unknown"
}
