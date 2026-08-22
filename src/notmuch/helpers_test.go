// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package notmuch

import (
	"context"
	"testing"

	"notmutt/lib/testutil"
)

// newTestBackend opens the cgo backend against the env's scratch DB
// with the scratch config passed explicitly (OpenConfig - no
// environment resolution; Setup validated the isolation) and closes it
// at teardown. Call it AFTER indexing: the read-only handle's Xapian
// snapshot is taken at open, so a CLI `notmuch new` after the open
// would be invisible until a reopen (TestCGONewBracket pins that
// behavior - it alone opens before its own new).
func newTestBackend(t *testing.T, e *testutil.Env) *CGOBackend {
	t.Helper()
	b := NewCGO()
	if err := b.OpenConfig(context.Background(), e.DB, e.NotmuchConfig); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close(context.Background()) })
	return b
}
