package config

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"notmutt/core"
)

func write(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestDeriveAccountViews pins the per-account view derivation (R1:
// virtual views are tag queries; the muttrc folder:/^<folder>\//
// account-tag pattern as data). Every account - read-only included, a
// view is a query, never a write (R2's rules govern writes only) -
// owns a view over its account tag, numbered by sorted account name in
// the vim index scheme (g1..gN), and the account tag respects the
// folder override.
func TestDeriveAccountViews(t *testing.T) {
	dir := write(t, "")
	os.WriteFile(filepath.Join(dir, "accounts.toml"), []byte(`
[accounts.acme]
from = "Ann <ann@example.com>"
folder = "acme-folder"

[accounts.atlas]
from = "Ann <ann@atlas.example.com>"

[accounts.gmail]
from = "Ann <ann@gmail.example.com>"

[accounts.toptal]
from = "Ann <ann@toptal.example.com>"
readonly = true
`), 0600)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"acme-folder", "atlas", "gmail", "toptal"} {
		v, ok := cfg.Views[tag]
		if !ok {
			t.Fatalf("view %q not derived", tag)
		}
		if v.Query != "tag:"+tag {
			t.Fatalf("view %q query = %q", tag, v.Query)
		}
	}
	if _, ok := cfg.Views["acme"]; ok {
		t.Fatal("view must use the account folder, not the account name")
	}
	// g1..gN by sorted account name: acme-folder, atlas, gmail, toptal
	for n, tag := range []string{"acme-folder", "atlas", "gmail", "toptal"} {
		if act := cfg.Bindings["index"][fmt.Sprintf("g %d", n+1)]; act != "goto-"+tag {
			t.Fatalf("g %d = %q, want goto-%s", n+1, act, tag)
		}
	}
	if cfg.ActiveView != "inbox" {
		t.Fatalf("active view = %q", cfg.ActiveView)
	}
}

// TestDeriveAccountViewsUserOverride pins the precedence: a [view]
// entry with the account's tag wins over the derivation, and a key the
// user already bound is never replaced. Derivation is idempotent - the
// second Load sees the merged accounts and no-op's on existing entries.
func TestDeriveAccountViewsUserOverride(t *testing.T) {
	dir := write(t, "")
	os.WriteFile(filepath.Join(dir, "accounts.toml"), []byte(`
[accounts.gmail]
from = "Ann <ann@example.com>"

[view.gmail]
query = "tag:inbox"
`), 0600)
	os.WriteFile(filepath.Join(dir, "binds.toml"), []byte(`
[schemes.vim.index]
"g 1" = { fun = "cursor-top" }
`), 0600)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Views["gmail"].Query != "tag:inbox" {
		t.Fatalf("user view must win: %+v", cfg.Views["gmail"])
	}
	if cfg.Bindings["index"]["g 1"] != "cursor-top" {
		t.Fatalf("user key must win: %q", cfg.Bindings["index"]["g 1"])
	}
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

// TestLoadMultipleFiles pins the multi-file merge: every *.toml in the
// dir loads, tables merge across files, config.toml wins on conflicts
// (the splits merge first, the main file last), keys absent from
// config.toml survive from the splits, and a single-file dir loads.
func TestLoadMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("accounts.toml", `
[accounts.gmail]
folder = "gmail"
preset = "gmail"
`)
	write("config.toml", `
[ui]
keymap = "emacs"

[notify]
max = 1
`)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.UI.Keymap != "emacs" || *cfg.Accounts["gmail"].Folder != "gmail" {
		t.Fatalf("cross-file merge wrong: %+v %+v", cfg.UI.Keymap, cfg.Accounts)
	}
	if cfg.Notify.Max != 1 {
		t.Fatalf("notify.max = %d", cfg.Notify.Max)
	}
	// a split's scalar conflicts with config.toml: config.toml wins
	// (it merges last); a key absent from config.toml survives from
	// the split
	write("filters.toml", `
[notify]
max = 4

[ui.tags]
max = 2
`)
	cfg, err = Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Notify.Max != 1 {
		t.Fatalf("config.toml must win the conflict: notify.max = %d", cfg.Notify.Max)
	}
	if cfg.UI.Tags.Max != 2 {
		t.Fatalf("split-only keys must survive: ui.tags.max = %d", cfg.UI.Tags.Max)
	}
	if *cfg.Accounts["gmail"].Folder != "gmail" {
		t.Fatalf("split-only account keys must survive: %+v", cfg.Accounts)
	}
}

// TestLoadUnknownKeyNamesFile pins strict-load attribution: an unknown
// key errors naming the file that carries it.
func TestLoadUnknownKeyNamesFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "accounts.toml"), []byte("[notify]\nnonesuch = 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(dir)
	if err == nil || !strings.Contains(err.Error(), "accounts.toml") || !strings.Contains(err.Error(), "nonesuch") {
		t.Fatalf("want accounts.toml naming nonesuch, got %v", err)
	}
}

