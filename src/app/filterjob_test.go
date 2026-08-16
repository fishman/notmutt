package app

import (
	"sync/atomic"
	"testing"
	"time"

	"notmutt/config"
	"notmutt/core"
	"notmutt/notmuch"
)

// fjWorker serves the filter job's revision bracket and canned
// snapshots, and records the writes. When hold is set, the first
// ActRevision signals on entered and blocks until the test closes hold
// (the guard-overlap test pins the job mid-run).
type fjWorker struct {
	rev     atomic.Uint64
	bump    atomic.Uint64 // ActNew adds this to rev (0 = nothing new)
	delta   []core.Message
	snaps   []core.Message
	tagged  atomic.Int32
	entered chan struct{}
	hold    chan struct{}
}

func (f *fjWorker) Call(a notmuch.Action) (notmuch.Reply, error) {
	switch a.Kind {
	case notmuch.ActRevision:
		if f.hold != nil {
			close(f.entered)
			f.entered = nil
			<-f.hold
			f.hold = nil
		}
		return notmuch.Reply{Rev: f.rev.Load()}, nil
	case notmuch.ActQueryMsgs:
		if a.Emit != nil {
			a.Emit(f.delta)
		}
	case notmuch.ActSnapshots:
		return notmuch.Reply{Msgs: f.snaps}, nil
	case notmuch.ActTag:
		f.tagged.Add(1)
	case notmuch.ActNew:
		if f.bump.Load() > 0 {
			f.rev.Add(f.bump.Load())
		}
	}
	return notmuch.Reply{}, nil
}

// drain collects FilterDone and JobError events until the timeout
// (the bus channel is never closed; runs are synchronous).
func drain(ch <-chan core.Event) (done []core.FilterDone, jerr []core.JobError) {
	t := time.NewTimer(time.Second)
	defer t.Stop()
	for {
		select {
		case e := <-ch:
			switch e := e.(type) {
			case core.FilterDone:
				done = append(done, e)
			case core.JobError:
				jerr = append(jerr, e)
			}
		case <-t.C:
			return
		}
	}
}

func TestFilterJob(t *testing.T) {
	cfg := config.Default()
	cfg.Accounts = map[string]config.Account{"gmail": {Preset: "gmail"}}
	st := config.NewStore(cfg)
	bus := core.NewBus()

	// the message sits in the archive folder: the folder rule fires
	// (+archive, the move tag), and the mover reports the file as a
	// source-gone skip (nothing on disk). Default config: dry-run.
	w := &fjWorker{
		delta: []core.Message{{ID: "m1"}},
		snaps: []core.Message{{ID: "m1", Tags: []string{"inbox"}, Paths: []string{"gmail/Archives/cur/1"}}},
	}
	w.rev.Store(5)
	w.bump.Store(5)

	ch := bus.Subscribe()
	newFilterJob(bus, w, st, t.TempDir(), "inbox").run()
	done, jerr := drain(ch)
	if len(done) != 1 {
		t.Fatalf("FilterDone = %+v, want 1", done)
	}
	if d := done[0]; !d.DryRun || d.Entries != 1 || d.Moves != 0 || d.Skips != 1 {
		t.Fatalf("FilterDone = %+v, want dry-run, 1 entry, 0 moves, 1 skip (source gone)", d)
	}
	if len(jerr) != 0 {
		t.Fatalf("JobError = %+v, want none", jerr)
	}
	if w.tagged.Load() != 0 {
		t.Fatalf("ActTag calls = %d, want 0 (dry-run)", w.tagged.Load())
	}

	// no delta: a revision-stable poll publishes nothing
	w.rev.Store(5)
	w.bump.Store(0)
	ch2 := bus.Subscribe()
	newFilterJob(bus, w, st, t.TempDir(), "inbox").run()
	done, jerr = drain(ch2)
	if len(done) != 0 || len(jerr) != 0 {
		t.Fatalf("no-delta poll published %v %v", done, jerr)
	}

	// guard: an overlapping run is a no-op. The first is pinned mid-run
	// (entered signals it reached the worker, hold blocks it there); the
	// second starts only after that signal, so it deterministically hits
	// the guard.
	w.rev.Store(5)
	w.bump.Store(5)
	entered := make(chan struct{})
	hold := make(chan struct{})
	w.entered = entered
	w.hold = hold
	ch3 := bus.Subscribe()
	j := newFilterJob(bus, w, st, t.TempDir(), "inbox")
	firstDone := make(chan struct{})
	go func() {
		j.run()
		close(firstDone)
	}()
	<-entered
	go j.run()
	close(hold)
	<-firstDone
	done, _ = drain(ch3)
	if len(done) != 1 {
		t.Fatalf("overlapping runs published %d FilterDone, want 1", len(done))
	}
}
