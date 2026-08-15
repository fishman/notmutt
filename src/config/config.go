package config

import (
	_ "embed"
	"fmt"
	"maps"
	"os"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"notmutt/core"
)

//go:embed base.toml
var baseTOML []byte

// baseConfig is the embedded base parsed once: the validation ground
// truth for schemes (a keymap and its contexts are code surfaces, R9 -
// the file overlays keys, never the schema).
var baseConfig = mustBase()

func mustBase() Config {
	var c Config
	if err := toml.Unmarshal(baseTOML, &c); err != nil {
		panic(err) // the embedded base is ours; corruption is a build error
	}
	return c
}

// bindingContexts is the dispatch surface (the tui switches on these
// names): a scheme table for any other context is dead data, rejected
// at load (strict, R8). Context names are code surfaces - the keys,
// actions, and descriptions are the translatable data.
var bindingContexts = map[string]bool{
	"index": true, "pager": true, "compose": true, "fuzzy": true,
}

type Config struct {
	UI             UI                                      `toml:"ui"`
	Views          map[string]View                         `toml:"view"`
	TagGroups      map[string]core.TagGroup                `toml:"tag-groups"`
	Bindings       map[string]map[string]string            `toml:"-"`
	TagActions     map[string]string                       `toml:"tag-actions"`
	Accounts       map[string]Account                      `toml:"accounts"`
	Send           Send                                    `toml:"send"`
	AttachCommands map[string][]string                     `toml:"attach-commands"`
	Palette        Palette                                 `toml:"palette"`
	Theme          Theme                                   `toml:"theme"`
	Schemes        map[string]map[string]map[string]string `toml:"schemes"`
	Descriptions   map[string]string                       `toml:"descriptions"`
}

type UI struct {
	Keymap string `toml:"keymap"`
	Tags   UITags `toml:"tags"`
	Glyphs Glyphs `toml:"glyphs"`
}

type UITags struct {
	Max       int               `toml:"max"`
	Attach    string            `toml:"attach"`     // the tag that marks attachments (renders in the row's attachment slot)
	ShowIcons bool              `toml:"show-icons"` // false renders tag names instead of icons (R11)
	Icons     map[string]string `toml:"icons"`      // tag name -> display icon (muttrc tag-transforms, R11)
}

// Glyphs are the config-data display glyphs (R11 tag-transforms rule);
// the raw strings never hardcode in code.
type Glyphs struct {
	Staged        string `toml:"staged"`
	ProgressFill  string `toml:"progress_fill"`
	ProgressEmpty string `toml:"progress_empty"`
	BorderTL      string `toml:"border_tl"`
	BorderTR      string `toml:"border_tr"`
	BorderBL      string `toml:"border_bl"`
	BorderBR      string `toml:"border_br"`
	BorderH       string `toml:"border_h"`
	BorderV       string `toml:"border_v"`
}

// Style is one theme style: palette names or raw hex for fg/bg, a
// fixed attr subset for attrs. Empty fields inherit from normal (R11).
type Style struct {
	Fg    string   `toml:"fg"`
	Bg    string   `toml:"bg"`
	Attrs []string `toml:"attrs"`
}

// Resolved resolves fg/bg through the palette: a raw hex stays, a
// palette name looks up the variant override first, then the base.
func (s Style) Resolved(p Palette, variant string) Style {
	if s.Fg != "" && !isHex(s.Fg) {
		s.Fg = p.Color(s.Fg, variant)
	}
	if s.Bg != "" && !isHex(s.Bg) {
		s.Bg = p.Color(s.Bg, variant)
	}
	return s
}

