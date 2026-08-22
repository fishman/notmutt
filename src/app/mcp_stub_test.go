// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build !mcp || !lua

package app

import (
	"strings"
	"testing"
)

// TestMCPStub: in a default build the mcp subcommand exists (dispatch
// compiles) but reports not-built-in instead of silently serving
// nothing.
func TestMCPStub(t *testing.T) {
	err := serveMCP()
	if err == nil {
		t.Fatal("serveMCP in a default build must error")
	}
	if !strings.Contains(err.Error(), "not built in") {
		t.Errorf("serveMCP error = %q, want the not-built-in message", err)
	}
}
