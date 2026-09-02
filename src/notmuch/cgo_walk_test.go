// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build !cli

package notmuch

import (
	"context"
	"errors"
	"testing"

	nm "github.com/fishman/go.notmuch"

	"notmutt/lib/testutil"
)

// TestCGOWalkExhausts pins the progressive walk: it must terminate,
// yield every thread exactly once, and match Count. Depth-1 threads
// keep threads == messages, so walk-vs-Count holds by construction.
func TestCGOWalkExhausts(t *testing.T) {
	e := testutil.Setup(t)
	testutil.ThreadTree(t, e.Maildir, 25, 1)
	testutil.NotmuchNew(t)
	b := newTestBackend(t, e)
	want, err := b.Count(context.Background(), "tag:inbox")
	if err != nil {
		t.Fatal(err)
	}
	if want != 25 {
		t.Fatalf("seed count = %d, want 25", want)
	}
	w, err := b.db.NewThreadsWalk("tag:inbox", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	seen := map[string]bool{}
	total := 0
	for chunks := 0; ; chunks++ {
		rows, done, err := w.Next(1000)
		if err != nil {
			t.Fatal(err)
		}
		if done {
			break
		}
		if len(rows) == 0 {
			t.Fatalf("walk yielded an empty chunk before done")
		}
		for _, s := range rows {
			if seen[s.ThreadID] {
				t.Fatalf("thread %q yielded twice", s.ThreadID)
			}
			seen[s.ThreadID] = true
		}
		total += len(rows)
		if chunks > 100 {
			t.Fatalf("walk did not terminate")
		}
	}
	if total != want {
		t.Fatalf("walk yielded %d threads, Count says %d", total, want)
	}
}

// TestBadQuerySurfacesRealError pins the binding error-propagation fix
// (the MCP "unknown error occurred" report): a query that fails to parse
// must surface notmuch's real status error, never the walk
// constructor's NULL-collapse ErrUnknownError. "and" is a bare boolean
// operator - the Xapian parser throws on it (unbalanced parens
// auto-close in notmuch 0.40, so they would not reproduce).
func TestBadQuerySurfacesRealError(t *testing.T) {
	e := testutil.Setup(t)
	testutil.NotmuchNew(t)
	b := newTestBackend(t, e)
	opens := []struct {
		name string
		open func() error
	}{
		{"full", func() error {
			w, err := b.db.NewFullWalk("and", 0)
			if err == nil {
				w.Close()
			}
			return err
		}},
		{"msg", func() error {
			w, err := b.db.NewMsgWalk("and", 0)
			if err == nil {
				w.Close()
			}
			return err
		}},
		{"threads", func() error {
			w, err := b.db.NewThreadsWalk("and", 0)
			if err == nil {
				w.Close()
			}
			return err
		}},
	}
	for _, o := range opens {
		err := o.open()
		if err == nil {
			t.Fatalf("%s walk: a malformed query must fail to open", o.name)
		}
		if errors.Is(err, nm.ErrUnknownError) {
			t.Fatalf("%s walk: malformed query surfaced as ErrUnknownError (%v), want the real notmuch status", o.name, err)
		}
	}
}
