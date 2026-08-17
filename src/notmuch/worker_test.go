package notmuch

import (
	"context"
	"errors"
	"testing"
	"time"

	"notmutt/core"
)

type fakeBackend struct {
	err   error
	addrs []core.AddressEntry
}

func (f *fakeBackend) Open(ctx context.Context, p string) error { return f.err }
func (f *fakeBackend) Close(ctx context.Context) error          { return f.err }
func (f *fakeBackend) Query(ctx context.Context, q string, limit int, emit func([]core.Message) bool) error {
	if emit != nil {
		emit([]core.Message{{ID: "m1", ThreadID: "t1"}})
	}
	return f.err
}
func (f *fakeBackend) QueryMsgs(ctx context.Context, q string, emit func([]core.Message) bool) error {
	if emit != nil {
		emit([]core.Message{{ID: "m1"}})
	}
	return f.err
}
func (f *fakeBackend) Snapshots(ctx context.Context, ids []string) ([]Message, error) {
	return []Message{{ID: "m1", Tags: []string{"inbox"}, Paths: []string{"/x/1"}}}, f.err
}
func (f *fakeBackend) Count(ctx context.Context, q string) (int, error) { return 1, f.err }
func (f *fakeBackend) Addresses(ctx context.Context, q string) ([]core.AddressEntry, error) {
	return f.addrs, f.err
}

func (f *fakeBackend) Thread(ctx context.Context, id string) ([]core.Message, error) {
	return []core.Message{{ID: "m1", ThreadID: id, References: []string{"p"}}}, f.err
}
func (f *fakeBackend) Tag(ctx context.Context, q string, ops []TagOp) error  { return f.err }
func (f *fakeBackend) AddPaths(ctx context.Context, paths []string) error    { return f.err }
func (f *fakeBackend) RemovePaths(ctx context.Context, paths []string) error { return f.err }
func (f *fakeBackend) Revision(ctx context.Context) (string, uint64, error) {
	return "uuid-1", 42, f.err
}
func (f *fakeBackend) New(ctx context.Context) (uint64, uint64, error) {
	if f.err != nil {
		return 0, 0, f.err
	}
	return 41, 42, nil
}

func TestWorkerCallQuery(t *testing.T) {
	bus := core.NewBus()
	w := NewWorker(bus, &fakeBackend{}, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)
	var got []core.Message
	rpl, err := w.Call(Action{Kind: ActQuery, Query: "tag:inbox", Limit: 10, Emit: func(msgs []core.Message) bool {
		got = append(got, msgs...)
		return true
	}})
	if err != nil {
		t.Fatal(err)
	}
	if rpl.Err != nil || len(got) != 1 || got[0].ID != "m1" {
		t.Fatalf("reply wrong: %+v %v", rpl, err)
	}
}

