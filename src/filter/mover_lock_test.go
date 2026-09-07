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
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
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
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
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
