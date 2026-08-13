# M1: Mailbox View + Foundation - Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the M1 slice: the index/mailbox view against the real notmuch DB, on top of the foundation (strict TOML config store, event bus, notmuch worker, MIME cache, thread diff-and-insert refresh).

**Architecture:** Bottom-up in `src/` (Go module `notmutt`). Pure core first (types, comparator, diff engine, view model - no terminal, no DB), then the async spine (event bus, worker with CLI backend, refresh cycle), then the surface (cache, TUI, app wiring). The notmuch CLI backend is the default (SECURITY.md F10); the cgo backend is written behind the same interface for the benchmark, then one of them is chosen.

**Tech Stack:** Go 1.26 (go1.26.5 on this machine), notmuch CLI 0.40 (DB at /home/user/Mail), BurntSushi/toml v1.4.0, emersion/go-message v0.18.2, go.etcd.io/bbolt v1.3.11, charmbracelet/bubbletea v1.1.0, mattn/go-runewidth v0.0.16. All pinned exactly, vendored (supply-chain policy, AGENTS.md R7).

---

## Before you start

- **Normative docs**: `AGENTS.md` (R1-R13), `DESIGN.md` section 6, `SECURITY.md` (F1-F13), the M1 spec `docs/superpowers/specs/2026-08-14-m1-mailbox-view-design.md`. When the spec and plan differ, the spec wins - amend the plan, not the spec.
- **Privacy hard rule**: never open mail files (.eml, maildir files) and never pipe notmuch search/show output that contains subjects or headers into the LLM. Tests use synthetic fixtures; the soak test prints counts and ids only, never content. `notmuch count` (numbers), `notmuch config get` (paths), and source greps are fine.
- **Commits**: every commit ends with `AI-assisted: Claude Code` and a blank line then `Co-Authored-By: Claude <noreply@anthropic.com>`. Commit on master (repo convention: bb41675, 52abed5).
- **Code style**: no unnecessary comments; ASCII only; gofmt-clean; one comparator, one diff engine (DRY, AGENTS.md R7).
- **Verify before running integration tests**: `notmuch --version` must say 0.40, `notmuch config get database.path` must say /home/user/Mail.

## File map

```
src/
  go.mod, go.sum, vendor/          # module notmutt, pinned + vendored
  main.go                          # package main -> app.Run()
  app/app.go                       # wiring: config, bus, worker, refresher, cache job, tui
  app/refresh.go                   # refresher: lastmod incremental cycle + full reload
  app/refresh_test.go              # refresher with fake worker
  app/cachejob.go                  # MIME cache fill job (budgeted)
  app/soak_test.go                 # real-DB soak (NOTMUCH_SOAK=1)
  app/cursor_test.go               # cursor invariants (pure)
  config/config.go                 # TOML schema, strict load, defaults, validation
  config/store.go                  # typed observers, single write path
  config/config_test.go, store_test.go
  core/types.go                    # Message, Attachment, Thread, Node, Row
  core/compare.go                  # ThreadLess, MsgLess - THE comparator (DRY)
  core/diff.go                     # DiffSorted, Apply, Op - sorted-list merge
  core/diff_test.go                # property test: apply(diff(old,new), old) == new
  core/bus.go                      # event bus + event types
  core/bus_test.go
  core/view.go                     # View: thread trees, rows, merge, cursor, collapse
  core/view_test.go
  notmuch/backend.go               # Backend interface, TagOp, Action, Reply, ErrLockTimeout
  notmuch/cli.go                   # CLI backend: argv run, JSON parse
  notmuch/cli_test.go              # fake runner
  notmuch/worker.go                # action loop, lock budgets, bus events
  notmuch/worker_test.go           # fake backend
  notmuch/cgo.go                   # cgo backend (build tag cgo) - task 13
  notmuch/bench_test.go            # env-gated benchmark - task 14
  cache/cache.go                   # Cache interface, Key
  cache/bbolt.go                   # bbolt backend, 0600
  cache/scan.go                    # go-message attachment scan
  cache/bbolt_test.go, scan_test.go
  tui/model.go                     # BubbleTea model, j/k/q/t hardcoded
  tui/bridge.go                    # bus -> tea.Msg relay
  tui/hooks.go                     # tag-op handler seam (wired by app)
  tui/model_test.go
```

## Task 1: Module scaffold

**Files:**
- Create: `src/go.mod`, `src/go.sum`, `src/vendor/`, `.gitignore`, `docs/examples/config.toml`

- [ ] **Step 1: Create the module**

```bash
cd /home/user/git/opencode/notmutt
mkdir -p src docs/examples
cd src
go mod init notmutt
```

Expected: `go: creating new go.mod: module notmutt`.

- [ ] **Step 2: Pin and vendor dependencies**

```bash
go get github.com/BurntSushi/toml@v1.4.0
go get github.com/emersion/go-message@v0.18.2
go get go.etcd.io/bbolt@v1.3.11
go get github.com/charmbracelet/bubbletea@v1.1.0
go get github.com/mattn/go-runewidth@v0.0.16
go mod tidy
go mod vendor
```

Expected: `go.mod` has the five requires with exactly those versions, `vendor/modules.txt` lists them, `go mod verify` reports "all modules verified". If any pin fails to resolve, bump only that one to the nearest published version and note it in the commit.

- [ ] **Step 3: .gitignore and example config**

Create `/home/user/git/opencode/notmutt/.gitignore`:

```gitignore
/src/notmutt
*.test
```

Create `/home/user/git/opencode/notmutt/docs/examples/config.toml`:

```toml
[ui]
keymap = "vim"

[view.inbox]
query = "tag:inbox"
threads = true
```

- [ ] **Step 4: Verify build of empty module**

```bash
cd /home/user/git/opencode/notmutt/src
go build ./...
```

Expected: no output, exit 0 (module with no Go files builds clean).

- [ ] **Step 5: Commit**

```bash
cd /home/user/git/opencode/notmutt
git add .gitignore src/go.mod src/go.sum src/vendor docs/examples/config.toml
git commit -m "Scaffold Go module with pinned, vendored dependencies"
```

## Task 2: Core types and the comparator

**Files:**
- Create: `src/core/types.go`, `src/core/compare.go`, `src/core/compare_test.go`

- [ ] **Step 1: Write the failing test**

`src/core/compare_test.go`:

```go
package core

import (
	"sort"
	"testing"
)

func TestMsgLessOrder(t *testing.T) {
	a := &Message{ID: "a", Timestamp: 100}
	b := &Message{ID: "b", Timestamp: 100}
	c := &Message{ID: "c", Timestamp: 200}
	msgs := []*Message{b, c, a}
	sort.Slice(msgs, func(i, j int) bool { return MsgLess(msgs[i], msgs[j]) })
	if msgs[0] != c || msgs[1] != a || msgs[2] != b {
		t.Fatalf("want [c a b], got [%s %s %s]", msgs[0].ID, msgs[1].ID, msgs[2].ID)
	}
}

func TestThreadLessOrder(t *testing.T) {
	x := &Thread{ID: "x", LastDate: 100}
	y := &Thread{ID: "y", LastDate: 100}
	z := &Thread{ID: "z", LastDate: 300}
	ts := []*Thread{y, z, x}
	sort.Slice(ts, func(i, j int) bool { return ThreadLess(ts[i], ts[j]) })
	if ts[0] != z || ts[1] != x || ts[2] != y {
		t.Fatalf("want [z x y], got [%s %s %s]", ts[0].ID, ts[1].ID, ts[2].ID)
	}
}
```

Expected: FAIL - cannot compile, `MsgLess`, `ThreadLess`, `Message`, `Thread` undefined.

- [ ] **Step 2: Run to verify it fails**

```bash
cd /home/user/git/opencode/notmutt/src
go test ./core/ 2>&1 | head -5
```

Expected: `undefined: MsgLess`.

- [ ] **Step 3: Implement**

`src/core/types.go`:

```go
package core

type Message struct {
	ID         string
	ThreadID   string
	Timestamp  int64
	Author     string
	Subject    string
	Tags       []string
	References []string
	Paths      []string
	Atts       []Attachment
}

type Attachment struct {
	Name     string
	MimeType string
	Size     int64
}

type Thread struct {
	ID        string
	LastDate  int64
	Collapsed bool
	Root      *Node
	msgs      []*Message
}

type Node struct {
	Msg      *Message
	Children []*Node
}

type Row struct {
	Msg      *Message
	ThreadID string
	Depth    int
	Root     bool
	Count    int
}
```

`src/core/compare.go`:

```go
package core

// ThreadLess orders threads by last date desc, then id bytes.
func ThreadLess(a, b *Thread) bool {
	if a.LastDate != b.LastDate {
		return a.LastDate > b.LastDate
	}
	return a.ID < b.ID
}

// MsgLess orders messages by date desc, then id bytes.
func MsgLess(a, b *Message) bool {
	if a.Timestamp != b.Timestamp {
		return a.Timestamp > b.Timestamp
	}
	return a.ID < b.ID
}
```

- [ ] **Step 4: Run to verify it passes**

```bash
go test ./core/
```

Expected: `ok notmutt/core` (2 tests pass). Then `gofmt -l .` prints nothing.

- [ ] **Step 5: Commit**

```bash
cd /home/user/git/opencode/notmutt
git add src/core
git commit -m "Add core message/thread types and the canonical comparator"
```

## Task 3: Diff engine with property test

**Files:**
- Create: `src/core/diff.go`, `src/core/diff_test.go`

- [ ] **Step 1: Write the failing property test**

`src/core/diff_test.go`:

```go
package core

import (
	"math/rand"
	"sort"
	"strings"
	"testing"
)

func randHex(r *rand.Rand, n int) string {
	const hex = "0123456789abcdef"
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte(hex[r.Intn(16)])
	}
	return b.String()
}

func genMsgs(r *rand.Rand, n int, prefix string) []*Message {
	msgs := make([]*Message, n)
	for i := range msgs {
		msgs[i] = &Message{ID: prefix + "-" + randHex(r, 8), Timestamp: r.Int63n(100)}
	}
	sort.Slice(msgs, func(i, j int) bool { return MsgLess(msgs[i], msgs[j]) })
	return msgs
}

func msgKey(m *Message) string { return m.ID }

func sameMsgs(a, b []*Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			return false
		}
	}
	return true
}

func genThreads(r *rand.Rand, n int, prefix string) []*Thread {
	ts := make([]*Thread, n)
	for i := range ts {
		ts[i] = &Thread{ID: prefix + "-" + randHex(r, 8), LastDate: r.Int63n(100)}
	}
	sort.Slice(ts, func(i, j int) bool { return ThreadLess(ts[i], ts[j]) })
	return ts
}

func threadKey(t *Thread) string { return t.ID }

func sameThreads(a, b []*Thread) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			return false
		}
	}
	return true
}

func TestDiffApplyPropertyMessages(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for iter := 0; iter < 1000; iter++ {
		old := genMsgs(r, r.Intn(30), "old")
		new := genMsgs(r, r.Intn(30), "new")
		shared := r.Intn(min(len(old), len(new)) + 1)
		for i := 0; i < shared; i++ {
			new[i].ID = old[i].ID
			new[i].Timestamp = old[i].Timestamp
		}
		// timestamps are dense (range 100), so shifts cross neighbors:
		// moves happen in both directions
		if len(new) > 0 && r.Intn(4) == 0 {
			k := r.Intn(len(new))
			if r.Intn(2) == 0 {
				new[k].Timestamp += r.Int63n(10)
			} else {
				new[k].Timestamp -= r.Int63n(10)
			}
		}
		sort.Slice(new, func(i, j int) bool { return MsgLess(new[i], new[j]) })
		ops := DiffSorted(old, new, MsgLess, msgKey)
		got := Apply(old, ops)
		if !sameMsgs(got, new) {
			t.Fatalf("iter %d: apply(diff) != new\nold=%v\nnew=%v\ngot=%v", iter, msgIDs(old), msgIDs(new), msgIDs(got))
		}
	}
}

func TestDiffApplyPropertyThreads(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for iter := 0; iter < 1000; iter++ {
		old := genThreads(r, r.Intn(20), "t")
		new := genThreads(r, r.Intn(20), "t")
		shared := r.Intn(min(len(old), len(new)) + 1)
		for i := 0; i < shared; i++ {
			new[i].ID = old[i].ID
			new[i].LastDate = old[i].LastDate
		}
		// LastDates are dense (range 100), so shifts cross neighbors:
		// moves happen in both directions
		if len(new) > 0 && r.Intn(4) == 0 {
			k := r.Intn(len(new))
			if r.Intn(2) == 0 {
				new[k].LastDate += r.Int63n(10)
			} else {
				new[k].LastDate -= r.Int63n(10)
			}
		}
		sort.Slice(new, func(i, j int) bool { return ThreadLess(new[i], new[j]) })
		got := Apply(old, ops)
		if !sameThreads(got, new) {
			t.Fatalf("iter %d: apply(diff) != new\nold=%v\nnew=%v\ngot=%v", iter, threadIDs(old), threadIDs(new), threadIDs(got))
		}
	}
}

func TestDiffMoveCollapse(t *testing.T) {
	// a sinks from newest to oldest: the walk emits Remove then Insert
	// of the same key - the second pass collapses the pair into a Move.
	old := []*Message{{ID: "a", Timestamp: 5}, {ID: "c", Timestamp: 3}, {ID: "b", Timestamp: 2}}
	new := []*Message{{ID: "c", Timestamp: 3}, {ID: "b", Timestamp: 2}, {ID: "a", Timestamp: 1}}
	ops := DiffSorted(old, new, MsgLess, msgKey)
	found := false
	for _, op := range ops {
		if op.Kind == OpMove && op.Key == "a" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a Move op for the sunk message, got %+v", ops)
	}
	if got := Apply(old, ops); !sameMsgs(got, new) {
		t.Fatalf("apply with move mismatch: %v != %v", msgIDs(got), msgIDs(new))
	}
}

func msgIDs(msgs []*Message) []string {
	ids := make([]string, len(msgs))
	for i, m := range msgs {
		ids[i] = m.ID
	}
	return ids
}

func threadIDs(ts []*Thread) []string {
	ids := make([]string, len(ts))
	for i, t := range ts {
		ids[i] = t.ID
	}
	return ids
}
```