func TestWorkerTagPublishesWorkerDone(t *testing.T) {
	bus := core.NewBus()
	w := NewWorker(bus, &fakeBackend{}, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)
	ch := bus.Subscribe()
	if _, err := w.Call(Action{Kind: ActTag, Query: "id:x", TagOps: []TagOp{{Tag: "unread", Add: false}}}); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-ch:
		if _, ok := e.(core.WorkerDone); !ok {
			t.Fatalf("expected WorkerDone, got %T", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no WorkerDone published")
	}
}

type blockingBackend struct {
	inner Backend
}

func (b *blockingBackend) Open(ctx context.Context, p string) error { return b.inner.Open(ctx, p) }
func (b *blockingBackend) Close(ctx context.Context) error          { return b.inner.Close(ctx) }
func (b *blockingBackend) Count(ctx context.Context, q string) (int, error) {
	return b.inner.Count(ctx, q)
}
func (b *blockingBackend) Query(ctx context.Context, q string, limit int, emit func([]core.Message) bool) error {
	time.Sleep(200 * time.Millisecond) // 4x the 50ms test budget
	return b.inner.Query(ctx, q, limit, emit)
}
func (b *blockingBackend) QueryMsgs(ctx context.Context, q string, emit func([]core.Message) bool) error {
	return b.inner.QueryMsgs(ctx, q, emit)
}
func (b *blockingBackend) Snapshots(ctx context.Context, ids []string) ([]Message, error) {
	return b.inner.Snapshots(ctx, ids)
}
func (b *blockingBackend) Addresses(ctx context.Context, q string) ([]core.AddressEntry, error) {
	return b.inner.Addresses(ctx, q)
}

func (b *blockingBackend) Thread(ctx context.Context, id string) ([]core.Message, error) {
	return b.inner.Thread(ctx, id)
}
func (b *blockingBackend) Tag(ctx context.Context, q string, ops []TagOp) error {
	<-ctx.Done()
	return ctx.Err()
}
func (b *blockingBackend) AddPaths(ctx context.Context, paths []string) error {
	<-ctx.Done()
	return ctx.Err()
}
func (b *blockingBackend) RemovePaths(ctx context.Context, paths []string) error {
	<-ctx.Done()
	return ctx.Err()
}
func (b *blockingBackend) Revision(ctx context.Context) (string, uint64, error) {
	return b.inner.Revision(ctx)
}
func (b *blockingBackend) New(ctx context.Context) (uint64, uint64, error) { return b.inner.New(ctx) }

// TestWorkerLockTimeout pins the WRITER budget: tag holds notmuch's
// write lock, so a hung tag errors out as ErrLockTimeout after the
// budget - never a blocked UI.
func TestWorkerLockTimeout(t *testing.T) {
	bus := core.NewBus()
	w := NewWorker(bus, &blockingBackend{inner: &fakeBackend{}}, 50*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)
	ch := bus.Subscribe()
	if _, err := w.Call(Action{Kind: ActTag, Query: "id:x", TagOps: []TagOp{{Tag: "unread", Add: false}}}); err != nil {
		t.Fatal(err)
	}
	select {
	case e := <-ch:
		if _, ok := e.(core.WorkerLockTimeout); !ok {
			t.Fatalf("expected WorkerLockTimeout, got %T", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no lock timeout event")
	}
}

// TestWorkerReadsUnbudgeted pins the read/write split: a query that
// runs longer than the lock budget still completes - reads run on the
// read handle, MVCC-safe, and the fill must never be cut off mid-walk.
func TestWorkerReadsUnbudgeted(t *testing.T) {
	bus := core.NewBus()
	w := NewWorker(bus, &blockingBackend{inner: &fakeBackend{}}, 50*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)
	rpl, err := w.Call(Action{Kind: ActQuery, Query: "tag:inbox"})
	if err != nil {
		t.Fatal(err)
	}
	if rpl.Err != nil {
		t.Fatalf("unbudgeted read must not hit the lock budget: %v", rpl.Err)
	}
}

type killBackend struct {
	inner Backend
}

func (b *killBackend) Open(ctx context.Context, p string) error { return b.inner.Open(ctx, p) }
func (b *killBackend) Close(ctx context.Context) error          { return b.inner.Close(ctx) }
func (b *killBackend) Query(ctx context.Context, q string, limit int, emit func([]core.Message) bool) error {
	return b.inner.Query(ctx, q, limit, emit)
}
func (b *killBackend) QueryMsgs(ctx context.Context, q string, emit func([]core.Message) bool) error {
	return b.inner.QueryMsgs(ctx, q, emit)
}
func (b *killBackend) Snapshots(ctx context.Context, ids []string) ([]Message, error) {
	return b.inner.Snapshots(ctx, ids)
}
func (b *killBackend) Count(ctx context.Context, q string) (int, error) {
	return b.inner.Count(ctx, q)
}
func (b *killBackend) Thread(ctx context.Context, id string) ([]core.Message, error) {
	return b.inner.Thread(ctx, id)
}
func (b *killBackend) Tag(ctx context.Context, q string, ops []TagOp) error {
	<-ctx.Done()
	return errors.New("signal: killed")
}
func (b *killBackend) AddPaths(ctx context.Context, paths []string) error {
	<-ctx.Done()
	return errors.New("signal: killed")
}
func (b *killBackend) RemovePaths(ctx context.Context, paths []string) error {
	<-ctx.Done()
	return errors.New("signal: killed")
}
func (b *killBackend) Revision(ctx context.Context) (string, uint64, error) {
	return b.inner.Revision(ctx)
}
func (b *killBackend) New(ctx context.Context) (uint64, uint64, error) { return b.inner.New(ctx) }
func (b *killBackend) Addresses(ctx context.Context, q string) ([]core.AddressEntry, error) {
	return b.inner.Addresses(ctx, q)
}

// exec.CommandContext reports a killed process as "signal: killed", not
// context.DeadlineExceeded; the worker must map that shape too. The
// kill shape only exists on the budgeted path (tag/new).
func TestWorkerMapsKillErrorToLockTimeout(t *testing.T) {
	bus := core.NewBus()
	w := NewWorker(bus, &killBackend{inner: &fakeBackend{}}, 50*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)
	ch := bus.Subscribe()
	rpl, err := w.Call(Action{Kind: ActTag, Query: "id:x", TagOps: []TagOp{{Tag: "unread", Add: false}}})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(rpl.Err, ErrLockTimeout) {
		t.Fatalf("expected ErrLockTimeout in reply, got %v", rpl.Err)
	}
	select {
	case e := <-ch:
		if _, ok := e.(core.WorkerLockTimeout); !ok {
			t.Fatalf("expected WorkerLockTimeout, got %T", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no WorkerLockTimeout event")
	}
}

func TestWorkerBackendError(t *testing.T) {
	bus := core.NewBus()
	w := NewWorker(bus, &fakeBackend{err: errors.New("boom")}, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)
	rpl, err := w.Call(Action{Kind: ActRevision})
	if err != nil {
		t.Fatal(err)
	}
	if rpl.Err == nil {
		t.Fatal("expected backend error in reply")
	}
}

func TestWorkerAddresses(t *testing.T) {
	bus := core.NewBus()
	w := NewWorker(bus, &fakeBackend{addrs: []core.AddressEntry{
		{Addr: "a@b.c", Name: "Ann"},
		{Addr: "bob@x.io"},
	}}, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)
	rpl, err := w.Call(Action{Kind: ActAddresses, Query: "*"})
	if err != nil {
		t.Fatal(err)
	}
	if rpl.Err != nil || len(rpl.Addrs) != 2 || rpl.Addrs[1].Addr != "bob@x.io" {
		t.Fatalf("reply wrong: %+v %v", rpl, err)
	}
}

// TestWorkerDeltaActions pins the filter engine's action surface: the
// lastmod walk emits ids, Snapshots resolves them to tags + paths, and
// the mover's path updates round-trip and publish WorkerDone like tag
// ops (the revision bump drives the refresh).
func TestWorkerDeltaActions(t *testing.T) {
	bus := core.NewBus()
	w := NewWorker(bus, &fakeBackend{}, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)
	ch := bus.Subscribe()
	var got []core.Message
	if _, err := w.Call(Action{Kind: ActQueryMsgs, Query: "lastmod:1..2", Emit: func(msgs []core.Message) bool {
		got = append(got, msgs...)
		return true
	}}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "m1" {
		t.Fatalf("delta walk wrong: %+v", got)
	}
	rpl, err := w.Call(Action{Kind: ActSnapshots, Paths: []string{"m1"}})
	if err != nil {
		t.Fatal(err)
	}
	if rpl.Err != nil || len(rpl.Msgs) != 1 || rpl.Msgs[0].Tags[0] != "inbox" || rpl.Msgs[0].Paths[0] != "/x/1" {
		t.Fatalf("snapshots wrong: %+v", rpl)
	}
	for _, kind := range []ActionKind{ActAddPaths, ActRemovePaths} {
		if _, err := w.Call(Action{Kind: kind, Paths: []string{"/x/1"}}); err != nil {
			t.Fatal(err)
		}
		select {
		case e := <-ch:
			if _, ok := e.(core.WorkerDone); !ok {
				t.Fatalf("expected WorkerDone, got %T", e)
			}
		case <-time.After(time.Second):
			t.Fatalf("no WorkerDone published for %v", kind)
		}
	}
}

// TestWorkerPathWritersBudgeted pins the path updates in the WRITER
// set: AddPaths/RemovePaths reopen the DB read-write, so a hung op must
// error out as ErrLockTimeout like tag/new - never a blocked UI.
func TestWorkerPathWritersBudgeted(t *testing.T) {
	bus := core.NewBus()
	w := NewWorker(bus, &blockingBackend{inner: &fakeBackend{}}, 50*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)
	for _, kind := range []ActionKind{ActAddPaths, ActRemovePaths} {
		rpl, err := w.Call(Action{Kind: kind, Paths: []string{"/x/1"}})
		if err != nil {
			t.Fatal(err)
		}
		if !errors.Is(rpl.Err, ErrLockTimeout) {
			t.Fatalf("%v must hit the lock budget, got %v", kind, rpl.Err)
		}
	}
}