func TestLoadAttachCommands(t *testing.T) {
	cfg, err := Load(write(t, `
[attach-commands]
yazi = ["yazi", "--chooser-file"]
fzf = ["fzf", "--multi"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.AttachCommands) != 2 || len(cfg.AttachCommands["yazi"]) != 2 || cfg.AttachCommands["yazi"][1] != "--chooser-file" {
		t.Fatalf("attach-commands = %+v", cfg.AttachCommands)
	}
}

func TestLoadAttachCommandsEmptyArgvErrors(t *testing.T) {
	_, err := Load(write(t, `
[attach-commands]
yazi = []
`))
	if err == nil || !strings.Contains(err.Error(), "yazi") {
		t.Fatalf("empty argv must error naming the command, got: %v", err)
	}
}

func TestLoadNotify(t *testing.T) {
	cfg, err := Load(write(t, `
[notify]
command = ["notify-send", "notmutt", "{count} new mail"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Notify.Command) != 3 || cfg.Notify.Command[2] != "{count} new mail" {
		t.Fatalf("notify = %+v", cfg.Notify.Command)
	}
}

func TestLoadNotifyEmptyArgvErrors(t *testing.T) {
	_, err := Load(write(t, `
[notify]
command = [""]
`))
	if err == nil {
		t.Fatal("expected error for an empty command element")
	}
}

func TestLoadNotifyFields(t *testing.T) {
	cfg, err := Load(write(t, `
[notify]
backend = "command"
priority = ["urgent"]
max = 5
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Notify.Backend != "command" || len(cfg.Notify.Priority) != 1 || cfg.Notify.Max != 5 {
		t.Fatalf("notify = %+v", cfg.Notify)
	}
	for _, b := range []string{"", "beeep"} { // auto and explicit both load
		if _, err := Load(write(t, "[notify]\nbackend = \""+b+"\"\n")); err != nil {
			t.Fatalf("backend %q: %v", b, err)
		}
	}
}

func TestLoadNotifyUnknownBackendErrors(t *testing.T) {
	_, err := Load(write(t, `
[notify]
backend = "carrier-pigeon"
`))
	if err == nil || !strings.Contains(err.Error(), "carrier-pigeon") {
		t.Fatalf("unknown backend must error naming it, got: %v", err)
	}
}

func TestLoadNotifyBadValuesError(t *testing.T) {
	for _, toml := range []string{
		"[notify]\nmax = -1\n",
		"[notify]\npriority = [\"\"]\n",
	} {
		if _, err := Load(write(t, toml)); err == nil {
			t.Fatalf("expected error for: %s", toml)
		}
	}
}

func TestDefaultNotify(t *testing.T) {
	cfg := Default()
	if cfg.Notify.Backend != "" || cfg.Notify.Max != 3 {
		t.Fatalf("default notify = %+v", cfg.Notify)
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
	cfg, err := Load(t.TempDir())
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
	_, err := Load(dir)
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

func TestRefreshDefaultInterval(t *testing.T) {
	cfg := Default()
	if cfg.Refresh.Interval != 1200 {
		t.Fatalf("default refresh interval = %d", cfg.Refresh.Interval)
	}
}

func TestRefreshIntervalOverride(t *testing.T) {
	cfg, err := Load(write(t, "\n[refresh]\ninterval = 3\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Refresh.Interval != 3 {
		t.Fatalf("refresh interval = %d", cfg.Refresh.Interval)
	}
}

func TestRefreshIntervalDisabled(t *testing.T) {
	cfg, err := Load(write(t, "\n[refresh]\ninterval = 0\n"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Refresh.Interval != 0 {
		t.Fatalf("interval 0 must load as disabled, got %d", cfg.Refresh.Interval)
	}
}

func TestRefreshIntervalNegativeErrors(t *testing.T) {
	_, err := Load(write(t, "\n[refresh]\ninterval = -1\n"))
	if err == nil || !strings.Contains(err.Error(), "refresh.interval") {
		t.Fatalf("want refresh.interval error, got %v", err)
	}
}

func TestAccountSendFields(t *testing.T) {
	cfg, err := Load(write(t, `
[accounts.gmail]
from = "Sender <sender@example.com>"
default_signature = "gmail"
`))
	if err != nil {
		t.Fatal(err)
	}
	a := cfg.Accounts["gmail"]
	if a.From != "Sender <sender@example.com>" || a.DefaultSignature != "gmail" {
		t.Fatalf("account send fields = %+v", a)
	}
}

func TestDefaultBindings(t *testing.T) {
	cfg := Default()
	// the default bindings ARE the embedded vim scheme (base.toml),
	// context for context - the Go side derives, never re-declares
	bs, _ := bindingsFromScheme(baseConfig.Schemes["vim"])
	// the per-account goto keys derive from the accounts table, not the
	// base scheme: drop them before the equality check
	for key := range cfg.DerivedGKeys {
		delete(cfg.Bindings["index"], key)
	}
	if !maps.EqualFunc(cfg.Bindings, bs, maps.Equal) {
		t.Fatalf("default bindings = %v, want the embedded vim scheme %v", cfg.Bindings, baseConfig.Schemes["vim"])
	}
	// the derived table is a clone: rebinding a Default never touches
	// the scheme the next Default hands out
	cfg.Bindings["index"]["j"] = "mutated"
	if next := Default(); next.Bindings["index"]["j"] != "cursor-down" {
		t.Fatalf("Default must hand out fresh bindings, got %q", next.Bindings["index"]["j"])
	}
	wantCompose := map[string]string{
		"j": "form-down", "k": "form-up", "down": "form-down", "up": "form-up",
		"ctrl+d": "half-page-down", "ctrl+u": "half-page-up",
		"ctrl+f": "page-down", "ctrl+b": "page-up",
		"t": "edit-to", "s": "edit-subject", "f": "edit-from",
		"c": "edit-cc", "b": "edit-bcc", "r": "edit-replyto", "S": "security",
		"e": "edit", "a": "attach", "tab": "attach", "d": "detach",
		"A": "account", "C": "signature", "y": "send", "q": "abort",
		"[": "tab-prev", "]": "tab-next",
		"?": "help", "~": "log",
	}
	if !maps.Equal(cfg.Bindings["compose"], wantCompose) {
		t.Fatalf("default compose bindings = %v, want %v", cfg.Bindings["compose"], wantCompose)
	}
	wantFuzzy := map[string]string{
		"j": "fuzzy-down", "k": "fuzzy-up", "down": "fuzzy-down", "up": "fuzzy-up",
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
	if cfg.Bindings["compose"]["ctrl+v"] != "page-down" ||
		cfg.Bindings["compose"]["alt+v"] != "page-up" {
		t.Fatalf("emacs compose scroll keys missing: %v", cfg.Bindings["compose"])
	}
	for _, tc := range []struct{ key, fun string }{
		{"c", "edit-cc"}, {"b", "edit-bcc"}, {"r", "edit-replyto"}, {"S", "security"},
	} {
		if cfg.Bindings["compose"][tc.key] != tc.fun {
			t.Fatalf("emacs compose %s must map to %s: %v", tc.key, tc.fun, cfg.Bindings["compose"])
		}
	}
}

func TestFormNavDescriptions(t *testing.T) {
	cfg := Default()
	if cfg.Descriptions["form-down"] != "Move to the next attachment" ||
		cfg.Descriptions["form-up"] != "Move to the previous attachment" {
		t.Fatalf("form nav descs must describe the attachment list: %v", cfg.Descriptions)
	}
}

func TestKeymapFileOverlay(t *testing.T) {
	cfg, err := Load(write(t, `
[ui]
keymap = "emacs"
[schemes.emacs.index]
q = "open"
[schemes.emacs.pager]
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
	cfg.Schemes["vim"]["index"] = map[string]Binding{}
	if err := validate(cfg); err == nil {
		t.Fatal("empty context must error")
	}
	cfg.Schemes["vim"]["index"] = map[string]Binding{"": {Fun: "archive"}}
	if err := validate(cfg); err == nil {
		t.Fatal("blank key must error")
	}
	cfg.Schemes["vim"]["index"] = map[string]Binding{"x": {Fun: " "}}
	if err := validate(cfg); err == nil {
		t.Fatal("blank action must error")
	}
}

func TestValidateUnknownBindingContext(t *testing.T) {
	cfg := Default()
	cfg.Schemes["vim"]["indicx"] = map[string]Binding{"q": {Fun: "quit"}}
	if err := validate(cfg); err == nil || !strings.Contains(err.Error(), "indicx") {
		t.Fatalf("unknown context must error naming it, got %v", err)
	}
}

func TestLoadUnknownBindingContext(t *testing.T) {
	_, err := Load(write(t, "\n[schemes.vim.indicx]\nq = \"quit\"\n"))
	if err == nil {
		t.Fatal("unknown binding context must error")
	}
	if !strings.Contains(err.Error(), "indicx") {
		t.Fatalf("error must name the context, got: %v", err)
	}
}

// TestBindingEntryForms pins the three entry shapes (string, array,
// table) and the derived descriptions: the help vocabulary comes from
// the scheme entries - the selected scheme's descs first, the other
// schemes as fallback - and a stale [descriptions] block fails load.
func TestBindingEntryForms(t *testing.T) {
	cfg, err := Load(write(t, `
[schemes.vim.index]
"plain" = "open"
"arr" = ["quit", "Leave the program"]
"tab" = { fun = "archive", desc = "Peek" }

[schemes.emacs.index]
"ctrl+q" = ["quit", "Emacs quits differently"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Bindings["index"]["plain"] != "open" || cfg.Bindings["index"]["arr"] != "quit" || cfg.Bindings["index"]["tab"] != "archive" {
		t.Fatalf("all entry forms must bind their action: %v", cfg.Bindings["index"])
	}
	if e := cfg.Schemes["vim"]["index"]["arr"]; e.Fun != "quit" || e.Desc != "Leave the program" {
		t.Fatalf("array entry must carry fun and desc, got %+v", e)
	}
	if e := cfg.Schemes["vim"]["index"]["tab"]; e.Fun != "archive" || e.Desc != "Peek" {
		t.Fatalf("table entry must carry fun and desc, got %+v", e)
	}
	// the selected scheme's desc wins; other schemes only fill gaps
	if cfg.Descriptions["quit"] != "Leave the program" {
		t.Fatalf("vim desc must win over the emacs one, got %q", cfg.Descriptions["quit"])
	}
	if cfg.Descriptions["archive"] != "Peek" {
		t.Fatalf("desc must derive from the table entry, got %q", cfg.Descriptions["archive"])
	}
	if cfg.Descriptions["open"] != "Open the message under the cursor" {
		t.Fatalf("an action without its own desc inherits the scheme default: %q", cfg.Descriptions["open"])
	}
	// a scheme switch re-derives: the emacs desc wins under emacs
	cfg, err = Load(write(t, `
[ui]
keymap = "emacs"
[schemes.emacs.index]
"ctrl+q" = ["quit", "Emacs quits differently"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Descriptions["quit"] != "Emacs quits differently" {
		t.Fatalf("the selected keymap's desc must win, got %q", cfg.Descriptions["quit"])
	}
	if cfg.Descriptions["open"] != "Open the message under the cursor" {
		t.Fatalf("unset emacs descs inherit the vim scheme: %q", cfg.Descriptions["open"])
	}
}

// TestBindingShowFlag pins the show flag: the table form carries it,
// the derived Shown set follows the selected keymap, the binding stays
// in the dispatch surface and the help vocabulary, and the retired
// hidden flag is a load error (the flip is opt-in, old configs fail
// loudly).
func TestBindingShowFlag(t *testing.T) {
	cfg, err := Load(write(t, `
[schemes.vim.index]
"ctrl+d" = { fun = "half-page-down", desc = "Scroll down half a page", show = true }
"x" = { fun = "open", desc = "Not shown" }
`))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Shown["index"]["ctrl+d"] {
		t.Fatal("a show-marked key must land in the derived set")
	}
	if cfg.Shown["index"]["x"] {
		t.Fatal("a key without the flag must not be shown")
	}
	if cfg.Bindings["index"]["ctrl+d"] != "half-page-down" {
		t.Fatal("an unshown binding must stay in the dispatch surface")
	}
	if cfg.Descriptions["half-page-down"] == "" {
		t.Fatal("an unshown binding keeps its description")
	}
	if _, err := Load(write(t, `
[schemes.vim.index]
"ctrl+d" = { fun = "half-page-down", hidden = true }
`)); err == nil {
		t.Fatal("the retired hidden flag must be a load error")
	}
}

// TestDefaultShownSet pins the base schemes: the keyhint row shows only
// the command surface - the movement keys (j/k, arrows, ctrl+n/ctrl+p,
// enter), the paging keys and g g/G stay out of it in every context and
// both keymaps; the command keys (quit/back/send, ...) are shown.
func TestDefaultShownSet(t *testing.T) {
	cfg := Default()
	for _, ctx := range []string{"index", "pager", "compose", "fuzzy"} {
		for _, k := range []string{"j", "k", "up", "down", "ctrl+n", "ctrl+p", "ctrl+d", "pgdown"} {
			if cfg.Shown[ctx][k] {
				t.Fatalf("generic key %q must stay out of the vim %s hint", k, ctx)
			}
		}
	}
	for _, k := range []string{"enter", "g g", "G"} {
		if cfg.Shown["index"][k] {
			t.Fatalf("generic navigation key %q must stay out of the index hint", k)
		}
	}
	if !cfg.Shown["index"]["q"] || !cfg.Shown["pager"]["q"] || !cfg.Shown["compose"]["y"] || !cfg.Shown["fuzzy"]["enter"] {
		t.Fatal("the command keys must be shown")
	}
	_, emacsShown := bindingsFromScheme(Default().Schemes["emacs"])
	for _, ctx := range []string{"index", "pager", "compose", "fuzzy"} {
		for _, k := range []string{"j", "k", "up", "down", "ctrl+n", "ctrl+p", "ctrl+v", "pgdown"} {
			if emacsShown[ctx][k] {
				t.Fatalf("generic key %q must stay out of the emacs %s hint", k, ctx)
			}
		}
	}
	if !emacsShown["index"]["q"] || !emacsShown["compose"]["t"] {
		t.Fatal("the emacs command keys must be shown")
	}
}

func TestBindingEntryBadShapes(t *testing.T) {
	for _, file := range []string{
		`[schemes.vim.index]
"x" = ["only-fun"]`,
		`[schemes.vim.index]
"x" = { wrong = "key" }`,
	} {
		if _, err := Load(write(t, file)); err == nil {
			t.Fatalf("bad entry shape must error: %s", file)
		}
	}
}

func TestLoadStaleDescriptionsBlock(t *testing.T) {
	// the [descriptions] block is gone: descriptions live on the
	// binding entries, so a stale block must fail load loudly (strict,
	// R8) instead of silently no-oping
	if _, err := Load(write(t, "\n[descriptions]\n\"quit\" = \"Leave\"\n")); err == nil || !strings.Contains(err.Error(), "descriptions") {
		t.Fatalf("a stale [descriptions] table must error naming it, got %v", err)
	}
}

func TestDefaultTagActions(t *testing.T) {
	cfg := Default()
	// the tag actions come from the embedded base (base.toml), like
	// the schemes and descriptions
	if !maps.Equal(cfg.TagActions, baseConfig.TagActions) {
		t.Fatalf("default tag actions = %v, want the embedded base's %v", cfg.TagActions, baseConfig.TagActions)
	}
	if cfg.TagActions["toggle-read"] != "unread" {
		t.Fatalf("toggle-read must map to unread, got %q", cfg.TagActions["toggle-read"])
	}
}

func TestLoadStaleBindingsKey(t *testing.T) {
	// the [bindings] table is gone: schemes replaced it, so a stale
	// table must fail load loudly (strict, R8) instead of silently
	// no-oping a user's rebinding
	if _, err := Load(write(t, "\n[bindings.index]\nj = \"cursor-up\"\n")); err == nil || !strings.Contains(err.Error(), "bindings") {
		t.Fatalf("a stale [bindings] table must error naming it, got %v", err)
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
	write := func(name, body string) string {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return dir
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

// TestThemeComposeLabel pins the compose.label id: a [theme.dark.compose]
// section with a label style resolves through the palette to the id.
func TestThemeComposeLabel(t *testing.T) {
	cfg, err := Load(writeThemeFile(t, "[theme.dark]\n[theme.dark.compose]\nlabel = { fg = \"base0D\" }"))
	if err != nil {
		t.Fatal(err)
	}
	res := cfg.Theme.Resolved(cfg.Palette, "dark")
	if res["compose.label"].Fg != "#61afef" {
		t.Fatalf("compose.label fg = %q, want the resolved base0D (#61afef)", res["compose.label"].Fg)
	}
}

func TestThemeComposeUnknownKey(t *testing.T) {
	_, err := Load(writeThemeFile(t, "[theme.dark]\n[theme.dark.compose]\nnonesuch = { fg = \"base0D\" }"))
	if err == nil || !strings.Contains(err.Error(), "compose.nonesuch") {
		t.Fatalf("unknown compose key must be a load error, got %v", err)
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
[accounts.atlas]
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Accounts["atlas"].Tag("atlas") != "atlas" {
		t.Fatalf("account tag defaults to the name: %+v", cfg.Accounts["atlas"])
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
[accounts.atlas]
nonesuch = "x"
`))
	if err == nil || !strings.Contains(err.Error(), "nonesuch") {
		t.Fatalf("want unknown key error, got %v", err)
	}
}

func TestAccountBlankFolder(t *testing.T) {
	_, err := Load(writeThemeFile(t, `
[accounts.atlas]
folder = ""
`))
	if err == nil || !strings.Contains(err.Error(), "folder") {
		t.Fatalf("want blank folder error, got %v", err)
	}
}

func TestAccountFoldersStrictLoad(t *testing.T) {
	dir := t.TempDir()
	body := `
[accounts.gmail]
folder = "gmail"

[accounts.gmail.folders]
inbox = "INBOX"
deleted = "[Gmail]/Trash"
`
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOTMUTT_CONFIG", dir)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("the setup-generated accounts shape must load strictly: %v", err)
	}
	got := cfg.Accounts["gmail"].Folders
	if got["inbox"] != "INBOX" || got["deleted"] != "[Gmail]/Trash" {
		t.Fatalf("folders = %v, want the detected map", got)
	}
}

func TestFilterDefaults(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Filter.Enabled || !cfg.Filter.DryRun {
		t.Fatalf("filter defaults = enabled %v dry-run %v", cfg.Filter.Enabled, cfg.Filter.DryRun)
	}
}

func TestFilterHeaderRules(t *testing.T) {
	cfg, err := Load(write(t, `
[filter]
enabled = false
dry-run = false

[[filter.header-rules]]
query = "from:*@atlas.example"
add = ["work"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Filter.Enabled || cfg.Filter.DryRun {
		t.Fatalf("filter overrides ignored: %+v", cfg.Filter)
	}
	if len(cfg.Filter.HeaderRules) != 1 || cfg.Filter.HeaderRules[0].Query != "from:*@atlas.example" ||
		len(cfg.Filter.HeaderRules[0].Add) != 1 || cfg.Filter.HeaderRules[0].Add[0] != "work" {
		t.Fatalf("header-rules = %+v", cfg.Filter.HeaderRules)
	}
}

func TestFilterHeaderRuleEmptyQueryErrors(t *testing.T) {
	_, err := Load(write(t, `
[[filter.header-rules]]
query = ""
add = ["work"]
`))
	if err == nil || !strings.Contains(err.Error(), "filter.header-rules") {
		t.Fatalf("empty query must error, got: %v", err)
	}
}

func TestFilterHeaderRuleEmptyAddErrors(t *testing.T) {
	_, err := Load(write(t, `
[[filter.header-rules]]
query = "from:x"
add = []
`))
	if err == nil || !strings.Contains(err.Error(), "add") {
		t.Fatalf("empty add must error, got: %v", err)
	}
}

func TestAccountPreset(t *testing.T) {
	cfg, err := Load(write(t, `
[accounts.gmail]
folder = "gmail"
preset = "gmail"
`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Accounts["gmail"].Preset != "gmail" {
		t.Fatalf("preset = %q", cfg.Accounts["gmail"].Preset)
	}
	if len(Presets["gmail"]["deleted"]) != 3 {
		t.Fatalf("gmail deleted candidates = %v", Presets["gmail"]["deleted"])
	}
}

func TestAccountUnknownPresetErrors(t *testing.T) {
	_, err := Load(write(t, `
[accounts.gmail]
preset = "exchange"
`))
	if err == nil || !strings.Contains(err.Error(), "exchange") {
		t.Fatalf("unknown preset must error naming it, got: %v", err)
	}
}

func TestAccountMoves(t *testing.T) {
	cfg, err := Load(write(t, `
[accounts.gmail]
folder = "gmail"

[accounts.gmail.moves]
archive = ["Archives", "Archive"]
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Accounts["gmail"].Moves["archive"]) != 2 {
		t.Fatalf("moves = %v", cfg.Accounts["gmail"].Moves)
	}
}

func TestAccountMovesInvalid(t *testing.T) {
	_, err := Load(write(t, "[accounts.gmail]\nfolder = \"gmail\"\n[accounts.gmail.moves]\narchive = []\n"))
	if err == nil || !strings.Contains(err.Error(), "moves.archive") {
		t.Fatalf("empty candidates must error, got: %v", err)
	}
	_, err = Load(write(t, "[accounts.gmail]\nfolder = \"gmail\"\n[accounts.gmail.moves]\narchive = ['Archives\"']\n"))
	if err == nil || !strings.Contains(err.Error(), "moves.archive") {
		t.Fatalf("a quoted candidate must error, got: %v", err)
	}
}

func TestEnumTags(t *testing.T) {
	for _, tc := range []struct {
		typ    reflect.Type
		field  string
		values []string
	}{
		{reflect.TypeOf(Notify{}), "Backend", []string{"command", "beeep"}},
		{reflect.TypeOf(Style{}), "Attrs", []string{"bold", "italic", "underline", "reverse"}},
	} {
		if got := enumOf(tc.typ, tc.field); !slices.Equal(got, tc.values) {
			t.Fatalf("%s.%s enum = %v, want %v", tc.typ.Name(), tc.field, got, tc.values)
		}
	}
	if enumOf(reflect.TypeOf(UI{}), "Keymap") != nil {
		t.Fatal("keymap enum must stay data-derived (the schemes map)")
	}
}
