package app

import (
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
