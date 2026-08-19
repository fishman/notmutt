// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

// Package state is the persisted client-state file (XDG state dir):
// TOML like config (same parser), but load is LENIENT - state is
// machine-written, a corrupt file degrades to defaults, never a
// startup error (config's strict load exists for human typos, state
// has no user to correct). The caller resolves the file's directory
// (lib/xdg).
package state

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// File is the state file schema; sections grow as the client gains
// persisted state (the chooser's last directory today, staged tags
// later).
type File struct {
	Chooser struct {
		LastDir string `toml:"last-dir"`
	} `toml:"chooser"`
}

// Load reads the state file; a missing or corrupt file yields the
// zero state.
func Load(path string) File {
	var f File
	if path == "" {
		return f
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return f
	}
	toml.Unmarshal(b, &f)
	return f
}

// Save writes the state atomically (same-dir temp + rename): a full
// disk mid-write fails the temp and the old state survives, the
// target is never truncated.
func Save(path string, f File) error {
	if path == "" {
		return fmt.Errorf("state: empty path")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := toml.Marshal(f)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
