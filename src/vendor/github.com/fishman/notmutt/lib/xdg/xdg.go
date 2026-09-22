// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

// Package xdg resolves the XDG base directories.
package xdg

import (
	"os"
	"path/filepath"
)

// ConfigHome returns $XDG_CONFIG_HOME or the platform default.
func ConfigHome() string {
	base, err := os.UserConfigDir()
	if err != nil { return "" }
	return base
}

// CacheHome returns $XDG_CACHE_HOME or the platform default.
func CacheHome() string {
	base, err := os.UserCacheDir()
	if err != nil { return "" }
	return base
}

// RuntimeHome returns $XDG_RUNTIME_DIR when set.
func RuntimeHome() string { return os.Getenv("XDG_RUNTIME_DIR") }

// RuntimeOrState returns the runtime home when available, else state home.
func RuntimeOrState() string {
	if runtime := RuntimeHome(); runtime != "" { return runtime }
	return StateHome()
}

// StateHome returns $XDG_STATE_HOME or ~/.local/state.
func StateHome() string { return home("XDG_STATE_HOME", ".local", "state") }

// DataHome returns $XDG_DATA_HOME or ~/.local/share.
func DataHome() string { return home("XDG_DATA_HOME", ".local", "share") }

func home(env string, suffix ...string) string {
	if value := os.Getenv(env); value != "" { return value }
	dir, err := os.UserHomeDir()
	if err != nil { return "" }
	return filepath.Join(append([]string{dir}, suffix...)...)
}
