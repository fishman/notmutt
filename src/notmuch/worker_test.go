package notmuch

import (
	"context"
	"errors"
	"testing"
	"time"

	"notmutt/core"
)

type fakeBackend struct {
	err error
}

func (f *fakeBackend) Open(ctx context.Context, p string) error { return f.err }
func (f *fakeBackend) Close(ctx context.Context) error          { return f.err }
func (f *fakeBackend) Query(ctx context.Context, q string, l int) ([]core.Message, error) {
	return []core.Message{{ID: "m1", ThreadID: "t1"}}, f.err
}
func (f *fakeBackend) Thread(ctx context.Context, id string) ([]core.Message, error) {
	return []core.Message{{ID: "m1", ThreadID: id, References: []string{"p"}}}, f.err
}
func (f *fakeBackend) Tag(ctx context.Context, q string, ops []TagOp) error { return f.err }
func (f *fakeBackend) Revision(ctx context.Context) (string, uint64, error) {
	return "uuid-1", 42, f.err
}
func (f *fakeBackend) New(ctx context.Context) error { return f.err }

func TestWorkerCallQuery(t *testing.T) {
	bus := core.NewBus()
	w := NewWorker(bus, &fakeBackend{}, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)
	rpl, err := w.Call(Action{Kind: ActQuery, Query: "tag:inbox", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if rpl.Err != nil || len(rpl.Msgs) != 1 || rpl.Msgs[0].ID != "m1" {
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
func (b *blockingBackend) Query(ctx context.Context, q string, l int) ([]core.Message, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (b *blockingBackend) Thread(ctx context.Context, id string) ([]core.Message, error) {
	return b.inner.Thread(ctx, id)
}
func (b *blockingBackend) Tag(ctx context.Context, q string, ops []TagOp) error {
	return b.inner.Tag(ctx, q, ops)
}
func (b *blockingBackend) Revision(ctx context.Context) (string, uint64, error) {
	return b.inner.Revision(ctx)
}
func (b *blockingBackend) New(ctx context.Context) error { return b.inner.New(ctx) }

func TestWorkerLockTimeout(t *testing.T) {
	bus := core.NewBus()
	w := NewWorker(bus, &blockingBackend{inner: &fakeBackend{}}, 50*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)
	ch := bus.Subscribe()
	if _, err := w.Call(Action{Kind: ActQuery, Query: "tag:inbox"}); err != nil {
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

type killBackend struct {
	inner Backend
}

func (b *killBackend) Open(ctx context.Context, p string) error { return b.inner.Open(ctx, p) }
func (b *killBackend) Close(ctx context.Context) error          { return b.inner.Close(ctx) }
func (b *killBackend) Query(ctx context.Context, q string, l int) ([]core.Message, error) {
	<-ctx.Done()
	return nil, errors.New("signal: killed")
}
func (b *killBackend) Thread(ctx context.Context, id string) ([]core.Message, error) {
	return b.inner.Thread(ctx, id)
}
func (b *killBackend) Tag(ctx context.Context, q string, ops []TagOp) error {
	return b.inner.Tag(ctx, q, ops)
}
func (b *killBackend) Revision(ctx context.Context) (string, uint64, error) {
	return b.inner.Revision(ctx)
}
func (b *killBackend) New(ctx context.Context) error { return b.inner.New(ctx) }

// exec.CommandContext reports a killed process as "signal: killed", not
// context.DeadlineExceeded; the worker must map that shape too.
func TestWorkerMapsKillErrorToLockTimeout(t *testing.T) {
	bus := core.NewBus()
	w := NewWorker(bus, &killBackend{inner: &fakeBackend{}}, 50*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Start(ctx)
	ch := bus.Subscribe()
	rpl, err := w.Call(Action{Kind: ActQuery, Query: "tag:inbox"})
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
