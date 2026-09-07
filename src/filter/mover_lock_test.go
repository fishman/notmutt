// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package filter

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"notmutt/config"
	"notmutt/lib/testutil"
)

// mkMaildirTree makes an account folder space with one source message.
func mkMaildirTree(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "mail")
	for _, d := range []string{"INBOX", "Archives"} {
		if err := os.MkdirAll(filepath.Join(root, "gmail", d, "cur"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "gmail", "INBOX", "cur", "1"), []byte("mail"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func moveRep(root string) *Report {
	return &Report{Entries: []Entry{
		{ID: "m1", Account: "gmail", Folder: "archive", Paths: []string{"gmail/INBOX/cur/1"}},
	}}
}

// holdLock takes the mover lock and keeps it held until the caller
// closes the returned file (flock releases on close).
func holdLock(t *testing.T) *os.File {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(moverLockPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(moverLockPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	return f
}

// TestMoverLockBestEffortSkips: a contended poll mover (NewMover) skips
// the batch with a report note, never errors, never copies.
func TestMoverLockBestEffortSkips(t *testing.T) {
	testutil.CacheDir(t)
	root := mkMaildirTree(t)
	hold := holdLock(t)
	defer hold.Close()

	cfg := config.Default()
	cfg.Accounts = map[string]config.Account{"gmail": {Preset: "gmail"}}
	cfg.Filter.DryRun = false

	mr, err := NewMover(&fakeWorker{}, cfg, root).Move(moveRep(root))
	if err != nil {
		t.Fatalf("a contended poll mover skips, never errors: %v", err)
	}
	if mr.Skip == "" {
		t.Fatal("a contended poll mover must report the skip")
	}
	if _, err := os.Stat(filepath.Join(root, "gmail", "Archives", "cur", "1")); err == nil {
		t.Fatal("a contended mover must not copy")
	}
}

// TestMoverLockStrictTimesOut: a contended apply mover (NewMoverLive)
// waits up to the timeout then errors - the apply entry stays staged.
func TestMoverLockStrictTimesOut(t *testing.T) {
	testutil.CacheDir(t)
	orig := moverLockTimeout
	moverLockTimeout = 50 * time.Millisecond
	defer func() { moverLockTimeout = orig }()
	root := mkMaildirTree(t)
	hold := holdLock(t)
	defer hold.Close()

	cfg := config.Default()
	cfg.Accounts = map[string]config.Account{"gmail": {Preset: "gmail"}}

	if _, err := NewMoverLive(&fakeWorker{}, cfg, root).Move(moveRep(root)); err == nil ||
		!strings.Contains(err.Error(), "held") {
		t.Fatalf("a contended apply mover must error on timeout, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "gmail", "Archives", "cur", "1")); err == nil {
		t.Fatal("a timed-out apply mover must not copy")
	}
}

// TestCopyFileTempThenRename: copyFile publishes the destination via a
// dot-prefixed temp in the destination directory and an atomic rename -
// a concurrent notmuch new never indexes a half-published destination
// (notmuch ignores dotfiles), and a leftover temp from a crashed copy
// does not survive a successful redo.
func TestCopyFileTempThenRename(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "s")
	dst := filepath.Join(dir, "d")
	if err := os.WriteFile(src, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	// a leftover temp of a crashed earlier copy
	if err := os.WriteFile(filepath.Join(dir, ".d.tmp"), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "content" {
		t.Fatalf("dst = %q, %v, want content", got, err)
	}
	// an existing destination is replaced (a second move over the same name)
	if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(src, dst); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(dst)
	if err != nil || string(got) != "content" {
		t.Fatalf("dst after replace = %q, %v, want content", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if len(e.Name()) > 0 && e.Name()[0] == '.' {
			t.Fatalf("temp leaked: %s", e.Name())
		}
	}
}
