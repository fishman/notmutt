# Reconciling Poll Classification Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the filter poll classify `(lastClassified, currentRevision]` on the notmuch database revision (not just the bracket its own `notmuch new` discovers), so tag ops, path moves, draft retires, and sibling-process writes all get the folder rules run on them.

**Architecture:** Gate the poll on a persisted high-water revision `L` (a 0600 file beside the poll stamp) instead of `new`'s discovery bracket. Add a cross-process `flock` to the mover's live move path (poll mover best-effort skip, apply mover bounded wait) so concurrent movers cannot double-copy, and make `copyFile` temp-then-rename so a concurrent `notmuch new` never indexes a half-published destination. The engine, folder-rule derivation, and exclusive-group model are untouched.

**Tech Stack:** Go. `syscall.Flock` (stdlib, linux + darwin), `os.Rename` for atomic publish, existing `notmutt/config`, `notmutt/filter`, `notmutt/notmuch`, `notmutt/lib/testutil`.

**Spec:** `docs/superpowers/specs/2026-09-07-poll-classify-reconcile-design.md`. Option 3 of the design (apply move-before-tag) is already landed and committed (`233b6cc` `fix(app)`).

---

## Context the executor needs (read this first)

The poll body today is `pollDiff` (`src/app/app.go:1402`), shared by three callers:

- `runFilterPipeline` (`src/app/filterjob.go:91`) - the in-client poll (refresher interval, manual refresh key). Called by `filterJob.run` and by `runPoll`.
- `runPoll` (`src/app/app.go:1357`) - the `notmutt poll` CLI body.
- `indexWrite` (`src/app/send.go:134`) - the draft-save/sent-fcc write-site immediacy.

