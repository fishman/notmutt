// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package notmuch

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	nm "github.com/fishman/go.notmuch"

	"notmutt/core"
)

// CGOBackend wraps the vendored fishman fork of go.notmuch (upstream
// contrib/go lacks Revision and was dormant 2018-2026). Runtime default
// since the batched walk closed the gap to the CLI (decision record 3);
// the CLI backend stays reachable behind -tags cli (SECURITY.md F10).
type CGOBackend struct {
	db  *nm.DB
	run runFn // the subprocess boundary: `notmuch new` (scanning is CLI machinery)
}

func NewCGO() *CGOBackend { return &CGOBackend{run: defaultRun} }

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

// OpenConfig opens the database with an explicit config file: no
// environment resolution (NOTMUCH_CONFIG or ~/.notmuch-config), so the
// caller guarantees which config applies (its search.exclude_tags
// included) - the test env's explicit-init form.
func (b *CGOBackend) OpenConfig(ctx context.Context, dbPath, configPath string) error {
	db, err := nm.OpenWithConfig(&dbPath, &configPath, nil, nm.DBReadOnly)
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

// Query walks the result as one native batch per chunk: the full walk
// emits each thread's summary and every message row in one pass
// (header-cache reads amortize C-side, zero file opens) - the view
// fills with real rows from the first chunk, no stubs. Chunk cadence:
// firstChunk then steadyChunk; limit counts threads.
//
// flat switches to the message-level walk (unread, deleted, search):
// the msg_walk keeps its messages iterator alive in C, so boundary
// crossings stay per-chunk - a 10k-message list walks without
// per-message iterator overhead.
func (b *CGOBackend) Query(ctx context.Context, query string, limit int, flat bool, emit func([]core.Message) bool) error {
	if b.db == nil {
		return fmt.Errorf("notmuch search: database not open")
	}
	if flat {
		w, err := b.db.NewMsgWalk(query, limit)
		if err != nil {
			return fmt.Errorf("notmuch search: %w", err)
		}
		defer w.Close()
		size := firstChunk
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			msgs, done, err := w.Next(size)
			if err != nil {
				return fmt.Errorf("notmuch search: %w", err)
			}
			if done {
				return nil
			}
			rows := make([]core.Message, 0, len(msgs))
			for _, m := range msgs {
				rows = append(rows, core.Message{
					ID:         m.ID,
					ThreadID:   m.ThreadID,
					Timestamp:  m.Timestamp,
					Author:     m.Author,
					Subject:    core.DecodeSubject(m.Subject),
					Tags:       m.Tags,
					Paths:      m.Paths,
					References: refsSplit(m.References),
				})
			}
			if emit != nil && !emit(rows) {
				return nil
			}
			size = steadyChunk
		}
	}
	w, err := b.db.NewFullWalk(query, limit)
	if err != nil {
		return fmt.Errorf("notmuch search: %w", err)
	}
	defer w.Close()
	size := firstChunk
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		threads, done, err := w.Next(size)
		if err != nil {
			return fmt.Errorf("notmuch search: %w", err)
		}
		if done {
			return nil
		}
		rows := make([]core.Message, 0, len(threads))
		for _, t := range threads {
			for _, m := range t.Msgs {
				rows = append(rows, core.Message{
					ID:         m.ID,
					ThreadID:   t.ThreadID,
					Timestamp:  m.Timestamp,
					Author:     m.Author,
					Subject:    core.DecodeSubject(m.Subject),
					Tags:       m.Tags,
					Paths:      m.Paths,
					References: refsSplit(m.References),
				})
			}
		}
		if emit != nil && !emit(rows) {
			return nil
		}
		size = steadyChunk
	}
}

// refsOf parses the message's reference chain out of the DB header
// cache (content-free: references and in-reply-to are Xapian header
// values, no file opens).
func refsOf(m *nm.Message) []string {
	var refs []string
	for _, h := range []string{"references", "in-reply-to"} {
		refs = append(refs, m.Header(h))
	}
	return refsSplit(strings.Join(refs, " "))
}

// refsSplit splits a folded reference header on whitespace and trims
// each token's angle brackets (RFC 5322 folding, the full walk's
// space-joined raw value, and the fetch path all land here).
func refsSplit(raw string) []string {
	var refs []string
	for _, f := range strings.Fields(raw) {
		refs = append(refs, strings.Trim(f, "<>"))
	}
	return refs
}

