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
