// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build !cli

package notmuch

import "testing"

// TestNewDefaultIsCGO pins the runtime default: the default build
// resolves New() to the cgo backend (backend_default.go); the cli tag
// flips it (SECURITY.md F10).
func TestNewDefaultIsCGO(t *testing.T) {
	if _, ok := New().(*CGOBackend); !ok {
		t.Fatalf("default backend must be cgo")
	}
}