`pollDiff(worker, cfg, root, spec, progress)` returns `(rep, mr, win, err)` where `win` is a `"pre..cur"` string (or `""` when there is nothing to classify). Its fresh branch (non-`windowed`) calls `ActNew`, gets `rpl.Pre`/`rpl.Rev` (the `(pre, cur]` lastmod bracket its own `notmuch new` indexed), and returns early when `cur == pre`. That early return is the bug: the DB revision advances on every write (tag ops, path adds/removes, a sibling's `new`), but a poll whose `new` found nothing sees `cur == pre` and never classifies the out-of-band mutations.

The reconciling design replaces that fresh branch with: index new mail, read `L` (absent -> `L = pre`, so a first run baselines to the present and never backfills), read `cur` as the current revision, classify `(L, cur]` when `cur > L`, and after an *applied* run persist the post-run revision as the new `L`.

This plan makes the reconcile opt-in via a new `pollSpec.reconcile` field so `indexWrite` keeps its exact today-behavior (classify only its own `new` bracket, never read or advance `L`).

The mover (`src/filter/mover.go`) does `copy-then-delete` over raw filesystem calls with no lock; two concurrent live movers can both copy a source before either deletes it. `Move` resolves accounts, walks entries, copies every file that should move, then deletes sources and issues `ActAddPaths`/`ActRemovePaths`.

Test fixtures to know:
- `fjWorker` (`src/app/filterjob_test.go:23`): `Call` handles `ActRevision` -> `Reply{Rev: rev}`, `ActNew` -> `Reply{Pre: oldRev, Rev: oldRev+bump}` (bump 0 = nothing new), `ActSnapshots` -> canned snaps, `ActTag` increments a counter. It does **not** bump `rev` on tag writes.
- `fakeWorker` (`src/filter/filter_test.go`): canned `ActQueryMsgs`/`ActSnapshots`, records tag/path ops.
- Cache-hermeticity convention: `os.UserCacheDir()` is redirected per-test with `t.Setenv("XDG_CACHE_HOME", t.TempDir())` (`TestRunPoll` and `TestRunPollWindow` already do this). Any new path derived from `os.UserCacheDir()` inherits the same convention.

All builds/tests run from `src/`: `cd src && go test ./...`.

Code commits carry no AI marker and no co-author line. ASCII only.

---

## File map

- Modify: `src/app/app.go` - `pollSpec` gains `reconcile bool`; `runPoll` sets it for fresh runs; `pollDiff` becomes a dispatch; add `runClassify`, `freshCapture`, `pollReconcile`.
- Create: `src/app/classify.go` - `lastClassifyPath`, `readLastClassify`, `writeLastClassify` (the L store).
- Create: `src/app/classify_test.go` - store tests.
- Modify: `src/app/filterjob.go` - `runFilterPipeline` sets `reconcile`; `classifyDelta` logs a mover batch-skip.
- Modify: `src/app/filterjob_test.go` - 4 reconcile tests + XDG redirects in `TestFilterJob`/`TestRunFilterPipeline`.
- Modify: `src/filter/mover.go` - `MoveReport.Skip`, `Mover.lockStrict`, `moverLockPath`, `lockLive`, `Move` integration, `copyFile` temp-then-rename; imports `syscall`, `time`.
- Create: `src/filter/mover_lock_test.go` - flock + temp-rename tests.
- Modify: `src/filter/filter_test.go` - XDG redirects in 5 live-mover tests.
- Modify: `src/app/apply_test.go` - XDG redirects in 2 live-mover tests.

---

## Task 1: The last-classified revision (L) store

**Files:**
- Create: `src/app/classify.go`
- Test: `src/app/classify_test.go`

- [ ] **Step 1: Write the failing tests**

Create `src/app/classify_test.go`:

```go
// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"os"
	"testing"
)

func TestLastClassifyRoundTrip(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := writeLastClassify(7); err != nil {
		t.Fatal(err)
	}
	got, ok := readLastClassify()
	if !ok || got != 7 {
		t.Fatalf("read = %d, %v, want 7, true", got, ok)
	}
}

func TestLastClassifyMissing(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if _, ok := readLastClassify(); ok {
		t.Fatal("a missing file must read as no-L")
	}
}

func TestLastClassifyCorrupt(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := lastClassifyPath()
	if err := os.MkdirAll(dir[:len(dir)-len("last-classify")], 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, []byte("not-a-number"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := readLastClassify(); ok {
		t.Fatal("a corrupt file must read as no-L (re-classify the bracket, never a full backfill)")
	}
}

func TestLastClassifyNeverRegresses(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if err := writeLastClassify(10); err != nil {
		t.Fatal(err)
	}
	if err := writeLastClassify(5); err != nil {
		t.Fatal(err)
	}
	got, ok := readLastClassify()
	if !ok || got != 10 {
		t.Fatalf("floor regressed to %d, %v, want 10, true (a concurrent advance must win)", got, ok)
	}
}
```

`lastClassifyPath()` has no parent directory on a fresh temp cache, so `writeLastClassify` must `MkdirAll` it (the corrupt test creates the dir by hand so it can drop the file).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./app -run TestLastClassify -count=1`
Expected: build failure - `undefined: writeLastClassify` / `undefined: readLastClassify` / `undefined: lastClassifyPath`.

- [ ] **Step 3: Implement the store**

Create `src/app/classify.go`:

```go
// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// lastClassifyPath is the reconciling poll's floor file (the
// classify-state store, poll-classify-reconcile design): the last
// database revision the filter applied folder rules through. It shares
// the poll stamp's cache directory (pollStampPath) so the CLI poll and
// every running client read and advance the same floor.
func lastClassifyPath() string {
	base, err := os.UserCacheDir()
	if err != nil {
		return "last-classify"
	}
	return filepath.Join(base, "notmutt", "last-classify")
}

// readLastClassify loads the persisted L revision. A missing file is
// "no L yet" (the caller baselines to the present); a corrupt file
// warns and is treated as no L - re-classify the discovery bracket,
// never a full backfill.
func readLastClassify() (uint64, bool) {
	b, err := os.ReadFile(lastClassifyPath())
	if err != nil {
		if os.IsNotExist(err) {
			return 0, false
		}
		diag.Warn("last-classify", "err", err.Error())
		return 0, false
	}
	v, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		diag.Warn("last-classify", "corrupt", string(b))
		return 0, false
	}
	return v, true
}

