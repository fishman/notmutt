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
// file returns its name, the top-level match names, and the tag ->
// candidate maps.
func TestLuaTemplatesLoad(t *testing.T) {
	dir := t.TempDir()
	templates := filepath.Join(dir, "templates")
	if err := os.MkdirAll(templates, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templates, "exchange.lua"), []byte(`
return {
  name = "exchange",
  match = { "INBOX", "Sent Items" },
  folders = {
    inbox = { "INBOX" },
    sent = { "Sent Items" },
    deleted = { "Deleted Items" },
    archive = { "Archive" },
    draft = { "Drafts" },
    spam = { "Spam" },
  },
}
`), 0600); err != nil {
		t.Fatal(err)
	}
	got := luaTemplates(dir, []string{"exchange"})
	if len(got) != 1 || got[0].Name != "exchange" {
		t.Fatalf("lua templates = %+v, want the exchange template", got)
	}
	wantMatch := []string{"INBOX", "Sent Items"}
	wantFolders := map[string][]string{
		"inbox": {"INBOX"}, "sent": {"Sent Items"}, "deleted": {"Deleted Items"},
		"archive": {"Archive"}, "draft": {"Drafts"}, "spam": {"Spam"},
	}
	if !slices.Equal(got[0].Match, wantMatch) {
		t.Fatalf("match = %v, want %v", got[0].Match, wantMatch)
	}
	if !maps.EqualFunc(got[0].Folders, wantFolders, slices.Equal) {
		t.Fatalf("folders = %v, want %v", got[0].Folders, wantFolders)
	}
}

// TestLuaTemplatesOptIn pins the opt-in rule: only the names in the
// active list load - an unlisted file is not even evaluated (a broken
// unlisted template stays invisible).
func TestLuaTemplatesOptIn(t *testing.T) {
	dir := t.TempDir()
	templates := filepath.Join(dir, "templates")
	if err := os.MkdirAll(templates, 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"gmail.lua":  `return { name = "gmail", match = { "INBOX" }, folders = { inbox = { "INBOX" } } }`,
		"vendor.lua": `return { name = "vendor", match = { "INBOX" }, folders = { inbox = { "INBOX" } } }`,
		"broken.lua": `return {`,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(templates, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	got := luaTemplates(dir, []string{"gmail"})
	if len(got) != 1 || got[0].Name != "gmail" {
		t.Fatalf("only the listed template must load, got %+v", got)
	}
}

// TestLuaTemplateLoadErrorSkips pins the degrade rule: a listed bad
// file is skipped, the good ones still load (the plugin load rule);
// a file whose name field does not match its file name is skipped.
func TestLuaTemplateLoadErrorSkips(t *testing.T) {
	dir := t.TempDir()
	templates := filepath.Join(dir, "templates")
	if err := os.MkdirAll(templates, 0700); err != nil {
		t.Fatal(err)
	}
	bad := map[string]string{
		"broken.lua":    `return { name = 7 }`,
		"missing.lua":   `return { name = "missing", folders = { inbox = { "INBOX" } } }`,
		"notatable.lua": `return "nope"`,
		"mismatch.lua":  `return { name = "other", match = { "INBOX" }, folders = { inbox = { "INBOX" } } }`,
		"outlook.lua":   `return { name = "outlook", match = { "INBOX", "Sent" }, folders = { inbox = { "INBOX" } } }`,
	}
	for name, body := range bad {
		if err := os.WriteFile(filepath.Join(templates, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	got := luaTemplates(dir, []string{"broken", "missing", "notatable", "mismatch", "outlook"})
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
return { name = "evil", match = { "INBOX" }, folders = { inbox = { "INBOX" } } }
`), 0600); err != nil {
		t.Fatal(err)
	}
	if got := luaTemplates(dir, []string{"evil"}); len(got) != 0 {
		t.Fatalf("a template needing libs must fail to load, got %+v", got)
	}
}

// TestMergedTemplates pins the merge: a listed Lua template replaces
// the built-in of the same name, a new name adds after the built-ins,
// an unlisted file never loads.
func TestMergedTemplates(t *testing.T) {
	dir := t.TempDir()
	templates := filepath.Join(dir, "templates")
	if err := os.MkdirAll(templates, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templates, "exchange.lua"), []byte(`
return { name = "exchange", match = { "INBOX" }, folders = { inbox = { "INBOX" } } }
`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templates, "gmail.lua"), []byte(`
return { name = "gmail", match = { "INBOX" }, folders = { inbox = { "INBOX" } } }
`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(templates, "vendor.lua"), []byte(`
return { name = "vendor", match = { "INBOX" }, folders = { inbox = { "INBOX" } } }
`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOTMUTT_CONFIG", filepath.Join(dir, "config.toml"))
	got := mergedTemplates([]string{"gmail", "exchange"})
	want := []string{"gmail", "exchange", "icloud", "outlook"}
	if len(got) != len(want) {
		t.Fatalf("merged templates = %+v, want %v", names(got), want)
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Fatalf("merged templates = %+v, want %v", names(got), want)
		}
	}
	if len(got[0].Match) != 1 || len(got[1].Match) != 1 {
		t.Fatalf("lua gmail/exchange must replace the built-ins, got %v %v",
			got[0].Match, got[1].Match)
	}
	if slices.Contains(names(got), "vendor") {
		t.Fatalf("an unlisted template must never load: %v", names(got))
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
  match = { "INBOX", "Sent Items" },
  folders = {
    inbox = { "INBOX" },
    sent = { "Sent Items" },
    deleted = { "Deleted Items" },
  },
}
`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOTMUTT_CONFIG", filepath.Join(dir, "config.toml"))
	accs, err := setup.Detect(root, mergedTemplates([]string{"exchange"}))
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
		if !slices.Equal(got[i].Match, want.Match) {
			t.Fatalf("template %s: lua match %v != Go match %v", want.Name, got[i].Match, want.Match)
		}
		if !maps.EqualFunc(got[i].Folders, want.Folders, slices.Equal) {
			t.Fatalf("template %s: lua folders %v != Go folders %v",
				want.Name, got[i].Folders, want.Folders)
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
