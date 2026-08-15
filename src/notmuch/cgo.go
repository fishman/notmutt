package notmuch

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	nm "github.com/fishman/go.notmuch"

	"notmutt/core"
)

// CGOBackend wraps github.com/fishman/go.notmuch (fishman fork of zenhack's
// go.notmuch, vendored; the upstream contrib/go bindings lack Revision and
// were dormant 2018-2026). Runtime default: the batched walk closed the gap
// to the CLI (decision record 3); the CLI backend stays reachable behind
// -tags cli (SECURITY.md F10).
type CGOBackend struct {
	db *nm.DB
}

func NewCGO() *CGOBackend { return &CGOBackend{} }

func (b *CGOBackend) Open(ctx context.Context, dbPath string) error {
	if dbPath == "" {
		// argv-only (F4): the config's database.path, resolved once at
		// open; the DB handle stays open for the process lifetime.
		out, err := exec.CommandContext(ctx, "notmuch", "config", "get", "database.path").Output()
		if err != nil {
			return fmt.Errorf("notmuch open: resolve database.path: %w", err)
		}
		dbPath = strings.TrimSpace(string(out))
	}
	db, err := nm.Open(dbPath, nm.DBReadOnly)
	if err != nil {
		return fmt.Errorf("notmuch open: %w", err)
	}
	b.db = db
	return nil
}

func (b *CGOBackend) Close(ctx context.Context) error {
	if b.db != nil {
		err := b.db.Close()
		b.db = nil
		return err
	}
	return nil
}

func (b *CGOBackend) Revision(ctx context.Context) (string, uint64, error) {
	if b.db == nil {
		return "", 0, fmt.Errorf("notmuch revision: database not open")
	}
	return b.db.Revision()
}

// Query walks the result as one native batch per chunk: the walk's C
// iterator stays alive across chunks, each pack crossing the boundary
// once (the CLI's bulk-JSON emit, in-process: the per-thread
// header-cache reads amortize C-side, zero file opens). The rows are
// the same stub data the CLI emits - thread id, newest date, authors,
// subject, tags - so the merge path is shared; per-message data comes
// from Thread, on open only. Chunk cadence: firstChunk then
// steadyChunk. limit counts threads, like `notmuch search --limit`.
func (b *CGOBackend) Query(ctx context.Context, query string, limit int, emit func([]core.Message) bool) error {
	if b.db == nil {
		return fmt.Errorf("notmuch search: database not open")
	}
	w, err := b.db.NewThreadsWalk(query, limit)
	if err != nil {
		return fmt.Errorf("notmuch search: %w", err)
	}
	defer w.Close()
	size := firstChunk
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		summaries, done, err := w.Next(size)
		if err != nil {
			return fmt.Errorf("notmuch search: %w", err)
		}
		if done {
			return nil
		}
		rows := make([]core.Message, 0, len(summaries))
		for _, s := range summaries {
			rows = append(rows, core.Message{
				ThreadID:  s.ThreadID,
				Timestamp: s.Timestamp,
				Author:    s.Authors,
				Subject:   s.Subject,
				Tags:      s.Tags,
			})
		}
		if emit != nil && !emit(rows) {
			return nil
		}
		size = steadyChunk
	}
}

// refsOf parses the message's reference chain out of the DB header
// cache (content-free: references and in-reply-to are Xapian header
// values, no file opens). Both headers are folded per RFC 5322, so the
// raw value is split on whitespace and each token trimmed of its angle
// brackets.
func refsOf(m *nm.Message) []string {
	var refs []string
	for _, h := range []string{"references", "in-reply-to"} {
		for _, f := range strings.Fields(m.Header(h)) {
			refs = append(refs, strings.Trim(f, "<>"))
		}
	}
	return refs
}

func (b *CGOBackend) Count(ctx context.Context, query string) (int, error) {
	if b.db == nil {
		return 0, fmt.Errorf("notmuch count: database not open")
	}
	q := b.db.NewQuery(query)
	defer q.Close()
	n, err := q.CountThreads()
	if err != nil {
		return 0, fmt.Errorf("notmuch count: %w", err)
	}
	return int(n), nil
}

// Thread fetches one thread's messages with the per-message iterators
// - real ids, paths, and reference chains for the pager tree. A thread
// is a handful of messages, so the per-message boundary crossings are
// negligible here; only the query path is batched.
func (b *CGOBackend) Thread(ctx context.Context, threadID string) ([]core.Message, error) {
	if b.db == nil {
		return nil, fmt.Errorf("notmuch show: database not open")
	}
	q := b.db.NewQuery("thread:" + threadID)
	defer q.Close()
	q.SetSortScheme(nm.SORT_NEWEST_FIRST)
	threads, err := q.Threads()
	if err != nil {
		return nil, fmt.Errorf("notmuch show: %w", err)
	}
	defer threads.Close()
	var msgs []core.Message
	for t := range threads.All() {
		it := t.Messages()
		for m := range it.All() {
			msgs = append(msgs, core.Message{
				ID:         m.ID(),
				ThreadID:   m.ThreadID(),
				Timestamp:  m.Date().Unix(),
				Author:     m.Header("from"),
				Subject:    m.Header("subject"),
				Tags:       tagsOf(m),
				Paths:      pathsOf(m),
				References: refsOf(m),
			})
		}
		if err := it.Err(); err != nil {
			return nil, fmt.Errorf("notmuch show: %w", err)
		}
		it.Close()
	}
	return msgs, nil
}

// Tag writes through a transient read-write reopen: the handle stays
// read-only for the fill (reads never hold notmuch's write lock), and
// the write lock is held only for the op - the CLI backend's lock
// footprint, so concurrent `notmuch new`/`notmuch tag` elsewhere keep
// working. The whole op is one atomic transaction, like `notmuch tag`.
func (b *CGOBackend) Tag(ctx context.Context, query string, ops []TagOp) error {
	if b.db == nil {
		return fmt.Errorf("notmuch tag: database not open")
	}
	if err := b.db.Reopen(nm.DBReadWrite); err != nil {
		return fmt.Errorf("notmuch tag: %w", err)
	}
	defer b.db.Reopen(nm.DBReadOnly)
	var opErr error
	b.db.Atomic(func(db *nm.DB) {
		q := db.NewQuery(query)
		defer q.Close()
		msgs, err := q.Messages()
		if err != nil {
			opErr = fmt.Errorf("notmuch tag: %w", err)
			return
		}
		defer msgs.Close()
		for m := range msgs.All() {
			if ctx.Err() != nil {
				opErr = ctx.Err()
				return
			}
			for _, op := range ops {
				if op.Add {
					if err := m.AddTag(op.Tag); err != nil {
						opErr = fmt.Errorf("notmuch tag: %w", err)
						return
					}
				} else if err := m.RemoveTag(op.Tag); err != nil {
					opErr = fmt.Errorf("notmuch tag: %w", err)
					return
				}
			}
		}
		if err := msgs.Err(); err != nil {
			opErr = fmt.Errorf("notmuch tag: %w", err)
		}
	})
	return opErr
}

func (b *CGOBackend) New(ctx context.Context) error {
	return fmt.Errorf("notmuch new: unsupported (run `notmuch new` outside the client)")
}

func tagsOf(m *nm.Message) []string {
	var out []string
	for t := range m.Tags().All() {
		out = append(out, t)
	}
	return out
}

func pathsOf(m *nm.Message) []string {
	var out []string
	for f := range m.Filenames().All() {
		out = append(out, f)
	}
	return out
}
