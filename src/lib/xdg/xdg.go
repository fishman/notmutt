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

// RuntimeHome returns $XDG_RUNTIME_DIR when set, else "" - the home for
// per-session files (sockets) that must never persist. The caller falls
// back to StateHome when unset; the XDG spec makes the runtime dir 0700
// and owned by the user, which is the socket's same-user boundary.
func RuntimeHome() string {
	return os.Getenv("XDG_RUNTIME_DIR")
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

// DataHome returns $XDG_DATA_HOME or ~/.local/share, "" when
// unresolvable - the home for data that must persist (scheduled
// mail, unlike cache, is not deletable-by-design).
func DataHome() string {
	if p := os.Getenv("XDG_DATA_HOME"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share")
}