func isHex(s string) bool {
	if len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, c := range s[1:] {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

// Palette holds named colors: the base table plus per-variant
// overrides that replace single names without touching styles (R11).
// Resolution order: style hex > variant palette > base palette.
type Palette struct {
	Base     map[string]string
	Variants map[string]map[string]string
}

func (p *Palette) UnmarshalTOML(v interface{}) error {
	raw, ok := v.(map[string]interface{})
	if !ok {
		return fmt.Errorf("palette: expected a table")
	}
	// Load merges file values over defaults (R8): colors and variants
	// the file does not name survive.
	if p.Base == nil {
		p.Base = map[string]string{}
	}
	if p.Variants == nil {
		p.Variants = map[string]map[string]string{}
	}
	for name, val := range raw {
		switch tv := val.(type) {
		case string:
			p.Base[name] = tv
		case map[string]interface{}:
			m := p.Variants[name]
			if m == nil {
				m = map[string]string{}
			}
			for k, c := range tv {
				s, ok := c.(string)
				if !ok {
					return fmt.Errorf("palette.%s.%s: expected a color string", name, k)
				}
				m[k] = s
			}
			p.Variants[name] = m
		default:
			return fmt.Errorf("palette.%s: expected a color string or a variant table", name)
		}
	}
	return nil
}

// Color resolves a palette name: the variant override first, then the
// base table.
func (p Palette) Color(name, variant string) string {
	if v, ok := p.Variants[variant][name]; ok {
		return v
	}
	return p.Base[name]
}

// StyleTable is one variant's full style surface. Style identifiers
// mirror the mutt color objects (R11): the same names the TUI renders
// with.
type StyleTable struct {
	Normal    Style
	Indicator Style
	Status    Style
	View      Style // statusline view pill
	Count     Style // statusline count pill
	Account   Style // statusline account pill (R2)
	Progress  Style
	Error     Style
	Index     IndexStyleTable
	Pager     PagerStyleTable
}

type IndexStyleTable struct {
	Number  Style
	Date    Style
	Author  Style
	Subject Style
	Flags   Style
	Staged  Style
	Ghost   Style
	Tag     TagStyleTable
}

// TagStyleTable: the spec shape is mixed - fg/bg/attrs at the tag
// table's own level are the DEFAULT tag glyph style, other keys are
// per-tag overrides:
//
//	[theme.dark.index.tag]        # default tag glyph style
//	fg = "base0E"
//	[theme.dark.index.tag.inbox]  # per-tag override
//	fg = "base0B"
type TagStyleTable struct {
	Default Style
	Tags    map[string]Style
}

type PagerStyleTable struct {
	Header     Style
	HdrDefault Style
	Quoted     [6]Style
	Signature  Style
	Attachment Style
}

type Theme struct {
	Default  string
	Variants map[string]StyleTable
}

// rawStyle decodes a style table {fg, bg, attrs} from a decoded TOML
// table (map). Every key is checked - strict load.
func rawStyle(v interface{}) (Style, error) {
	raw, ok := v.(map[string]interface{})
	if !ok {
		return Style{}, fmt.Errorf("expected a style table")
	}
	s := Style{}
	for k, val := range raw {
		switch k {
		case "fg":
			str, ok := val.(string)
			if !ok {
				return Style{}, fmt.Errorf("fg: expected a string")
			}
			s.Fg = str
		case "bg":
			str, ok := val.(string)
			if !ok {
				return Style{}, fmt.Errorf("bg: expected a string")
			}
			s.Bg = str
		case "attrs":
			arr, ok := val.([]interface{})
			if !ok {
				return Style{}, fmt.Errorf("attrs: expected an array")
			}
			for _, a := range arr {
				str, ok := a.(string)
				if !ok {
					return Style{}, fmt.Errorf("attrs: expected strings")
				}
				s.Attrs = append(s.Attrs, str)
			}
		default:
			return Style{}, fmt.Errorf("unknown key %q", k)
		}
	}
	return s, nil
}

// rawStyleTable decodes a full style table (normal/status/index/...)
// as an overlay over base: Load merges file values over defaults (R8),
// so a file naming one style in a variant keeps the variant's other
// styles. Every style key merges individually - a [theme.dark.index]
// naming only subject keeps the variant's number/date/... - and
// unknown keys are load errors.
func rawStyleTable(v interface{}, base StyleTable) (StyleTable, error) {
	raw, ok := v.(map[string]interface{})
	if !ok {
		return StyleTable{}, fmt.Errorf("expected a style table")
	}
	t := base
	for k, val := range raw {
		switch k {
		case "normal", "indicator", "status", "progress", "error":
			s, err := rawStyle(val)
			if err != nil {
				return StyleTable{}, err
			}
			switch k {
			case "normal":
				t.Normal = s
			case "indicator":
				t.Indicator = s
			case "status":
				t.Status = s
			case "progress":
				t.Progress = s
			case "error":
				t.Error = s
			}
		case "index":
			im, ok := val.(map[string]interface{})
			if !ok {
				return StyleTable{}, fmt.Errorf("index: expected a table")
			}
			for ik, iv := range im {
				if ik == "tag" {
					tm, ok := iv.(map[string]interface{})
					if !ok {
						return StyleTable{}, fmt.Errorf("index.tag: expected a table")
					}
					// mixed shape: fg/bg/attrs strings at this level are the
					// default glyph style; tables are per-tag overrides
					for tn, tv := range tm {
						if s, ok := tv.(string); ok {
							switch tn {
							case "fg":
								t.Index.Tag.Default.Fg = s
							case "bg":
								t.Index.Tag.Default.Bg = s
							default:
								return StyleTable{}, fmt.Errorf("index.tag.%s: expected a style table", tn)
							}
							continue
						}
						if tn == "attrs" {
							arr, ok := tv.([]interface{})
							if !ok {
								return StyleTable{}, fmt.Errorf("index.tag.attrs: expected an array")
							}
							for _, a := range arr {
								str, ok := a.(string)
								if !ok {
									return StyleTable{}, fmt.Errorf("index.tag.attrs: expected strings")
								}
								t.Index.Tag.Default.Attrs = append(t.Index.Tag.Default.Attrs, str)
							}
							continue
						}
						style, err := rawStyle(tv)
						if err != nil {
							return StyleTable{}, err
						}
						if t.Index.Tag.Tags == nil {
							t.Index.Tag.Tags = map[string]Style{}
						}
						t.Index.Tag.Tags[tn] = style
					}
					continue
				}
				style, err := rawStyle(iv)
				if err != nil {
					return StyleTable{}, err
				}
				switch ik {
				case "number":
					t.Index.Number = style
				case "date":
					t.Index.Date = style
				case "author":
					t.Index.Author = style
				case "subject":
					t.Index.Subject = style
				case "flags":
					t.Index.Flags = style
				case "staged":
					t.Index.Staged = style
				case "ghost":
					t.Index.Ghost = style
				default:
					return StyleTable{}, fmt.Errorf("index: unknown key %q", ik)
				}
			}
		case "pager":
			pm, ok := val.(map[string]interface{})
			if !ok {
				return StyleTable{}, fmt.Errorf("pager: expected a table")
			}
			for pk, pv := range pm {
				style, err := rawStyle(pv)
				if err != nil {
					return StyleTable{}, err
				}
				switch pk {
				case "header":
					t.Pager.Header = style
				case "hdrdefault":
					t.Pager.HdrDefault = style
				case "quoted0", "quoted1", "quoted2", "quoted3", "quoted4", "quoted5":
					t.Pager.Quoted[pk[6]-'0'] = style
				case "signature":
					t.Pager.Signature = style
				case "attachment":
					t.Pager.Attachment = style
				default:
					return StyleTable{}, fmt.Errorf("pager: unknown key %q", pk)
				}
			}
		default:
			return StyleTable{}, fmt.Errorf("unknown key %q", k)
		}
	}
	return t, nil
}

func (t *Theme) UnmarshalTOML(v interface{}) error {
	raw, ok := v.(map[string]interface{})
	if !ok {
		return fmt.Errorf("theme: expected a table")
	}
	// Load merges file values over defaults (R8): a named variant is an
	// overlay over the existing one (defaults included), variants the
	// file does not name survive untouched.
	variants := t.Variants
	if variants == nil {
		variants = map[string]StyleTable{}
	}
	for name, val := range raw {
		if name == "default" {
			s, ok := val.(string)
			if !ok {
				return fmt.Errorf("theme.default: expected a string")
			}
			t.Default = s
			continue
		}
		table, err := rawStyleTable(val, variants[name])
		if err != nil {
			return fmt.Errorf("theme.%s: %w", name, err)
		}
		variants[name] = table
	}
	t.Variants = variants
	return nil
}

// Resolved returns the variant's styles with normal-inheritance and
// palette resolution applied, keyed by identifier (normal, indicator,
// status, progress, error, index.number, index.tag.<name>, ...).
func (t Theme) Resolved(p Palette, variant string) map[string]Style {
	table, ok := t.Variants[variant]
	if !ok {
		table = StyleTable{}
	}
	out := map[string]Style{}
	normal := table.Normal.Resolved(p, variant)
	apply := func(id string, s Style) Style {
		if s.Fg == "" {
			s.Fg = normal.Fg
		} else {
			s.Fg = s.Resolved(p, variant).Fg
		}
		if s.Bg == "" {
			s.Bg = normal.Bg
		} else {
			s.Bg = s.Resolved(p, variant).Bg
		}
		if len(s.Attrs) == 0 {
			s.Attrs = append([]string(nil), normal.Attrs...)
		}
		return s
	}
	out["normal"] = normal
	out["indicator"] = apply("indicator", table.Indicator)
	out["status"] = apply("status", table.Status)
	out["status.view"] = apply("status.view", table.View)
	out["status.count"] = apply("status.count", table.Count)
	out["status.account"] = apply("status.account", table.Account)
	out["progress"] = apply("progress", table.Progress)
	out["error"] = apply("error", table.Error)
	for id, s := range map[string]Style{
		"index.number": table.Index.Number, "index.date": table.Index.Date,
		"index.author": table.Index.Author, "index.subject": table.Index.Subject,
		"index.flags": table.Index.Flags, "index.staged": table.Index.Staged,
		"index.ghost": table.Index.Ghost,
	} {
		out[id] = apply(id, s)
	}
	out["index.tag"] = apply("index.tag", table.Index.Tag.Default)
	for name, s := range table.Index.Tag.Tags {
		out["index.tag."+name] = apply("index.tag."+name, s)
	}
	out["pager.header"] = apply("pager.header", table.Pager.Header)
	out["pager.hdrdefault"] = apply("pager.hdrdefault", table.Pager.HdrDefault)
	for i := 0; i < 6; i++ {
		out[fmt.Sprintf("pager.quoted%d", i)] = apply(fmt.Sprintf("pager.quoted%d", i), table.Pager.Quoted[i])
	}
	out["pager.signature"] = apply("pager.signature", table.Pager.Signature)
	out["pager.attachment"] = apply("pager.attachment", table.Pager.Attachment)
	return out
}

type View struct {
	Query   string `toml:"query"`
	Threads bool   `toml:"threads"`
}

// Send is the send transport argv (R4): ONE configurable command,
// tokenized at load, exec'd as argv (F4). The default reads the
// envelope sender from the message's own From header, so msmtp's
// account table resolves per message - the client never sees it.
type Send struct {
	Command string   `toml:"command"`
	Args    []string `toml:"args"`
}

// Account is one mail account: the section key is the account name, the
// folder prefix is its folder space in the maildir (R2). The account
// tag in notmuch is the folder prefix - the muttrc folder:/^<folder>\//
// pattern as data. Folder defaults to the account name (the common
// case: [accounts.dynamia] maps to the "dynamia" tag directly); the
// pointer distinguishes unset from an explicitly empty value, which is
// a load error.
type Account struct {
	Folder           *string `toml:"folder"`
	From             string  `toml:"from"`
	SentFolder       string  `toml:"sent_folder"`
	DefaultSignature string  `toml:"default_signature"`
}

func (a Account) Tag(name string) string {
	if a.Folder != nil {
		return *a.Folder
	}
	return name
}

// AccountTags derives the account tag set: one tag per account (the
// folder prefix, R2). A message's account is the account tag it
// carries; the row render skips these tags and the status bar resolves
// against the set.
func (c Config) AccountTags() map[string]bool {
	set := make(map[string]bool, len(c.Accounts))
	for name, a := range c.Accounts {
		set[a.Tag(name)] = true
	}
	return set
}

// mergeSchemes overlays file scheme tables over the embedded base per
// key. BurntSushi merges only the top-level map field; a
// [schemes.vim.index] table replaces the whole context table when
// decoded into the nested map, so the R9 file overlay is applied
// explicitly here (context and key levels merge).
func mergeSchemes(base, over map[string]map[string]map[string]string) map[string]map[string]map[string]string {
	out := make(map[string]map[string]map[string]string, len(base))
	for km, ctxs := range base {
		out[km] = make(map[string]map[string]string, len(ctxs))
		for c, keys := range ctxs {
			out[km][c] = maps.Clone(keys)
		}
	}
	for km, ctxs := range over {
		if out[km] == nil {
			out[km] = make(map[string]map[string]string)
		}
		for c, keys := range ctxs {
			if out[km][c] == nil {
				out[km][c] = make(map[string]string)
			}
			maps.Copy(out[km][c], keys)
		}
	}
	return out
}

// cloneBindings deep-copies a binding table set: the resolved scheme
// (Default and Load) hands out a fresh map so a caller's rebind never
// touches the store or the next Default.
func cloneBindings(b map[string]map[string]string) map[string]map[string]string {
	out := make(map[string]map[string]string, len(b))
	for ctx, km := range b {
		out[ctx] = maps.Clone(km)
	}
	return out
}

func Default() Config {
	cfg := Config{
		UI: UI{
			Keymap: "vim",
			Tags: UITags{
				Max:       2,
				Attach:    "attachment",
				ShowIcons: true,
				Icons: map[string]string{
					"attachment": "📎", "archive": "📦", "deleted": "🗑",
					"draft": "✏️", "sent": "📤", "spam": "🚫", "pending": "⏰", "inbox": "📥",
					"unread": "✉", "xolo": "💼", "work": "🏢",
					"receipt": "🧾", "important": "⭐", "todo": "✅",
					"later": "⏳", "personal": "👤", "cfp": "🎤",
					"conference": "🎫", "exhibition": "🏛", "flagged": "🚩",
					"signed": "🔒", "meeting": "📅", "newsletter": "📰",
				},
			},
			Glyphs: Glyphs{
				Staged: "*", ProgressFill: "#", ProgressEmpty: "-",
				BorderTL: "╭", BorderTR: "╮", BorderBL: "╰", BorderBR: "╯",
				BorderH: "─", BorderV: "│",
			},
		},
		Views: map[string]View{
			"inbox": {Query: "tag:inbox", Threads: true},
		},
		TagGroups: map[string]core.TagGroup{
			"folder": {Tags: []string{"inbox", "archive", "deleted", "sent", "draft", "pending", "spam"}},
		},
		// the reference mail setup (muttrc): one account per maildir
		// root, each mapping to its folder tag by name
		Accounts: map[string]Account{
			"gmail": {}, "jelveh": {}, "toptal": {}, "dynamia": {},
		},
		Send: Send{
			Command: "msmtp",
			Args:    []string{"--read-envelope-from"},
		},
		Palette: defaultPalette(),
		Theme:   defaultTheme(),
	}
	// the embedded base (base.toml) overlays the Go defaults: the
	// binding schemes, the tag actions, and the help descriptions are
	// user data, ready for translation. Bindings is the derived view -
	// the selected keymap's scheme, cloned per caller.
	if err := toml.Unmarshal(baseTOML, &cfg); err != nil {
		panic(err)
	}
	cfg.Bindings = cloneBindings(cfg.Schemes["vim"])
	if err := validate(cfg); err != nil {
		panic(err)
	}
	return cfg
}

// defaultPalette is the one-dark base16 palette the reference theme
// resolves through (muttrc/themes/palette, R11).
func defaultPalette() Palette {
	return Palette{
		Base: map[string]string{
			"base00": "#21252b", "base01": "#3e4451", "base02": "#353b45",
			"base03": "#5c6370", "base04": "#565c64", "base05": "#abb2bf",
			"base06": "#b6bdc9", "base07": "#c8ccd4", "base08": "#e06c75",
			"base09": "#d19a66", "base0A": "#e5c07b", "base0B": "#98c379",
			"base0C": "#56b6c2", "base0D": "#61afef", "base0E": "#c678dd",
			"base0F": "#be5046",
		},
		Variants: map[string]map[string]string{},
	}
}

// defaultTheme is the reference port of muttrc/theme/onedark.muttrc as
// theme data (R11, spec section 3): styles reference palette names,
// inheritance fills the rest.
func defaultTheme() Theme {
	return Theme{
		Default: "dark",
		Variants: map[string]StyleTable{
			"dark": {
				// the onedark port (muttrc/theme/onedark.muttrc): bg
				// base00, fg base05, the status bar on base01, the
				// statusline pills base-on-accent (mutt's progress
				// pattern)
				Normal:    Style{Fg: "base05", Bg: "base00"},
				Indicator: Style{Fg: "base00", Bg: "base0A"},
				Status:    Style{Fg: "base05", Bg: "base01"},
				View:      Style{Fg: "base00", Bg: "base0B"},
				Count:     Style{Fg: "base00", Bg: "base0A"},
				Account:   Style{Fg: "base00", Bg: "base0D"},
				Progress:  Style{Fg: "base00", Bg: "base0D"},
				Index: IndexStyleTable{
					Number: Style{Fg: "base03"}, Date: Style{Fg: "base0A"},
					Author: Style{Fg: "base0D"}, Subject: Style{Fg: "base05"},
					Flags: Style{Fg: "base08"}, Staged: Style{Fg: "base04", Attrs: []string{"bold"}},
					Ghost: Style{Fg: "base03"},
					Tag: TagStyleTable{
						// the base.colors tag markers (muttrc/base.colors):
						// a color per hard tag; inbox stays plain green -
						// red is the deleted marker, not the inbox one
						Default: Style{Fg: "base0B"},
						Tags: map[string]Style{
							"deleted": {Fg: "base08"},
							"archive": {Fg: "base0B"},
							"spam":    {Fg: "base0A"},
							"pending": {Fg: "base0E"},
							"inbox":   {Fg: "base0B"},
							"unread":  {Fg: "base0B"},
						},
					},
				},
				Pager: PagerStyleTable{
					Header: Style{Fg: "base0D"}, HdrDefault: Style{Fg: "base05"},
					Quoted: [6]Style{
						{Fg: "base0B"}, {Fg: "base0C"}, {Fg: "base0D"},
						{Fg: "base0E"}, {Fg: "base0A"}, {Fg: "base08"},
					},
					Signature: Style{Fg: "base03"}, Attachment: Style{Fg: "base0E"},
				},
			},
		},
	}
}

// TagGroupList returns the groups sorted by name - the deterministic
// order the resolver consumes (map iteration is not).
func (c Config) TagGroupList() []core.TagGroup {
	names := make([]string, 0, len(c.TagGroups))
	for n := range c.TagGroups {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]core.TagGroup, 0, len(names))
	for _, n := range names {
		out = append(out, c.TagGroups[n])
	}
	return out
}

// Load merges file values over defaults. Unknown keys are load errors
// naming the key (strict load, R8). A missing file means defaults.
// The file's [schemes.*] tables overlay the embedded base per key
// (mergeSchemes; BurntSushi replaces whole context tables in nested
// maps), so a rebinding names the scheme, context, and key it touches.
func Load(path string) (Config, error) {
	cfg := Default()
	md, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, 0, len(und))
		for _, k := range und {
			// palette/theme decode through custom unmarshalers that
			// consume their whole subtree; BurntSushi does not mark
			// Unmarshaler subtrees as decoded, so their inner keys
			// always land here. The unmarshalers are strict themselves.
			if k[0] == "palette" || k[0] == "theme" {
				continue
			}
			keys = append(keys, k.String())
		}
		if len(keys) > 0 {
			return cfg, fmt.Errorf("%s: unknown key(s): %s", path, strings.Join(keys, ", "))
		}
	}
	cfg.Schemes = mergeSchemes(baseConfig.Schemes, cfg.Schemes)
	cfg.Bindings = cloneBindings(cfg.Schemes[cfg.UI.Keymap])
	if err := validate(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func validate(cfg Config) error {
	if _, ok := baseConfig.Schemes[cfg.UI.Keymap]; !ok {
		return fmt.Errorf("keymap: must be vim or emacs, got %q", cfg.UI.Keymap)
	}
	if cfg.UI.Tags.Max < 1 {
		return fmt.Errorf("ui.tags.max: must be >= 1, got %d", cfg.UI.Tags.Max)
	}
	for name, argv := range cfg.AttachCommands {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("attach-commands: empty command name")
		}
		if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
			return fmt.Errorf("attach-commands.%s: argv must not be empty", name)
		}
	}
	if len(cfg.Views) == 0 {
		return fmt.Errorf("at least one view required")
	}
	for name, v := range cfg.Views {
		if strings.TrimSpace(v.Query) == "" {
			return fmt.Errorf("view %q: query must not be empty", name)
		}
	}
	seen := map[string]bool{}
	for name, g := range cfg.TagGroups {
		if len(g.Tags) == 0 {
			return fmt.Errorf("tag-groups.%s: at least one tag required", name)
		}
		for _, t := range g.Tags {
			if strings.TrimSpace(t) == "" {
				return fmt.Errorf("tag-groups.%s: empty tag name", name)
			}
			if seen[t] {
				return fmt.Errorf("tag %q in multiple groups", t)
			}
			seen[t] = true
		}
	}
	for keymap, contexts := range cfg.Schemes {
		for name, km := range contexts {
			if !bindingContexts[name] {
				return fmt.Errorf("schemes.%s.%s: unknown context %q", keymap, name, name)
			}
			if len(km) == 0 {
				return fmt.Errorf("schemes.%s.%s: at least one binding required", keymap, name)
			}
			for k, v := range km {
				if strings.TrimSpace(k) == "" {
					return fmt.Errorf("schemes.%s.%s: empty key", keymap, name)
				}
				if strings.TrimSpace(v) == "" {
					return fmt.Errorf("schemes.%s.%s: empty action for key %q", keymap, name, k)
				}
			}
		}
	}
	for name, tag := range cfg.TagActions {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("tag-actions: empty action name")
		}
		if strings.TrimSpace(tag) == "" {
			return fmt.Errorf("tag-actions.%s: empty tag value", name)
		}
	}
	for name, color := range cfg.Palette.Base {
		if !isHex(color) {
			return fmt.Errorf("palette.%s: bad color %q", name, color)
		}
	}
	for variant, colors := range cfg.Palette.Variants {
		for name, color := range colors {
			if !isHex(color) {
				return fmt.Errorf("palette.%s.%s: bad color %q", variant, name, color)
			}
		}
	}
	for variant, table := range cfg.Theme.Variants {
		if err := validateStyleTable(table, cfg.Palette); err != nil {
			return fmt.Errorf("theme.%s: %w", variant, err)
		}
	}
	if _, ok := cfg.Theme.Variants[cfg.Theme.Default]; !ok {
		return fmt.Errorf("theme.default: no variant %q", cfg.Theme.Default)
	}
	g := cfg.UI.Glyphs
	if g.Staged == "" || g.ProgressFill == "" || g.ProgressEmpty == "" ||
		g.BorderTL == "" || g.BorderTR == "" || g.BorderBL == "" || g.BorderBR == "" || g.BorderH == "" || g.BorderV == "" {
		return fmt.Errorf("ui.glyphs: no glyph may be empty")
	}
	if strings.TrimSpace(cfg.Send.Command) == "" {
		return fmt.Errorf("send.command: must not be empty")
	}
	for name, a := range cfg.Accounts {
		if a.Folder != nil && strings.TrimSpace(*a.Folder) == "" {
			return fmt.Errorf("accounts.%s: folder must not be blank", name)
		}
	}
	return nil
}