// CountMsgs returns the message count - the flat fill's progress
// total. The msg walk omits search.exclude_tags; the count applies
// the same excludes, or excluded matches never reach Done == Total
// and the bar sticks short of completion.
func (b *CGOBackend) CountMsgs(ctx context.Context, query string) (int, error) {
	if b.db == nil {
		return 0, fmt.Errorf("notmuch count: database not open")
	}
	q := b.db.NewQuery(query)
	defer q.Close()
	// mirror the msg walk: omit search.exclude_tags (EXCLUDE_TRUE); an
	// unset key means no excludes on either side.
	if ex, err := b.db.GetConfig("search.exclude_tags"); err == nil {
		for _, tag := range strings.Split(ex, ";") {
			if tag != "" {
				q.AddTagExclude(tag)
			}
		}
		q.SetExcludeScheme(nm.EXCLUDE_TRUE)
	}
	n, err := q.CountMessages()
	if err != nil {
		return 0, fmt.Errorf("notmuch count: %w", err)
	}
	return int(n), nil
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

// Thread fetches one thread's messages with per-message iterators -
// real ids, paths, and reference chains for the pager tree. A thread
// is a handful of messages, so per-message crossings are negligible;
// only the query path is batched.
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
				Subject:    core.DecodeSubject(m.Header("subject")),
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

// Addresses harvests deduplicated sender addresses (from: only - the
// completion corpus, spec section 3). The binding walks in C with the
// header cache (zero file opens); counts stay in the binding (the
// fuzzy match ranks by position, not frequency).
func (b *CGOBackend) Addresses(ctx context.Context, query string) ([]core.AddressEntry, error) {
	if b.db == nil {
		return nil, fmt.Errorf("notmuch address: database not open")
	}
	got, err := b.db.Addresses(query, nm.AddressOpts{Sender: true})
	if err != nil {
		return nil, fmt.Errorf("notmuch address: %w", err)
	}
	out := make([]core.AddressEntry, 0, len(got))
	for _, e := range got {
		out = append(out, core.AddressEntry{Addr: e.Addr, Name: e.Name})
	}
	return out, nil
}

// withWriteLock runs fn on a transient read-write handle: the handle
// stays read-only for the fill (reads never hold notmuch's write
// lock), the write lock is held only for the op - the CLI backend's
// lock footprint, so concurrent notmuch new/tag elsewhere keep
// working. The whole op is one atomic transaction, like `notmuch tag`.
func (b *CGOBackend) withWriteLock(ctx context.Context, what string, fn func(*nm.DB) error) error {
	if b.db == nil {
		return fmt.Errorf("notmuch %s: database not open", what)
	}
	if err := b.db.Reopen(nm.DBReadWrite); err != nil {
		return fmt.Errorf("notmuch %s: %w", what, err)
	}
	defer b.db.Reopen(nm.DBReadOnly)
	var opErr error
	b.db.Atomic(func(db *nm.DB) { opErr = fn(db) })
	return opErr
}

func (b *CGOBackend) Tag(ctx context.Context, query string, ops []TagOp) error {
	return b.withWriteLock(ctx, "tag", func(db *nm.DB) error {
		q := db.NewQuery(query)
		defer q.Close()
		msgs, err := q.Messages()
		if err != nil {
			return fmt.Errorf("notmuch tag: %w", err)
		}
		defer msgs.Close()
		for m := range msgs.All() {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			for _, op := range ops {
				if op.Add {
					if err := m.AddTag(op.Tag); err != nil {
						return fmt.Errorf("notmuch tag: %w", err)
					}
				} else if err := m.RemoveTag(op.Tag); err != nil {
					return fmt.Errorf("notmuch tag: %w", err)
				}
			}
		}
		if err := msgs.Err(); err != nil {
			return fmt.Errorf("notmuch tag: %w", err)
		}
		return nil
	})
}

// AddPaths indexes the given files (the mover's copy-side update).
// AddMessage finds the message by id and appends the path - add-first
// is the ordering that preserves tags. Duplicate-id maps to success:
// the association already happened (a copy of a known message).
func (b *CGOBackend) AddPaths(ctx context.Context, paths []string) error {
	return b.withWriteLock(ctx, "add", func(db *nm.DB) error {
		for _, p := range paths {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if _, err := db.AddMessage(p); err != nil && !errors.Is(err, nm.ErrDuplicateMessageID) {
				return fmt.Errorf("notmuch add: %w", err)
			}
		}
		return nil
	})
}

func (b *CGOBackend) RemovePaths(ctx context.Context, paths []string) error {
	return b.withWriteLock(ctx, "remove", func(db *nm.DB) error {
		for _, p := range paths {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			// duplicate-id means the message has other files, but the link was
			// removed - the mover's copy-then-delete case.
			if err := db.RemoveMessage(p); err != nil && !errors.Is(err, nm.ErrDuplicateMessageID) {
				return fmt.Errorf("notmuch remove: %w", err)
			}
		}
		return nil
	})
}

// New runs `notmuch new` as a subprocess (argv-only, F4): scanning is
// notmuch-new.c - the incremental scan, write lock, and post-new hooks
// have no library API. The filter engine takes over classification on
// the revision delta after this returns (early overlap is guarded,
// never double-worked).
//
// The read handle is stale across an external commit (revision cached
// at open, the Xapian snapshot hides new messages), so the handle
// reopens around the run: the pre read and every later read must see
// the commit.
func (b *CGOBackend) New(ctx context.Context) (uint64, uint64, error) {
	if err := b.reopen(ctx); err != nil {
		return 0, 0, err
	}
	_, pre, err := b.Revision(ctx)
	if err != nil {
		return 0, 0, err
	}
	out, err := b.run(ctx, "notmuch", []string{"new"})
	if err != nil {
		return 0, 0, fmt.Errorf("notmuch new: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if err := b.reopen(ctx); err != nil {
		return 0, 0, err
	}
	_, cur, err := b.Revision(ctx)
	if err != nil {
		return 0, 0, err
	}
	return pre, cur, nil
}

// reopen refreshes the read snapshot: an external commit (the new run
// itself, another instance, a CLI new from a hook) is invisible to the
// earlier snapshot - cached revisions and query results go stale until
// notmuch_database_reopen replaces it.
func (b *CGOBackend) reopen(ctx context.Context) error {
	if b.db == nil {
		return fmt.Errorf("notmuch new: database not open")
	}
	return b.db.Reopen(nm.DBReadOnly)
}

// QueryMsgs walks a message-level query (delta scans - lastmod
// ranges): bare message ids, chunked like Query. Per-message C
// crossings make it for small sets - the delta, never the fill.
func (b *CGOBackend) QueryMsgs(ctx context.Context, query string, emit func([]core.Message) bool) error {
	if b.db == nil {
		return fmt.Errorf("notmuch search: database not open")
	}
	q := b.db.NewQuery(query)
	defer q.Close()
	msgs, err := q.Messages()
	if err != nil {
		return fmt.Errorf("notmuch search: %w", err)
	}
	defer msgs.Close()
	size := firstChunk
	rows := make([]core.Message, 0, size)
	for m := range msgs.All() {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows = append(rows, core.Message{ID: strings.TrimPrefix(m.ID(), "id:")})
		if len(rows) >= size {
			if emit != nil && !emit(rows) {
				return nil
			}
			rows = make([]core.Message, 0, steadyChunk)
			size = steadyChunk
		}
	}
	if err := msgs.Err(); err != nil {
		return fmt.Errorf("notmuch search: %w", err)
	}
	if len(rows) > 0 && emit != nil {
		emit(rows)
	}
	return nil
}

// Snapshots fetches per-message tags and paths via the header cache
// (zero file opens) - the engine's working set. A message that
// vanished between the delta query and this call (an external new or
// move) is skipped, never an error.
func (b *CGOBackend) Snapshots(ctx context.Context, ids []string) ([]Message, error) {
	if b.db == nil {
		return nil, fmt.Errorf("notmuch snapshot: database not open")
	}
	out := make([]Message, 0, len(ids))
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		m, err := b.db.FindMessage(id)
		if errors.Is(err, nm.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("notmuch snapshot: %w", err)
		}
		out = append(out, Message{ID: id, Timestamp: m.Date().Unix(), Author: m.Header("from"), Subject: core.DecodeSubject(m.Header("subject")), Tags: tagsOf(m), Paths: pathsOf(m)})
	}
	return out, nil
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
