// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build !cli

package testutil

import (
	"context"
	"testing"

	"notmutt/notmuch"
)

// TestEnv pins the self-contained env: isolation (config resolves to
// the scratch DB), the account tag from the notmutt config, seeded
// mail tagged (folder-prefix rule), default new tags, explicit tags.
func TestEnv(t *testing.T) {
	e := Setup(t)
	id1 := e.WriteMail("hello alpha")
	id2 := e.WriteMail("hello beta")
	e.Index()
	if !e.Config.AccountTags()[e.Account] {
		t.Fatal("the env account tag must resolve from the config")
	}
	if n := e.Count("tag:inbox"); n != 2 {
		t.Fatalf("inbox count = %d, want 2", n)
	}
	if n := e.Count("tag:" + e.Account); n != 2 {
		t.Fatalf("account tag count = %d, want 2", n)
	}
	if n := e.Count("tag:unread"); n != 2 {
		t.Fatalf("default new tags must apply: unread = %d, want 2", n)
	}
	e.Tag([]string{id1}, "work", "replied")
	if n := e.Count("tag:work"); n != 1 {
		t.Fatalf("work count = %d, want 1", n)
	}
	if n := e.Count("tag:replied"); n != 1 {
		t.Fatalf("replied count = %d, want 1", n)
	}
	// a second index round-trips new mail without re-tagging old
	e.WriteMail("hello gamma")
	e.Index()
	if n := e.Count("tag:" + e.Account); n != 3 {
		t.Fatalf("second index: account tag count = %d, want 3", n)
	}
	if e.Count("tag:work") != 1 {
		t.Fatalf("idempotent tag: work count changed after second index")
	}
	_ = id2
}

// TestEnvBackendOpenConfig pins the explicit OpenConfig init: the cgo
// backend opens the scratch DB directly - no env resolution, so the
// live mailbox stays unreachable.
func TestEnvBackendOpenConfig(t *testing.T) {
	e := Setup(t)
	e.WriteMail("one")
	e.WriteMail("two")
	e.Index()
	b := notmuch.NewCGO()
	if err := b.OpenConfig(context.Background(), e.DB, e.NotmuchConfig); err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	rows := 0
	if err := b.Query(context.Background(), "tag:"+e.Account, 0, true, func(chunk []notmuch.Message) bool {
		rows += len(chunk)
		return true
	}); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("backend saw %d rows, want 2", rows)
	}
}