// validateStyleTable checks every style in the table: fg/bg are hex or
// base palette names (the error names the bad reference), attrs come
// from a fixed subset.
func validateStyleTable(t StyleTable, p Palette) error {
	styles := []struct {
		path  string
		style Style
	}{
		{"normal", t.Normal}, {"indicator", t.Indicator},
		{"status", t.Status}, {"progress", t.Progress}, {"error", t.Error},
		{"index.number", t.Index.Number}, {"index.date", t.Index.Date},
		{"index.author", t.Index.Author}, {"index.subject", t.Index.Subject},
		{"index.flags", t.Index.Flags}, {"index.staged", t.Index.Staged},
		{"index.ghost", t.Index.Ghost}, {"index.tag", t.Index.Tag.Default},
		{"pager.header", t.Pager.Header}, {"pager.hdrdefault", t.Pager.HdrDefault},
		{"pager.signature", t.Pager.Signature}, {"pager.attachment", t.Pager.Attachment},
	}
	for i := 0; i < 6; i++ {
		styles = append(styles, struct {
			path  string
			style Style
		}{fmt.Sprintf("pager.quoted%d", i), t.Pager.Quoted[i]})
	}
	for _, s := range styles {
		if err := validateStyle(s.style, p, s.path); err != nil {
			return err
		}
	}
	for name, s := range t.Index.Tag.Tags {
		if err := validateStyle(s, p, "index.tag."+name); err != nil {
			return err
		}
	}
	return nil
}

func validateStyle(s Style, p Palette, path string) error {
	for _, v := range []struct{ kind, val string }{{"fg", s.Fg}, {"bg", s.Bg}} {
		if v.val == "" || isHex(v.val) {
			continue
		}
		if _, ok := p.Base[v.val]; !ok {
			return fmt.Errorf("%s.%s: unknown palette color %q", path, v.kind, v.val)
		}
	}
	for _, a := range s.Attrs {
		switch a {
		case "bold", "italic", "underline", "reverse":
		default:
			return fmt.Errorf("%s.attrs: unknown attr %q", path, a)
		}
	}
	return nil
}
