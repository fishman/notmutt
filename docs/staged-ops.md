---
layout: default
title: Staged operations (persistent)
nav_order: 12
---

# Staged operations: persistent, multi-writer buffer

Spec for R14's "persistence = future work" line: the staged tag buffer
becomes a durable, crash-safe, cross-process queue. The driver is the
MCP server: agent-driven classification must stage, never apply -
notmuch is only ever written by the human's APPLY. But the MCP server
is a separate process from the live session, so the buffer cannot stay
in the session's memory; it must be a file structure any writer
(MCP, CLI, Lua, the TUI's own keys) can append to and the session can
pick up.

Status: spec. Not implemented.

## Requirements

- **R1**: notmuch stays the single source of truth. The buffer holds
  only *pending* ops; applied ops are gone. Nothing derived from the
  buffer is authoritative.
- **R14**: the apply contract is unchanged - ops stage, the view
  renders the staged state, `$` flushes, `u` discards. Persistence
  only changes where the staged ops live between sessions.
- **R8 / MCP**: an agent can stage a tag op on an in-scope message but
  can never write notmuch. The scope boundary
  (`TestMCPScopeEnforcement`) stays locked and gains a stage case.
- **F5**: everything written is 0600 files / 0700 dirs.
- **Crash isolation**: one failing or crashing writer must never
  corrupt the buffer. A writer's failure is contained to its own
  entry.
- **At-least-once apply with idempotent ops**: a crash between the
  notmuch commit and the entry's removal re-applies as a no-op
  (notmuch tag ops are idempotent; `ResolveOps` recomputes against
  current tags, so stale ops net to nothing).

## Storage: one file per op, ULID ids

The buffer is a directory in the state home:

```
~/.local/share/notmutt/staged/     (0700)
  <ulid>.staged                    (0600, one op per file)
```

- **One file per op** (the schedule-spool discipline, proven in-repo):
  a writer creates its own file, so a crash mid-write poisons at most
  that file. There is no shared artifact to corrupt - a directory,
  not a document.
- **ULID ids** (`oklog/ulid`, the only new dependency): globally
  unique across writers (no collision on `O_CREATE|O_EXCL`) and
  lexicographically sortable, so the session's high-water mark is
  "last processed ULID" and pickup is a scan for newer entries.
- **Writers only create, the session only removes**: a writer never
  races the apply's removal on the same file (different files), and
  the session never collides with a writer. The spool flock is not
  needed for appends; the apply's scan-vs-remove lives inside one
  process.

### Entry format

```json
{
  "id": "01JQ9Y3F1K...",       // ULID
  "at": "2026-08-24T12:00:00Z",
  "thread_id": "00000000000001",
  "tag": "unread",
  "add": false
}
```

The identity is the thread id (the apply path emits `thread:<id>`,
notmuch's natural unit; a message-level op stores its message id and
the reader resolves it). The tag must be a known soft tag or an
exclusive-group member (validated at load - unknown tags skip the
entry with a diag, never a fatal).

### Crash and failure matrix

| Failure | Consequence | Containment |
| --- | --- | --- |
| writer crashes mid-write | one torn file | skipped at load (unmarshal fails), diag logged |
| writer writes garbage | one invalid file | same - per-entry validation, the rest load |
| writer floods entries | many files | bounded by the apply gate: only a human applies, and the staged view shows the whole set |
| two writers, same id | impossible | ULID + `O_CREATE|O_EXCL` |
| session crashes mid-apply | entry file remains | re-apply is an idempotent no-op (at-least-once) |
| writer vs apply race | impossible | writers create, the session removes - disjoint files |

## Writers

All writers append the same file shape; none of them write notmuch.

| Writer | Transport |
| --- | --- |
| TUI keys (`t`/`a`/`d`/...) | unchanged - the in-process view buffer; the file mirrors the view's staged map for pickup on the next session |
| Lua actions | unchanged - `core.TagStaged` on the bus (in-process) |
| MCP `stage_tag(thread_id, tag, add)` | writes a file, scope-checked first (below) |
| CLI `notmutt stage <query> <tag> [+/-]` | future - the same writer behind a different front end |

The in-process paths (TUI, Lua) keep staging straight into the view
as today; the durable file is the *cross-process* and *restart*
conduit. Both feed the same `$` apply.

## MCP stage tool and the locked boundary

`stage_tag` mirrors the read tools' deny-by-default scope: the message
must be in scope (an account grant: folder prefix AND account tag, or
an allowed soft tag from `[mcp] tags`). An out-of-scope id refuses
before any file is created. `TestMCPScopeEnforcement` gains the stage
cases - it stays locked, never loosened without explicit approval.

The agent gets no feedback beyond the op being accepted into the
buffer: no notmuch state changed, the op waits for the human's `$`.

## Session pickup

At startup and on the refresh cadence (the `runScheduler` pattern):
scan the `staged/` dir, take entries with ULID newer than the
high-water mark, validate, and `activeView().Stage(identity, op)` -
the rows render the staged state immediately. Apply (`$`) flushes via
the existing `applyStaged`; the entry file is removed after the
notmuch commit. Discard (`u`, or the staged view's discard) removes
the file without touching notmuch.

The high-water mark persists (a small state file in the same dir) so a
restarted session does not re-stage entries it already rendered.
Re-application after a crash is harmless by the idempotency argument,
but the mark keeps the render clean.

## The staged view

A separate read-only view (the `s` scheduled list is the precedent)
listing the pending buffer: per entry, the subject, the op (`+tag` /
`-tag`), the entry's age. Actions: apply all (`$` semantics), discard
one, discard all. The human review gate is the point of the whole
design - the view is where the review happens.

## Why not a database or a shared WAL

- **bbolt** (already vendored, R13): transactional and crash-safe, but
  single-writer behind an exclusive flock with a 1s timeout - the
  exact "one cache instance" contention this design exists to avoid.
  A separate buffer-only bbolt file would still serialize writers
  behind one flock. Documented as the upgrade path if the buffer ever
  needs transactions or real queries.
- **SQLite** (WAL): the one file DB that handles concurrent process
  writers gracefully, but a large dependency (pure-Go or cgo) to
  defend a property per-op files give structurally.
- **Per-op files**: native Go, zero machinery, proven in-repo by the
  schedule spool. A directory of small immutable facts with a
  human-gated apply is not a query surface.
- **A shared write-ahead log** (a single append-only file, the
  etcd/wal or hashicorp/raft-wal shape - both welded to their parent
  projects' record models, neither a clean standalone): per-op files
  already ARE a WAL - append-only immutable records replayed from the
  head - with each record in its own segment, so the directory is the
  log and record isolation is by construction. A shared log would
  trade that for: writer serialization on one artifact (a flock, or
  O_APPEND atomic small writes with torn-tail recovery becoming
  load-bearing), per-record CRCs to skip a corrupt region, a byte-
  offset replay position, and log compaction with temp+rename. Every
  one of those is machinery to defend a property the directory gives
  for free; ordering is already provided by the ULID sort, and the
  volume is a handful of human-reviewed ops. A shared WAL wins only
  for cross-writer ordering guarantees or a single compact artifact -
  neither applies here.

The only new dependency is `oklog/ulid` (MIT, small, established) for
the ordered unique ids - it fits the minimal-deliberate-deps policy,
vendored and audited on the bump.

## Non-goals (v1)

- Multi-view reconciliation: the buffer is session-global; the session
  attaches entries to whatever views hold the identity (the apply
  path already resolves per identity).
- The CLI writer and the staged view's interactive actions are spec'd
  here and built after the core buffer lands.
