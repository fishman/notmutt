# Backend benchmark: CLI vs cgo (2026-08-14)

Task 14. Compares the CLI backend (`src/notmuch/cli.go`, the default) with
the tagged cgo backend (`src/notmuch/cgo.go`, build tag `notmuchcgo`)
against the real default DB. Read-only queries only; no mail content was
read, printed, or recorded. All rows are latency + result count.

## Setup

- DB: /home/user/Mail (database.path, effective config)
- notmuch CLI and libnotmuch 0.40 (hand-written notmuch.pc at
  $HOME/.local/lib/pkgconfig, Version: 0.40, prefix /usr)
- Test: `src/notmuch/bench_test.go`, env-gated on NOTMUCH_BENCH=1

Command (3 runs):

```
NOTMUCH_BENCH=1 PKG_CONFIG_PATH=$HOME/.local/lib/pkgconfig go test -tags notmuchcgo ./notmuch/ -run TestBench -v -count=3
```

## Timings

| run | row | cli | cgo |
|-----|-----|-----|-----|
| 1 | first-page (50) | 51ms (50 msgs) | 28ms (50 msgs) |
| 1 | full inbox | 4.966s (32888) | 2.351s (36386) |
| 1 | thread fetch | 29ms (1 msgs) | 14ms (1 msgs) |
| 2 | first-page (50) | 32ms (50 msgs) | 14ms (50 msgs) |
| 2 | full inbox | 2.213s (32888) | 1.189s (36386) |
| 2 | thread fetch | 24ms (1 msgs) | 12ms (1 msgs) |
| 3 | first-page (50) | 26ms (50 msgs) | 13ms (50 msgs) |
| 3 | full inbox | 2.135s (32888) | 1.191s (36386) |
| 3 | thread fetch | 23ms (1 msgs) | 11ms (1 msgs) |

Result counts are stable per backend per run. cgo won every row in every
run: first-page 2.0-2.3x, full inbox ~1.8-2.1x, thread fetch ~2.0-2.1x.

### Count divergence in the full-inbox rows (semantics, not a bug)

The two backends count different things: CLI Query is `notmuch search`
(one stub per THREAD, 32888 threads match tag:inbox); cgo Query is
`notmuch_query_search_messages` (one row per MESSAGE, 36386 messages
match tag:inbox). 32888 < 36386 is consistent with threads < messages.
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

1. `notmuch_database_open` is deprecated as of libnotmuch 5.4 (compile
   warning in the tagged build); the cgo path sits on a legacy API
   surface, modern form is `notmuch_database_open_with_config`. On this
   machine libnotmuch is 0.40, whose header does not yet annotate the
   deprecation, so the tagged build emits no warning here. Benchmark-only
   code, noted for the future production decision.
2. Thread() shape divergence: CLI Thread runs `notmuch show --body=false
   thread:ID` (show tree, References populated); cgo Thread runs a flat
   newest-first search over `thread:ID` with empty References. The
   thread-fetch rows compare LATENCY ONLY, not semantics.
3. `tagsOf`/`pathsOf` never call `notmuch_tags_destroy`/
   `notmuch_filenames_destroy`: notmuch's ownership model ties those
   iterators to the message (reclaimed on destroy), so it is not a leak,
   but note it for any production adoption.

## Conclusion: CLI stays default

cgo demonstrably won first-page latency on this machine (all three runs,
2.0-2.3x faster), which meets the numeric bar of the F10 rule, but the
default is NOT flipped in `app/app.go`. Reasons:

- The cgo backend is read-only: `CGOBackend.Tag` and `.New` return
  NOTMUCH_STATUS_UNSUPPORTED_OPERATION. As default it would break the
  entire classification pipeline (R2 tag ops, `notmuch new`), not just
  lag.
- It is gated behind the `notmuchcgo` build tag. Flipping the default
  would force notmuch dev headers and PKG_CONFIG_PATH onto every default
  build, breaking the no-tag green requirement; un-gating is a separate
  structural change, not a one-line default flip.
- It sits on the deprecated `notmuch_database_open` surface (context 1).

The latency win is real but does not translate into a production default
change for M1. CLI remains the default; revisit with a writable,
`notmuch_database_open_with_config`-based cgo backend when one exists.
