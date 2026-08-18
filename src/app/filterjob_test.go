// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"os"
	"os/exec"
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
// ActNew signals on entered and blocks until the test closes hold
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
		if f.hold != nil {
			close(f.entered)
			f.entered = nil
			<-f.hold
			f.hold = nil
		}
		pre := f.rev.Load()
		if f.bump.Load() > 0 {
			f.rev.Add(f.bump.Load())
		}
		return notmuch.Reply{Pre: pre, Rev: f.rev.Load()}, nil
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

	line, _, err := runPoll(w, cfg, root, pollSpec{})
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
	line, _, err = runPoll(w, cfg, root, pollSpec{})
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
	line, _, err = runPoll(w, cfg, root, pollSpec{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "filter disabled") {
		t.Fatalf("disabled-filter summary = %q", line)
	}
	if _, err := os.Stat(pollStampPath()); err != nil {
		t.Fatalf("stamp missing after the plain-new poll: %v", err)
	}
	// ... and refuses a fixed-window replay
	if _, _, err := runPoll(w, cfg, root, pollSpec{windowed: true, from: 0, to: 5}); err == nil {
		t.Fatal("a replay on a disabled filter must fail")
	}
}

// TestRunPollWindow: the reproducibility contract - a fixed (from, to]
// window skips the new run and the revision capture (the replay of a
// stored diff), reclassifies the SAME bracket, and must produce
// byte-identical output on every replay. --apply is the one-shot live
// override of the dry-run config, the config untouched, and stamps.
func TestRunPollWindow(t *testing.T) {
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
	w := &fjWorker{
		delta: []core.Message{{ID: "m1"}, {ID: "m2"}},
		snaps: []core.Message{
			{ID: "m1", Tags: []string{"inbox"}, Paths: []string{"gmail/Archives/cur/1"}},
			{ID: "m2", Tags: []string{"inbox", "spam"}, Paths: []string{"gmail/INBOX/cur/2"}},
		},
	}
	w.rev.Store(5)

	// replay 1: the stored window, dry-run
	line, diff, err := runPoll(w, cfg, root, pollSpec{windowed: true, from: 0, to: 5})
	if err != nil {
		t.Fatal(err)
	}
	if line != "poll: dry-run: 0..5: 2 entries, 0 moved, 1 skipped" {
		t.Fatalf("summary = %q", line)
	}
	if w.rev.Load() != 5 {
		t.Fatalf("window replay touched the revision: %d", w.rev.Load())
	}
	if w.tagged.Load() != 0 {
		t.Fatalf("dry-run replay wrote tags: %d", w.tagged.Load())
	}
	if _, err := os.Stat(pollStampPath()); err == nil {
		t.Fatal("a dry-run replay must not touch the stamp")
	}
	want := []string{
		"+archive +gmail -inbox  gmail/Archives/cur/1  (skip: already home)",
		"+gmail -spam  gmail/INBOX/cur/2",
	}
	if strings.Join(diff, "\n") != strings.Join(want, "\n") {
		t.Fatalf("diff = %q, want %q", diff, want)
	}

	// replay 2: the same window must reproduce the same output
	line2, diff2, err := runPoll(w, cfg, root, pollSpec{windowed: true, from: 0, to: 5})
	if err != nil {
		t.Fatal(err)
	}
	if line2 != line || strings.Join(diff2, "\n") != strings.Join(diff, "\n") {
		t.Fatalf("replay differs:\n%q\n%q", line, line2)
	}

	// apply: the one-shot live override, the config untouched
	line, _, err = runPoll(w, cfg, root, pollSpec{windowed: true, from: 0, to: 5, apply: true})
	if err != nil {
		t.Fatal(err)
	}
	if line != "poll: applied: 0..5: 2 entries, 0 moved, 1 skipped" {
		t.Fatalf("summary = %q", line)
	}
	if w.tagged.Load() == 0 {
		t.Fatal("apply wrote no tags")
	}
	if cfg.Filter.DryRun != true {
		t.Fatal("--apply must not touch the config")
	}
	if _, err := os.Stat(pollStampPath()); err != nil {
		t.Fatal("an applied replay must stamp")
	}
}

func TestParsePollSpec(t *testing.T) {
	ok := func(args ...string) pollSpec {
		spec, err := parsePollSpec(args)
		if err != nil {
			t.Fatalf("parsePollSpec(%v): %v", args, err)
		}
		return spec
	}
	if spec := ok(); spec.windowed || spec.apply {
		t.Fatalf("bare poll = %+v, want the default", spec)
	}
	if spec := ok("--apply"); !spec.apply || spec.windowed {
		t.Fatalf("--apply = %+v", spec)
	}
	if spec := ok("--from", "5", "--to", "10"); !spec.windowed || spec.from != 5 || spec.to != 10 {
		t.Fatalf("window = %+v", spec)
	}
	if _, err := parsePollSpec([]string{"--from", "5"}); err == nil {
		t.Fatal("--from alone must fail")
	}
	if _, err := parsePollSpec([]string{"--to", "5"}); err == nil {
		t.Fatal("--to alone must fail")
	}
	if _, err := parsePollSpec([]string{"--from", "10", "--to", "5"}); err == nil {
		t.Fatal("an inverted window must fail")
	}
	if _, err := parsePollSpec([]string{"--bogus"}); err == nil {
		t.Fatal("an unknown flag must fail")
	}
}

