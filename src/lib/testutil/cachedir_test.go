// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"os"
	"testing"

	"notmutt/lib/xdg"
)

// TestCacheDir pins the hermetic-cache contract: the harness exports a
// fresh cache home and the production resolver (xdg.CacheHome) reads
// exactly that variable - no test can drift to the real cache while the
// harness is in place.
func TestCacheDir(t *testing.T) {
	dir := CacheDir(t)
	if got := os.Getenv("XDG_CACHE_HOME"); got != dir {
		t.Fatalf("XDG_CACHE_HOME = %q, want the harness dir %q", got, dir)
	}
	if got := xdg.CacheHome(); got != dir {
		t.Fatalf("xdg.CacheHome() = %q, want the harness dir %q", got, dir)
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		t.Fatalf("harness dir: %v", err)
	}
}
