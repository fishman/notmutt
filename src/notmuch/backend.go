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
// Query is THE ingestion interface - the batch unit: the refresh loop
// fetches the whole view one page per call, and a page counts THREADS
// (limit/offset are thread positions, matching Count). Both backends
// serve it content-free - the index is DB-only, no mail file is ever
// opened in the load path:
//
//   - CLI: one `notmuch search` subprocess per page, one summary per
//     thread (DB-side thread fields; the CLI has no content-free
//     per-message dump - show opens files, so it exists only for the
//     open path);
//   - cgo: the native batch - the in-process threads iterator,
//     skip/limit on thread positions, messages mapped directly into
//     core.Message from the DB header cache (ids, references, from,
//     subject, date, tags).
type Backend interface {
	Open(ctx context.Context, dbPath string) error
	Close(ctx context.Context) error
	Query(ctx context.Context, query string, limit, offset int) ([]Message, error)
	Count(ctx context.Context, query string) (int, error)
	Thread(ctx context.Context, threadID string) ([]Message, error)
	Tag(ctx context.Context, query string, ops []TagOp) error
	Revision(ctx context.Context) (uuid string, rev uint64, err error)
	New(ctx context.Context) error
}
