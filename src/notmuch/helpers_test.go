// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build !cli

package notmuch

import (
	"context"
	"testing"

	"notmutt/lib/testutil"
)

// newTestBackend opens the cgo backend against the env's scratch DB
// with the config passed explicitly (OpenConfig - no environment
// resolution), closed at teardown. Call AFTER indexing: the read
// handle's Xapian snapshot is taken at open, so a later CLI `notmuch
// new` is invisible until a reopen (TestCGONewBracket pins that).
func newTestBackend(t *testing.T, e *testutil.Env) *CGOBackend {
	t.Helper()
	b := NewCGO()
	if err := b.OpenConfig(context.Background(), e.DB, e.NotmuchConfig); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close(context.Background()) })
	return b
}