Expected: FAIL - cannot compile, `DiffSorted`, `Apply`, `Op`, `OpMove` undefined.

- [ ] **Step 2: Run to verify it fails**

```bash
cd /home/user/git/opencode/notmutt/src
go test ./core/ 2>&1 | head -5
```

Expected: `undefined: DiffSorted`.

- [ ] **Step 3: Implement the diff engine**

`src/core/diff.go`:

```go
package core

type OpKind int

const (
	OpInsert OpKind = iota
	OpRemove
	OpMove
)

type Op[T any] struct {
	Kind OpKind
	Item T      // OpInsert payload
	Key  string // OpRemove, OpMove
	From int    // OpRemove, OpMove: index in the current list
	To   int    // OpInsert, OpMove: index in the current list
}

// DiffSorted merges old and new (both sorted by less, unique keys) into
// replayable ops. Positions are in the current list at the time each op
// applies, so Apply(old, ops) == new. A remove+insert of the same key
// collapses into a Move (second pass, still O(n+m)). Equality is
// key-order equality: matched keys keep the old element (values are not
// updated), and moved elements retain old values. Callers reconcile
// element fields from the incoming snapshot. less must be a strict total
// order on keys.
func DiffSorted[T any](old, new []T, less func(a, b T) bool, key func(T) string) []Op[T] {
	var ops []Op[T]
	i, j := 0, 0
	for i < len(old) && j < len(new) {
		if key(old[i]) == key(new[j]) {
			i++
			j++
			continue
		}
		if less(old[i], new[j]) {
			ops = append(ops, Op[T]{Kind: OpRemove, Key: key(old[i]), From: j})
			i++
		} else {
			ops = append(ops, Op[T]{Kind: OpInsert, Item: new[j], To: j})
			j++
		}
	}
	for ; i < len(old); i++ {
		ops = append(ops, Op[T]{Kind: OpRemove, Key: key(old[i]), From: j})
	}
	for ; j < len(new); j++ {
		ops = append(ops, Op[T]{Kind: OpInsert, Item: new[j], To: j})
	}
	return collapseMoves(ops, key)
}

// collapseMoves turns an ADJACENT remove-then-insert pair of the same key
// into a Move. Only adjacent pairs collapse: between them the walk
// advances via matched pairs alone, so From and To reference the same
// frame and the collapsed op is equivalent to the original two (the
// property test is the gate). Any intervening op shifts the frames, and
// the reverse order (insert then remove - an element rising) never
// collapses: both stay as remove+insert churn.
func collapseMoves[T any](ops []Op[T], key func(T) string) []Op[T] {
	out := make([]Op[T], 0, len(ops))
	for i := 0; i < len(ops); i++ {
		op := ops[i]
		if op.Kind == OpRemove && i+1 < len(ops) && ops[i+1].Kind == OpInsert && key(ops[i+1].Item) == op.Key {
			out = append(out, Op[T]{Kind: OpMove, Key: op.Key, From: op.From, To: ops[i+1].To})
			i++
			continue
		}
		out = append(out, op)
	}
	return out
}

func removeAt[T any](items []T, i int) []T {
	return append(items[:i], items[i+1:]...)
}

func insertAtIdx[T any](items []T, item T, i int) []T {
	items = append(items, *new(T))
	copy(items[i+1:], items[i:])
	items[i] = item
	return items
}

// Apply replays ops in order over items; the result equals the new list
// the ops were diffed from. Apply mutates items' backing array in place;
// callers must use the returned slice and must not retain the original
// header or alias the backing array.
func Apply[T any](items []T, ops []Op[T]) []T {
	for _, op := range ops {
		switch op.Kind {
		case OpInsert:
			items = insertAtIdx(items, op.Item, op.To)
		case OpRemove:
			items = removeAt(items, op.From)
		case OpMove:
			item := items[op.From]
			items = removeAt(items, op.From)
			items = insertAtIdx(items, item, op.To)
		}
	}
	return items
}
```

- [ ] **Step 4: Run to verify it passes**

```bash
go test ./core/ -run TestDiff -v
```

Expected: all three tests pass (2000 property iterations + the move-collapse check). If `TestDiffMoveCollapse` fails, the collapse logic is wrong - debug against the failing iteration before committing.

- [ ] **Step 5: Commit**

```bash
cd /home/user/git/opencode/notmutt
git add src/core
git commit -m "Add sorted-merge diff engine with move collapse and property test"
```

## Task 4: Event bus

**Files:**
- Create: `src/core/bus.go`, `src/core/bus_test.go`

- [ ] **Step 1: Write the failing test**

`src/core/bus_test.go`:

```go
package core

import (
	"testing"
	"time"
)

func TestBusFanout(t *testing.T) {
	b := NewBus()
	ch1 := b.Subscribe()
	ch2 := b.Subscribe()
	b.Publish(WorkerDone{Job: "new"})
	select {
	case e := <-ch1:
		if e.(WorkerDone).Job != "new" {
			t.Fatalf("wrong event: %+v", e)
		}
	case <-time.After(time.Second):
		t.Fatal("subscriber 1 got nothing")
	}
	select {
	case <-ch2:
	case <-time.After(time.Second):
		t.Fatal("subscriber 2 got nothing")
	}
}

func TestBusSlowSubscriberDrops(t *testing.T) {
	b := NewBus()
	b.Subscribe() // nobody drains this one
	done := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			b.Publish(WorkerDone{Job: "x"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publish blocked on a slow subscriber")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

```bash
cd /home/user/git/opencode/notmutt/src
go test ./core/ -run TestBus 2>&1 | head -5
```

Expected: `undefined: NewBus`.

- [ ] **Step 3: Implement**

`src/core/bus.go`:

```go
package core

import "sync"

type Event any

// Bus fans events out to subscribers. A full subscriber drops the event
// (coalescing): consumers repaint from state, never from events.
type Bus struct {
	mu   sync.Mutex
	subs []chan Event
}

func NewBus() *Bus { return &Bus{} }

func (b *Bus) Subscribe() <-chan Event {
	ch := make(chan Event, 64)
	b.mu.Lock()
	b.subs = append(b.subs, ch)
	b.mu.Unlock()
	return ch
}

func (b *Bus) Publish(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, s := range b.subs {
		select {
		case s <- e:
		default:
		}
	}
}

type QueryBatch struct {
	View  string
	Total int
	Done  bool
}

type WorkerDone struct{ Job string }

type WorkerLockTimeout struct{ Kind string }

type CacheResult struct {
	MsgID string
	Atts  []Attachment
	Err   error
}

type ConfigChanged struct{ Section string }

