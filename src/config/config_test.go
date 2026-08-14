package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"notmutt/core"
)

func write(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadValid(t *testing.T) {
	cfg, err := Load(write(t, `
[ui]
keymap = "emacs"

[view.inbox]
query = "tag:inbox"
threads = true
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.Keymap != "emacs" {
		t.Fatalf("keymap = %q", cfg.UI.Keymap)
	}
	if cfg.Views["inbox"].Query != "tag:inbox" || !cfg.Views["inbox"].Threads {
		t.Fatalf("view parse wrong: %+v", cfg.Views["inbox"])
	}
}

func TestLoadUnknownKeyErrors(t *testing.T) {
	_, err := Load(write(t, "\n[ui]\nkeymap = \"vim\"\nksy = true\n"))
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
	if !strings.Contains(err.Error(), "ksy") {
		t.Fatalf("error must name the key, got: %v", err)
	}
}

func TestLoadInvalidEnum(t *testing.T) {
	_, err := Load(write(t, "\n[ui]\nkeymap = \"vi\"\n"))
	if err == nil {
		t.Fatal("expected error for invalid keymap")
	}
}

func TestLoadDefaultsWhenMissing(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.Keymap != "vim" {
		t.Fatalf("default keymap = %q", cfg.UI.Keymap)
	}
	if _, ok := cfg.Views["inbox"]; !ok {
		t.Fatal("default view missing")
	}
}

func TestLoadEmptyViewQueryErrors(t *testing.T) {
	_, err := Load(write(t, "\n[view.x]\nquery = \"\"\n"))
	if err == nil {
		t.Fatal("expected error for empty view query")
	}
}

func TestLoadThreadsFalseOverridesDefault(t *testing.T) {
	cfg, err := Load(write(t, `
[view.inbox]
query = "tag:inbox"
threads = false
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Views["inbox"].Threads {
		t.Fatal("threads = false must override the default true")
	}
}

func TestDefaultTagGroups(t *testing.T) {
	cfg := Default()
	g, ok := cfg.TagGroups["folder"]
	if !ok {
		t.Fatal("folder group missing from default")
	}
	if !slices.Equal(g.Tags, []string{"inbox", "archive", "deleted", "sent", "draft", "pending", "spam"}) {
		t.Fatalf("folder group = %v", g.Tags)
	}
}

func TestTagGroupListSorted(t *testing.T) {
	cfg := Default()
	cfg.TagGroups = map[string]core.TagGroup{
		"z": {Tags: []string{"x"}},
		"a": {Tags: []string{"y"}},
	}
	got := cfg.TagGroupList()
	if got[0].Tags[0] != "y" || got[1].Tags[0] != "x" {
		t.Fatalf("TagGroupList not sorted: %v", got)
	}
}

func TestLoadUnknownTagGroupKey(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(p, []byte("[tag-groups.folder]\ntags = [\"inbox\"]\nbogus = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(p)
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("unknown key must error, got %v", err)
	}
}

func TestValidateTagGroup(t *testing.T) {
	cfg := Default()
	cfg.TagGroups["empty"] = core.TagGroup{}
	if err := validate(cfg); err == nil {
		t.Fatal("empty group must error")
	}
	cfg.TagGroups["empty"] = core.TagGroup{Tags: []string{"a", "a"}}
	if err := validate(cfg); err == nil {
		t.Fatal("duplicate member must error")
	}
	cfg.TagGroups["empty"] = core.TagGroup{Tags: []string{" "}}
	if err := validate(cfg); err == nil {
		t.Fatal("blank tag name must error")
	}
}
