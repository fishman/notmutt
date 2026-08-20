// Package testutil holds shared test helpers for the notmutt suite.
package testutil

import (
	"fmt"
	"os"
	"os/exec"
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

// ScratchMailbox sets up a hermetic notmuch database in the notmuch
// test-harness shape: a temp mail folder, an own config (exported so
// subprocesses inherit it), and an initial `notmuch new`. Tests write
// fixtures into maildir and call NotmuchNew to index them; the real
// mailbox is never touched. NOTMUCH_DB set in the environment would
// point the binding at a live database, so the test skips instead of
// running unisolated.
func ScratchMailbox(t testing.TB) (db, maildir string) {
	if os.Getenv("NOTMUCH_DB") != "" {
		t.Skip("NOTMUCH_DB is set; scratch-db test would not isolate")
	}
	db = t.TempDir()
	maildir = filepath.Join(db, "mail")
	if err := os.MkdirAll(maildir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(db, "config")
	cfg := "[database]\npath=" + db + "\n[user]\nname=alpha\nprimary_email=alpha@example.com\n"
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	// the binding resolves the config from the process env; export the
	// scratch config so the test is hermetic (no ~/.notmuch-config);
	// t.Setenv restores after the test
	t.Setenv("NOTMUCH_CONFIG", cfgPath)
	NotmuchNew(t)
	return db, maildir
}

// NotmuchNew runs `notmuch new` against the environment's config (the
// ScratchMailbox setup), the harness's index step.
func NotmuchNew(t testing.TB) {
	t.Helper()
	if out, err := exec.Command("notmuch", "new").CombinedOutput(); err != nil {
		t.Fatalf("notmuch new: %v: %s", err, out)
	}
}

// ThreadTree seeds maildir with nested reply threads: roots root
// messages, each a chain of depth messages (a reply references its
// parent, so each chain is one notmuch thread of depth levels).
// Lorem-ipsum bodies, ids and dates derived from the position - the
// tree is deterministic, the same call reproduces it exactly. The
// caller runs NotmuchNew to index it.
func ThreadTree(t testing.TB, maildir string, roots, depth int) {
	t.Helper()
	for r := 0; r < roots; r++ {
		parent := ""
		for d := 0; d < depth; d++ {
			id := fmt.Sprintf("t%d-%d@example.com", r, d)
			refs := ""
			if parent != "" {
				refs = "References: <" + parent + ">\n"
			}
			body := "From: alpha <alpha@example.com>\n" +
				"To: beta@example.com\n" +
				"Subject: lorem ipsum " + id + "\n" +
				fmt.Sprintf("Date: Sat, 16 Aug 2026 12:%02d:00 +0000\n", d) +
				"Message-ID: <" + id + ">\n" + refs + "\n" + loremIpsum + "\n"
			if err := os.WriteFile(filepath.Join(maildir, id+".eml"), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
			parent = id
		}
	}
}

const loremIpsum = "Lorem ipsum dolor sit amet, consectetur adipiscing elit, " +
	"sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. " +
	"Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris " +
	"nisi ut aliquip ex ea commodo consequat."

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
