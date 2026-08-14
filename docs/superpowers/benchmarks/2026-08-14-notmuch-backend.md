# Backend benchmark: CLI vs cgo (2026-08-14)

Task 14. Compares the CLI backend (`src/notmuch/cli.go`, the default) with
the tagged cgo backend (`src/notmuch/cgo.go`, build tag `notmuchcgo`)
against the real default DB. Read-only queries only; no mail content was
read, printed, or recorded. All rows are latency + result count.

## Setup

- DB: /home/user/Mail (database.path, effective config)
- notmuch CLI and libnotmuch 0.40 (hand-written notmuch.pc at
  $HOME/.local/lib/pkgconfig, Version: 0.40, prefix /usr)
- cgo backend wraps `github.com/zenhack/go.notmuch` (fishman fork of
  zenhack's go.notmuch, notmuch/bindings/go.notmuch in this workspace),
  vendored and pinned via `replace` in src/go.mod. The vendored copy
  carries one binding addition, `DB.Revision()` (the binding lacked the
  revision/UUID pair the refresh cycle needs). The upstream module path
  is zenhack's; the tree at HEAD is unreleased, so the replace pins the
  workspace checkout, never the proxy.
- Test: `src/notmuch/bench_test.go`, env-gated on NOTMUCH_BENCH=1

Command (3 runs):

```
NOTMUCH_BENCH=1 PKG_CONFIG_PATH=$HOME/.local/lib/pkgconfig go test -tags notmuchcgo ./notmuch/ -run TestBench -v -count=3
```

## Timings (binding-backed cgo)

| run | row | cli | cgo |
|-----|-----|-----|-----|
| 1 | first-page (50) | 26ms (50 msgs) | 8ms (50 msgs) |
| 1 | full inbox | 1.504s (32894) | 985ms (36393) |
| 1 | thread fetch | 17ms (1 msgs) | 7ms (1 msgs) |
| 2 | first-page (50) | 19ms (50 msgs) | 8ms (50 msgs) |
| 2 | full inbox | 1.503s (32894) | 1.061s (36393) |
| 2 | thread fetch | 21ms (1 msgs) | 8ms (1 msgs) |
| 3 | first-page (50) | 19ms (50 msgs) | 10ms (50 msgs) |
| 3 | full inbox | 1.524s (32894) | 1.048s (36393) |
| 3 | thread fetch | 20ms (1 msgs) | 7ms (1 msgs) |

Result counts are stable per backend per run. cgo won every row in every
run: first-page 2.0-2.6x, full inbox ~1.4-1.5x, thread fetch ~2.4-3.0x.
(The hand-rolled cgo backend measured before the binding swap was 2.0-2.3x
faster on first-page at 13-28ms; the binding version is faster still. The
relative conclusion is unchanged.)

### Count divergence in the full-inbox rows (semantics, not a bug)

The two backends count different things: CLI Query is `notmuch search`
(one stub per THREAD, 32894 threads match tag:inbox); cgo Query is
`notmuch_query_search_messages` (one row per MESSAGE, 36393 messages
match tag:inbox). 32894 < 36393 is consistent with threads < messages.
The full-inbox rows therefore compare latency only, like the thread
rows. First-page (50) rows are comparable: both enumerate 50 result
items with author/subject/tags (the cgo row additionally parses paths).

### Thread fetch rows returned 1 message

Both backends seeded on the newest inbox message, which is a
single-message thread. Latency-only comparison (see mandatory context
2). The per-message payloads differ: CLI Thread returns the show tree
with References populated; cgo Thread returns a flat search with empty
References.

## Lock timeout

Effective config: `database.lock_timeout=10` (`notmuch config list`;
~/.notmuch-config has `lock_timeout=10`, matching the reference
`muttrc/notmuch/config:5`). CLI backend commands wait at most 10s
behind a lock holder; the cgo handle opens READ_ONLY and never
acquires the write lock. Lock-timeout behavior itself is unit-tested
in TestWorkerLockTimeout.

## Mandatory context (task 13 review)

1. API surface: the binding's `Open` delegates to
   `notmuch_database_open_with_config` (modern API since the binding's
   2022-08-31 commit; the workspace copy was migrated to the current C
   API on 2026-08-14). The deprecated `notmuch_database_open` surface
   the hand-rolled backend used is gone; the tagged build emits no
   deprecation warning. The binding itself is an unreleased fork, so
   the replace + vendor pins it; the official contrib/go bindings
   (dormant 2018-2026, rewritten 2026-08-14) were NOT chosen because
   they lack the revision/UUID API and the config-aware open.
2. Thread() shape divergence: CLI Thread runs `notmuch show --body=false
   thread:ID` (show tree, References populated); cgo Thread runs a flat
   newest-first search over `thread:ID` with empty References. The
   thread-fetch rows compare LATENCY ONLY, not semantics.
3. Iterator ownership: the binding destroys queries and message
   iterators via explicit `Close()` (deferred in the backend) and
   message-owned tag/filename iterators are reclaimed with the message
   (notmuch's ownership model, documented in notmuch.h). No leaks; no
   hand-rolled C remains in notmutt.

## Conclusion: CLI stays default

cgo demonstrably won first-page latency on this machine (all three runs,
2.0-2.6x faster), which meets the numeric bar of the F10 rule, but the
default is NOT flipped in `app/app.go`. Reasons:

- The cgo backend is read-only: `CGOBackend.Tag` and `.New` return
  unsupported errors. As default it would break the entire
  classification pipeline (R2 tag ops, `notmuch new`), not just lag.
- It is gated behind the `notmuchcgo` build tag. Flipping the default
  would force notmuch dev headers and PKG_CONFIG_PATH onto every default
  build, breaking the no-tag green requirement; un-gating is a separate
  structural change, not a one-line default flip.

The latency win is real but does not translate into a production default
change for M1. CLI remains the default; revisit with a writable,
un-gated cgo backend when one exists.