type ViewDiff struct{ View string }
```

- [ ] **Step 4: Run to verify it passes**

```bash
go test ./core/ -run TestBus -v
```

Expected: both pass.

- [ ] **Step 5: Commit**

```bash
cd /home/user/git/opencode/notmutt
git add src/core
git commit -m "Add fan-out event bus with drop-on-full coalescing"
```

## Task 5: Config store

**Files:**
- Create: `src/config/config.go`, `src/config/store.go`, `src/config/config_test.go`, `src/config/store_test.go`

- [ ] **Step 1: Write the failing tests**

`src/config/config_test.go`:

```go
package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadValid(t *testing.T) {
	cfg, err := Load(write(t, `
[ui]
keymap = "emacs"

[view.inbox]
query = "tag:inbox"
threads = true
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.Keymap != "emacs" {
		t.Fatalf("keymap = %q", cfg.UI.Keymap)
	}
	if cfg.Views["inbox"].Query != "tag:inbox" || !cfg.Views["inbox"].Threads {
		t.Fatalf("view parse wrong: %+v", cfg.Views["inbox"])
	}
}

func TestLoadUnknownKeyErrors(t *testing.T) {
	_, err := Load(write(t, "\n[ui]\nkeymap = \"vim\"\nksy = true\n"))
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
	if !strings.Contains(err.Error(), "ksy") {
		t.Fatalf("error must name the key, got: %v", err)
	}
}

func TestLoadInvalidEnum(t *testing.T) {
	_, err := Load(write(t, "\n[ui]\nkeymap = \"vi\"\n"))
	if err == nil {
		t.Fatal("expected error for invalid keymap")
	}
}

func TestLoadDefaultsWhenMissing(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.Keymap != "vim" {
		t.Fatalf("default keymap = %q", cfg.UI.Keymap)
	}
	if _, ok := cfg.Views["inbox"]; !ok {
		t.Fatal("default view missing")
	}
}

func TestLoadEmptyViewQueryErrors(t *testing.T) {
	_, err := Load(write(t, "\n[view.x]\nquery = \"\"\n"))
	if err == nil {
		t.Fatal("expected error for empty view query")
	}
}
```

`src/config/store_test.go`:

```go
package config

import "testing"

func TestStoreSetKeymapNotifies(t *testing.T) {
	s := NewStore(Default())
	got := false
	s.Subscribe("ui", func() { got = true })
	if err := s.SetKeymap("emacs"); err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("ui observer not notified")
	}
}

func TestStoreSetKeymapRejects(t *testing.T) {
	s := NewStore(Default())
	if err := s.SetKeymap("vi"); err == nil {
		t.Fatal("expected error for invalid keymap")
	}
}

func TestStoreSetViewQueryNotifies(t *testing.T) {
	s := NewStore(Default())
	got := false
	s.Subscribe("view", func() { got = true })
	if err := s.SetViewQuery("inbox", "tag:unread"); err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("view observer not notified")
	}
	if c := s.Config(); c.Views["inbox"].Query != "tag:unread" {
		t.Fatalf("query not stored: %+v", c.Views["inbox"])
	}
}
```

- [ ] **Step 2: Run to verify they fail**

```bash
cd /home/user/git/opencode/notmutt/src
go test ./config/ 2>&1 | head -5
```

Expected: `undefined: Load`.

- [ ] **Step 3: Implement**

`src/config/config.go`:

```go
package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	UI    UI              `toml:"ui"`
	Views map[string]View `toml:"view"`
}

type UI struct {
	Keymap string `toml:"keymap"`
}

type View struct {
	Query   string `toml:"query"`
	Threads bool   `toml:"threads"`
}

func Default() Config {
	return Config{
		UI: UI{Keymap: "vim"},
		Views: map[string]View{
			"inbox": {Query: "tag:inbox", Threads: true},
		},
	}
}

// Load merges file values over defaults. Unknown keys are load errors
// with file:line (strict load, R8). A missing file means defaults.
func Load(path string) (Config, error) {
	cfg := Default()
	md, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, len(und))
		for i, k := range und {
			keys[i] = k.String()
		}
		return cfg, fmt.Errorf("%s: unknown key(s): %s", path, strings.Join(keys, ", "))
	}
	if err := validate(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func validate(cfg Config) error {
	if cfg.UI.Keymap != "vim" && cfg.UI.Keymap != "emacs" {
		return fmt.Errorf("keymap: must be vim or emacs, got %q", cfg.UI.Keymap)
	}
	if len(cfg.Views) == 0 {
		return fmt.Errorf("at least one view required")
	}
	for name, v := range cfg.Views {
		if strings.TrimSpace(v.Query) == "" {
			return fmt.Errorf("view %q: query must not be empty", name)
		}
	}
	return nil
}
```

`src/config/store.go`:

```go
package config

import (
	"fmt"
	"maps"
	"strings"
	"sync"
)

// Store is the single write path for runtime config mutations. Setters
// validate, mutate, and notify section observers, which publish
// ConfigChanged on the bus (wired in app).
type Store struct {
	mu   sync.Mutex
	cfg  Config
	subs map[string][]func()
}

func NewStore(cfg Config) *Store {
	return &Store{cfg: cfg, subs: map[string][]func(){}}
}

func (s *Store) Config() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cfg
	c.Views = maps.Clone(s.cfg.Views) // deep copy: snapshots must not alias store state
	return c
}

func (s *Store) Subscribe(section string, fn func()) {
	s.mu.Lock()
	s.subs[section] = append(s.subs[section], fn)
	s.mu.Unlock()
}

func (s *Store) SetKeymap(k string) error {
	if k != "vim" && k != "emacs" {
		return fmt.Errorf("keymap: must be vim or emacs, got %q", k)
	}
	s.mu.Lock()
	s.cfg.UI.Keymap = k
	s.mu.Unlock()
	s.notify("ui")
	return nil
}

func (s *Store) SetViewQuery(name, q string) error {
	if strings.TrimSpace(q) == "" {
		return fmt.Errorf("view %q: query must not be empty", name)
	}
	s.mu.Lock()
	v, ok := s.cfg.Views[name]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("view %q: no such view", name)
	}
	v.Query = q
	s.cfg.Views[name] = v
	s.mu.Unlock()
	s.notify("view")
	return nil
}

func (s *Store) notify(section string) {
	s.mu.Lock()
	fns := append([]func(){}, s.subs[section]...)
	s.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}
```

- [ ] **Step 4: Run to verify they pass**

```bash
go test ./config/ -v
```

Expected: all 8 tests pass. `gofmt -l .` prints nothing.

- [ ] **Step 5: Commit**

```bash
cd /home/user/git/opencode/notmutt
git add src/config
git commit -m "Add strict TOML config store with typed section observers"
```

## Task 6: View model

**Files:**
- Create: `src/core/view.go`, `src/core/view_test.go`

- [ ] **Step 1: Write the failing tests**

`src/core/view_test.go`:

```go
package core

import "testing"

func msg(id string, ts int64, refs ...string) *Message {
	return &Message{ID: id, Timestamp: ts, References: refs}
}

func TestRowsFlattenThreadTree(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	t1 := NewThread("t1", []*Message{msg("root", 100), msg("kid", 200, "root")})
	t2 := NewThread("t2", []*Message{msg("other", 300)})
	v.MergeThreads([]*Thread{t2, t1})
	rows := v.Rows()
	if len(rows) != 3 {
		t.Fatalf("want 3 rows, got %d", len(rows))
	}
	if rows[0].Msg.ID != "other" || !rows[0].Root {
		t.Fatalf("first row wrong: %+v", rows[0])
	}
	if rows[1].Msg.ID != "root" || !rows[1].Root {
		t.Fatalf("second row wrong: %+v", rows[1])
	}
	if rows[2].Msg.ID != "kid" || rows[2].Depth != 1 {
		t.Fatalf("third row wrong: %+v", rows[2])
	}
}

func TestMergeInsertsIntoExistingThread(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{msg("root", 100)})})
	// a reply arrives, sorts before its parent in the message list under
	// reverse-date; the tree still renders the parent above the child
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{msg("root", 100), msg("reply", 200, "root")})})
	rows := v.Rows()
	if len(rows) != 2 {
		t.Fatalf("want 2 rows after insert, got %d", len(rows))
	}
	if rows[0].Msg.ID != "root" || !rows[0].Root {
		t.Fatalf("root must stay first row: %+v", rows[0])
	}
	if rows[1].Msg.ID != "reply" || rows[1].Depth != 1 {
		t.Fatalf("reply not inserted under parent: %+v", rows[1])
	}
}

func TestMergeThreadMoves(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	old := NewThread("old", []*Message{msg("a", 100)})
	newer := NewThread("newer", []*Message{msg("b", 300)})
	v.MergeThreads([]*Thread{newer, old})
	// old thread gets a new message, jumps to the top
	old2 := NewThread("old", []*Message{msg("a", 100), msg("c", 500)})
	v.MergeThreads([]*Thread{old2, newer})
	rows := v.Rows()
	if rows[0].Msg.ID != "c" {
		t.Fatalf("moved thread should be first, got %+v", rows[0])
	}
}

func TestMergeThreadMerge(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	t1 := NewThread("t1", []*Message{msg("a", 100)})
	t2 := NewThread("t2", []*Message{msg("b", 200)})
	v.MergeThreads([]*Thread{t1, t2})
	merged := NewThread("t1", []*Message{msg("a", 100), msg("b", 200)})
	v.MergeThreads([]*Thread{merged})
	rows := v.Rows()
	if len(rows) != 2 {
		t.Fatalf("thread merge lost rows: %d", len(rows))
	}
}

func TestCursorSurvivesMerge(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{msg("a", 100)})})
	v.SetCursor("a")
	v.MergeThreads([]*Thread{NewThread("t1", []*Message{msg("a", 100), msg("b", 200)})})
	if _, ok := v.CursorRow(); !ok {
		t.Fatal("cursor lost after merge")
	}
}

func TestCollapseHidesChildren(t *testing.T) {
	v := NewView("inbox", "tag:inbox")
	th := NewThread("t1", []*Message{msg("root", 100), msg("kid", 200, "root")})
	v.MergeThreads([]*Thread{th})
	v.Threads[0].Collapsed = true
	rows := v.Rows()
	if len(rows) != 1 {
		t.Fatalf("collapsed thread must render 1 row, got %d", len(rows))
	}
	if rows[0].Count != 2 {
		t.Fatalf("collapsed row must still count the thread, got %d", rows[0].Count)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

```bash
cd /home/user/git/opencode/notmutt/src
go test ./core/ -run TestRows\|TestMerge\|TestCursor\|TestCollapse 2>&1 | head -5
```

Expected: `undefined: NewView`.

- [ ] **Step 3: Implement**

`src/core/view.go`:

```go
package core

import "sort"

type View struct {
	Name     string
	Query    string
	Threads  []*Thread // sorted by ThreadLess
	cursorID string
}

func NewView(name, query string) *View {
	return &View{Name: name, Query: query}
}

// NewThread builds a thread with a reference tree. msgs are sorted by
// MsgLess and copied.
func NewThread(id string, msgs []*Message) *Thread {
	sorted := append([]*Message(nil), msgs...)
	sort.Slice(sorted, func(i, j int) bool { return MsgLess(sorted[i], sorted[j]) })
	t := &Thread{ID: id, msgs: sorted}
	var last int64
	for _, m := range sorted {
		if m.Timestamp > last {
			last = m.Timestamp
		}
	}
	t.LastDate = last
	t.Root = buildTree(sorted)
	return t
}

func (t *Thread) Count() int {
	if t.Root == nil {
		return 0
	}
	n := 0
	var walk func(*Node)
	walk = func(node *Node) {
		if node.Msg != nil {
			n++
		}
		for _, c := range node.Children {
			walk(c)
		}
	}
	walk(t.Root)
	return n
}

// Rows flattens the thread forest depth-first. Collapsed threads render
// only their root row.
func (v *View) Rows() []Row {
	var rows []Row
	for _, t := range v.Threads {
		rows = append(rows, flattenThread(t, t.Collapsed)...)
	}
	return rows
}

func flattenThread(t *Thread, collapsed bool) []Row {
	var rows []Row
	if t.Root == nil {
		return rows
	}
	count := t.Count()
	var walk func(*Node, int)
	walk = func(node *Node, depth int) {
		if node.Msg == nil {
			for _, c := range node.Children {
				walk(c, depth)
			}
			return
		}
		rows = append(rows, Row{Msg: node.Msg, ThreadID: t.ID, Depth: depth, Root: depth == 0, Count: count})
		if collapsed {
			return
		}
		for _, c := range node.Children {
			walk(c, depth+1)
		}
	}
	walk(t.Root, 0)
	return rows
}

// MergeThreads diffs the incoming threads into the view: thread-level
// diff plus per-thread message diffs for threads present on both sides.
// Input must be sorted by ThreadLess. The cursor survives by id.
func (v *View) MergeThreads(threads []*Thread) {
	ops := DiffSorted(v.Threads, threads, ThreadLess, func(t *Thread) string { return t.ID })
	v.Threads = Apply(v.Threads, ops)
	for _, in := range threads {
		cur := findThread(v.Threads, in.ID)
		if cur == nil {
			continue // pure insert: already carries its tree
		}
		mops := DiffSorted(cur.msgs, in.msgs, MsgLess, func(m *Message) string { return m.ID })
		cur.msgs = Apply(cur.msgs, mops)
		cur.LastDate = in.LastDate
		cur.Root = buildTree(cur.msgs)
	}
}

func findThread(threads []*Thread, id string) *Thread {
	for _, t := range threads {
		if t.ID == id {
			return t
		}
	}
	return nil
}

func (v *View) SetCursor(id string) {
	v.cursorID = id
}

// CursorRow returns the row the cursor points at, or the first row when
// the id is gone; ok is false only when the view is empty.
func (v *View) CursorRow() (Row, bool) {
	rows := v.Rows()
	if len(rows) == 0 {
		return Row{}, false
	}
	if v.cursorID != "" {
		for _, r := range rows {
			if r.Msg.ID == v.cursorID {
				return r, true
			}
		}
	}
	return rows[0], true
}

// buildTree attaches each message under the first reference present in
// the set; messages without a present parent become roots. Multiple
// roots get a synthetic ghost root (mutt "[...]" row).
func buildTree(msgs []*Message) *Node {
	nodes := make(map[string]*Node, len(msgs))
	for _, m := range msgs {
		nodes[m.ID] = &Node{Msg: m}
	}
	var roots []*Node
	for _, m := range msgs {
		n := nodes[m.ID]
		p := parentOf(m, nodes)
		if p == nil {
			roots = append(roots, n)
		} else {
			p.Children = append(p.Children, n)
		}
	}
	if len(roots) == 0 {
		return nil
	}
	if len(roots) == 1 {
		return roots[0]
	}
	return &Node{Children: roots}
}

func parentOf(m *Message, nodes map[string]*Node) *Node {
	for _, ref := range m.References {
		if ref == m.ID {
			continue
		}
		if p, ok := nodes[ref]; ok {
			return p
		}
	}
	return nil
}
```

- [ ] **Step 4: Run to verify they pass**

```bash
go test ./core/ -v
```

Expected: all core tests pass, including the diff property tests. `gofmt -l .` prints nothing.

- [ ] **Step 5: Commit**

```bash
cd /home/user/git/opencode/notmutt
git add src/core
git commit -m "Add thread-tree view model with diff-merge, cursor, collapse"
```

## Task 7: MIME cache

**Files:**
- Create: `src/cache/cache.go`, `src/cache/bbolt.go`, `src/cache/scan.go`, `src/cache/bbolt_test.go`, `src/cache/scan_test.go`

- [ ] **Step 1: Write the failing tests**

`src/cache/bbolt_test.go`:

```go
package cache

import (
	"os"
	"path/filepath"
	"testing"

	"go.etcd.io/bbolt"

	"notmutt/core"
)

func TestBboltRoundtrip(t *testing.T) {
	c, err := Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	k := Key{Path: "/m/Mail/x", Size: 10, Mtime: 5}
	atts := []core.Attachment{{Name: "evil.txt", MimeType: "text/plain", Size: 3}}
	if err := c.Put(k, atts); err != nil {
		t.Fatal(err)
	}
	got, ok, err := c.Get(k)
	if err != nil || !ok || len(got) != 1 || got[0].Name != "evil.txt" {
		t.Fatalf("get: %v %v %v", got, ok, err)
	}
	miss := Key{Path: "/m/Mail/y", Size: 1, Mtime: 1}
	if _, ok, _ := c.Get(miss); ok {
		t.Fatal("expected miss")
	}
}

func TestBboltFilePerms(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	c, _ := Open(path)
	c.Close()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Fatalf("cache file must be 0600, got %v", fi.Mode().Perm())
	}
}

func TestBboltCorruptPayloadDiscarded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache.db")
	c, _ := Open(path)
	k := Key{Path: "/m/Mail/z", Size: 2, Mtime: 2}
	c.Put(k, []core.Attachment{{Name: "ok"}})
	c.Close()

	db, err := bbolt.Open(path, 0600, nil)
	if err != nil {
		t.Fatal(err)
	}
	db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket([]byte("atts")).Put([]byte(k.String()), []byte("garbage"))
	})
	db.Close()

	c2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if _, ok, err := c2.Get(k); ok || err != nil {
		t.Fatalf("corrupt entry must be a miss without error, got ok=%v err=%v", ok, err)
	}
	if _, ok, _ := c2.Get(k); ok {
		t.Fatal("corrupt entry must be deleted")
	}
}
```

`src/cache/scan_test.go`:

```go
package cache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const mimeSample = `From: a@x
To: b@x
Subject: t
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="B"

--B
Content-Type: text/plain

body
--B
Content-Type: application/octet-stream
Content-Disposition: attachment; filename="evil.txt"

data
--B--
`

