// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package notmuch

import (
	"context"
	"errors"
	"os/exec"

	"notmutt/core"
)

var ErrLockTimeout = errors.New("notmuch lock timeout")

// Message is the core type; the alias keeps the Backend interface text short.
type Message = core.Message

// TagOp is the core type; the alias keeps the Backend interface text short.
type TagOp = core.TagOp

// firstChunk: 100 threads land fast for a near-instant first paint,
// then steadyChunk batches (big merges, few paints). The refresh merges
// and paints whatever chunk arrives (a full-result first chunk stalls
// the first paint) - backend contract.
const (
	firstChunk  = 100
	steadyChunk = 5000
)

// Backend is the notmuch access boundary: cgo is the runtime backend,
// the CLI backend the -tags cli escape hatch (decision record 3).
//
// Query is THE ingestion interface: one call walks the whole query
// result and hands it to emit in bounded chunks (firstChunk, then
// steadyChunk - the refresh merges and paints whatever chunk arrives).
// Both backends serve it content-free - the index is DB-only, no mail
// file is opened in the load path (CLI: one `notmuch search` subprocess,
// one summary per thread; cgo: one native batch per chunk from the
// Xapian header cache, zero subprocesses, zero file opens). limit stops
// the walk after N threads (0 = all; startup validation probes each view
// query with 1); emit false stops early; nil emit collects nothing.
//
// There is no offset: paged offset calls each re-walk the notmuch mset
// (measured 0.2s first page, 2.3s at offset 120000), and json emits
// nothing until the mset is computed, so one full call is strictly
// faster.
type Backend interface {
	Open(ctx context.Context, dbPath string) error
	Close(ctx context.Context) error
	// Query walks matched THREADS (flat=false; inbox, archive) or matched
	// MESSAGES (flat=true; unread, deleted, search - one row per match).
	// Rows carry ThreadID either way; limit stops after N threads/messages.
	Query(ctx context.Context, query string, limit int, flat bool, emit func([]core.Message) bool) error
	// CountMsgs returns the number of MESSAGES matching the query -
	// the flat fill's progress total (Count counts threads).
	CountMsgs(ctx context.Context, query string) (int, error)
	// QueryMsgs walks a message-level query (delta scans - lastmod
	// ranges): bare message ids (no "id:" prefix; the engine prefixes
	// when it builds query terms), chunked like Query.
	QueryMsgs(ctx context.Context, query string, emit func([]core.Message) bool) error
	Count(ctx context.Context, query string) (int, error)
	Thread(ctx context.Context, threadID string) ([]Message, error)
	// Snapshots fetches per-message tags and paths for the given bare
	// message ids - the engine's working set (small: the lastmod
	// delta). Messages missing from the DB are skipped, never an error.
	Snapshots(ctx context.Context, ids []string) ([]Message, error)
	Addresses(ctx context.Context, query string) ([]core.AddressEntry, error)
	Tag(ctx context.Context, query string, ops []TagOp) error
	// AddPaths/RemovePaths register or drop files for the given paths
	// (the mover's copy-then-delete DB update: AddPaths first, so the
	// message keeps its tags).
	AddPaths(ctx context.Context, paths []string) error
	RemovePaths(ctx context.Context, paths []string) error
	Revision(ctx context.Context) (uuid string, rev uint64, err error)
	// New runs `notmuch new` and returns the lastmod bracket around it
	// (pre before, cur after) - the poll's classification window. The
	// bracket is captured in one call: a stale handle reports the
	// revision cached at open, so the cgo backend reopens its read
	// handle around the run and every later read sees the commit.
	New(ctx context.Context) (pre, cur uint64, err error)
	// Reopen refreshes this handle's read snapshot: commits landed on
	// ANOTHER handle (the interactive worker's writes reopen only its own
	// cgo handle) are invisible here until reopened. The walk worker
	// calls it before each cycle or search, so it never reports a stale
	// revision. The CLI backend is stateless - every call is a fresh
	// subprocess - and no-ops.
	Reopen(ctx context.Context) error
}

// runFn abstracts one CLI invocation: `notmuch new` for cgo, everything
// for the CLI backend. argv only, never a shell (F4).
type runFn func(ctx context.Context, name string, args []string) ([]byte, error)

func defaultRun(ctx context.Context, name string, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil && ctx.Err() != nil {
		return out, ctx.Err()
	}
	return out, err
}
