package notmuch

import (
	"context"
	"errors"
	"time"

	"notmutt/core"
)

type ActionKind int

const (
	ActOpen ActionKind = iota
	ActQuery
	ActCount
	ActThread
	ActTag
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
	Emit     func([]core.Message) bool // ActQuery only: the fill consumes chunks as the backend walks
	TagOps   []TagOp
	replyCh  chan Reply
}

type Reply struct {
	ID    uint64
	Err   error
	Msgs  []core.Message
	Count int
	UUID  string
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
	if a.Kind == ActTag || a.Kind == ActNew {
		ctx, cancel = context.WithTimeout(w.ctx, w.timeout)
	}
	defer cancel()
	r := Reply{ID: a.ID}
	var err error
	switch a.Kind {
	case ActOpen:
		err = w.backend.Open(ctx, a.Query)
	case ActQuery:
		err = w.backend.Query(ctx, a.Query, a.Limit, a.Emit)
	case ActCount:
		r.Count, err = w.backend.Count(ctx, a.Query)
	case ActThread:
		r.Msgs, err = w.backend.Thread(ctx, a.ThreadID)
	case ActTag:
		err = w.backend.Tag(ctx, a.Query, a.TagOps)
		if err == nil {
			w.bus.Publish(core.WorkerDone{Job: "tag"})
		}
	case ActRevision:
		r.UUID, r.Rev, err = w.backend.Revision(ctx)
	case ActAddresses:
		r.Addrs, err = w.backend.Addresses(ctx, a.Query)
	case ActNew:
		err = w.backend.New(ctx)
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

func actionName(k ActionKind) string {
	switch k {
	case ActOpen:
		return "open"
	case ActQuery:
		return "query"
	case ActThread:
		return "thread"
	case ActTag:
		return "tag"
	case ActRevision:
		return "revision"
	case ActNew:
		return "new"
	case ActAddresses:
		return "addresses"
	case ActClose:
		return "close"
	}
	return "unknown"
}