// writeLastClassify persists L atomically (temp + rename, 0600). The
// higher-of-two guard keeps a concurrent process's advance from being
// regressed; a regressed floor would only cost an idempotent re-run,
// but the guard keeps the common quiet-mailbox case quiet.
func writeLastClassify(v uint64) error {
	p := lastClassifyPath()
	dir := filepath.Dir(p)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("last-classify: %w", err)
	}
	tmp := filepath.Join(dir, ".last-classify.tmp")
	if err := os.WriteFile(tmp, []byte(strconv.FormatUint(v, 10)+"\n"), 0o600); err != nil {
		return fmt.Errorf("last-classify: %w", err)
	}
	if cur, err := os.ReadFile(p); err == nil {
		if have, err := strconv.ParseUint(strings.TrimSpace(string(cur)), 10, 64); err == nil && have > v {
			os.Remove(tmp)
			return nil
		}
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("last-classify: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./app -run TestLastClassify -count=1`
Expected: PASS (all four).

- [ ] **Step 5: Commit**

```bash
git add src/app/classify.go src/app/classify_test.go
git commit -m "feat(app): persist the last-classified revision (L)"
```

---

## Task 2: The reconciling poll

**Files:**
- Modify: `src/app/app.go`
- Modify: `src/app/filterjob.go`
- Modify: `src/app/filterjob_test.go`
- Test: `src/app/filterjob_test.go`

- [ ] **Step 1: Write the failing regression tests**

Append to `src/app/filterjob_test.go`. `writeLastClassify` (Task 1) seeds `L`; `os.ReadFile`/`os.Stat` assert the floor state.

```go
// TestPollReconcileOutOfBandMutation (the core regression): the filter
// already applied through L=5; an out-of-band write (a tag op, a path
// move, a sibling process's new) advanced the database to 10. This
// poll's own ActNew finds nothing (bump 0), but the poll must classify
// (5, 10] anyway. Current code: ActNew reports (10, 10), cur == pre,
// and the poll returns before classifying - RED.
func TestPollReconcileOutOfBandMutation(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	root := testutil.MaildirTree(t, map[string]string{"Archives": "1", "INBOX": "2"})

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
	w.rev.Store(10) // already past L=5, and ActNew must find nothing new
	if err := writeLastClassify(5); err != nil {
		t.Fatal(err)
	}

	changed, _, _, err := runFilterPipeline(w, cfg, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("out-of-band revision movement must still classify (L, cur]")
	}
	if w.tagged.Load() == 0 {
		t.Fatal("no tag writes for the reconciled entries")
	}
	got, _ := os.ReadFile(lastClassifyPath())
	if string(got) != "10\n" {
		t.Fatalf("floor = %q, want 10 (the applied run must advance L to the current revision)", got)
	}
}

// TestPollReconcileDryRunKeepsFloor: a dry-run reconcile classifies the
// full window but never advances L - the reviewer sees the pending set
// until an applied run.
func TestPollReconcileDryRunKeepsFloor(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	root := testutil.MaildirTree(t, map[string]string{"Archives": "1", "INBOX": "2"})

	cfg := config.Default() // dry-run
	cfg.Accounts = map[string]config.Account{"gmail": {Preset: "gmail"}}
	w := &fjWorker{
		delta: []core.Message{{ID: "m1"}},
		snaps: []core.Message{{ID: "m1", Tags: []string{"inbox"}, Paths: []string{"gmail/Archives/cur/1"}}},
	}
	w.rev.Store(20)
	if err := writeLastClassify(15); err != nil {
		t.Fatal(err)
	}

	changed, _, _, err := runFilterPipeline(w, cfg, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("a dry-run reconcile must still classify (L, cur]")
	}
	got, _ := os.ReadFile(lastClassifyPath())
	if string(got) != "15\n" {
		t.Fatalf("floor moved on a dry run: %q, want 15 (only applied runs advance L)", got)
	}
}

// TestPollReconcileFirstRunNoBackfill: no L file, a far-behind mailbox
// (rev at 100k), nothing new. L baselines to the present revision, so
// the window is empty and no L file is created - never a full backfill.
func TestPollReconcileFirstRunNoBackfill(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	root := testutil.MaildirTree(t, map[string]string{"Archives": "1", "INBOX": "2"})

	cfg := config.Default()
	cfg.Accounts = map[string]config.Account{"gmail": {Preset: "gmail"}}
	w := &fjWorker{}
	w.rev.Store(100000)

	changed, _, _, err := runFilterPipeline(w, cfg, root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Fatal("a first run on a quiet far-behind mailbox must not classify")
	}
	if _, err := os.Stat(lastClassifyPath()); err == nil {
		t.Fatal("a quiet first run must not create the floor file")
	}
}

// TestPollReconcileWindowedIgnoresFloor: a fixed-window replay
// reclassifies its stored bracket and never reads or advances L.
func TestPollReconcileWindowedIgnoresFloor(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	root := testutil.MaildirTree(t, map[string]string{"Archives": "1", "INBOX": "2"})

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
	if err := writeLastClassify(99); err != nil {
		t.Fatal(err)
	}

	line, _, err := runPoll(w, cfg, root, pollSpec{windowed: true, from: 0, to: 5})
	if err != nil {
		t.Fatal(err)
	}
	if line != "poll: dry-run: 0..5: 2 entries, 0 moved, 1 skipped" {
		t.Fatalf("summary = %q", line)
	}
	got, _ := os.ReadFile(lastClassifyPath())
	if string(got) != "99\n" {
		t.Fatalf("a windowed replay touched the floor: %q, want 99", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./app -run 'TestPollReconcile' -count=1`
Expected:
- `TestPollReconcileOutOfBandMutation` FAILS (`out-of-band revision movement must still classify`).
- `TestPollReconcileDryRunKeepsFloor` FAILS (`a dry-run reconcile must still classify`).
- `TestPollReconcileFirstRunNoBackfill` PASSES (guard).
- `TestPollReconcileWindowedIgnoresFloor` PASSES (guard).

- [ ] **Step 3: Implement the reconciling fresh poll**

In `src/app/app.go`, add the flag to `pollSpec`:

```go
type pollSpec struct {
	apply    bool
	from, to uint64
	windowed bool
	reconcile bool // fresh runs classify (L, cur]; indexWrite's own-bracket classify leaves this false
}
```

In `src/app/app.go`, `runPoll`, right after the `--apply` one-shot override (`if spec.apply { cfg.Filter.DryRun = false }`), make fresh runs reconcile:

```go
	if !spec.windowed {
		spec.reconcile = true // the poll reconciles (L, cur], not just new mail
	}
```

In `src/app/filterjob.go`, `runFilterPipeline`, change its call so the in-client poll reconciles:

```go
	rep, mr, win, err := pollDiff(worker, cfg, root, pollSpec{reconcile: true}, progress)
```

Replace the whole `pollDiff` body (`src/app/app.go:1402-1424`) and its comment with a dispatch plus three helpers:

```go
// pollDiff classifies the poll's window. Three modes:
//   - windowed: replay the fixed (from, to] bracket of a stored diff;
//     the repro harness, no new run, no floor.
//   - reconcile (fresh): index new mail, then classify (L, cur] on the
//     database revision - the poll owns classification, whatever moved
//     the revision since L gets the folder rules run on it.
//   - bracket (fresh, indexWrite): classify only this run's own new
//     discovery bracket, never touching L.
//
// Returns the window as the summary reports it; an empty window means
// no classification pass.
func pollDiff(worker workerAPI, cfg config.Config, root string, spec pollSpec, progress func(done, total int)) (*filter.Report, *filter.MoveReport, string, error) {
	if spec.windowed {
		return runClassify(worker, cfg, root, spec.from, spec.to, progress)
	}
	if spec.reconcile {
		return pollReconcile(worker, cfg, root, progress)
	}
	pre, cur, ok, err := freshCapture(worker)
	if err != nil {
		return nil, nil, "", err
	}
	if !ok {
		return nil, nil, "", nil
	}
	return runClassify(worker, cfg, root, pre, cur, progress)
}

// freshCapture runs notmuch new and returns the (pre, cur] lastmod
// bracket its own indexing discovered. ok is false when nothing was
// indexed or the backend has no New path (ErrUnsupported degrades to a
// no-op poll, as today).
func freshCapture(worker workerAPI) (pre, cur uint64, ok bool, err error) {
	rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActNew})
	if err != nil || rpl.Err != nil {
		if !errors.Is(err, notmuch.ErrUnsupported) && !errors.Is(rpl.Err, notmuch.ErrUnsupported) {
			return 0, 0, false, fmt.Errorf("new: %v %v", err, rpl.Err)
		}
		return 0, 0, false, nil
	}
	return rpl.Pre, rpl.Rev, rpl.Rev > rpl.Pre, nil
}

// runClassify runs the engine and the mover over the fixed (pre, cur]
// bracket and renders the window label.
func runClassify(worker workerAPI, cfg config.Config, root string, pre, cur uint64, progress func(done, total int)) (*filter.Report, *filter.MoveReport, string, error) {
	rep, mr, err := classifyDelta(worker, cfg, root, pre, cur, progress)
	if err != nil {
		return rep, mr, "", err
	}
	return rep, mr, fmt.Sprintf("%d..%d", pre, cur), nil
}

// pollReconcile is the reconciling fresh poll: index new mail (its
// files' lastmod must land inside the window), read the persisted L
// floor (absent -> baseline to this new run's pre, so a first run
// classifies only the fresh discovery and never backfills), and
// classify (L, cur]. An applied run persists the post-run revision as
// the new L - the classify's own tag/path writes advanced the revision
// past cur, and persisting the higher value keeps those messages out of
// the next poll's window. A dry run leaves L where it is so the
// reviewer keeps seeing the full pending set. cur == L (a quiet
// mailbox) classifies nothing, exactly as the old cur == pre gate.
func pollReconcile(worker workerAPI, cfg config.Config, root string, progress func(done, total int)) (*filter.Report, *filter.MoveReport, string, error) {
	rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActNew})
	if err != nil || rpl.Err != nil {
		if !errors.Is(err, notmuch.ErrUnsupported) && !errors.Is(rpl.Err, notmuch.ErrUnsupported) {
			return nil, nil, "", fmt.Errorf("new: %v %v", err, rpl.Err)
		}
		return nil, nil, "", nil // no New path: no bracket, no reconcile
	}
	L, have := readLastClassify()
	if !have {
		L = rpl.Pre
	}
	curRpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActRevision})
	if err != nil || curRpl.Err != nil {
		return nil, nil, "", fmt.Errorf("revision: %v %v", err, curRpl.Err)
	}
	cur := curRpl.Rev
	if cur <= L {
		return nil, nil, "", nil // nothing mutated since L: the quiet poll
	}
	rep, mr, err := classifyDelta(worker, cfg, root, L, cur, progress)
	if err != nil {
		return rep, mr, "", err
	}
	if !rep.DryRun {
		if final, err := worker.Call(notmuch.Action{Kind: notmuch.ActRevision}); err == nil && final.Err == nil {
			if err := writeLastClassify(final.Rev); err != nil {
				diag.Warn("last-classify", "err", err.Error())
			}
		} else {
			diag.Warn("reconcile", "floor", "revision read failed")
		}
	}
	return rep, mr, fmt.Sprintf("%d..%d", L, cur), nil
}
```

- [ ] **Step 4: Run the reconcile tests to verify they pass**

Run: `go test ./app -run 'TestPollReconcile|TestRunFilterPipeline|TestRunPoll|TestFilterJob' -count=1`

Expected: the four new tests PASS. `TestRunFilterPipeline`, `TestRunPoll`, and `TestFilterJob` may now read the real cache file (they do not yet redirect) - see the next step, then re-run.

- [ ] **Step 5: Hermeticize the poll tests that route the reconcile**

The reconcile reads/writes the floor file; tests that route `pollDiff`'s reconcile path must redirect `XDG_CACHE_HOME` so no unit test touches the real cache (and a developer's live client's floor does not leak into assertions). Add the two lines immediately after each test's opening line:

In `TestFilterJob` (`src/app/filterjob_test.go:75`):

```go
func TestFilterJob(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	cfg := config.Default()
```

In `TestRunFilterPipeline` (`src/app/filterjob_test.go:138`):

```go
func TestRunFilterPipeline(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	root := testutil.MaildirTree(t, map[string]string{"Archives": "1", "INBOX": "2"})
```

Behavior under the reconcile (verify by re-running): each test's first block sees no L file, baselines to its `ActNew` pre, classifies, and the live one (`TestRunFilterPipeline`, `TestRunPoll`) persists L; each quiet block then finds `cur == L` and stays quiet - the existing assertions hold unchanged.

- [ ] **Step 6: Run the app suite**

Run: `go test ./app -count=1`
Expected: all PASS. Watch for any test that newly creates `notmutt/last-classify` under the real cache during this run - it is a missed redirect.

- [ ] **Step 7: Commit**

```bash
git add src/app/app.go src/app/filterjob.go src/app/filterjob_test.go
git commit -m "feat(app): reconcile the poll over the database revision, not new's bracket"
```

---

## Task 3: The mover flock

**Files:**
- Modify: `src/filter/mover.go`
- Create: `src/filter/mover_lock_test.go`
- Modify: `src/filter/filter_test.go`
- Modify: `src/app/apply_test.go`

- [ ] **Step 1: Write the failing tests**

Create `src/filter/mover_lock_test.go`:

```go
// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package filter

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"notmutt/config"
)

// mkMaildirTree makes an account folder space with one source message.
func mkMaildirTree(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "mail")
	for _, d := range []string{"INBOX", "Archives"} {
		if err := os.MkdirAll(filepath.Join(root, "gmail", d, "cur"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "gmail", "INBOX", "cur", "1"), []byte("mail"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func moveRep(root string) *Report {
	return &Report{Entries: []Entry{
		{ID: "m1", Account: "gmail", Folder: "archive", Paths: []string{"gmail/INBOX/cur/1"}},
	}}
}

// holdLock takes the mover lock and keeps it held until the caller
// closes the returned file (flock releases on close).
func holdLock(t *testing.T) *os.File {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(moverLockPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(moverLockPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	return f
}

// TestMoverLockBestEffortSkips: a contended poll mover (NewMover) skips
// the batch with a report note, never errors, never copies.
func TestMoverLockBestEffortSkips(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := mkMaildirTree(t)
	hold := holdLock(t)
	defer hold.Close()

	cfg := config.Default()
	cfg.Accounts = map[string]config.Account{"gmail": {Preset: "gmail"}}
	cfg.Filter.DryRun = false

	mr, err := NewMover(&fakeWorker{}, cfg, root).Move(moveRep(root))
	if err != nil {
		t.Fatalf("a contended poll mover skips, never errors: %v", err)
	}
	if mr.Skip == "" {
		t.Fatal("a contended poll mover must report the skip")
	}
	if _, err := os.Stat(filepath.Join(root, "gmail", "Archives", "cur", "1")); err == nil {
		t.Fatal("a contended mover must not copy")
	}
}

// TestMoverLockStrictTimesOut: a contended apply mover (NewMoverLive)
// waits up to the timeout then errors - the apply entry stays staged.
func TestMoverLockStrictTimesOut(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	orig := moverLockTimeout
	moverLockTimeout = 50 * time.Millisecond
	defer func() { moverLockTimeout = orig }()
	root := mkMaildirTree(t)
	hold := holdLock(t)
	defer hold.Close()

	cfg := config.Default()
	cfg.Accounts = map[string]config.Account{"gmail": {Preset: "gmail"}}

	if _, err := NewMoverLive(&fakeWorker{}, cfg, root).Move(moveRep(root)); err == nil ||
		!strings.Contains(err.Error(), "held") {
		t.Fatalf("a contended apply mover must error on timeout, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "gmail", "Archives", "cur", "1")); err == nil {
		t.Fatal("a timed-out apply mover must not copy")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./filter -run TestMoverLock -count=1`
Expected: compile failures - `undefined: moverLockPath`, `undefined: moverLockTimeout`, `NewMover` reports no skip field access at compile time (`mr.Skip` unknown on `*MoveReport`).

- [ ] **Step 3: Implement the mover lock**

In `src/filter/mover.go`:
- add `"syscall"` and `"time"` to the imports (keep the others);
- add the `Skip` field to `MoveReport`, the `lockStrict` field to `Mover`, `NewMoverLive` to set it, and the path/lock machinery;

```go
// MoveReport is the run's outcome; dry-run writes nothing and the
// entries ARE the review surface (what-would-move per file). Skip, when
// set, is a batch-level reason no files moved (the poll mover's
// contended-flock skip).
type MoveReport struct {
	Moves []MoveEntry
	Skip  string
}
```

```go
type Mover struct {
	worker Worker
	cfg    config.Config
	root   string
	dryRun bool
	// lockStrict marks the apply mover (NewMoverLive): it waits for a
	// contended flock up to the timeout and then errors. The poll mover
	// (NewMover) is best-effort: contended, it skips the batch.
	lockStrict bool
	// Progress, when set, reports each processed entry (R15 batch boundary: the per-message loop).
	Progress func(done, total int)
}
```

```go
func NewMoverLive(w Worker, cfg config.Config, root string) *Mover {
	m := newMover(w, cfg, root, false)
	m.lockStrict = true
	return m
}
```

```go
// moverLockPath is the cross-process mover lock, beside the poll stamp
// (the same cache dir the app's classify floor uses): one file serializes
// every client's live mover, apply and poll alike.
func moverLockPath() string {
	base, err := os.UserCacheDir()
	if err != nil {
		return "mover.lock"
	}
	return filepath.Join(base, "notmutt", "mover.lock")
}

const moverLockTick = 100 * time.Millisecond

// moverLockTimeout caps the apply mover's wait for a contended flock -
// the UI rule that a tag op never hangs behind a lock (notmuch's
// lock_timeout / the app's lockBudget). Overridden in tests.
var moverLockTimeout = 10 * time.Second

// lockLive takes the cross-process flock for a live Move. flock is
// chosen over a pidfile or create/delete lockfile because the kernel
// releases it the instant the holder's fd closes - on crash, SIGKILL,
// or panic - so a dead mover never leaves a stale lock and a blocked
// waiter wakes automatically. The lock file is opened fresh per call:
// separate fds contend even inside one process, so one file serializes
// the apply mover against the poll mover too. A fresh fd opened on the
// same file by this process is not reentrant, but a Move never nests
// inside a Move. Dry runs write nothing and skip the lock entirely.
func (m *Mover) lockLive(out *MoveReport) (unlock func(), err error) {
	if m.dryRun {
		return func() {}, nil
	}
	p := moverLockPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, fmt.Errorf("mover: lock dir: %w", err)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("mover: lock: %w", err)
	}
	deadline := time.Now().Add(moverLockTimeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		switch {
		case err == nil:
			return func() { f.Close() }, nil
		case errors.Is(err, syscall.EINTR):
			continue
		case !errors.Is(err, syscall.EWOULDBLOCK):
			f.Close()
			return nil, fmt.Errorf("mover: lock: %w", err)
		}
		if !m.lockStrict {
			// best effort (the poll mover): contended -> skip the batch;
			// the reconciling poll re-runs and whoever holds the lock is
			// classifying the same account - eventual.
			out.Skip = "mover lock held by another process"
			f.Close()
			return func() {}, nil
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("mover: lock held by another process (waited %s)", moverLockTimeout)
		}
		time.Sleep(moverLockTick)
	}
}
```

Wire the lock into `Move` (`src/filter/mover.go:67`). Replace the opening of `Move`:

```go
func (m *Mover) Move(rep *Report) (*MoveReport, error) {
	out := &MoveReport{}
	unlock, err := m.lockLive(out)
	if err != nil {
		return out, err
	}
	defer unlock()
	if out.Skip != "" {
		return out, nil
	}
	targets, managed := m.resolveAccounts(rep)
```

(The rest of `Move` - resolution, the per-file loop, copy-then-delete, the path ops - is unchanged.)

In `src/app/filterjob.go`, log a skipped batch once, inside `classifyDelta` right after `Move` returns (a poll mover that skipped still produced a valid report; the engine's tags already landed, the reconcile will move the files):

```go
	mv := filter.NewMover(worker, cfg, root)
	mv.Progress = progress
	mr, err := mv.Move(rep)
	if err != nil {
		return rep, nil, err
	}
	if mr.Skip != "" {
		diag.Info("filter", "mover", "skipped", "reason", mr.Skip)
	}
	return rep, mr, nil
```

- [ ] **Step 4: Run the lock tests to verify they pass**

Run: `go test ./filter -run TestMoverLock -count=1`
Expected: PASS.

- [ ] **Step 5: Hermeticize the live-mover tests**

Every test that runs a live `Move` (`cfg.Filter.DryRun = false` before a `NewMover`/`NewMoverLive` call) now opens the lock file and must redirect the cache so no test touches the real `~/.cache/notmutt/mover.lock`. Insert `t.Setenv("XDG_CACHE_HOME", t.TempDir())` immediately after each test's opening line:

`src/filter/filter_test.go` (all five set `cfg.Filter.DryRun = false`):
- `TestMover` (line 262)
- `TestMoverStripsMbsyncUID` (line 384)
- `TestMoverReadOnlyAccount` (line 430)
- `TestTwoCopyResolvesToSent` (line 488)
- `TestMoverSkipsMessageAlreadyHome` (line 518)

```go
func TestMover(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
```

`src/app/apply_test.go` (both reach `NewMoverLive.Move`):
- `TestApplyMovesToFolderTag` (line 355)
- `TestApplyMoveFailureRefusesTag` (line 388)

```go
func TestApplyMovesToFolderTag(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := testutil.MaildirTree(t, map[string]string{"INBOX": "1"})
```

- [ ] **Step 6: Run the filter and app suites**

Run: `go test ./filter ./app -count=1`
Expected: all PASS. Watch for a newly-created real `notmutt/mover.lock` during the run - a missed redirect.

- [ ] **Step 7: Commit**

```bash
git add src/filter/mover.go src/filter/mover_lock_test.go src/filter/filter_test.go src/app/apply_test.go src/app/filterjob.go
git commit -m "feat(filter): flock the mover's live move"
```

---

## Task 4: Copy via temp-then-rename

**Files:**
- Modify: `src/filter/mover.go`
- Test: `src/filter/mover_lock_test.go`

- [ ] **Step 1: Write the failing test**

Append to `src/filter/mover_lock_test.go`:

```go
// TestCopyFileTempThenRename: copyFile publishes the destination via a
// dot-prefixed temp in the destination directory and an atomic rename -
// a concurrent notmuch new never indexes a half-published destination
// (notmuch ignores dotfiles), and a leftover temp from a crashed copy
// does not survive a successful redo.
func TestCopyFileTempThenRename(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "s")
	dst := filepath.Join(dir, "d")
	if err := os.WriteFile(src, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	// a leftover temp of a crashed earlier copy
	if err := os.WriteFile(filepath.Join(dir, ".d.tmp"), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "content" {
		t.Fatalf("dst = %q, %v, want content", got, err)
	}
	// an existing destination is replaced (a second move over the same name)
	if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(dst)
	if err != nil || string(got) != "content" {
		t.Fatalf("dst after replace = %q, %v, want content", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if len(e.Name()) > 0 && e.Name()[0] == '.' {
			t.Fatalf("temp leaked: %s", e.Name())
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./filter -run TestCopyFileTempThenRename -count=1`
Expected: FAIL with `temp leaked: .d.tmp` (the current `copyFile` opens the final name directly and never removes the leftover).

- [ ] **Step 3: Implement the temp-then-rename copy**

Replace `copyFile` in `src/filter/mover.go`:

```go
// copyFile is shutil.copy2: content, mode, and mtime. The mtime is
// kept because the untag-reversal delivery gate compares file times;
// destination dirs are created so a first move never fails on a
// missing folder. The copy lands in a dot-prefixed temp in the
// destination directory and renames over the final name: a concurrent
// notmuch new must never index a half-published destination (headers
// copy first - a truncated file still parses a Message-ID), and rename
// is atomic within a directory while notmuch ignores dotfiles.
func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp")
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Chtimes(tmp, fi.ModTime(), fi.ModTime()); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./filter -run 'TestCopyFileTempThenRename|TestMover|TestMoverLock' -count=1`
Expected: PASS. `TestMover` still finds its moved file content preserved and its add-before-remove path ops intact.

- [ ] **Step 5: Commit**

```bash
git add src/filter/mover.go src/filter/mover_lock_test.go
git commit -m "feat(filter): copy moves via temp-then-rename"
```

---

## Task 5: Full-suite verification

- [ ] **Step 1: Run the whole suite**

Run: `go test ./... -count=1`
Expected: all PASS.

- [ ] **Step 2: Confirm no stray cache files**

Run: `git status --porcelain` and confirm the working tree holds only the committed source/test/doc changes. Check the real cache dir is untouched by the tests (no `notmutt/mover.lock` or `notmutt/last-classify` created during the run on a dev machine that had neither).

- [ ] **Step 3: Review the spec against the result**

Walk the spec sections:
- L store location/content/write rules -> Task 1.
- First-run baseline, no backfill -> Task 2 (`pollReconcile` seeds `L = pre`).
- Fresh reconcile in `pollDiff`/`runPoll`/`runFilterPipeline`, windowed untouched, indexWrite bracket-only -> Task 2.
- Mover flock with poll-skip / apply-wait, kernel release on close, darwin stdlib -> Task 3.
- temp-then-rename copy, notmuch ignores dotfiles -> Task 4.
- Dry-run never advances L, FilterDone on reconcile, quiet poll preserved -> Task 2 tests.

If anything from the spec has no implementing task, stop and add it before calling the plan done.

- [ ] **Step 4: Note the remaining risk in the code**

The design's acknowledged remaining window - a kill between a temp rename and the source delete leaves both files with the same Message-ID; the next `notmuch new` merges them and the reconciling poll re-runs the mover - is unchanged and documented in the spec's Risks section. No code needed.

---

## Self-review notes (from the author)

- `TestPollReconcileFirstRunNoBackfill` and `TestPollReconcileWindowedIgnoresFloor` pass before the reconcile is implemented (guards); `TestPollReconcileOutOfBandMutation` and `TestPollReconcileDryRunKeepsFloor` are the genuine REDs.
- `indexWrite` (`src/app/send.go:139`) is intentionally untouched: it calls `pollDiff(worker, cfg, root, pollSpec{}, nil)`, and the zero-valued `reconcile` routes it to the bracket branch, preserving today's behavior exactly and never reading or advancing L. The Bug-1 send regression tests need no edit.
- The `fjWorker` fixture never bumps its revision on tag writes, so every "final revision" assertion above reads the worker's stored `rev`; the real backend advances on its own tag/path writes, which is what the post-run `ActRevision` persists.
- Type names are consistent across tasks: `writeLastClassify`/`readLastClassify`/`lastClassifyPath` (Task 1) are used verbatim by `pollReconcile` (Task 2) and the Task 2 tests; `moverLockPath`/`moverLockTimeout`/`NewMoverLive`/`MoveReport.Skip` (Task 3) are used by the Task 3 tests.
