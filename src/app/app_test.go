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
	cfg.TagActions["quit"] = "x"
	if err := validateBindings(&cfg); err == nil {
		t.Fatal("tag action colliding with a builtin must error")
	}
}
