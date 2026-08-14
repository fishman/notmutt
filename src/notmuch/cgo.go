//go:build notmuchcgo

package notmuch

import (
	"context"
	"fmt"
	"strings"

	nm "github.com/fishman/go.notmuch"

	"notmutt/core"
)

// CGOBackend wraps github.com/fishman/go.notmuch (fishman fork of zenhack's
// go.notmuch, vendored; the upstream contrib/go bindings lack Revision and
// were dormant 2018-2026). It exists only for the benchmark; the CLI backend
// stays the default unless cgo demonstrably wins (SECURITY.md F10).
type CGOBackend struct {
	db *nm.DB
}

func NewCGO() *CGOBackend { return &CGOBackend{} }

func (b *CGOBackend) Open(ctx context.Context, dbPath string) error {
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

// Query walks the whole result as the native batch: the in-process
// threads iterator, each thread's messages mapped directly into
// core.Message from the DB header cache (ids, references, from, subject,
// date, tags - zero subprocesses, zero file opens), emitted in chunks
// at thread boundaries (a thread never straddles a chunk, so the merge
// sees every thread at most once). The walk is naturally progressive:
// no write-at-end wall, unlike the CLI's single `notmuch search` call.
func (b *CGOBackend) Query(ctx context.Context, query string, limit int, emit func([]core.Message) bool) error {
	if b.db == nil {
		return fmt.Errorf("notmuch search: database not open")
	}
	q := b.db.NewQuery(query)
	defer q.Close()
	q.SetSortScheme(nm.SORT_NEWEST_FIRST)
	threads, err := q.Threads()
	if err != nil {
		return fmt.Errorf("notmuch search: %w", err)
	}
	defer threads.Close()
	var chunk []core.Message
	flush := func() bool {
		if len(chunk) == 0 || emit == nil {
			return true
		}
		if !emit(chunk) {
			return false
		}
		chunk = nil
		return true
	}
	nthreads := 0
	size := firstChunk
	for t := range threads.All() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if limit > 0 && nthreads >= limit {
			break
		}
		nthreads++
		msgs := t.Messages()
		for m := range msgs.All() {
			chunk = append(chunk, core.Message{
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
		if err := msgs.Err(); err != nil {
			return fmt.Errorf("notmuch search: %w", err)
		}
		msgs.Close()
		if len(chunk) >= size {
			if !flush() {
				return nil
			}
			size = steadyChunk
		}
	}
	if !flush() {
		return nil
	}
	return nil
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

func (b *CGOBackend) Thread(ctx context.Context, threadID string) ([]core.Message, error) {
	var msgs []core.Message
	err := b.Query(ctx, "thread:"+threadID, 0, func(chunk []core.Message) bool {
		msgs = append(msgs, chunk...)
		return true
	})
	return msgs, err
}

func (b *CGOBackend) Tag(ctx context.Context, query string, ops []TagOp) error {
	return fmt.Errorf("notmuch tag: unsupported (read-only handle)")
}

func (b *CGOBackend) New(ctx context.Context) error {
	return fmt.Errorf("notmuch new: unsupported (read-only handle)")
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
