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

func TestSeedTemplates(t *testing.T) {
	dir := t.TempDir()
	seedTemplates(dir)
	dst := filepath.Join(dir, "lua", "templates")
	got, err := os.ReadDir(dst)
	if err != nil {
		t.Fatalf("seed must create %s: %v", dst, err)
	}
	if len(got) != 5 {
		t.Fatalf("seed must copy the 5 shipped templates, got %d", len(got))
	}
	// a customized file must survive a re-run
	edited := filepath.Join(dst, "gmail.lua")
	if err := os.WriteFile(edited, []byte("return {}"), 0600); err != nil {
		t.Fatal(err)
	}
	seedTemplates(dir)
	body, err := os.ReadFile(edited)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "return {}" {
		t.Fatal("seed must never overwrite an existing template")
	}
}

func TestSeedAICommands(t *testing.T) {
	dir := t.TempDir()
	seedAICommands(dir)
	dst := filepath.Join(dir, "ai")
	entries, err := os.ReadDir(dst)
	if err != nil {
		t.Fatalf("seed must create %s: %v", dst, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	want := []string{"accounts", "context", "draft-reply.md", "next-steps.md"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("seeded files = %v, want %v", names, want)
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
	if _, err := os.Stat(filepath.Join(dst, "accounts", "README.md")); err != nil {
		t.Fatalf("accounts README missing: %v", err)
	}
	// the default style context must seed and survive a user edit
	def := filepath.Join(dst, "context", "default.md")
	body, err := os.ReadFile(def)
	if err != nil {
		t.Fatalf("default context missing: %v", err)
	}
	if !strings.Contains(string(body), "concise") {
		t.Fatalf("default context must carry the brief style: %q", body)
	}
	if err := os.WriteFile(def, []byte("mine"), 0600); err != nil {
		t.Fatal(err)
	}
	seedAICommands(dir)
	if got, _ := os.ReadFile(def); string(got) != "mine" {
		t.Fatal("seed must never overwrite the user's default context")
	}
	// a user edit must survive a re-run
	edited := filepath.Join(dst, "next-steps.md")
	if err := os.WriteFile(edited, []byte("mine"), 0600); err != nil {
		t.Fatal(err)
	}
	seedAICommands(dir)
	body, err = os.ReadFile(edited)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "mine" {
		t.Fatal("seed must never overwrite an existing prompt")
	}
	// permissions: dirs 0700, files 0600
	fi, err := os.Stat(filepath.Join(dst, "accounts"))
	if err != nil || fi.Mode().Perm() != 0700 {
		t.Errorf("accounts dir mode = %v, want 0700", fi.Mode().Perm())
	}
	fi, err = os.Stat(filepath.Join(dst, "context"))
	if err != nil || fi.Mode().Perm() != 0700 {
		t.Errorf("context dir mode = %v, want 0700", fi.Mode().Perm())
	}
	fi, err = os.Stat(filepath.Join(dst, "context", "default.md"))
	if err != nil || fi.Mode().Perm() != 0600 {
		t.Errorf("default context mode = %v, want 0600", fi.Mode().Perm())
	}
	fi, err = os.Stat(filepath.Join(dst, "draft-reply.md"))
	if err != nil || fi.Mode().Perm() != 0600 {
		t.Errorf("prompt file mode = %v, want 0600", fi.Mode().Perm())
	}
}
