// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIconsLayerOverBase pins the tag-icon layering: the builtin set is
// a base, a personal config ADDS to it - naming one icon must not drop
// the shipped ones (the base/personal split the index and status bar
// both read).
func TestIconsLayerOverBase(t *testing.T) {
	dir := t.TempDir()
	personal := "[ui.tags.icons]\nxolo = \"briefcase\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(personal), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := cfg.UI.Tags.Icons["xolo"]; got != "briefcase" {
		t.Fatalf("personal icon must load, got %q", got)
	}
	if got := cfg.UI.Tags.Icons["inbox"]; got == "" {
		t.Fatal("a personal icon entry must not drop the base icons")
	}
}