// TestPollReproScript: the harness contract - diff stores the poll
// output, check replays the stored window and must reproduce it, and
// apply passes --apply. The notmutt binary is a stub that mirrors the
// poll's deterministic output for any poll invocation, so the match
// and mismatch paths are exercised against it.
func TestPollReproScript(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	stub := filepath.Join(cache, "notmutt-stub")
	stubBody := "#!/bin/sh\n" +
		"case \"$*\" in\n" +
		"*--apply*) echo \"$@\" ;;\n" +
		"*poll*) echo \"poll: dry-run: 1..5: 1 entries, 0 moved, 0 skipped\"; echo \"+archive gmail/INBOX/cur/1\" ;;\n" +
		"*) echo \"$@\" ;;\n" +
		"esac\n"
	os.WriteFile(stub, []byte(stubBody), 0o700)
	t.Setenv("NOTMUTT", stub)

	script := filepath.Join("..", "..", "scripts", "poll-repro.sh")
	run := func(args ...string) (string, error) {
		out, err := exec.Command("sh", append([]string{script}, args...)...).CombinedOutput()
		return string(out), err
	}

	// diff: the bare poll invocation, output stored
	if out, err := run(); err != nil {
		t.Fatalf("diff: %v: %s", err, out)
	}
	diffFile := filepath.Join(cache, "notmutt", "poll-repro", "diff.txt")
	stored := "poll: dry-run: 1..5: 1 entries, 0 moved, 0 skipped\n+archive gmail/INBOX/cur/1\n"
	got, err := os.ReadFile(diffFile)
	if err != nil || string(got) != stored {
		t.Fatalf("diff.txt = %q (err %v), want the stored poll output", got, err)
	}

	// check: replays the stored window and reproduces it
	out, err := run("check")
	if err != nil || !strings.Contains(out, "reproducible") {
		t.Fatalf("check = %q (err %v), want reproducible", out, err)
	}

	// tampering breaks the reproduction
	f, err := os.OpenFile(diffFile, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("tampered\n")
	f.Close()
	out, err = run("check")
	if err == nil || !strings.Contains(out, "MISMATCH") {
		t.Fatalf("tampered check = %q (err %v), want MISMATCH and a nonzero exit", out, err)
	}

	// apply: replays the stored window with --apply
	os.WriteFile(diffFile, []byte(stored), 0o600)
	out, err = run("apply")
	if err != nil || !strings.Contains(out, "--apply") {
		t.Fatalf("apply = %q (err %v), want the --apply invocation", out, err)
	}

	// an unknown command is usage, a missing diff is an error
	if out, err = run("bogus"); err == nil || !strings.Contains(out, "poll-repro") {
		t.Fatalf("bogus = %q (err %v), want usage", out, err)
	}
	os.Remove(diffFile)
	if out, err = run("check"); err == nil {
		t.Fatalf("check without a stored diff = %q, want an error", out)
	}
}

// TestRunPollConfig: the dry-run uses the config files, not defaults -
// a fixture config dir (the NOTMUTT_CONFIG path pollOnce takes) whose
// filters.toml header rule and accounts.toml account with its hard-tag
// folder map must show up in the replayed diff: +work from the rule,
// +gmail from the account, +archive from the hard-tag folder map.
func TestRunPollConfig(t *testing.T) {
	conf := t.TempDir()
	os.WriteFile(filepath.Join(conf, "filters.toml"), []byte(
		"[[filter.header-rules]]\nquery = \"from:acme\"\nadd = [\"work\"]\n"), 0o600)
	os.WriteFile(filepath.Join(conf, "accounts.toml"), []byte(
		"[accounts.gmail]\nfolder = \"gmail\"\n\n"+
			"[accounts.gmail.folders]\narchive = \"Archives\"\ninbox = \"INBOX\"\n"), 0o600)
	t.Setenv("NOTMUTT_CONFIG", conf)
	cfg, err := config.Load(configDir())
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	root := filepath.Join(dir, "mail")
	os.MkdirAll(filepath.Join(root, "gmail", "Archives", "cur"), 0o700)
	os.WriteFile(filepath.Join(root, "gmail", "Archives", "cur", "1"), []byte("x"), 0o600)
	w := &fjWorker{
		delta: []core.Message{{ID: "m1"}},
		snaps: []core.Message{{ID: "m1", Tags: []string{"inbox"}, Paths: []string{"gmail/Archives/cur/1"}}},
	}
	w.rev.Store(5)

	line, diff, err := runPoll(w, cfg, root, pollSpec{windowed: true, from: 0, to: 5})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(line, "dry-run: 0..5: 1 entries") {
		t.Fatalf("summary = %q", line)
	}
	joined := strings.Join(diff, "\n")
	for _, want := range []string{"+work", "+gmail", "+archive", "-inbox"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("diff lacks %s (the rule/account/hard-tag from the config): %q", want, diff)
		}
	}
}
