// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build !cli

package notmuch

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"notmutt/core"
	"notmutt/lib/testutil"
)

func TestCGOSmoke(t *testing.T) {
	e := testutil.Setup(t)
	for i := 0; i < 10; i++ {
		body := fmt.Sprintf("From: alpha <alpha@example.com>\nTo: beta@example.com\n"+
			"Subject: smoke %d\nDate: Sat, 16 Aug 2026 12:00:00 +0000\n"+
			"Message-ID: <smoke%d@test.invalid>\n\nsynthetic fixture body\n", i, i)
		if err := os.WriteFile(filepath.Join(e.Maildir, fmt.Sprintf("smoke%d.eml", i)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	testutil.NotmuchNew(t)
	b := newTestBackend(t, e)
	uuid, rev, err := b.Revision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if uuid == "" || rev == 0 {
		t.Fatalf("revision: %q %d", uuid, rev)
	}
	n := 0
	threads := map[string]bool{}
	err = b.Query(context.Background(), "tag:inbox", 10, false, func(chunk []core.Message) bool {
		for _, m := range chunk {
			// full-walk rows are real messages: ids, paths, and
			// references arrive with the thread in one pass
			if m.ID == "" || m.ThreadID == "" || m.Timestamp == 0 {
				t.Fatalf("row is not a real message row: %+v", m)
			}
			threads[m.ThreadID] = true
			n++
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(threads) != 10 {
		t.Fatalf("limit must bound the walk to 10 threads, got %d", len(threads))
	}
	t.Logf("got %d message rows in %d threads, rev %d (counts only, no content)", n, len(threads), rev)
}

// TestCGOMsgWalk pins the flat (message-level) walk: one row per
// matched message (unread, deleted, search). Rows carry the real
// thread id, so open still finds the conversation.
func TestCGOMsgWalk(t *testing.T) {
	e := testutil.Setup(t)
	for i := 0; i < 5; i++ {
		body := fmt.Sprintf("From: alpha <alpha@example.com>\nTo: beta@example.com\n"+
			"Subject: flat %d\nDate: Sat, 16 Aug 2026 12:00:00 +0000\n"+
			"Message-ID: <flat%d@test.invalid>\n\nsynthetic fixture body\n", i, i)
		if err := os.WriteFile(filepath.Join(e.Maildir, fmt.Sprintf("flat%d.eml", i)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	testutil.NotmuchNew(t)
	b := newTestBackend(t, e)
	if n, err := b.CountMsgs(context.Background(), "tag:inbox"); err != nil || n != 5 {
		t.Fatalf("count msgs = %d, %v (want 5)", n, err)
	}
	got := 0
	if err := b.Query(context.Background(), "tag:inbox", 0, true, func(chunk []core.Message) bool {
		for _, m := range chunk {
			if m.ID == "" || m.ThreadID == "" || m.Timestamp == 0 {
				t.Fatalf("flat row is not a real message row: %+v", m)
			}
			got++
		}
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if got != 5 {
		t.Fatalf("flat walk = %d rows (want 5)", got)
	}
}

// TestCGOMsgWalkExcludes pins the flat path's exclude scheme: the msg
// walk omits search.exclude_tags, and CountMsgs must count exactly
// what the walk emits - a mismatch leaves the progress bar stuck short
// of completion. The threaded Count keeps full membership: the threaded
// walk does not exclude (the view applies the deleted-leaf rule).
func TestCGOMsgWalkExcludes(t *testing.T) {
	e := testutil.Setup(t)
	for i := range 6 {
		body := fmt.Sprintf("From: alpha <alpha@example.com>\nTo: beta@example.com\n"+
			"Subject: excl %d\nDate: Sat, 16 Aug 2026 12:00:00 +0000\n"+
			"Message-ID: <excl%d@test.invalid>\n\nsynthetic fixture body\n", i, i)
		if err := os.WriteFile(filepath.Join(e.Maildir, fmt.Sprintf("excl%d.eml", i)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	testutil.NotmuchNew(t)
	if out, err := exec.Command("notmuch", "tag", "+deleted", "--",
		"id:excl1@test.invalid or id:excl2@test.invalid").CombinedOutput(); err != nil {
		t.Fatalf("notmuch tag: %v: %s", err, out)
	}
	// the exclude scheme is read from the config at database open
	cfgPath := filepath.Join(e.DB, "config")
	f, err := os.OpenFile(cfgPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("\n[search]\nexclude_tags = deleted\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	b := newTestBackend(t, e)
	n, err := b.CountMsgs(context.Background(), "tag:inbox")
	if err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("count msgs with excludes = %d (want 4: 6 minus 2 deleted)", n)
	}
	got := 0
	if err := b.Query(context.Background(), "tag:inbox", 0, true, func(chunk []core.Message) bool {
		got += len(chunk)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if got != 4 {
		t.Fatalf("flat walk with excludes = %d rows (want 4)", got)
	}
	if n, err := b.Count(context.Background(), "tag:inbox"); err != nil || n != 6 {
		t.Fatalf("count threads = %d, %v (want 6, full membership)", n, err)
	}
}

// TestCGOTagRoundTrip exercises the write path on a scratch database:
// one fixture message, add a scratch tag, assert via Count, remove,
// assert again. The fixture is authored test data; the real mailbox is
// never written by the test suite.
func TestCGOTagRoundTrip(t *testing.T) {
	e := testutil.Setup(t)
	fixture := []byte("From: alpha <alpha@example.com>\n" +
		"To: beta@example.com\n" +
		"Subject: cgo tag roundtrip\n" +
		"Date: Sat, 16 Aug 2026 12:00:00 +0000\n" +
		"Message-ID: <cgo-tag-roundtrip@test.invalid>\n\n" +
		"synthetic fixture body\n")
	if err := os.WriteFile(filepath.Join(e.Maildir, "fixture.eml"), fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.NotmuchNew(t)
	b := newTestBackend(t, e)
	const id = "id:cgo-tag-roundtrip@test.invalid"
	if n, err := b.Count(context.Background(), "tag:scratch"); err != nil || n != 0 {
		t.Fatalf("scratch tag present before add: %d %v", n, err)
	}
	if err := b.Tag(context.Background(), id, []TagOp{{Tag: "scratch", Add: true}}); err != nil {
		t.Fatal(err)
	}
	if n, err := b.Count(context.Background(), "tag:scratch"); err != nil || n != 1 {
		t.Fatalf("scratch tag missing after add: %d %v", n, err)
	}
	if err := b.Tag(context.Background(), id, []TagOp{{Tag: "scratch", Add: false}}); err != nil {
		t.Fatal(err)
	}
	if n, err := b.Count(context.Background(), "tag:scratch"); err != nil || n != 0 {
		t.Fatalf("scratch tag survives removal: %d %v", n, err)
	}
}

// TestCGODeltaRoundTrip exercises the filter engine's working set: a
// second file for the same message (the mover's copy), the revision
// bracket around AddPaths, the lastmod delta walk, the snapshot, and
// the remove-side update - tags survive the path swap (the mover's
// add-first guarantee).
func TestCGODeltaRoundTrip(t *testing.T) {
	e := testutil.Setup(t)
	fixture := []byte("From: alpha <alpha@example.com>\n" +
		"To: beta@example.com\n" +
		"Subject: cgo delta roundtrip\n" +
		"Date: Sat, 16 Aug 2026 12:00:00 +0000\n" +
		"Message-ID: <cgo-delta-roundtrip@test.invalid>\n\n" +
		"synthetic fixture body\n")
	fixture1 := filepath.Join(e.Maildir, "fixture1.eml")
	fixture2 := filepath.Join(e.Maildir, "fixture2.eml")
	wantTS := time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC).Unix()
	if err := os.WriteFile(fixture1, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	testutil.NotmuchNew(t)
	b := newTestBackend(t, e)
	const id = "cgo-delta-roundtrip@test.invalid"
	if err := b.Tag(context.Background(), "id:"+id, []TagOp{{Tag: "scratch", Add: true}}); err != nil {
		t.Fatal(err)
	}
	_, rev0, err := b.Revision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture2, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := b.AddPaths(context.Background(), []string{fixture2}); err != nil {
		t.Fatal(err)
	}
	_, rev1, err := b.Revision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rev1 <= rev0 {
		t.Fatalf("AddPaths must bump the revision: %d -> %d", rev0, rev1)
	}
	var ids []string
	if err := b.QueryMsgs(context.Background(), fmt.Sprintf("lastmod:%d..%d", rev0, rev1), func(rows []core.Message) bool {
		for _, m := range rows {
			ids = append(ids, m.ID)
		}
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || ids[0] != id {
		t.Fatalf("delta walk = %v, want [%s]", ids, id)
	}
	snaps, err := b.Snapshots(context.Background(), []string{id})
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 || len(snaps[0].Paths) != 2 || !hasTag(snaps[0], "scratch") ||
		snaps[0].Author != "alpha <alpha@example.com>" || snaps[0].Timestamp != wantTS {
		t.Fatalf("snapshot after add wrong: %+v", snaps)
	}
	if err := b.RemovePaths(context.Background(), []string{fixture1}); err != nil {
		t.Fatal(err)
	}
	snaps, err = b.Snapshots(context.Background(), []string{id})
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) != 1 || len(snaps[0].Paths) != 1 || snaps[0].Paths[0] != fixture2 || !hasTag(snaps[0], "scratch") {
		t.Fatalf("snapshot after remove wrong: %+v", snaps)
	}
}

func hasTag(m Message, tag string) bool {
	for _, t := range m.Tags {
		if t == tag {
			return true
		}
	}
	return false
}
