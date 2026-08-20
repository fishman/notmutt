// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build !mcp || !lua

package app

import "fmt"

// serveMCP answers `notmutt mcp` in builds without the mcp+lua tags:
// the server exists only in that build (the paired-stub pattern), so
// the subcommand reports the missing tags instead of silently
// dropping. The !lua leg covers `-tags mcp` without lua, so no build
// hits an undefined symbol.
func serveMCP() error {
	return fmt.Errorf("mcp: not built in (compile with -tags \"mcp lua\")")
}
