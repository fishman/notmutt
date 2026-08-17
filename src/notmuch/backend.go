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

// firstChunk is the initial fill chunk: 100 threads land fast so the
// first paint is near-instant, then the fill continues in steadyChunk
// batches (big merges, few paints). The cadence is a backend contract -
// the refresh merges and paints whatever chunk arrives (the
// render-batching requirement: a full-result first chunk stalls the
// first paint).
const (
	firstChunk  = 100
	steadyChunk = 5000
)

// Backend is the notmuch access boundary. cgo is the runtime backend;
// the CLI backend is the -tags cli escape hatch (decision record 3).
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
//   - cgo: one native batch per chunk - the in-process threads
//     iterator packs per-thread summaries (thread id, date, authors,
//     subject, tags from the Xapian header cache) into one buffer per
//     crossing, zero subprocesses, zero file opens.
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
	// QueryMsgs walks a message-level query (the filter engine's delta
	// scans - lastmod ranges): bare message ids (no "id:" prefix; the
	// engine prefixes when it builds query terms), chunked like Query.
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
	// (pre before, cur after) - the poll's classification window.
	// The bracket is captured in one call because a revision read
	// through a stale handle reports the value cached at open; the
	// cgo backend reopens its read handle around the run so the
	// bracket reads and every later read see the commit.
	New(ctx context.Context) (pre, cur uint64, err error)
}

// runFn abstracts one CLI invocation: the cgo backend runs `notmuch
// new` through it, the CLI backend everything. argv only, never a
// shell (F4).
type runFn func(ctx context.Context, name string, args []string) ([]byte, error)

func defaultRun(ctx context.Context, name string, args []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil && ctx.Err() != nil {
		return out, ctx.Err()
	}
	return out, err
}
