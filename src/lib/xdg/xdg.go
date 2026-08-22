// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

// Package xdg resolves the XDG base directories (os.UserStateDir is
// absent from this toolchain's stdlib). App-specific name suffixing
// belongs to the caller.
package xdg

import (
	"os"
	"path/filepath"
)

// ConfigHome returns $XDG_CONFIG_HOME or the platform default, ""
// when unresolvable.
func ConfigHome() string {
	base, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return base
}

// CacheHome returns $XDG_CACHE_HOME or the platform default, ""
// when unresolvable.
func CacheHome() string {
	base, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return base
}

// StateHome returns $XDG_STATE_HOME or ~/.local/state, "" when
// unresolvable.
func StateHome() string {
	if p := os.Getenv("XDG_STATE_HOME"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state")
}
