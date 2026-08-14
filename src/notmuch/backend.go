package notmuch

import (
	"context"
	"errors"

	"notmutt/core"
)

var ErrLockTimeout = errors.New("notmuch lock timeout")

// Message is the core type; the alias keeps the Backend interface text short.
type Message = core.Message

// TagOp is the core type; the alias keeps the Backend interface text short.
type TagOp = core.TagOp

// Backend is the notmuch access boundary. M1 ships the CLI backend; the
// cgo backend implements the same interface for the benchmark (task 13).
//
// Query is THE ingestion interface: one call walks the whole query
// result and hands it to emit in bounded chunks (the page cadence:
// firstChunk, then steadyChunk - the refresh merges and paints whatever
// chunk arrives). Both backends serve it content-free - the index is
// DB-only, no mail file is ever opened in the load path:
//
//   - CLI: one `notmuch search` subprocess per call, one summary per
//     thread (DB-side thread fields; the CLI has no content-free
//     per-message dump - show opens files, so it exists only for the
//     open path);
//   - cgo: the native batch - the in-process threads iterator, each
//     thread's messages mapped directly into core.Message from the DB
//     header cache (ids, references, from, subject, date, tags), zero
//     subprocesses.
//
// limit stops the walk after N threads (0 = all; the startup validation
// probes each view query with 1). emit returning false stops the walk
// early; nil emit collects nothing.
//
// There is no offset: paged offset calls each re-walk the notmuch mset
// (measured: 0.2s for the first page, 2.3s at offset 120000 - 33 pages
// of a 33k-thread inbox take ~40s, one full call takes ~5s), and
// `notmuch search --format=json` emits nothing until the mset is
// computed, so a single call is strictly faster.
type Backend interface {
	Open(ctx context.Context, dbPath string) error
	Close(ctx context.Context) error
	Query(ctx context.Context, query string, limit int, emit func([]core.Message) bool) error
	Count(ctx context.Context, query string) (int, error)
	Thread(ctx context.Context, threadID string) ([]Message, error)
	Tag(ctx context.Context, query string, ops []TagOp) error
	Revision(ctx context.Context) (uuid string, rev uint64, err error)
	New(ctx context.Context) error
}