func TestScanAttachments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.eml")
	os.WriteFile(path, []byte(mimeSample), 0600)
	atts, err := ScanAttachments(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 1 || atts[0].Name != "evil.txt" {
		t.Fatalf("want 1 attachment evil.txt, got %+v", atts)
	}
}

func TestScanHostileFilename(t *testing.T) {
	hostile := strings.Replace(mimeSample, `evil.txt`, `$(rm -rf /).txt`, 1)
	path := filepath.Join(t.TempDir(), "m.eml")
	os.WriteFile(path, []byte(hostile), 0600)
	atts, err := ScanAttachments(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 1 || atts[0].Name != "$(rm -rf /).txt" {
		t.Fatalf("hostile name must be stored verbatim and inert: %+v", atts)
	}
}

func TestScanPlainTextNoAttachments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.eml")
	os.WriteFile(path, []byte("From: a@x\nSubject: t\nContent-Type: text/plain\n\nhi\n"), 0600)
	atts, err := ScanAttachments(path)
	if err != nil || len(atts) != 0 {
		t.Fatalf("plain text: %v %v", atts, err)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

```bash
cd /home/user/git/opencode/notmutt/src
go test ./cache/ 2>&1 | head -5
```

Expected: `undefined: Open`.

- [ ] **Step 3: Implement**

`src/cache/cache.go`:

```go
package cache

import (
	"fmt"

	"notmutt/core"
)

type Key struct {
	Path  string
	Size  int64
	Mtime int64
}

func (k Key) String() string {
	return fmt.Sprintf("%s\x00%d\x00%d", k.Path, k.Size, k.Mtime)
}

type Cache interface {
	Get(k Key) ([]core.Attachment, bool, error)
	Put(k Key, atts []core.Attachment) error
	Delete(k Key) error
	Close() error
}
```

`src/cache/bbolt.go`:

```go
package cache

import (
	"bytes"
	"encoding/gob"

	"go.etcd.io/bbolt"

	"notmutt/core"
)

var bucket = []byte("atts")

// Bbolt is the default Cache backend. The file is 0600 (F5); corrupt
// payloads are discarded, never fatal (defensive parse).
type Bbolt struct {
	db *bbolt.DB
}

func Open(path string) (*Bbolt, error) {
	db, err := bbolt.Open(path, 0600, nil)
	if err != nil {
		return nil, err
	}
	err = db.Update(func(tx *bbolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists(bucket)
		return err
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Bbolt{db: db}, nil
}

func (b *Bbolt) Get(k Key) ([]core.Attachment, bool, error) {
	var atts []core.Attachment
	var found bool
	err := b.db.View(func(tx *bbolt.Tx) error {
		v := tx.Bucket(bucket).Get([]byte(k.String()))
		if v == nil {
			return nil
		}
		atts = decode(v)
		found = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if found && atts == nil {
		b.Delete(k) // corrupt entry: discard
		return nil, false, nil
	}
	return atts, found, nil
}

func decode(v []byte) []core.Attachment {
	var atts []core.Attachment
	if err := gob.NewDecoder(bytes.NewReader(v)).Decode(&atts); err != nil {
		return nil
	}
	return atts
}

func (b *Bbolt) Put(k Key, atts []core.Attachment) error {
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(atts); err != nil {
		return err
	}
	return b.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucket).Put([]byte(k.String()), buf.Bytes())
	})
}

func (b *Bbolt) Delete(k Key) error {
	return b.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucket).Delete([]byte(k.String()))
	})
}

func (b *Bbolt) Close() error {
	return b.db.Close()
}
```

`src/cache/scan.go`:

```go
package cache

import (
	"io"
	"mime"
	"os"
	"strconv"
	"strings"

	"github.com/emersion/go-message/mail"

	"notmutt/core"
)

// ScanAttachments parses a message file and returns its attachment list.
// Content never leaves this function; the result feeds the row slot only.
func ScanAttachments(path string) ([]core.Attachment, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m, err := mail.ReadMessage(f)
	if err != nil {
		return nil, err
	}
	var atts []core.Attachment
	walk(m, &atts)
	return atts, nil
}

func walk(m *mail.Message, atts *[]core.Attachment) {
	mt, _, err := m.Header.ContentType()
	if err != nil || !strings.HasPrefix(mt, "multipart/") {
		return
	}
	mr := m.MultipartReader()
	if mr == nil {
		return
	}
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			return
		}
		if err != nil {
			return
		}
		if name := filename(p); name != "" || isAttachment(p) {
			*atts = append(*atts, core.Attachment{
				Name:     name,
				MimeType: p.Header.Get("Content-Type"),
				Size:     contentLength(p.Header.Get("Content-Length")),
			})
		}
		walk(p, atts)
	}
}

func filename(p *mail.Message) string {
	if cd, err := p.Header.ContentDisposition(); err == nil {
		if _, params, err := mime.ParseMediaType(cd); err == nil {
			return params["filename"]
		}
	}
	return ""
}

func isAttachment(p *mail.Message) bool {
	cd, err := p.Header.ContentDisposition()
	if err != nil {
		return false
	}
	return strings.HasPrefix(cd, "attachment")
}

func contentLength(s string) int64 {
	if s == "" {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}
```

- [ ] **Step 4: Run to verify they pass**

```bash
go test ./cache/ -v
```

Expected: all 6 tests pass. `gofmt -l .` prints nothing.

- [ ] **Step 5: Commit**

```bash
cd /home/user/git/opencode/notmutt
git add src/cache
git commit -m "Add bbolt MIME cache and go-message attachment scan"
```

## Task 8: Notmuch CLI backend

**Files:**
- Create: `src/notmuch/backend.go`, `src/notmuch/cli.go`, `src/notmuch/cli_test.go`

- [ ] **Step 1: Write the failing tests**

`src/notmuch/cli_test.go`:

```go
package notmuch

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const searchJSON = `[{"thread":"t1","timestamp":1700000000,"authors":"A B","subject":"S","tags":["inbox","unread"],"id":"m1","total":1,"matched":1}]`

const showJSON = `[[{"id":"m1","thread":"t1","timestamp":1700000000,"authors":"A B","subject":"S","tags":["inbox"],"references":["p1"]}]]`

func fakeRun(b *CLIBackend, respond func(name string, args []string) ([]byte, error)) {
	b.run = func(ctx context.Context, name string, args []string) ([]byte, error) {
		return respond(name, args)
	}
}

func TestCLIQuery(t *testing.T) {
	b := NewCLI()
	fakeRun(b, func(name string, args []string) ([]byte, error) {
		if strings.Contains(strings.Join(args, " "), "--output=files") {
			return []byte("/m/Mail/x/1\n"), nil
		}
		return []byte(searchJSON), nil
	})
	msgs, err := b.Query(context.Background(), "tag:inbox", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].ID != "m1" || msgs[0].ThreadID != "t1" || msgs[0].Author != "A B" {
		t.Fatalf("parse wrong: %+v", msgs)
	}
	if len(msgs[0].Paths) != 1 || msgs[0].Paths[0] != "/m/Mail/x/1" {
		t.Fatalf("paths pairing wrong: %+v", msgs[0].Paths)
	}
	if msgs[0].Tags[0] != "inbox" {
		t.Fatalf("tags wrong: %+v", msgs[0].Tags)
	}
}

func TestCLIThread(t *testing.T) {
	b := NewCLI()
	fakeRun(b, func(name string, args []string) ([]byte, error) {
		return []byte(showJSON), nil
	})
	msgs, err := b.Thread(context.Background(), "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 1 || msgs[0].References[0] != "p1" {
		t.Fatalf("thread parse wrong: %+v", msgs)
	}
}

func TestCLIRevision(t *testing.T) {
	b := NewCLI()
	fakeRun(b, func(name string, args []string) ([]byte, error) {
		return []byte("94443\t03b22d86-cf7e-4c5f-ac9d-1678e29d8232\t557071\n"), nil
	})
	uuid, rev, err := b.Revision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if uuid != "03b22d86-cf7e-4c5f-ac9d-1678e29d8232" || rev != 557071 {
		t.Fatalf("revision parse wrong: %q %d", uuid, rev)
	}
}

func TestCLITagArgs(t *testing.T) {
	b := NewCLI()
	var got []string
	fakeRun(b, func(name string, args []string) ([]byte, error) {
		got = args
		return nil, nil
	})
	if err := b.Tag(context.Background(), `id:"weird id"`, []TagOp{{Tag: "unread", Add: false}, {Tag: "inbox", Add: true}}); err != nil {
		t.Fatal(err)
	}
	want := []string{"tag", "-unread", "+inbox", `id:"weird id"`}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("argv wrong: %v", got)
	}
}

func TestCLIQueryError(t *testing.T) {
	b := NewCLI()
	fakeRun(b, func(name string, args []string) ([]byte, error) {
		return []byte("notmuch error: something"), errors.New("exit status 1")
	})
	if _, err := b.Query(context.Background(), "tag:inbox", 10); err == nil {
		t.Fatal("expected error")
	}
}
```

- [ ] **Step 2: Run to verify they fail**

```bash
cd /home/user/git/opencode/notmutt/src
go test ./notmuch/ 2>&1 | head -5
```

Expected: `undefined: NewCLI`.

- [ ] **Step 3: Implement**

`src/notmuch/backend.go`:

```go
package notmuch

import (
	"context"
	"errors"
)

var ErrLockTimeout = errors.New("notmuch lock timeout")

type TagOp struct {
	Tag string
	Add bool
}

// Backend is the notmuch access boundary. M1 ships the CLI backend; the
// cgo backend implements the same interface for the benchmark (task 13).
type Backend interface {
	Open(ctx context.Context, dbPath string) error
	Close(ctx context.Context) error
	Query(ctx context.Context, query string, limit int) ([]Message, error)
	Thread(ctx context.Context, threadID string) ([]Message, error)
	Tag(ctx context.Context, query string, ops []TagOp) error
	Revision(ctx context.Context) (uuid string, rev uint64, err error)
	New(ctx context.Context) error
}
```

`src/notmuch/cli.go`:

```go
package notmuch

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"notmutt/core"
)

type runFn func(ctx context.Context, name string, args ...string) ([]byte, error)

// CLIBackend drives the notmuch CLI. argv only, never a shell (F4).
type CLIBackend struct {
	run runFn
}

func NewCLI() *CLIBackend {
	return &CLIBackend{run: defaultRun}
}

func defaultRun(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}

func (b *CLIBackend) Open(ctx context.Context, dbPath string) error {
	return nil
}

func (b *CLIBackend) Close(ctx context.Context) error {
	return nil
}

type searchItem struct {
	Thread    string   `json:"thread"`
	Timestamp int64    `json:"timestamp"`
	Authors   string   `json:"authors"`
	Subject   string   `json:"subject"`
	Tags      []string `json:"tags"`
	ID        string   `json:"id"`
}

// Query runs one search run plus one files run, pairing paths by index
// (maildir: one file per message; a count mismatch leaves Paths short).
func (b *CLIBackend) Query(ctx context.Context, query string, limit int) ([]core.Message, error) {
	args := []string{"search", "--format=json", "--sort=newest-first"}
	if limit > 0 {
		args = append(args, "--limit="+strconv.Itoa(limit))
	}
	args = append(args, query)
	out, err := b.run(ctx, "notmuch", args...)
	if err != nil {
		return nil, fmt.Errorf("notmuch search: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var items []searchItem
	if err := json.Unmarshal(out, &items); err != nil {
		return nil, fmt.Errorf("notmuch search: parse: %w", err)
	}
	msgs := make([]core.Message, len(items))
	for i, it := range items {
		msgs[i] = core.Message{ID: it.ID, ThreadID: it.Thread, Timestamp: it.Timestamp, Author: it.Authors, Subject: it.Subject, Tags: it.Tags}
	}
	paths, err := b.run(ctx, "notmuch", "search", "--output=files", "--sort=newest-first", query)
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(paths)), "\n")
		if lines[0] != "" {
			for i := range msgs {
				if i >= len(lines) {
					break
				}
				msgs[i].Paths = append(msgs[i].Paths, lines[i])
			}
		}
	}
	return msgs, nil
}

type showItem struct {
	ID         string   `json:"id"`
	Thread     string   `json:"thread"`
	Timestamp  int64    `json:"timestamp"`
	Authors    string   `json:"authors"`
	Subject    string   `json:"subject"`
	Tags       []string `json:"tags"`
	References []string `json:"references"`
}

// Thread fetches one thread's messages with references (show json is
// grouped by thread: a list of lists).
func (b *CLIBackend) Thread(ctx context.Context, threadID string) ([]core.Message, error) {
	out, err := b.run(ctx, "notmuch", "show", "--format=json", "--body=false", "thread:"+threadID)
	if err != nil {
		return nil, fmt.Errorf("notmuch show: %w: %s", err, strings.TrimSpace(string(out)))
	}
	var groups [][]showItem
	if err := json.Unmarshal(out, &groups); err != nil {
		return nil, fmt.Errorf("notmuch show: parse: %w", err)
	}
	var msgs []core.Message
	for _, g := range groups {
		for _, it := range g {
			msgs = append(msgs, core.Message{
				ID: it.ID, ThreadID: it.Thread, Timestamp: it.Timestamp,
				Author: it.Authors, Subject: it.Subject, Tags: it.Tags, References: it.References,
			})
		}
	}
	return msgs, nil
}

func (b *CLIBackend) Tag(ctx context.Context, query string, ops []TagOp) error {
	args := []string{"tag"}
	for _, op := range ops {
		if op.Add {
			args = append(args, "+"+op.Tag)
		} else {
			args = append(args, "-"+op.Tag)
		}
	}
	args = append(args, query)
	out, err := b.run(ctx, "notmuch", args...)
	if err != nil {
		return fmt.Errorf("notmuch tag: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Revision parses "count\tuuid\trevision" from `notmuch count --lastmod`.
func (b *CLIBackend) Revision(ctx context.Context) (string, uint64, error) {
	out, err := b.run(ctx, "notmuch", "count", "--lastmod", "")
	if err != nil {
		return "", 0, fmt.Errorf("notmuch count: %w: %s", err, strings.TrimSpace(string(out)))
	}
	fields := strings.Fields(string(bytes.TrimSpace(out)))
	if len(fields) != 3 {
		return "", 0, fmt.Errorf("notmuch count --lastmod: expected 3 fields, got %q", string(out))
	}
	rev, err := strconv.ParseUint(fields[2], 10, 64)
	if err != nil {
		return "", 0, fmt.Errorf("notmuch count --lastmod: bad revision %q", fields[2])
	}
	return fields[1], rev, nil
}

func (b *CLIBackend) New(ctx context.Context) error {
	out, err := b.run(ctx, "notmuch", "new")
	if err != nil {
		return fmt.Errorf("notmuch new: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
```

- [ ] **Step 4: Run to verify they pass**

```bash
go test ./notmuch/ -v
```

Expected: all 5 tests pass. Also verify the parse shape against the real CLI (read-only, numbers only):

```bash
notmuch count --lastmod ''
```

Expected: one line, 3 tab-separated fields (count, uuid, revision).

- [ ] **Step 5: Commit**

```bash
cd /home/user/git/opencode/notmutt
git add src/notmuch
git commit -m "Add notmuch CLI backend with JSON parsing and argv-only run"
```

## Task 9: Notmuch worker

**Files:**
- Create: `src/notmuch/worker.go`, `src/notmuch/worker_test.go`

- [ ] **Step 1: Write the failing tests**

`src/notmuch/worker_test.go`:

```go
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

func (f *fakeBackend) Open(ctx context.Context, p string) error              { return f.err }
func (f *fakeBackend) Close(ctx context.Context) error                        { return f.err }
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
func (b *blockingBackend) Close(ctx context.Context) error           { return b.inner.Close(ctx) }
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
	if _, err := w.Call(Action{Kind: ActRevision}); err != nil {
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
```

- [ ] **Step 2: Run to verify they fail**

```bash
cd /home/user/git/opencode/notmutt/src
go test ./notmuch/ -run TestWorker 2>&1 | head -5
```

Expected: `undefined: NewWorker`.

- [ ] **Step 3: Implement**

`src/notmuch/worker.go`:

```go
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
	ActThread
	ActTag
	ActRevision
	ActNew
	ActClose
)

type Action struct {
	ID       uint64
	Kind     ActionKind
	Query    string
	ThreadID string
	Limit    int
	TagOps   []TagOp
	replyCh  chan Reply
}

type Reply struct {
	ID    uint64
	Err   error
	Msgs  []core.Message
	UUID  string
	Rev   uint64
	Paths []string
}

// Worker owns backend access. Actions are handled serially; every op runs
// under a lock budget - a timeout becomes ErrLockTimeout plus a
// WorkerLockTimeout event, never a blocked UI. Start must run before
// any Call.
type Worker struct {
	bus     *core.Bus
	backend Backend
	timeout time.Duration
	actions chan Action
	ctx     context.Context
}

func NewWorker(bus *core.Bus, backend Backend, timeout time.Duration) *Worker {
	return &Worker{
		bus: bus, backend: backend, timeout: timeout,
		actions: make(chan Action, 16),
	}
}

func (w *Worker) Start(ctx context.Context) {
	w.ctx = ctx
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
	ctx, cancel := context.WithTimeout(w.ctx, w.timeout)
	defer cancel()
	r := Reply{ID: a.ID}
	var err error
	switch a.Kind {
	case ActOpen:
		err = w.backend.Open(ctx, a.Query)
	case ActQuery:
		r.Msgs, err = w.backend.Query(ctx, a.Query, a.Limit)
	case ActThread:
		r.Msgs, err = w.backend.Thread(ctx, a.ThreadID)
	case ActTag:
		err = w.backend.Tag(ctx, a.Query, a.TagOps)
		if err == nil {
			w.bus.Publish(core.WorkerDone{Job: "tag"})
		}
	case ActRevision:
		r.UUID, r.Rev, err = w.backend.Revision(ctx)
	case ActNew:
		err = w.backend.New(ctx)
		if err == nil {
			w.bus.Publish(core.WorkerDone{Job: "new"})
		}
	case ActClose:
		err = w.backend.Close(ctx)
	}
	if errors.Is(err, context.DeadlineExceeded) {
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
	case ActClose:
		return "close"
	}
	return "unknown"
}
```

- [ ] **Step 4: Run to verify they pass**

```bash
go test ./notmuch/ -v
```

Expected: all 9 tests pass.

- [ ] **Step 5: Commit**

```bash
cd /home/user/git/opencode/notmutt
git add src/notmuch
git commit -m "Add worker action loop with lock budgets and bus events"
```

## Task 10: Refresh cycle

**Files:**
- Create: `src/app/refresh.go`, `src/app/refresh_test.go`, `src/app/doc.go`

- [ ] **Step 1: Write the failing tests**

`src/app/doc.go`:

```go
package app
```

`src/app/refresh_test.go`:

```go
package app

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"notmutt/core"
	"notmutt/notmuch"
)

type fakeWorker struct {
	uuid atomic.Value
	rev  atomic.Uint64
}

func (f *fakeWorker) set(uuid string, rev uint64) {
	f.uuid.Store(uuid)
	f.rev.Store(rev)
}

func (f *fakeWorker) Call(a notmuch.Action) (notmuch.Reply, error) {
	r := notmuch.Reply{ID: a.ID}
	switch a.Kind {
	case notmuch.ActRevision:
		r.UUID, _ = f.uuid.Load().(string)
		r.Rev = f.rev.Load()
	case notmuch.ActQuery:
		r.Msgs = []core.Message{{ID: "changed", ThreadID: "t1"}}
	case notmuch.ActThread:
		r.Msgs = []core.Message{{ID: "changed", ThreadID: a.ThreadID}}
	}
	return r, nil
}

func TestCycleIncremental(t *testing.T) {
	bus := core.NewBus()
	fw := &fakeWorker{}
	fw.set("u", 10)
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{{ID: "changed", ThreadID: "t1"}})})
	r := newRefresher(bus, fw, view, 10)
	r.cycle()
	if r.rPrev != 10 || r.uuid != "u" {
		t.Fatalf("state wrong: %v %v", r.uuid, r.rPrev)
	}
	// no change: cycle is a no-op
	r.cycle()
	if len(view.Threads) != 1 {
		t.Fatalf("no-op cycle changed the view: %d threads", len(view.Threads))
	}
	// rev bump with no new ids: nothing to fetch
	fw.set("u", 11)
	r.cycle()
	// rev bump with a changed message: merged
	fw.set("u", 12)
	r.cycle()
	if len(view.Threads) != 1 {
		t.Fatalf("expected 1 thread after merge, got %d", len(view.Threads))
	}
}

func TestCycleUUIDFlipFullReload(t *testing.T) {
	bus := core.NewBus()
	fw := &fakeWorker{}
	fw.set("u1", 5)
	view := core.NewView("inbox", "tag:inbox")
	r := newRefresher(bus, fw, view, 5)
	r.cycle() // stores u1
	fw.set("u2", 6)
	ch := bus.Subscribe()
	r.cycle() // uuid mismatch: full reload path
	select {
	case e := <-ch:
		if _, ok := e.(core.ViewDiff); !ok {
			t.Fatalf("expected ViewDiff from full reload, got %T", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no ViewDiff after uuid flip")
	}
	if r.uuid != "u2" || r.rPrev != 6 {
		t.Fatalf("state not advanced: %v %v", r.uuid, r.rPrev)
	}
}

func TestCycleQuiet(t *testing.T) {
	bus := core.NewBus()
	fw := &fakeWorker{}
	fw.set("u", 10)
	view := core.NewView("inbox", "tag:inbox")
	r := newRefresher(bus, fw, view, 10)
	ch := bus.Subscribe()
	r.cycle()
	select {
	case <-ch:
		t.Fatal("no events expected on a clean cycle")
	case <-time.After(50 * time.Millisecond):
	}
}
```

- [ ] **Step 2: Run to verify they fail**

```bash
cd /home/user/git/opencode/notmutt/src
go test ./app/ 2>&1 | head -5
```

Expected: `undefined: newRefresher`.

- [ ] **Step 3: Implement**

`src/app/refresh.go`:

```go
package app

import (
	"fmt"
	"sort"
	"sync"

	"notmutt/core"
	"notmutt/notmuch"
)

type workerAPI interface {
	Call(a notmuch.Action) (notmuch.Reply, error)
}

// refresher owns the lastmod incremental cycle and the full-reload
// triggers. R_prev is the revision queried through - a change landing
// mid-cycle falls into the next one: one-cycle lag, deterministic.
type refresher struct {
	bus     *core.Bus
	worker  workerAPI
	view    *core.View
	page    int
	uuid    string
	rPrev   uint64
	running bool
	mu      sync.Mutex
}

func newRefresher(bus *core.Bus, w workerAPI, view *core.View, rPrev uint64) *refresher {
	return &refresher{bus: bus, worker: w, view: view, page: 200, rPrev: rPrev}
}

func (r *refresher) cycle() {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return
	}
	r.running = true
	defer func() { r.mu.Lock(); r.running = false; r.mu.Unlock() }()

	rpl, err := r.worker.Call(notmuch.Action{Kind: notmuch.ActRevision})
	if err != nil || rpl.Err != nil {
		return
	}
	if rpl.UUID != r.uuid {
		r.fullReload()
		r.uuid, r.rPrev = rpl.UUID, rpl.Rev
		return
	}
	if rpl.Rev == r.rPrev {
		return
	}
	msgs, err := r.changed(r.rPrev, rpl.Rev)
	if err != nil {
		return
	}
	threads := r.fetchThreads(msgs)
	if len(threads) > 0 {
		sortThreads(threads)
		r.view.MergeThreads(threads)
		r.bus.Publish(core.ViewDiff{View: r.view.Name})
	}
	r.rPrev = rpl.Rev
}

func (r *refresher) changed(prev, cur uint64) ([]core.Message, error) {
	rpl, err := r.worker.Call(notmuch.Action{
		Kind:  notmuch.ActQuery,
		Query: fmt.Sprintf("lastmod:%d..%d", prev, cur),
	})
	if err != nil || rpl.Err != nil {
		return nil, fmt.Errorf("changed query: %v %v", err, rpl.Err)
	}
	return rpl.Msgs, nil
}

// fetchThreads maps changed messages to their threads and fetches each
// thread's full state, budgeted to 3 concurrent calls.
func (r *refresher) fetchThreads(msgs []core.Message) []*core.Thread {
	ids := map[string]bool{}
	for _, m := range msgs {
		ids[m.ThreadID] = true
	}
	sem := make(chan struct{}, 3)
	threads := make([]*core.Thread, 0, len(ids))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for id := range ids {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			rpl, err := r.worker.Call(notmuch.Action{Kind: notmuch.ActThread, ThreadID: id})
			if err != nil || rpl.Err != nil {
				return
			}
			mu.Lock()
			threads = append(threads, core.NewThread(id, rpl.Msgs))
			mu.Unlock()
		}(id)
	}
	wg.Wait()
	return threads
}

// fullReload re-fetches the view query and merges; cursor survives via
// the merge walk. Called for uuid changes, manual refresh, view config
// changes, and first load.
func (r *refresher) fullReload() {
	rpl, err := r.worker.Call(notmuch.Action{Kind: notmuch.ActQuery, Query: r.view.Query, Limit: r.page})
	if err != nil || rpl.Err != nil {
		return
	}
	threads := r.fetchThreads(rpl.Msgs)
	sortThreads(threads)
	r.view.MergeThreads(threads)
	r.bus.Publish(core.ViewDiff{View: r.view.Name})
}

func sortThreads(threads []*core.Thread) {
	sort.Slice(threads, func(i, j int) bool { return core.ThreadLess(threads[i], threads[j]) })
}
```

- [ ] **Step 4: Run to verify they pass**

```bash
go test ./app/ -v
```

Expected: all 3 tests pass.

- [ ] **Step 5: Commit**

```bash
cd /home/user/git/opencode/notmutt
git add src/app
git commit -m "Add lastmod incremental refresh cycle with full-reload triggers"
```

## Task 11: TUI

**Files:**
- Create: `src/tui/model.go`, `src/tui/bridge.go`, `src/tui/hooks.go`, `src/tui/model_test.go`

- [ ] **Step 1: Write the failing tests**

`src/tui/model_test.go`:

```go
package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"notmutt/core"
)

func model() Model {
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Author: "Ann", Subject: "hello", Tags: []string{"inbox", "unread"}},
		{ID: "b", Timestamp: 200, Author: "Bob", Subject: "re: hello", Tags: []string{"inbox"}},
	})})
	return New(view, nil)
}

func press(t *testing.T, m tea.Model, key string) tea.Model {
	t.Helper()
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	return next.(Model)
}

func TestCursorMoves(t *testing.T) {
	m := model()
	if m.CursorIndex() != 0 {
		t.Fatalf("cursor starts at 0, got %d", m.CursorIndex())
	}
	m = press(t, m, "j")
	if m.CursorIndex() != 1 {
		t.Fatalf("cursor after j = %d", m.CursorIndex())
	}
	m = press(t, m, "j")
	if m.CursorIndex() != 1 {
		t.Fatalf("cursor must clamp at bottom, got %d", m.CursorIndex())
	}
	m = press(t, m, "k")
	if m.CursorIndex() != 0 {
		t.Fatalf("cursor after k = %d", m.CursorIndex())
	}
}

func TestRenderShowsRows(t *testing.T) {
	m := model()
	m.width, m.height = 80, 24
	out := m.View()
	if out == "" {
		t.Fatal("empty render")
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("subject missing from render:\n%s", out)
	}
	if !strings.Contains(out, "Ann") {
		t.Fatalf("author missing from render:\n%s", out)
	}
}

func TestQuit(t *testing.T) {
	m := model()
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Fatal("q must return a quit command")
	}
}

func TestEventMsgRepaints(t *testing.T) {
	m := model()
	m.view.SetCursor("a")
	next, _ := m.Update(EventMsg{Event: core.ViewDiff{View: "inbox"}})
	if next.(Model).CursorIndex() != 1 {
		t.Fatalf("cursor by id after event = %d", next.(Model).CursorIndex())
	}
}
```

- [ ] **Step 2: Run to verify they fail**

```bash
cd /home/user/git/opencode/notmutt/src
go test ./tui/ 2>&1 | head -5
```

Expected: `undefined: New`.

- [ ] **Step 3: Implement**

`src/tui/model.go`:

```go
package tui

import (
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"notmutt/core"
)

type Model struct {
	view   *core.View
	ch     <-chan core.Event
	rows   []core.Row
	width  int
	height int
}

func New(view *core.View, ch <-chan core.Event) Model {
	return Model{view: view, ch: ch}
}

func (m Model) Init() tea.Cmd {
	return EventCmd(m.ch)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case tea.KeyMsg:
		switch string(msg.Runes) {
		case "j":
			m.moveCursor(1)
		case "k":
			m.moveCursor(-1)
		case "q":
			return m, tea.Quit
		case "t":
			m.toggleRead()
		}
	case EventMsg:
		m.rows = m.view.Rows()
		return m, EventCmd(m.ch)
	}
	return m, nil
}

func (m *Model) moveCursor(delta int) {
	rows := m.view.Rows()
	m.rows = rows
	if len(rows) == 0 {
		return
	}
	idx := m.CursorIndex()
	idx += delta
	if idx < 0 {
		idx = 0
	}
	if idx >= len(rows) {
		idx = len(rows) - 1
	}
	m.view.SetCursor(rows[idx].Msg.ID)
}

func (m *Model) toggleRead() {
	row, ok := m.view.CursorRow()
	if !ok {
		return
	}
	has := false
	for _, t := range row.Msg.Tags {
		if t == "unread" {
			has = true
		}
	}
	// optimistic local flip; the refresh cycle converges from DB truth
	if has {
		row.Msg.Tags = removeTag(row.Msg.Tags, "unread")
	} else {
		row.Msg.Tags = append(row.Msg.Tags, "unread")
	}
	onTagOp(row.Msg.ID, !has)
}

func removeTag(tags []string, tag string) []string {
	out := tags[:0]
	for _, t := range tags {
		if t != tag {
			out = append(out, t)
		}
	}
	return out
}

func (m Model) CursorIndex() int {
	row, ok := m.view.CursorRow()
	if !ok {
		return 0
	}
	for i, r := range m.rows {
		if r.Msg.ID == row.Msg.ID {
			return i
		}
	}
	return 0
}

func (m Model) View() string {
	if m.rows == nil {
		m.rows = m.view.Rows()
	}
	rows := m.rows
	if len(rows) == 0 {
		return "empty\n"
	}
	cur := m.CursorIndex()
	top := cur - m.height/2
	if top < 0 {
		top = 0
	}
	bottom := top + m.height
	if bottom > len(rows) {
		bottom = len(rows)
		top = bottom - m.height
		if top < 0 {
			top = 0
		}
	}
	var b strings.Builder
	for i := top; i < bottom; i++ {
		line := renderRow(i+1, rows[i])
		if i == cur {
			line = "\x1b[7m" + line + "\x1b[0m"
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// renderRow renders the fixed-slot template (R11): number, flags,
// attachment, date, author, subject, tags. Optional slots reserve width.
func renderRow(n int, row core.Row) string {
	var b strings.Builder
	b.WriteString(padCellsRight(strconv.Itoa(n), 4))
	b.WriteByte(' ')
	b.WriteString(flags(row.Msg))
	b.WriteString(attachIcon(row.Msg))
	b.WriteByte(' ')
	b.WriteString(padCellsRight(formatDate(row.Msg.Timestamp), 15))
	b.WriteByte(' ')
	b.WriteString(padCellsRight(truncCells(row.Msg.Author, 16), 16))
	b.WriteByte(' ')
	b.WriteString(truncCells(row.Msg.Subject, 40))
	b.WriteByte(' ')
	b.WriteString(tagGlyphs(row.Msg))
	return b.String()
}

func flags(m *core.Message) string {
	var f strings.Builder
	for _, t := range m.Tags {
		switch t {
		case "unread":
			f.WriteByte('U')
		case "replied":
			f.WriteByte('R')
		case "forwarded":
			f.WriteByte('F')
		case "deleted":
			f.WriteByte('D')
		}
	}
	return padCellsRight(f.String(), 3)
}

func attachIcon(m *core.Message) string {
	if len(m.Atts) > 0 {
		return "A"
	}
	return " "
}

func formatDate(ts int64) string {
	return time.Unix(ts, 0).Format("06/01/02 15:04")
}

func tagGlyphs(m *core.Message) string {
	// max 2 tags, first two of the message's own order; the tag-groups
	// slice supplies the priority list later (spec section 6)
	var b strings.Builder
	n := 0
	for _, t := range m.Tags {
		if t == "unread" {
			continue
		}
		if n >= 2 {
			break
		}
		b.WriteString(padCellsRight(truncCells(t, 4), 4))
		b.WriteByte(' ')
		n++
	}
	return strings.TrimRight(b.String(), " ")
}

// truncCells truncates s to at most w terminal cells; padCellsRight pads
// it to exactly w cells (wcwidth, not runes).
func truncCells(s string, w int) string {
	var b strings.Builder
	cells := 0
	for _, r := range s {
		cw := runewidth.RuneWidth(r)
		if cells+cw > w {
			break
		}
		b.WriteRune(r)
		cells += cw
	}
	return b.String()
}

func padCellsRight(s string, w int) string {
	cells := runewidth.StringWidth(s)
	if cells >= w {
		return truncCells(s, w)
	}
	return s + strings.Repeat(" ", w-cells)
}
```

`src/tui/bridge.go`:

```go
package tui

import (
	tea "github.com/charmbracelet/bubbletea"

	"notmutt/core"
)

type EventMsg struct{ Event core.Event }

// EventCmd forwards one bus event into BubbleTea. Re-arm it after every
// EventMsg (and from Init) to keep the loop alive. A nil channel waits
// forever (tests), which is fine.
func EventCmd(ch <-chan core.Event) tea.Cmd {
	return func() tea.Msg {
		return EventMsg{Event: <-ch}
	}
}
```

`src/tui/hooks.go`:

```go
package tui

// onTagOp is the worker seam: the app wires it with SetTagOpHandler; the
// default is a no-op so the model works in tests.
var onTagOp = func(msgID string, add bool) {}

func SetTagOpHandler(fn func(msgID string, add bool)) {
	onTagOp = fn
}
```

- [ ] **Step 4: Run to verify they pass**

```bash
go test ./tui/ -v
```

Expected: all 4 tests pass. `gofmt -l .` prints nothing.

- [ ] **Step 5: Commit**

```bash
cd /home/user/git/opencode/notmutt
git add src/tui
git commit -m "Add BubbleTea index view with fixed-slot rows and bus bridge"
```

## Task 12: App wiring

**Files:**
- Create: `src/app/app.go`, `src/app/cachejob.go`, `src/main.go`

- [ ] **Step 1: Implement**

`src/main.go`:

```go
package main

import (
	"os"

	"notmutt/app"
)

func main() {
	if err := app.Run(); err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
}
```

`src/app/app.go`:

```go
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"notmutt/cache"
	"notmutt/config"
	"notmutt/core"
	"notmutt/notmuch"
	"notmutt/tui"
)

const lockBudget = 10 * time.Second

func Run() error {
	cfg, err := config.Load(configPath())
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	bus := core.NewBus()
	worker := notmuch.NewWorker(bus, notmuch.NewCLI(), lockBudget)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go worker.Start(ctx)

	// DB open check plus per-view query validation (spec section 3:
	// notmuch dry run for every view query)
	for name, v := range cfg.Views {
		rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActQuery, Query: v.Query, Limit: 1})
		if err != nil || rpl.Err != nil {
			return fmt.Errorf("view %q: query %q: %v %v", name, v.Query, err, rpl.Err)
		}
	}

	name := firstView(cfg)
	view := core.NewView(name, cfg.Views[name].Query)
	refresher := newRefresher(bus, worker, view, 0)
	refresher.cycle() // initial load (full reload path)

	cjob := newCacheJob(bus, worker, view, cachePath())
	go cjob.Run(ctx)

	tui.SetTagOpHandler(func(msgID string, add bool) {
		go func() {
			worker.Call(notmuch.Action{
				Kind:  notmuch.ActTag,
				Query: "id:\"" + strings.ReplaceAll(msgID, `"`, `\"`) + `"`,
				TagOps: []notmuch.TagOp{{Tag: "unread", Add: add}},
			})
		}()
	})

	go runRefresher(ctx, bus, worker, view, refresher)

	busCh := bus.Subscribe()
	prog := tea.NewProgram(tui.New(view, busCh), tea.WithAltScreen())
	go func() {
		<-ctx.Done()
		prog.Quit()
	}()
	return prog.Run()
}

func runRefresher(ctx context.Context, bus *core.Bus, worker workerAPI, view *core.View, r *refresher) {
	ch := bus.Subscribe()
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			worker.Call(notmuch.Action{Kind: notmuch.ActNew})
			r.cycle()
		case e := <-ch:
			switch e.(type) {
			case core.WorkerDone:
				r.cycle()
			case core.ConfigChanged:
				r.fullReload()
			}
		}
	}
}

func configPath() string {
	if p := os.Getenv("NOTMUTT_CONFIG"); p != "" {
		return p
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "config.toml"
	}
	return filepath.Join(base, "notmutt", "config.toml")
}

func cachePath() string {
	base, err := os.UserCacheDir()
	if err != nil {
		return "mime-cache.db"
	}
	return filepath.Join(base, "notmutt", "mime-cache.db")
}

func firstView(cfg config.Config) string {
	for name := range cfg.Views {
		return name
	}
	return ""
}
```

`src/app/cachejob.go`:

```go
package app

import (
	"context"
	"os"
	"path/filepath"
	"sync"

	"notmutt/cache"
	"notmutt/core"
)

const scanPage = 40

// cacheJob fills the MIME cache for visible rows, budgeted to 2
// concurrent scans. Results land in the row model; the TUI repaints on
// any event.
type cacheJob struct {
	bus    *core.Bus
	worker workerAPI
	view   *core.View
	cache  cache.Cache
}

func newCacheJob(bus *core.Bus, w workerAPI, view *core.View, dbPath string) *cacheJob {
	cj := &cacheJob{bus: bus, worker: w, view: view}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		return cj
	}
	c, err := cache.Open(dbPath)
	if err != nil {
		return cj
	}
	cj.cache = c
	return cj
}

func (c *cacheJob) Run(ctx context.Context) {
	if c.cache == nil {
		return
	}
	ch := c.bus.Subscribe()
	sem := make(chan struct{}, 2)
	c.scanVisible(sem) // initial fill: the first ViewDiff was already published
	for {
		select {
		case <-ctx.Done():
			c.cache.Close()
			return
		case e := <-ch:
			switch e.(type) {
			case core.ViewDiff, core.QueryBatch:
				c.scanVisible(sem)
			}
		}
	}
}

func (c *cacheJob) scanVisible(sem chan struct{}) {
	rows := c.view.Rows()
	if len(rows) > scanPage {
		rows = rows[:scanPage]
	}
	var wg sync.WaitGroup
	for _, r := range rows {
		m := r.Msg
		if len(m.Paths) == 0 || len(m.Atts) > 0 {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			for _, p := range m.Paths {
				fi, err := os.Stat(p)
				if err != nil {
					continue
				}
				k := cache.Key{Path: p, Size: fi.Size(), Mtime: fi.ModTime().Unix()}
				if atts, ok, err := c.cache.Get(k); err == nil && ok {
					m.Atts = atts
					c.bus.Publish(core.CacheResult{MsgID: m.ID, Atts: atts})
					return
				}
				atts, err := cache.ScanAttachments(p)
				if err != nil {
					continue
				}
				c.cache.Put(k, atts)
				m.Atts = atts
				c.bus.Publish(core.CacheResult{MsgID: m.ID, Atts: atts})
				return
			}
		}()
	}
	wg.Wait()
}
```

- [ ] **Step 2: Compile and smoke test against the real DB**

```bash
cd /home/user/git/opencode/notmutt/src
go build ./... && go vet ./...
NOTMUTT_CONFIG=/home/user/git/opencode/notmutt/docs/examples/config.toml timeout 5 ./notmutt
```

Expected: builds clean, vet clean; `timeout 5` kills the TUI after 5s - the run is the smoke check (it opens the real DB, renders the inbox; exit code 124 from timeout is expected). Any panic or error message on stderr is a failure.

Note: the TUI is an alt-screen program; run it in a real terminal to confirm rendering - inbox rows, j/k navigation, `t` flips unread (check with `notmuch count tag:inbox and tag:unread` before/after in another terminal), `q` quits.

- [ ] **Step 3: Commit**

```bash
cd /home/user/git/opencode/notmutt
git add src/main.go src/app
git commit -m "Wire app: config, worker, refresher, cache job, TUI"
```

## Task 13: cgo backend (benchmark path)

**Files:**
- Create: `src/notmuch/cgo.go` (build tag `cgo`), `src/notmuch/cgo_test.go`

- [ ] **Step 1: Check dev headers**

```bash
pkg-config --exists notmuch && echo "headers present" || echo "headers MISSING"
```

Expected (current machine): `headers MISSING`. Install one of:

```bash
# option A (needs user approval, sudo):
sudo pacman -S notmuch
# option B: build from the workspace checkout (no sudo) - needs cmake and
# the xapian/gmime/zlib dev packages installed first
cd /home/user/git/opencode/notmutt/notmuch
cmake -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build -j
export PKG_CONFIG_PATH=$PWD/build/pkgconfig
```

Ask the user which option they prefer before running anything that needs sudo. The cgo backend must not compile if headers are missing - that is the point of the build tag.

- [ ] **Step 2: Implement the cgo backend**

`src/notmuch/cgo.go`:

```go
//go:build cgo

package notmuch

/*
#cgo pkg-config: notmuch
#include <notmuch.h>
#include <stdlib.h>
*/
import "C"

import (
	"context"
	"fmt"
	"unsafe"

	"notmutt/core"
)

// CGOBackend is the in-tree binding (notmuch contrib pattern). It exists
// only for the benchmark; the CLI backend stays the default unless cgo
// demonstrably wins (SECURITY.md F10).
type CGOBackend struct {
	db *C.notmuch_database_t
}

func NewCGO() *CGOBackend { return &CGOBackend{} }

func (b *CGOBackend) Open(ctx context.Context, dbPath string) error {
	var db *C.notmuch_database_t
	path := C.CString(dbPath)
	defer C.free(unsafe.Pointer(path))
	if st := C.notmuch_database_open(path, C.NOTMUCH_DATABASE_MODE_READ_ONLY, &db); st != C.NOTMUCH_STATUS_SUCCESS {
		return errStatus(st, "open")
	}
	b.db = db
	return nil
}

func (b *CGOBackend) Close(ctx context.Context) error {
	if b.db != nil {
		C.notmuch_database_destroy(b.db)
		b.db = nil
	}
	return nil
}

func (b *CGOBackend) Revision(ctx context.Context) (string, uint64, error) {
	rev := C.notmuch_database_get_revision(b.db)
	uuid := C.notmuch_database_get_revision_uuid(b.db)
	return C.GoString(uuid), uint64(rev), nil
}

func (b *CGOBackend) Query(ctx context.Context, query string, limit int) ([]core.Message, error) {
	qstr := C.CString(query)
	defer C.free(unsafe.Pointer(qstr))
	q := C.notmuch_query_create(b.db, qstr)
	if q == nil {
		return nil, fmt.Errorf("notmuch: query_create failed")
	}
	defer C.notmuch_query_destroy(q)
	C.notmuch_query_set_sort(q, C.NOTMUCH_SORT_NEWEST_FIRST)
	var msgs *C.notmuch_messages_t
	if st := C.notmuch_query_search_messages(q, &msgs); st != C.NOTMUCH_STATUS_SUCCESS {
		return nil, errStatus(st, "search")
	}
	defer C.notmuch_messages_destroy(msgs)
	var out []core.Message
	for C.notmuch_messages_valid(msgs) != 0 {
		m := C.notmuch_messages_get(msgs)
		header := func(name string) string {
			c := C.CString(name)
			defer C.free(unsafe.Pointer(c))
			return C.GoString(C.notmuch_message_get_header(m, c))
		}
		out = append(out, core.Message{
			ID:        C.GoString(C.notmuch_message_get_message_id(m)),
			ThreadID:  C.GoString(C.notmuch_message_get_thread_id(m)),
			Timestamp: int64(C.notmuch_message_get_date(m)),
			Author:    header("from"),
			Subject:   header("subject"),
			Tags:      tagsOf(m),
			Paths:     pathsOf(m),
		})
		C.notmuch_message_destroy(m)
		C.notmuch_messages_move_to_next(msgs)
	}
	return out, nil
}

func (b *CGOBackend) Thread(ctx context.Context, threadID string) ([]core.Message, error) {
	return b.Query(ctx, "thread:"+threadID, 0)
}

func (b *CGOBackend) Tag(ctx context.Context, query string, ops []TagOp) error {
	return errStatus(C.NOTMUCH_STATUS_UNSUPPORTED_OPERATION, "tag (read-only handle)")
}

func (b *CGOBackend) New(ctx context.Context) error {
	return errStatus(C.NOTMUCH_STATUS_UNSUPPORTED_OPERATION, "new (read-only handle)")
}

func tagsOf(m *C.notmuch_message_t) []string {
	var out []string
	for t := C.notmuch_message_get_tags(m); C.notmuch_tags_valid(t) != 0; C.notmuch_tags_move_to_next(t) {
		out = append(out, C.GoString(C.notmuch_tags_get(t)))
	}
	return out
}

func pathsOf(m *C.notmuch_message_t) []string {
	var out []string
	for f := C.notmuch_message_get_filenames(m); C.notmuch_filenames_valid(f) != 0; C.notmuch_filenames_move_to_next(f) {
		out = append(out, C.GoString(C.notmuch_filenames_get(f)))
	}
	return out
}

func errStatus(st C.notmuch_status_t, op string) error {
	return fmt.Errorf("notmuch %s: %s", op, C.GoString(C.notmuch_status_to_string(st)))
}
```

- [ ] **Step 3: Verify it compiles with the tag**

```bash
cd /home/user/git/opencode/notmutt/src
go build -tags cgo ./...
go test -tags cgo ./notmuch/ -run TestCGOSmoke -v
```

`src/notmuch/cgo_test.go`:

```go
//go:build cgo

package notmuch

import (
	"context"
	"os"
	"testing"
)

func TestCGOSmoke(t *testing.T) {
	db := os.Getenv("NOTMUCH_DB")
	if db == "" {
		db = "/home/user/Mail"
	}
	b := NewCGO()
	if err := b.Open(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	uuid, rev, err := b.Revision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if uuid == "" || rev == 0 {
		t.Fatalf("revision: %q %d", uuid, rev)
	}
	msgs, err := b.Query(context.Background(), "tag:inbox", 10)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("got %d messages, rev %d (counts only, no content)", len(msgs), rev)
}
```

Expected: PASS, logs message count only. If headers are missing, this task stays blocked until the user installs them - the build tag keeps the default build clean either way.

- [ ] **Step 4: Commit**

```bash
cd /home/user/git/opencode/notmutt
git add src/notmuch
git commit -m "Add cgo notmuch backend behind the Backend interface (build tag)"
```

## Task 14: Benchmark and backend selection

**Files:**
- Create: `src/notmuch/bench_test.go`, `docs/superpowers/benchmarks/2026-08-14-notmuch-backend.md`

- [ ] **Step 1: Write the benchmark test (env-gated)**

`src/notmuch/bench_test.go`:

```go
package notmuch

import (
	"context"
	"os"
	"testing"
	"time"
)

// Run: NOTMUCH_BENCH=1 go test -tags cgo ./notmuch/ -run TestBench -v
// Requires notmuch dev headers (task 13). Prints a comparison report.
func TestBench(t *testing.T) {
	if os.Getenv("NOTMUCH_BENCH") == "" {
		t.Skip("set NOTMUCH_BENCH=1 to run")
	}
	db := os.Getenv("NOTMUCH_DB")
	if db == "" {
		db = "/home/user/Mail"
	}
	ctx := context.Background()

	cli := NewCLI()
	cli.Open(ctx, db)
	defer cli.Close(ctx)
	cgoB := NewCGO()
	if err := cgoB.Open(ctx, db); err != nil {
		t.Fatalf("cgo open: %v", err)
	}
	defer cgoB.Close(ctx)

	report := func(name string, fn func() (int, error)) {
		t0 := time.Now()
		n, err := fn()
		if err != nil {
			t.Logf("%-30s ERROR %v", name, err)
			return
		}
		t.Logf("%-30s %8s  (%d msgs)", name, time.Since(t0).Round(time.Millisecond), n)
	}

	report("cli first-page (50)", func() (int, error) {
		msgs, err := cli.Query(ctx, "tag:inbox", 50)
		return len(msgs), err
	})
	report("cgo first-page (50)", func() (int, error) {
		msgs, err := cgoB.Query(ctx, "tag:inbox", 50)
		return len(msgs), err
	})
	report("cli full inbox", func() (int, error) {
		msgs, err := cli.Query(ctx, "tag:inbox", 0)
		return len(msgs), err
	})
	report("cgo full inbox", func() (int, error) {
		msgs, err := cgoB.Query(ctx, "tag:inbox", 0)
		return len(msgs), err
	})
	report("cli thread fetch", func() (int, error) {
		msgs, err := cli.Thread(ctx, firstThreadID(t, ctx, cli))
		return len(msgs), err
	})
	report("cgo thread fetch", func() (int, error) {
		msgs, err := cgoB.Thread(ctx, firstThreadID(t, ctx, cgoB))
		return len(msgs), err
	})
}

func firstThreadID(t *testing.T, ctx context.Context, b Backend) string {
	t.Helper()
	msgs, err := b.Query(ctx, "tag:inbox", 1)
	if err != nil || len(msgs) == 0 {
		t.Fatalf("seed query: %v %d", err, len(msgs))
	}
	return msgs[0].ThreadID
}
```

- [ ] **Step 2: Run the benchmark**

```bash
cd /home/user/git/opencode/notmutt/src
NOTMUCH_BENCH=1 go test -tags cgo ./notmuch/ -run TestBench -v -count=3 2>&1 | grep -E "cli|cgo|PASS|FAIL"
```

Expected: a timing table over 3 runs each. Lock-timeout behavior is already unit-tested (TestWorkerLockTimeout); record the configured CLI value in the report:

```bash
notmuch config get lock_timeout
```

Expected: `10` (or the configured value; record it).

- [ ] **Step 3: Write the report and pick the default**

`docs/superpowers/benchmarks/2026-08-14-notmuch-backend.md` with the actual timings table from Step 2, the lock-timeout observation, and the conclusion. Rule (SECURITY.md F10): CLI stays default unless cgo demonstrably wins on first-page latency; flip the default in `app/app.go` only with the numbers in the report.

- [ ] **Step 4: Commit**

```bash
cd /home/user/git/opencode/notmutt
git add src/notmuch/bench_test.go docs/superpowers/benchmarks
git commit -m "Add backend benchmark and report; CLI remains default"
```

## Task 15: Integration tests, soak, acceptance

**Files:**
- Create: `src/app/soak_test.go`, `src/app/cursor_test.go`

- [ ] **Step 1: Write the soak test (env-gated, never in normal CI)**

`src/app/soak_test.go`:

```go
package app

import (
	"context"
	"os"
	"testing"
	"time"

	"notmutt/core"
	"notmutt/notmuch"
)

// Run: NOTMUCH_SOAK=1 go test ./app/ -run TestSoak -v
// Mutates the real DB with a scratch tag, fully reversed. Prints counts
// and ids only - never subjects or headers (privacy rule).
func TestSoak(t *testing.T) {
	if os.Getenv("NOTMUCH_SOAK") == "" {
		t.Skip("set NOTMUCH_SOAK=1 to run against the real DB")
	}
	ctx := context.Background()
	worker := notmuch.NewWorker(core.NewBus(), notmuch.NewCLI(), 10*time.Second)
	go worker.Start(ctx)
	defer worker.Call(notmuch.Action{Kind: notmuch.ActClose})

	const scratch = "notmutt-soak"
	defer worker.Call(notmuch.Action{Kind: notmuch.ActTag, Query: "tag:" + scratch, TagOps: []notmuch.TagOp{{Tag: scratch, Add: false}}})

	rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActQuery, Query: "tag:inbox", Limit: 50})
	if err != nil || rpl.Err != nil {
		t.Fatalf("seed query: %v %v", err, rpl.Err)
	}
	if len(rpl.Msgs) == 0 {
		t.Skip("empty inbox")
	}
	before := len(rpl.Msgs)
	target := rpl.Msgs[0].ID

	if _, err := worker.Call(notmuch.Action{Kind: notmuch.ActTag, Query: `id:"` + target + `"`, TagOps: []notmuch.TagOp{{Tag: scratch, Add: true}}}); err != nil {
		t.Fatal(err)
	}

	// the next cycle's changeset must include the scratch tag
	_, rev, err := worker.Call(notmuch.Action{Kind: notmuch.ActRevision})
	if err != nil || rev == 0 {
		t.Fatalf("revision: %v %v", err, rev)
	}
	changed, err := worker.Call(notmuch.Action{Kind: notmuch.ActQuery, Query: "tag:" + scratch})
	if err != nil || changed.Err != nil {
		t.Fatalf("lastmod check: %v %v", err, changed.Err)
	}
	if len(changed.Msgs) == 0 {
		t.Fatal("scratch-tagged message not visible after tag op")
	}

	if _, err := worker.Call(notmuch.Action{Kind: notmuch.ActTag, Query: `id:"` + target + `"`, TagOps: []notmuch.TagOp{{Tag: scratch, Add: false}}}); err != nil {
		t.Fatal(err)
	}
	after, err := worker.Call(notmuch.Action{Kind: notmuch.ActQuery, Query: "tag:inbox", Limit: 50})
	if err != nil || len(after.Msgs) != before {
		t.Fatalf("inbox changed by soak (%d -> %d): %v", before, len(after.Msgs), err)
	}
	t.Logf("soak ok: %d inbox msgs, scratch tag applied and removed on %s", before, target)
}
```

- [ ] **Step 2: Write the cursor invariant test (pure)**

`src/app/cursor_test.go`:

```go
package app

import (
	"math/rand"
	"testing"

	"notmutt/core"
)

// Cursor invariant: if the cursor's id is present in the new snapshot,
// the cursor points at it after the merge.
func TestCursorInvariant(t *testing.T) {
	r := rand.New(rand.NewSource(11))
	for iter := 0; iter < 500; iter++ {
		view := core.NewView("inbox", "tag:inbox")
		var msgs []*core.Message
		for i := 0; i < r.Intn(20); i++ {
			msgs = append(msgs, &core.Message{ID: randID(r), Timestamp: r.Int63n(1e9), ThreadID: "t"})
		}
		if len(msgs) == 0 {
			continue
		}
		view.MergeThreads([]*core.Thread{core.NewThread("t", msgs)})
		cursor := msgs[r.Intn(len(msgs))].ID
		view.SetCursor(cursor)

		for i := 0; i < r.Intn(5); i++ {
			switch r.Intn(3) {
			case 0:
				msgs = append(msgs, &core.Message{ID: randID(r), Timestamp: r.Int63n(1e9), ThreadID: "t"})
			case 1:
				if len(msgs) > 1 {
					msgs = append(msgs[:0], msgs[1:]...)
				}
			case 2:
				msgs[r.Intn(len(msgs))].Timestamp += r.Int63n(10)
			}
		}
		view.MergeThreads([]*core.Thread{core.NewThread("t", msgs)})
		row, ok := view.CursorRow()
		if !ok {
			continue
		}
		if row.Msg.ID == cursor {
			continue
		}
		present := false
		for _, m := range msgs {
			if m.ID == cursor {
				present = true
			}
		}
		if present {
			t.Fatalf("iter %d: cursor %s present but lost", iter, cursor)
		}
	}
}

func randID(r *rand.Rand) string {
	const hex = "0123456789abcdef"
	b := make([]byte, 16)
	for j := range b {
		b[j] = hex[r.Intn(16)]
	}
	return string(b)
}
```

- [ ] **Step 3: Run everything**

```bash
cd /home/user/git/opencode/notmutt/src
go test ./... && go vet ./...
NOTMUCH_SOAK=1 go test ./app/ -run TestSoak -v
```

Expected: `go test ./...` passes (soak skips); soak passes and prints `soak ok: N inbox msgs, scratch tag applied and removed on <id>`. The soak must NOT print subjects or headers.

- [ ] **Step 4: Acceptance checklist against the spec**

Run each and record in the commit:

1. Strict config load: `NOTMUTT_CONFIG=/tmp/bad.toml ./notmutt` with an unknown key -> error naming the key (manual).
2. Real mailbox async render: TUI shows the inbox, first page before the full query (manual, terminal).
3. Tag op from the index: press `t` in the TUI - row flips, refresh converges (manual).
4. Benchmark report exists (task 14) with the backend decision.
5. Diff property test + soak green (this task).

- [ ] **Step 5: Commit**

```bash
cd /home/user/git/opencode/notmutt
git add src/app
git commit -m "Add soak and cursor-invariant tests; M1 acceptance"
```

## Self-review notes

- **Spec coverage**: section 1 (acceptance) -> tasks 14-15; section 2 (layout) -> task 1; section 3 (config) -> task 5 + task 12 validation; section 4 (bus) -> task 4; section 5 (worker) -> tasks 8-9; section 6 (view model) -> tasks 2-3, 6; section 7 (refresh) -> task 10; section 8 (diff) -> task 3; section 9 (testing) -> tasks 3, 15; section 10 (MIME cache) -> task 7 + cachejob; section 11 (UI) -> task 11; section 13 (out of scope) -> not implemented, as designed.
- **Known deferrals**: load-more batching past the first page (knob, spec section 12); tag glyph priority list from tag-groups (spec section 6 note); the worker never publishes `QueryBatch` (the cache job keys on `ViewDiff`; the event type stays defined per spec section 4); cgo Tag/New unimplemented (read-only handle, benchmark measures reads).
- **Consistency**: one comparator pair (`ThreadLess`/`MsgLess`), one diff engine (`DiffSorted`/`Apply`), one cache key type. Type names used across tasks: `core.Message`, `core.Thread`, `core.Row`, `core.Op`, `notmuch.Action`, `notmuch.Reply`, `notmuch.TagOp`, `cache.Key`, `config.Config`, `tui.Model`, `tui.EventMsg`. The diff engine is exercised only through `DiffSorted`/`Apply`; `Apply` with a `Move` op is `removeAt` then `insertAtIdx`, which equals the original remove+insert pair - the property test is the gate. `Apply` mutates the caller's backing array in place and preserves old values for matched/moved keys (key-order equality): callers use the returned slice and reconcile element fields from the incoming snapshot. `collapseMoves` merges only ADJACENT remove-then-insert pairs (a sinking element); non-adjacent and rising moves stay as remove+insert churn (verified sound: the plan's original any-later matching panics on old=[k(10),x(8)] new=[y(9),x(8),k(5)]).

## Execution

Plan complete. Two execution options:

1. **Subagent-Driven** (recommended): use superpowers:subagent-driven-development - one subagent executes each task, tests it, commits it, then hands to the next.
2. **Inline**: use superpowers:executing-plans - execute the tasks here in the main conversation.

Ask the user which they prefer before starting.
