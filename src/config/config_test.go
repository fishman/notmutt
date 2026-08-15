package config

import (
	"maps"
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
	cfg = Default()
	cfg.TagGroups["other"] = core.TagGroup{Tags: []string{"inbox"}}
	if err := validate(cfg); err == nil {
		t.Fatal("a tag in multiple groups must error")
	}
	cfg = Default()
	cfg.TagGroups["other"] = core.TagGroup{Tags: []string{"wip"}}
	if err := validate(cfg); err != nil {
		t.Fatalf("disjoint groups must pass: %v", err)
	}
}

func TestLoadSendDefaults(t *testing.T) {
	cfg := Default()
	if cfg.Send.Command != "msmtp" || !slices.Equal(cfg.Send.Args, []string{"--read-envelope-from"}) {
		t.Fatalf("default send = %+v", cfg.Send)
	}
}

func TestSendOverrides(t *testing.T) {
	cfg, err := Load(write(t, `
[send]
command = "stub-send"
args = ["-v"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Send.Command != "stub-send" || !slices.Equal(cfg.Send.Args, []string{"-v"}) {
		t.Fatalf("send overrides = %+v", cfg.Send)
	}
}

func TestValidateSendCommand(t *testing.T) {
	_, err := Load(write(t, "\n[send]\ncommand = \"\"\n"))
	if err == nil || !strings.Contains(err.Error(), "send.command") {
		t.Fatalf("want send.command error, got %v", err)
	}
}

func TestAccountSendFields(t *testing.T) {
	cfg, err := Load(write(t, `
[accounts.gmail]
from = "Reza <reza@example.com>"
sent_folder = "/home/me/Mail/gmail/Sent"
default_signature = "gmail"
`))
	if err != nil {
		t.Fatal(err)
	}
	a := cfg.Accounts["gmail"]
	if a.From != "Reza <reza@example.com>" || a.SentFolder != "/home/me/Mail/gmail/Sent" || a.DefaultSignature != "gmail" {
		t.Fatalf("account send fields = %+v", a)
	}
}

func TestDefaultBindings(t *testing.T) {
	cfg := Default()
	want := map[string]string{
		"j": "cursor-down", "k": "cursor-up",
		"enter": "open", "q": "quit",
		"r": "reply", "R": "reply-all", "f": "forward", "m": "compose",
		"t": "toggle-read", "a": "archive", "d": "delete",
		"u": "undo", "$": "apply",
		"y": "spam", "p": "pending",
		"P": "preview",
		"g g": "cursor-top", "G": "cursor-bottom", "g r": "reply-all",
		"ctrl+d": "half-page-down", "ctrl+u": "half-page-up",
		"pgdown": "page-down", "pgup": "page-up",
		"?": "help",
		"[": "tab-prev", "]": "tab-next",
	}
	if !maps.Equal(cfg.Bindings["index"], want) {
		t.Fatalf("default index bindings = %v, want %v", cfg.Bindings["index"], want)
	}
	wantPager := map[string]string{
		"j": "scroll-down", "k": "scroll-up",
		"space": "page-down",
		"ctrl+d": "half-page-down", "ctrl+u": "half-page-up",
		"pgdown": "page-down", "pgup": "page-up",
		"g": "scroll-top", "G": "scroll-bottom",
		"q": "back",
		"[": "tab-prev", "]": "tab-next",
		"?": "help",
	}
	if !maps.Equal(cfg.Bindings["pager"], wantPager) {
		t.Fatalf("default pager bindings = %v, want %v", cfg.Bindings["pager"], wantPager)
	}
	wantCompose := map[string]string{
		"j": "form-down", "k": "form-up",
		"t": "edit-to", "s": "edit-subject", "f": "edit-from",
		"e": "edit", "a": "attach", "d": "detach",
		"c": "account", "C": "signature", "y": "send", "q": "abort",
		"[": "tab-prev", "]": "tab-next",
		"?": "help",
	}
	if !maps.Equal(cfg.Bindings["compose"], wantCompose) {
		t.Fatalf("default compose bindings = %v, want %v", cfg.Bindings["compose"], wantCompose)
	}
	wantFuzzy := map[string]string{
		"j": "fuzzy-down", "k": "fuzzy-up",
		"ctrl+n": "fuzzy-down", "ctrl+p": "fuzzy-up",
		"enter": "fuzzy-select", "esc": "fuzzy-cancel",
	}
	if !maps.Equal(cfg.Bindings["fuzzy"], wantFuzzy) {
		t.Fatalf("default fuzzy bindings = %v, want %v", cfg.Bindings["fuzzy"], wantFuzzy)
	}
}

func TestKeymapSchemes(t *testing.T) {
	cfg := Default()
	if cfg.Bindings["index"]["j"] != "cursor-down" {
		t.Fatalf("vim defaults missing: %v", cfg.Bindings["index"])
	}
	cfg, err := Load(write(t, "\n[ui]\nkeymap = \"emacs\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Bindings["index"]["ctrl+n"] != "cursor-down" {
		t.Fatalf("emacs index defaults missing: %v", cfg.Bindings["index"])
	}
	if cfg.Bindings["index"]["j"] != "" {
		t.Fatalf("emacs scheme must not carry vim keys: %v", cfg.Bindings["index"])
	}
	if cfg.Bindings["compose"]["ctrl+n"] != "form-down" {
		t.Fatalf("emacs compose movement missing: %v", cfg.Bindings["compose"])
	}
}

func TestKeymapFileOverlay(t *testing.T) {
	cfg, err := Load(write(t, `
[ui]
keymap = "emacs"
[bindings.index]
q = "open"
[bindings.pager]
q = "back"
`))
	if err != nil {
		t.Fatal(err)
	}
	// the file overrides per key on top of the scheme; untouched keys
	// and contexts keep the scheme defaults
	if cfg.Bindings["index"]["q"] != "open" || cfg.Bindings["index"]["ctrl+n"] != "cursor-down" {
		t.Fatalf("index overlay wrong: %v", cfg.Bindings["index"])
	}
	if cfg.Bindings["pager"]["q"] != "back" || cfg.Bindings["pager"]["ctrl+g"] != "back" {
		t.Fatalf("pager overlay wrong: %v", cfg.Bindings["pager"])
	}
	// scheme defaults still present, no vim leakage
	if cfg.Bindings["index"]["j"] != "" {
		t.Fatalf("emacs scheme must not carry vim keys after overlay: %v", cfg.Bindings["index"])
	}
}

func TestLoadUnknownBindingKey(t *testing.T) {
	// binding keys inside a map field are arbitrary by design (rebinding),
	// so strict load catches typo'd table names at the section level
	_, err := Load(write(t, "\n[binding.index]\nq = \"quit\"\n"))
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
	if !strings.Contains(err.Error(), "binding") {
		t.Fatalf("error must name the key, got: %v", err)
	}
}

func TestValidateBindings(t *testing.T) {
	cfg := Default()
	cfg.Bindings["index"] = map[string]string{}
	if err := validate(cfg); err == nil {
		t.Fatal("empty context must error")
	}
	cfg.Bindings["index"] = map[string]string{"": "archive"}
	if err := validate(cfg); err == nil {
		t.Fatal("blank key must error")
	}
	cfg.Bindings["index"] = map[string]string{"x": " "}
	if err := validate(cfg); err == nil {
		t.Fatal("blank action must error")
	}
}

func TestValidateUnknownBindingContext(t *testing.T) {
	cfg := Default()
	cfg.Bindings["indicx"] = map[string]string{"q": "quit"}
	if err := validate(cfg); err == nil || !strings.Contains(err.Error(), "indicx") {
		t.Fatalf("unknown context must error naming it, got %v", err)
	}
}

func TestLoadUnknownBindingContext(t *testing.T) {
	_, err := Load(write(t, "\n[bindings.indicx]\nq = \"quit\"\n"))
	if err == nil {
		t.Fatal("unknown binding context must error")
	}
	if !strings.Contains(err.Error(), "indicx") {
		t.Fatalf("error must name the context, got: %v", err)
	}
}

func TestDefaultTagActions(t *testing.T) {
	cfg := Default()
	want := map[string]string{
		"toggle-read": "unread",
		"archive":     "archive",
		"delete":      "deleted",
		"spam":        "spam",
		"pending":     "pending",
	}
	if !maps.Equal(cfg.TagActions, want) {
		t.Fatalf("default tag actions = %v, want %v", cfg.TagActions, want)
	}
}

func TestValidateTagAction(t *testing.T) {
	cfg := Default()
	cfg.TagActions[""] = "x"
	if err := validate(cfg); err == nil {
		t.Fatal("blank action name must error")
	}
	cfg = Default()
	cfg.TagActions["x"] = " "
	if err := validate(cfg); err == nil {
		t.Fatal("blank tag value must error")
	}
}

// writeThemeFile writes a theme fixture into a fresh temp dir. Default()
// already provides the inbox view, so fixtures need no view table; the
// keymap defaults to "vim".
func writeThemeFile(t *testing.T, body string) string {
	t.Helper()
	return write(t, body)
}

func TestThemeStrictLoad(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for name, tc := range map[string]struct{ body, want string }{
		"unknown key":     {"[theme.dark]\nnonesuch = { fg = \"base00\" }", "unknown key"},
		"unknown palette": {"[theme.dark]\nstatus = { fg = \"base99\" }", "base99"},
		"bad hex":         {"[palette]\nbase00 = \"zzz\"", "base00"},
		"bad attr":        {"[theme.dark]\nnormal = { attrs = [\"glow\"] }", "glow"},
		"missing variant": {"[theme]\ndefault = \"light\"", "light"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(write(name+".toml", tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestStyleResolutionOrder(t *testing.T) {
	cfg, err := Load(writeThemeFile(t, `
[palette]
base00 = "#21252b"
base05 = "#abb2bf"
base0A = "#e5c07b"
[palette.light]
base00 = "#fafafa"
[theme]
default = "dark"
[theme.dark]
normal = { fg = "base05", bg = "base00" }
status = { fg = "base0A" }
[theme.light]
normal = { fg = "base05", bg = "base00" }
status = { fg = "#ff0000" }
`))
	if err != nil {
		t.Fatal(err)
	}
	// dark: fg (base0A) resolves through the base palette, bg inherits
	// normal's base00 through the base palette
	res := cfg.Theme.Resolved(cfg.Palette, "dark")
	if res["status"].Fg != "#e5c07b" || res["status"].Bg != "#21252b" {
		t.Fatalf("dark resolution: %+v", res["status"])
	}
	// light: the style pins a raw hex, which beats the light palette;
	// bg inherits normal's base00 resolved through the LIGHT palette
	res = cfg.Theme.Resolved(cfg.Palette, "light")
	if res["status"].Fg != "#ff0000" || res["status"].Bg != "#fafafa" {
		t.Fatalf("light resolution: %+v", res["status"])
	}
}

// TestThemeIndexPartialOverlay pins the per-key overlay merge: a
// [theme.dark.index] naming only subject keeps the variant's other
// index styles (R8 merge, not replace).
func TestThemeIndexPartialOverlay(t *testing.T) {
	cfg, err := Load(writeThemeFile(t, `
[theme.dark.index]
subject = { fg = "base0B" }
`))
	if err != nil {
		t.Fatal(err)
	}
	res := cfg.Theme.Resolved(cfg.Palette, "dark")
	if got := res["index.number"].Fg; got != "#5c6370" {
		t.Fatalf("index.number must survive the partial overlay, got %q", got)
	}
	if got := res["index.subject"].Fg; got != "#98c379" {
		t.Fatalf("index.subject must be overridden, got %q", got)
	}
}

func TestValidateTagsMax(t *testing.T) {
	cfg := Default()
	cfg.UI.Tags.Max = 0
	if err := validate(cfg); err == nil || !strings.Contains(err.Error(), "ui.tags.max") {
		t.Fatalf("max 0 must error, got %v", err)
	}
	cfg = Default()
	cfg.UI.Tags.Max = -2
	if err := validate(cfg); err == nil {
		t.Fatal("negative max must error")
	}
}

func TestSetThemeVariant(t *testing.T) {
	cfg := Default()
	cfg.Theme = Theme{Default: "dark", Variants: map[string]StyleTable{
		"dark": {}, "light": {},
	}}
	s := NewStore(cfg)
	got := ""
	s.Subscribe("theme", func() { got = "theme" })
	if err := s.SetThemeVariant("light"); err != nil {
		t.Fatal(err)
	}
	if s.Config().Theme.Default != "light" || got != "theme" {
		t.Fatalf("variant switch: %q %q", s.Config().Theme.Default, got)
	}
	if err := s.SetThemeVariant("nope"); err == nil {
		t.Fatal("unknown variant must error")
	}
}

func TestAccounts(t *testing.T) {
	cfg, err := Load(writeThemeFile(t, `
[accounts.dynamia]
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Accounts["dynamia"].Tag("dynamia") != "dynamia" {
		t.Fatalf("account tag defaults to the name: %+v", cfg.Accounts["dynamia"])
	}
	// a file adding one account merges over the reference defaults (R8)
	if cfg.Accounts["gmail"].Tag("gmail") != "gmail" {
		t.Fatalf("default accounts survive merge: %+v", cfg.Accounts)
	}
}

func TestAccountFolderOverride(t *testing.T) {
	cfg, err := Load(writeThemeFile(t, `
[accounts.main]
folder = "gmail"
`))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Accounts["main"].Tag("main"); got != "gmail" {
		t.Fatalf("folder overrides the account tag: want gmail, got %q", got)
	}
}

func TestAccountStrictLoad(t *testing.T) {
	_, err := Load(writeThemeFile(t, `
[accounts.dynamia]
nonesuch = "x"
`))
	if err == nil || !strings.Contains(err.Error(), "nonesuch") {
		t.Fatalf("want unknown key error, got %v", err)
	}
}

func TestAccountBlankFolder(t *testing.T) {
	_, err := Load(writeThemeFile(t, `
[accounts.dynamia]
folder = ""
`))
	if err == nil || !strings.Contains(err.Error(), "folder") {
		t.Fatalf("want blank folder error, got %v", err)
	}
}
