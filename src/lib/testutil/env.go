// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"notmutt/config"
)

// Env is a self-contained test environment: a hermetic notmuch
// database (ScratchMailbox), its fixture maildir, and a notmutt
// config dir whose accounts.toml maps one account ("alpha") to the
// account tag. Tests seed emails with WriteMail, index with Index
// (which applies the account tag - the folder-prefix rule the muttrc
// runs), then use e.Config and e.DB. Every subprocess and backend
// open carries the scratch config explicitly (--config / OpenConfig) -
// nothing resolves from the ambient environment. NOTMUCH_CONFIG is
// still forced to the scratch config and validated (Setup) as the
// belt for test code that shells out directly; NOTMUTT_CONFIG points
// at the notmutt config dir. The notmuch backend is a one-liner at
// the call site (notmuch.NewCGO().OpenConfig(ctx, e.DB,
// e.NotmuchConfig)): the helper stays import-cycle-free (notmuch's
// in-package tests import this package).
type Env struct {
	t             testing.TB
	Root          string // the notmutt config dir (accounts.toml lives here)
	DB            string // the notmuch database path
	NotmuchConfig string // the scratch notmuch config file (explicit OpenConfig init)
	Maildir       string // the fixture maildir root
	Account       string // the configured account name and tag
	Config        config.Config
	n             int
}

// New creates the env. NOTMUCH_DB set in the environment skips (the
// scratch DB would not isolate).
func New(t testing.TB) *Env {
	t.Helper()
	if os.Getenv("NOTMUCH_DB") != "" {
		t.Skip("NOTMUCH_DB is set; hermetic env would not isolate")
	}
	db, maildir := ScratchMailbox(t)
	root := t.TempDir()
	env := &Env{t: t, Root: root, DB: db, NotmuchConfig: filepath.Join(db, "config"), Maildir: maildir, Account: "alpha"}
	if err := os.WriteFile(filepath.Join(root, "accounts.toml"), []byte(
		"[accounts."+env.Account+"]\nfolder = \""+env.Account+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOTMUTT_CONFIG", root)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("notmutt config: %v", err)
	}
	env.Config = cfg
	return env
}

// Setup is the generic test entry: a self-contained env guaranteed to
// never touch the live mailbox. The effective notmuch config is
// validated before the test can open anything - the explicit scratch
// config must resolve database.path to the scratch DB (a stray
// NOTMUCH_CONFIG or a ~/.notmuch-config fallback fails the test on
// the spot). NOTMUCH_DB set in the environment still skips (the
// scratch DB would not isolate). Teardown is t-managed: t.TempDir
// removes the scratch dirs, t.Setenv restores the environment; Setup
// registers the teardown hook (Env.Cleanup) for the env's owned
// state.
func Setup(t testing.TB) *Env {
	e := New(t)
	out, err := exec.Command("notmuch", "--config="+e.NotmuchConfig, "config", "get", "database.path").CombinedOutput()
	if err != nil {
		t.Fatalf("notmuch config: %v: %s", err, out)
	}
	if got := strings.TrimSpace(string(out)); got != e.DB {
		t.Fatalf("test isolation broken: notmuch database.path = %q, want the scratch DB %q; the live mailbox must never be reachable from a test", got, e.DB)
	}
	t.Cleanup(e.Cleanup)
	return e
}

// Cleanup is the teardown hook Setup registers. The env owns no
// resources beyond the t-scoped temp dirs and env vars (t.TempDir and
// t.Setenv restore them at test end), so the hook currently has
// nothing to release - it is the extension point for owned state.
func (e *Env) Cleanup() {}

// WriteMail writes one synthetic mail into the account's inbox folder
// (cur/) and returns its message id. Fabricated test data, never real
// mail. Dates step deterministically with the write count.
func (e *Env) WriteMail(subject string) string {
	e.t.Helper()
	e.n++
	id := fmt.Sprintf("msg%d@example.com", e.n)
	body := "From: alpha <alpha@example.com>\n" +
		"To: beta@example.com\n" +
		"Subject: " + subject + "\n" +
		fmt.Sprintf("Date: Sat, 16 Aug 2026 12:%02d:00 +0000\n", e.n%60) +
		"Message-ID: <" + id + ">\n\n" + loremIpsum + "\n"
	dir := filepath.Join(e.Maildir, e.Account, "INBOX", "cur")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".eml"), []byte(body), 0o600); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// Index runs notmuch new (via the env - NOTMUCH_CONFIG is forced to
// the scratch config and validated by Setup) and applies the account
// tag to the inbox (the folder-prefix rule: +alpha -- folder:/^alpha\//)
// with the config passed explicitly. Idempotent: a second Index tags
// only what the second new added.
func (e *Env) Index() {
	e.t.Helper()
	NotmuchNew(e.t)
	e.run("tag", "+"+e.Account, "--", "tag:inbox")
}

// Tag applies tags to the given message ids - the classification
// surface beyond the account tag (folder tags, soft tags, flags).
func (e *Env) Tag(ids []string, tags ...string) {
	e.t.Helper()
	if len(ids) == 0 {
		return
	}
	args := []string{"tag"}
	for _, tag := range tags {
		args = append(args, "+"+tag)
	}
	args = append(args, "--")
	for i, id := range ids {
		if i > 0 {
			args = append(args, "or")
		}
		args = append(args, "id:\""+id+"\"")
	}
	e.run(args...)
}

// Count returns the message count for a query via the notmuch CLI
// (the scratch config passed explicitly).
func (e *Env) Count(query string) int {
	e.t.Helper()
	out, err := exec.Command("notmuch", "--config="+e.NotmuchConfig, "count", query).CombinedOutput()
	if err != nil {
		e.t.Fatalf("notmuch count %q: %v: %s", query, err, out)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		e.t.Fatalf("notmuch count %q: %s", query, out)
	}
	return n
}

// run executes the notmuch CLI with the scratch config passed
// explicitly (--config) - no environment resolution in any helper
// path.
func (e *Env) run(args ...string) {
	e.t.Helper()
	cmd := append([]string{"--config=" + e.NotmuchConfig}, args...)
	if out, err := exec.Command("notmuch", cmd...).CombinedOutput(); err != nil {
		e.t.Fatalf("notmuch %v: %v: %s", args, err, out)
	}
}
