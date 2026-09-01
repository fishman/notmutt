// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"notmutt/app/aicmd"
	"notmutt/config"
)

func TestValidateBindings(t *testing.T) {
	cfg := config.Default()
	if err := validateBindings(&cfg); err != nil {
		t.Fatalf("defaults must pass: %v", err)
	}
	cfg.Bindings["index"]["x"] = "frobnicate"
	err := validateBindings(&cfg)
	if err == nil {
		t.Fatal("unknown action must error")
	}
	if !strings.Contains(err.Error(), "x") {
		t.Fatalf("error must name the key, got: %v", err)
	}
	cfg = config.Default()
	cfg.Bindings["pager"]["x"] = "frobnicate"
	err = validateBindings(&cfg)
	if err == nil {
		t.Fatal("unknown action in a non-index context must error")
	}
	if !strings.Contains(err.Error(), "pager") {
		t.Fatalf("error must name the context, got: %v", err)
	}
	cfg = config.Default()
	cfg.Bindings["pager"]["ctrl+f"] = "page-down"
	if err := validateBindings(&cfg); err != nil {
		t.Fatalf("a valid pager binding must load: %v", err)
	}
	cfg = config.Default()
	cfg.TagActions["quit"] = "x"
	if err := validateBindings(&cfg); err == nil {
		t.Fatal("tag action colliding with a builtin must error")
	}
	cfg = config.Default()
	cfg.TagActions["extra"] = "wip"
	if err := validateBindings(&cfg); err == nil {
		t.Fatal("unbound tag action must error")
	}
	cfg = config.Default()
	cfg.Bindings["index"]["x"] = "scroll-down"
	cfg.TagActions["scroll-down"] = "wip"
	if err := validateBindings(&cfg); err != nil {
		t.Fatalf("a pager-only action name must not collide with a tag action: %v", err)
	}
}

// readDirNames lists a directory's entry names, failing the test on error.
func readDirNames(t *testing.T, path string) []string {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// wantMode asserts path exists with exactly the given permission bits.
func wantMode(t *testing.T, path string, perm os.FileMode) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != perm {
		t.Errorf("%s mode = %v, want %04o", path, fi.Mode().Perm(), perm)
	}
}

// assertSeedPreserves pins the write-if-absent contract: the user's file
// at path survives a re-run of the seeding function.
func assertSeedPreserves(t *testing.T, path string, reseed func()) {
	t.Helper()
	if err := os.WriteFile(path, []byte("mine"), 0600); err != nil {
		t.Fatal(err)
	}
	reseed()
	if got, _ := os.ReadFile(path); string(got) != "mine" {
		t.Fatal("seed must never overwrite the user's file")
	}
}

func TestSeedTemplates(t *testing.T) {
	dir := t.TempDir()
	seedTemplates(dir)
	dst := filepath.Join(dir, "lua", "templates")
	if names := readDirNames(t, dst); len(names) != 5 {
		t.Fatalf("seed must copy the 5 shipped templates, got %d", len(names))
	}
	// a customized file must survive a re-run
	assertSeedPreserves(t, filepath.Join(dst, "gmail.lua"), func() { seedTemplates(dir) })
}

func TestSeedAICommands(t *testing.T) {
	dir := t.TempDir()
	seedAICommands(dir)
	dst := filepath.Join(dir, "ai")
	want := []string{"accounts", "context", "prompts"}
	if names := readDirNames(t, dst); !reflect.DeepEqual(names, want) {
		t.Fatalf("seeded files = %v, want %v", names, want)
	}
	if fi, err := os.Stat(filepath.Join(dst, "prompts")); err != nil || !fi.IsDir() {
		t.Fatalf("prompts dir missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "accounts", "README.md")); err != nil {
		t.Fatalf("accounts README missing: %v", err)
	}
	// the seeded prompts must parse - a broken seed file would brick the
	// picker on first load
	cmds, err := aicmd.LoadCommands(dst)
	if err != nil {
		t.Fatalf("seeded commands must load: %v", err)
	}
	if len(cmds) != 2 {
		t.Fatalf("seeded commands = %d, want 2", len(cmds))
	}
	// the default style context must seed and survive a user edit
	def := filepath.Join(dst, "context", "default.md")
	if body, err := os.ReadFile(def); err != nil {
		t.Fatalf("default context missing: %v", err)
	} else if !strings.Contains(string(body), "concise") {
		t.Fatalf("default context must carry the brief style: %q", body)
	}
	assertSeedPreserves(t, def, func() { seedAICommands(dir) })
	// a user edit must survive a re-run
	assertSeedPreserves(t, filepath.Join(dst, "prompts", "next-steps.md"), func() { seedAICommands(dir) })
	// permissions: dirs 0700, files 0600
	wantMode(t, filepath.Join(dst, "accounts"), 0700)
	wantMode(t, filepath.Join(dst, "context"), 0700)
	wantMode(t, filepath.Join(dst, "context", "default.md"), 0600)
	wantMode(t, filepath.Join(dst, "prompts", "draft-reply.md"), 0600)
}

// TestSeedFiles covers the single-file first-load seeds (config.toml,
// ai.toml): each must write the shipped template, load strict-clean,
// carry the template's default posture, and survive a user edit.
func TestSeedFiles(t *testing.T) {
	cases := []struct {
		name  string
		seed  []byte
		check func(t *testing.T, cfg config.Config)
	}{
		{name: "config.toml", seed: configSeed, check: func(t *testing.T, cfg config.Config) {
			if cfg.Refresh.Interval != 10 {
				t.Fatalf("refresh interval = %d, want 10", cfg.Refresh.Interval)
			}
			if from := cfg.Accounts["gmail"].From; from != "" {
				t.Fatalf("no account from, got %q", from)
			}
			if len(cfg.Pager.DefaultViews) != 0 {
				t.Fatal("no default-view")
			}
		}},
		{name: "ai.toml", seed: aiConfigSeed, check: func(t *testing.T, cfg config.Config) {
			if len(cfg.AI) != 0 {
				t.Fatalf("no provider, got %d", len(cfg.AI))
			}
			if _, ok := cfg.AIDataGrant("any"); ok {
				t.Fatal("no account grant")
			}
			if len(cfg.MCP.Accounts) != 0 || len(cfg.MCP.Tags) != 0 {
				t.Fatal("no mcp scope")
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, tc.name)
			seedFile(dir, tc.name, tc.seed)
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("seed must write %s: %v", tc.name, err)
			}
			if string(body) != string(tc.seed) {
				t.Fatal("seed must write the shipped template")
			}
			// a seeded file must load - a broken seed would brick startup
			// (strict load)
			cfg, err := config.Load(dir)
			if err != nil {
				t.Fatalf("seeded %s must load: %v", tc.name, err)
			}
			tc.check(t, cfg)
			// a user's file must survive a re-run
			assertSeedPreserves(t, path, func() { seedFile(dir, tc.name, tc.seed) })
		})
	}
}
