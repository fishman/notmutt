// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package notmuch

import (
	"context"
	"os"
	"testing"
)

// TestCGOWalkExhausts pins the progressive walk: the full walk must
// terminate, yield every thread exactly once, and match Count - the
// per-chunk restart bug made it loop forever re-yielding the head of
// the result.
func TestCGOWalkExhausts(t *testing.T) {
	db := os.Getenv("NOTMUCH_DB")
	if db == "" {
		db = "/home/user/Mail"
	}
	b := NewCGO()
	if err := b.Open(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	want, err := b.Count(context.Background(), "tag:inbox")
	if err != nil {
		t.Fatal(err)
	}
	if want == 0 {
		t.Skip("empty test mailbox")
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
