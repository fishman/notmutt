//go:build notmuchcgo

package notmuch

import (
	"context"
	"fmt"

	nm "github.com/fishman/go.notmuch"

	"notmutt/core"
)

// CGOBackend wraps github.com/zenhack/go.notmuch (fishman fork of zenhack's
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
	return b.db.Revision()
}

func (b *CGOBackend) Query(ctx context.Context, query string, limit int) ([]core.Message, error) {
	q := b.db.NewQuery(query)
	defer q.Close()
	q.SetSortScheme(nm.SORT_NEWEST_FIRST)
	msgs, err := q.Messages()
	if err != nil {
		return nil, fmt.Errorf("notmuch search: %w", err)
	}
	defer msgs.Close()
	var out []core.Message
	for m := range msgs.All() {
		if limit > 0 && len(out) >= limit {
			break
		}
		out = append(out, core.Message{
			ID:        m.ID(),
			ThreadID:  m.ThreadID(),
			Timestamp: m.Date().Unix(),
			Author:    m.Header("from"),
			Subject:   m.Header("subject"),
			Tags:      tagsOf(m),
			Paths:     pathsOf(m),
		})
	}
	return out, nil
}

func (b *CGOBackend) Thread(ctx context.Context, threadID string) ([]core.Message, error) {
	return b.Query(ctx, "thread:"+threadID, 0)
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
