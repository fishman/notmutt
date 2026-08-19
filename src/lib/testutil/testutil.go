// Package testutil holds shared test helpers for the notmutt suite.
package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// DevMailbox resolves the dev-mailbox path (NOTMUCH_DB or the default
// $HOME/Mail) and its notmuch config, skipping the test when either is
// missing: CI has neither (hermetic skip), a dev machine runs against
// its real mailbox. Tests that need a live database call it as their
// preflight.
func DevMailbox(t testing.TB) string {
	db := os.Getenv("NOTMUCH_DB")
	if db == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skipf("no home dir: %v", err)
		}
		db = filepath.Join(home, "Mail")
	}
	cfg := os.Getenv("NOTMUCH_CONFIG")
	if cfg == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			t.Skipf("no home dir: %v", err)
		}
		cfg = filepath.Join(home, ".notmuch-config")
	}
	if _, err := os.Stat(db); err != nil {
		t.Skipf("no dev mailbox at %s, set NOTMUCH_DB to run", db)
	}
	if _, err := os.Stat(cfg); err != nil {
		t.Skipf("no notmuch config at %s, set NOTMUCH_CONFIG to run", cfg)
	}
	return db
}

// MaildirTree creates the fixture maildir tree the mover tests use: a
// gmail-account tree under t.TempDir() with one file per folder
// (folder -> file name), named by the caller because mover behavior
// depends on the exact paths. Returns the mail root.
func MaildirTree(t testing.TB, files map[string]string) string {
	root := filepath.Join(t.TempDir(), "mail")
	for folder, name := range files {
		if err := os.MkdirAll(filepath.Join(root, "gmail", folder, "cur"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "gmail", folder, "cur", name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
