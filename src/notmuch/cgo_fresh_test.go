// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package notmuch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"notmutt/lib/testutil"
)

// TestCGONewBracket: the new wrapper must capture the (pre, cur]
// bracket around its own `notmuch new` through fresh snapshots. A
// read handle is stale across an external commit - the revision is
// cached at open and the Xapian snapshot hides the new messages - so
// the wrapper reopens the handle around the run. This pins: an
// external commit while the handle is open does not leak into the
// window (the entry reopen makes the pre read fresh), the bracket
// moves with the wrapper's own new, and the post-run handle sees
// both the wrapper's message and the external one.
func TestCGONewBracket(t *testing.T) {
	e := testutil.Setup(t)
	b := newTestBackend(t, e)
	newRun := testutil.NotmuchNew
	write := func(id string) {
		body := "From: probe <probe@test.invalid>\n" +
			"To: probe@test.invalid\n" +
			"Subject: probe " + id + "\n" +
			"Date: Sat, 16 Aug 2026 12:00:00 +0000\n" +
			"Message-ID: <" + id + "@test.invalid>\n\n" +
			"synthetic fixture body\n"
		if err := os.WriteFile(filepath.Join(e.Maildir, id+".eml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	revCLI := func() uint64 {
		out, err := exec.Command("notmuch", "count", "--lastmod", "").CombinedOutput()
		if err != nil {
			t.Fatalf("notmuch count --lastmod: %v: %s", err, out)
		}
		fields := strings.Fields(string(out))
		if len(fields) != 3 {
			t.Fatalf("unexpected --lastmod output: %q", out)
		}
		rev, err := strconv.ParseUint(fields[2], 10, 64)
		if err != nil {
			t.Fatalf("bad revision %q: %v", fields[2], err)
		}
		return rev
	}

	write("m1")
	newRun(t)

	// an external commit while the handle is open (a CLI new from a
	// hook, another client) must not leak into the bracket: the entry
	// reopen makes the pre read fresh.
	write("m3")
	newRun(t)
	preCLI := revCLI()

	// m2 sits in the maildir for the wrapper's own new run.
	write("m2")
	pre, cur, err := b.New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if pre != preCLI {
		t.Fatalf("entry reopen missed the external commit: pre %d, fresh CLI revision %d", pre, preCLI)
	}
	if cur <= pre {
		t.Fatalf("bracket did not move: %d..%d", pre, cur)
	}
	// the post-run handle sees the wrapper's message and the external
	// one - a stale snapshot would hide both.
	for id, want := range map[string]int{"id:m2@test.invalid": 1, "id:m3@test.invalid": 1} {
		n, err := b.Count(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Fatalf("count %s = %d, want %d (stale snapshot)", id, n, want)
		}
	}
	if _, rev, err := b.Revision(context.Background()); err != nil || rev != cur {
		t.Fatalf("post-run revision %d != bracket end %d (%v)", rev, cur, err)
	}
}
