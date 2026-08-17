package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
