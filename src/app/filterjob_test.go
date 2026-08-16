package app

import (
	"os"
	"path/filepath"
	"strings"
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

// TestRunFilterPipeline: the shared poll body - a revision bump after
// ActNew classifies the delta and moves (the manual trigger's effect);
// a quiet mailbox (no bump) produces no classification pass.
func TestRunFilterPipeline(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "mail")
	for _, d := range []string{"Archives", "INBOX"} {
		if err := os.MkdirAll(filepath.Join(root, "gmail", d, "cur"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(root, "gmail", "Archives", "cur", "1"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(root, "gmail", "INBOX", "cur", "2"), []byte("x"), 0o600)

	cfg := config.Default()
	cfg.Accounts = map[string]config.Account{"gmail": {Preset: "gmail"}}
	cfg.Filter.DryRun = false
	w := &fjWorker{
		delta: []core.Message{{ID: "m1"}, {ID: "m2"}},
		snaps: []core.Message{
			{ID: "m1", Tags: []string{"inbox"}, Paths: []string{"gmail/Archives/cur/1"}},
			{ID: "m2", Tags: []string{"inbox", "spam"}, Paths: []string{"gmail/INBOX/cur/2"}},
		},
	}
	w.rev.Store(0)
	w.bump.Store(5)

	changed, rep, mr, err := runFilterPipeline(w, cfg, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || rep == nil || mr == nil {
		t.Fatalf("changed=%v rep=%v mr=%v, want a classification pass", changed, rep, mr)
	}
	if len(rep.Entries) != 2 {
		t.Fatalf("entries=%d, want 2", len(rep.Entries))
	}
	// the mover consumed the report: the archive entry is already home
	// (skip), the inbox entry has no move folder - one move entry total
	if len(mr.Moves) != 1 || mr.Moves[0].Skip == "" {
		t.Fatalf("moves = %+v, want the single already-home skip", mr.Moves)
	}
	if w.tagged.Load() == 0 {
		t.Fatal("no tag writes on a live poll")
	}

	// a quiet poll: the revision did not move, no classification
	w.bump.Store(0)
	changed, rep, mr, err = runFilterPipeline(w, cfg, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("quiet poll produced a classification pass")
	}
}

// TestRunPoll: the headless poll command's contract - a successful run
// touches the poll stamp (the cross-process wake-up for running
// clients), a quiet poll leaves it alone, and a disabled filter
// degrades to a plain new that still stamps.
func TestRunPoll(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	dir := t.TempDir()
	root := filepath.Join(dir, "mail")
	for _, d := range []string{"Archives", "INBOX"} {
		if err := os.MkdirAll(filepath.Join(root, "gmail", d, "cur"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(root, "gmail", "Archives", "cur", "1"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(root, "gmail", "INBOX", "cur", "2"), []byte("x"), 0o600)

	cfg := config.Default()
	cfg.Accounts = map[string]config.Account{"gmail": {Preset: "gmail"}}
	cfg.Filter.DryRun = false
	w := &fjWorker{
		delta: []core.Message{{ID: "m1"}, {ID: "m2"}},
		snaps: []core.Message{
			{ID: "m1", Tags: []string{"inbox"}, Paths: []string{"gmail/Archives/cur/1"}},
			{ID: "m2", Tags: []string{"inbox", "spam"}, Paths: []string{"gmail/INBOX/cur/2"}},
		},
	}
	w.rev.Store(0)
	w.bump.Store(5)

	line, err := runPoll(w, cfg, root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "poll:") {
		t.Fatalf("summary = %q", line)
	}
	fi, err := os.Stat(pollStampPath())
	if err != nil {
		t.Fatalf("stamp missing after a successful poll: %v", err)
	}
	stamp := fi.ModTime()

	// a quiet poll changes nothing, the stamp stays untouched
	w.bump.Store(0)
	line, err = runPoll(w, cfg, root)
	if err != nil {
		t.Fatal(err)
	}
	if line != "poll: no new mail" {
		t.Fatalf("quiet poll = %q", line)
	}
	fi, err = os.Stat(pollStampPath())
	if err != nil || !fi.ModTime().Equal(stamp) {
		t.Fatalf("stamp changed on a quiet poll: %v %v", fi, err)
	}

	// a disabled filter degrades to a plain new that still stamps
	cfg.Filter.Enabled = false
	line, err = runPoll(w, cfg, root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "filter disabled") {
		t.Fatalf("disabled-filter summary = %q", line)
	}
	if _, err := os.Stat(pollStampPath()); err != nil {
		t.Fatalf("stamp missing after the plain-new poll: %v", err)
	}
}
