//go:build lua

package app

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"notmutt/setup"
)

// TestLuaTemplatesLoad pins the contribution contract: a template
// file returns its name and the tag -> candidate maps.
func TestLuaTemplatesLoad(t *testing.T) {
	dir := t.TempDir()
	templates := filepath.Join(dir, "templates")
	if err := os.MkdirAll(templates, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templates, "exchange.lua"), []byte(`
return {
  name = "exchange",
  required = {
    inbox = { "INBOX" },
    sent = { "Sent Items" },
    deleted = { "Deleted Items" },
  },
  optional = {
    archive = { "Archive" },
    draft = { "Drafts" },
    spam = { "Spam" },
  },
}
`), 0600); err != nil {
		t.Fatal(err)
	}
	got := luaTemplates(dir)
	if len(got) != 1 || got[0].Name != "exchange" {
		t.Fatalf("lua templates = %+v, want the exchange template", got)
	}
	wantReq := map[string][]string{
		"inbox": {"INBOX"}, "sent": {"Sent Items"}, "deleted": {"Deleted Items"},
	}
	wantOpt := map[string][]string{
		"archive": {"Archive"}, "draft": {"Drafts"}, "spam": {"Spam"},
	}
	if !maps.EqualFunc(got[0].Required, wantReq, slices.Equal) || !maps.EqualFunc(got[0].Optional, wantOpt, slices.Equal) {
		t.Fatalf("template maps = %v %v, want %v %v", got[0].Required, got[0].Optional, wantReq, wantOpt)
	}
}

// TestLuaTemplateLoadErrorSkips pins the degrade rule: a bad file is
// skipped, the good ones still load (the plugin load rule).
func TestLuaTemplateLoadErrorSkips(t *testing.T) {
	dir := t.TempDir()
	templates := filepath.Join(dir, "templates")
	if err := os.MkdirAll(templates, 0700); err != nil {
		t.Fatal(err)
	}
	bad := map[string]string{
		"broken.lua":    `return { name = 7 }`,
		"missing.lua":   `return { required = { inbox = { "INBOX" } } }`,
		"notatable.lua": `return "nope"`,
		"ok.lua":        `return { name = "outlook", required = { inbox = { "INBOX" } } }`,
	}
	for name, body := range bad {
		if err := os.WriteFile(filepath.Join(templates, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	got := luaTemplates(dir)
	if len(got) != 1 || got[0].Name != "outlook" {
		t.Fatalf("bad templates must be skipped, got %+v", got)
	}
}

// TestLuaTemplateSandbox pins the lib-less VM: a template has no
// surface to call - os.load is not available, so the file cannot
// escape its data shape.
func TestLuaTemplateSandbox(t *testing.T) {
	dir := t.TempDir()
	templates := filepath.Join(dir, "templates")
	if err := os.MkdirAll(templates, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templates, "evil.lua"), []byte(`
local os = require("os")
return { name = "evil", required = { inbox = { "INBOX" } } }
`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := luaTemplates(dir); len(got) != 0 {
		t.Fatalf("a template needing libs must fail to load, got %+v", got)
	}
}

// TestMergedTemplates pins the merge: a Lua template replaces the
// built-in of the same name, a new name adds after the built-ins.
func TestMergedTemplates(t *testing.T) {
	dir := t.TempDir()
	templates := filepath.Join(dir, "templates")
	if err := os.MkdirAll(templates, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templates, "exchange.lua"), []byte(`
return { name = "exchange", required = { inbox = { "INBOX" } } }
`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templates, "gmail.lua"), []byte(`
return { name = "gmail", required = { inbox = { "INBOX" } } }
`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templates, "vendor.lua"), []byte(`
return { name = "vendor", required = { inbox = { "INBOX" } } }
`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOTMUTT_CONFIG", filepath.Join(dir, "config.toml"))
	got := mergedTemplates()
	want := []string{"gmail", "exchange", "icloud", "outlook", "vendor"}
	if len(got) != len(want) {
		t.Fatalf("merged templates = %+v, want %v", names(got), want)
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Fatalf("merged templates = %+v, want %v", names(got), want)
		}
	}
	if len(got[0].Required) != 1 || len(got[1].Required) != 1 {
		t.Fatalf("lua gmail/exchange must replace the built-ins, got %v %v",
			got[0].Required, got[1].Required)
	}
}

// TestSetupDetectionFromLua end-to-end: a contributed exchange
// template detects an exchange-shaped account in the merged flow.
func TestSetupDetectionFromLua(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "exchange/INBOX"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "exchange/Sent Items"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "exchange/Deleted Items"), 0700); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	templates := filepath.Join(dir, "templates")
	if err := os.MkdirAll(templates, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templates, "exchange.lua"), []byte(`
return {
  name = "exchange",
  required = {
    inbox = { "INBOX" },
    sent = { "Sent Items" },
    deleted = { "Deleted Items" },
  },
}
`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOTMUTT_CONFIG", filepath.Join(dir, "config.toml"))
	accs, err := setup.Detect(root, mergedTemplates())
	if err != nil {
		t.Fatal(err)
	}
	if len(accs) != 1 || accs[0].Name != "exchange" || accs[0].Template != "exchange" {
		t.Fatalf("the lua template must detect the account: %+v", accs)
	}
}

// TestBuiltinTemplatesMatchGoData pins the no-Lua fallback: the
// embedded template files and setup.Templates are the same data, so
// the default and lua builds detect identically. A drift breaks the
// build - the files must change together.
func TestBuiltinTemplatesMatchGoData(t *testing.T) {
	got := builtinTemplates()
	if len(got) != len(setup.Templates) {
		t.Fatalf("builtin lua templates = %d, Go fallback = %d", len(got), len(setup.Templates))
	}
	for i := range got {
		want := setup.Templates[i]
		if got[i].Name != want.Name {
			t.Fatalf("template %d: lua name %q != Go name %q", i, got[i].Name, want.Name)
		}
		if !maps.EqualFunc(got[i].Required, want.Required, slices.Equal) || !maps.EqualFunc(got[i].Optional, want.Optional, slices.Equal) {
			t.Fatalf("template %s: lua maps %v %v != Go maps %v %v",
				want.Name, got[i].Required, got[i].Optional, want.Required, want.Optional)
		}
	}
}

// TestSetupDetectsProviderSeeds pins the shipped exchange/icloud/
// outlook seeds against their folder shapes.
func TestSetupDetectsProviderSeeds(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{
		"work/INBOX", "work/Sent Items", "work/Deleted Items",
		"home/INBOX", "home/Sent Messages", "home/Trash",
		"junkbox/INBOX", "junkbox/Sent", "junkbox/Deleted Items",
	} {
		if err := os.MkdirAll(filepath.Join(root, d), 0700); err != nil {
			t.Fatal(err)
		}
	}
	accs, err := setup.Detect(root, setup.Templates)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, a := range accs {
		got[a.Name] = a.Template
	}
	want := map[string]string{"work": "exchange", "home": "icloud", "junkbox": "outlook"}
	for name, tmpl := range want {
		if got[name] != tmpl {
			t.Fatalf("account %s = %q, want %q (all: %v)", name, got[name], tmpl, got)
		}
	}
}

func names(ts []setup.Template) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.Name)
	}
	return out
}
