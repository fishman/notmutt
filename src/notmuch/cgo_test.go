// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package notmuch

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"notmutt/core"
)

func TestCGOSmoke(t *testing.T) {
	db := os.Getenv("NOTMUCH_DB")
	if db == "" {
		db = "/home/user/Mail"
	}
	b := NewCGO()
	if err := b.Open(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	uuid, rev, err := b.Revision(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if uuid == "" || rev == 0 {
		t.Fatalf("revision: %q %d", uuid, rev)
	}
	n := 0
	err = b.Query(context.Background(), "tag:inbox", 10, func(chunk []core.Message) bool {
		for _, m := range chunk {
			// batched summaries are CLI-shaped stubs: thread-level
			// fields only, no message ids, paths, or references
			if m.ID != "" || m.Paths != nil || m.References != nil || m.ThreadID == "" || m.Timestamp == 0 {
				t.Fatalf("row is not a summary stub: %+v", m)
			}
			n++
		}
		return true
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 10 {
		t.Fatalf("limit must bound the walk to 10 threads, got %d", n)
	}
	t.Logf("got %d thread summaries, rev %d (counts only, no content)", n, rev)
}

// TestCGOTagRoundTrip exercises the write path on a scratch database:
// one synthetic fixture message, add a scratch tag, assert via Count,
// remove it, assert again. The fixture is authored test data (no real
// mail); the real mailbox is never written by the test suite.
func TestCGOTagRoundTrip(t *testing.T) {
	if os.Getenv("NOTMUCH_DB") != "" {
		t.Skip("NOTMUCH_DB is set; scratch-db test would not isolate")
	}
	dir := t.TempDir()
	maildir := filepath.Join(dir, "mail")
	if err := os.MkdirAll(maildir, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := []byte("From: cgo test <cgo@test.invalid>\n" +
		"To: cgo@test.invalid\n" +
		"Subject: cgo tag roundtrip\n" +
		"Date: Sat, 16 Aug 2026 12:00:00 +0000\n" +
		"Message-ID: <cgo-tag-roundtrip@test.invalid>\n\n" +
		"synthetic fixture body\n")
	if err := os.WriteFile(filepath.Join(maildir, "fixture.eml"), fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config")
	cfg := "[database]\npath=" + dir + "\n[user]\nname=cgo test\nprimary_email=cgo@test.invalid\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("notmuch", "new")
	cmd.Env = append(os.Environ(), "NOTMUCH_CONFIG="+cfgPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("notmuch new: %v: %s", err, out)
	}

	b := NewCGO()
	if err := b.Open(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
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

// TestCGODeltaRoundTrip exercises the filter engine's working set on a
// scratch database: a second file for the same message (the mover's
// copy), the revision bracket around the AddPaths op, the lastmod delta
// walk, the snapshot, and the remove-side update - tags must survive
// the path swap (the mover's add-first guarantee).
func TestCGODeltaRoundTrip(t *testing.T) {
	if os.Getenv("NOTMUCH_DB") != "" {
		t.Skip("NOTMUCH_DB is set; scratch-db test would not isolate")
	}
	dir := t.TempDir()
	maildir := filepath.Join(dir, "mail")
	if err := os.MkdirAll(maildir, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := []byte("From: cgo test <cgo@test.invalid>\n" +
		"To: cgo@test.invalid\n" +
		"Subject: cgo delta roundtrip\n" +
		"Date: Sat, 16 Aug 2026 12:00:00 +0000\n" +
		"Message-ID: <cgo-delta-roundtrip@test.invalid>\n\n" +
		"synthetic fixture body\n")
	fixture1 := filepath.Join(maildir, "fixture1.eml")
	fixture2 := filepath.Join(maildir, "fixture2.eml")
	if err := os.WriteFile(fixture1, fixture, 0o600); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(dir, "config")
	cfg := "[database]\npath=" + dir + "\n[user]\nname=cgo test\nprimary_email=cgo@test.invalid\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("notmuch", "new")
	cmd.Env = append(os.Environ(), "NOTMUCH_CONFIG="+cfgPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("notmuch new: %v: %s", err, out)
	}

	b := NewCGO()
	if err := b.Open(context.Background(), dir); err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
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
	if len(snaps) != 1 || len(snaps[0].Paths) != 2 || !hasTag(snaps[0], "scratch") {
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
