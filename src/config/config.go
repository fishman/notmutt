// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package config

import (
	_ "embed"
	"fmt"
	"maps"
	"net/mail"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"golang.org/x/text/language"

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

// Binding is one keybinding entry: a plain string (the action), a
// two-element array ["action", "description"], or a table
// { fun = "...", desc = "...", show = true }. The description travels
// with the binding - there is no separate descriptions block; the
// help vocabulary derives from these entries (R8). Visibility is
// opt-in: every binding is hidden from the keyhint row by default,
// only entries marked show = true appear there (the help dialog lists
// every binding regardless).
type Binding struct {
	Fun  string
	Desc string
	Show bool
}

func (b *Binding) UnmarshalTOML(v any) error {
	switch t := v.(type) {
	case string:
		b.Fun = t
	case []any:
		if len(t) != 2 {
			return fmt.Errorf("binding: array must be [fun, desc], got %d elements", len(t))
		}
		fun, ok := t[0].(string)
		if !ok {
			return fmt.Errorf("binding: array fun must be a string")
		}
		desc, ok := t[1].(string)
		if !ok {
			return fmt.Errorf("binding: array desc must be a string")
		}
		b.Fun, b.Desc = fun, desc
	case map[string]any:
		fun, ok := t["fun"].(string)
		if !ok {
			return fmt.Errorf("binding: table must carry a string fun")
		}
		b.Fun = fun
		if desc, ok := t["desc"].(string); ok {
			b.Desc = desc
		}
		if show, ok := t["show"].(bool); ok {
			b.Show = show
		}
		for k := range t {
			if k != "fun" && k != "desc" && k != "show" {
				return fmt.Errorf("binding: unknown key %q", k)
			}
		}
	default:
		return fmt.Errorf("binding: expected a string, [fun, desc], or { fun = ..., desc = ... }")
	}
	return nil
}

type Config struct {
	UI         UI              `toml:"ui"`
	Views      map[string]View `toml:"view"`
	ActiveView string          `toml:"-"`
	// Index is the [index] section: the index surface budgets (R11).
	Index IndexSection `toml:"index"`
	// DerivedGKeys tracks the per-account goto keys deriveAccountViews
	// added (key -> tag): the next derivation run removes them first,
	// so re-numbering over the merged accounts never collides with
	// stale Default-time entries (toml:"-": session state, not config)
	DerivedGKeys map[string]string            `toml:"-"`
	TagGroups    map[string]core.TagGroup     `toml:"tag-groups"`
	Setup        Setup                        `toml:"setup"`
	Lua          Lua                          `toml:"lua"`
	Bindings     map[string]map[string]string `toml:"-"`
	// Shown is the per-context key set the keyhint row shows (the
	// help dialog shows every binding): derived from the scheme
	// entries' show flag - visibility is opt-in, never a config block
	Shown          map[string]map[string]bool `toml:"-"`
	TagActions     map[string]string          `toml:"tag-actions"`
	Accounts       map[string]Account         `toml:"accounts"`
	Send           Send                       `toml:"send"`
	Refresh        Refresh                    `toml:"refresh"`
	Filter         Filter                     `toml:"filter"`
	Notify         Notify                     `toml:"notify"`
	Attachments    Attachments                `toml:"attachments"`
	MCP            MCP                        `toml:"mcp"`
	AttachCommands map[string][]string        `toml:"attach-commands"`
	AI             map[string]AIProvider      `toml:"ai"`
	// Opener is the link opener argv (the pager F key): the url is
	// appended as the final argv element (F4 - argv only, never
	// shell-interpolated). Empty = xdg-open.
	Opener  []string                                 `toml:"opener"`
	Pager   Pager                                    `toml:"pager"`
	Palette Palette                                  `toml:"palette"`
	Theme   Theme                                    `toml:"theme"`
	Schemes map[string]map[string]map[string]Binding `toml:"schemes"`
	// Descriptions is the derived help vocabulary (action -> text),
	// collected from the scheme entries - never a config block
	Descriptions map[string]string `toml:"-"`
}

// Setup configures the `notmutt setup` subcommand: Templates names the
// OPT-IN contributed detection templates in <configdir>/lua/templates
// that load (not all templates are autoloaded - the seeded examples
// stay inert until listed). Empty = built-in templates only.
type Setup struct {
	Templates []string `toml:"templates"`
}

// Pager is the [pager] table: per-sender-domain default views. The
// open key resolves the thread's sender domain against DefaultViews
// and opens in the mapped view; unmapped domains keep the plain
// default. The v toggle and the F/ctrl+u keys always request explicit
// views, never the map. ImageProtocol picks the terminal image
// protocol: sixel (the default - most terminals support it) or kitty
// (opt-in - kitty/wezterm/ghostty and friends). AllowTrackingImages
// lifts the 1x1 tracking-pixel block on fetched remote images.
type Pager struct {
	DefaultViews        map[string]string `toml:"default-views"`
	ImageProtocol       string            `toml:"image-protocol"`
	AllowTrackingImages bool              `toml:"allow-tracking-images"`
}

// Lua configures the Lua plugin layer (R8): Tags is the config-level
// tag list the plugins reference (cfg.tags) - the ai-tags example
// restricts its proposals to these names.
type Lua struct {
	Tags    []string              `toml:"tags"`
	Network map[string]LuaNetwork `toml:"network"`
}

// LuaNetwork is one plugin's network gate ([lua.network.<plugin>], key
// = plugin file base name). Network is deny-by-default: the sandbox
// http module exists for a plugin only when this section does, and
// every request (redirect hops included) must match Targets - exact
// hosts or "*.suffix" - AND one Paths rule. A Paths entry is a verb
// plus a path glob ("METHOD /path", case-insensitive verb): "GET
// /crm/v3/objects/contacts*" allows that GET (prefix match), nothing
// else. A verb without its path is meaningless, so the two are one
// rule unit; empty Paths = no request ever matches.
type LuaNetwork struct {
	Targets []string `toml:"targets"`
	Paths   []string `toml:"paths"`
}

// AIProvider is one named AI backend ([ai.<name>], R8): Type selects
// the wire protocol - "anthropic" (api.anthropic.com/v1/messages) or
// "openai" (any OpenAI-compatible /chat/completions endpoint via
// BaseURL - ollama, llama.cpp, groq, ...). PassCmd is the argv that
// prints the API key on stdout (F4: tokenized at load, never a shell
// string); empty = no auth header. The key is fetched per request,
// held only for that request, never logged (F6).
type AIProvider struct {
	Type      string   `toml:"type"`
	Model     string   `toml:"model"`
	MaxTokens int      `toml:"max-tokens"` // anthropic requires it; default 1024
	BaseURL   string   `toml:"base-url"`   // empty = provider default
	Timeout   int      `toml:"timeout"`    // seconds, streaming budget; default 180
	PassCmd   []string `toml:"pass_cmd"`
}

type UI struct {
	Keymap string `toml:"keymap"`
	// Language selects the interface language: "auto" resolves from
	// LANG/LC_MESSAGES at startup, or a BCP 47 tag pins one ("de").
	Language string `toml:"language"`
	Tags     UITags `toml:"tags"`
	// GlyphSet selects the thread tree glyph set: "ascii" (default) or
	// "utf-8" (box-drawing). Per-glyph [ui.glyphs] tree keys override
	// the preset.
	GlyphSet string `toml:"glyph-set"`
	Glyphs   Glyphs `toml:"glyphs"`
	// SearchOpen is how the ctrl+f search tab activates: "active" (the
	// default) attaches the new tab and shows its results, "background"
	// runs the query while the current surface stays (the tab bar shows
	// it, the [ / ] keys cycle to it).
	SearchOpen string `toml:"search-open"`
}

// Refresh is the [refresh] section: the periodic new-mail poll (R2/R3).
// The poll runs `notmuch new` and refreshes the view at the cadence
// (default 20 min - the refresh key checks manually in between).
type Refresh struct {
	// Interval is the poll cadence in minutes (default 20; 0 disables
	// the automatic poll - the refresh key still works).
	Interval int `toml:"interval"`
}

// Filter configures the classification pipeline (R2): Enabled turns the
// post-new engine on, DryRun reports what-would-change without writing
// (the first runs against a real mailbox are always dry), HeaderRules
// are the content-based soft-tag rules - the engine evaluates each
// rule's query over the delta and enforces the NOT guards itself
// (muttrc/notmuch/post-new as data).
type Filter struct {
	Enabled     bool         `toml:"enabled"`
	DryRun      bool         `toml:"dry-run"`
	HeaderRules []HeaderRule `toml:"header-rules"`
}

// Notify configures the new-mail notification side effect (R2): the
// argv command backend or the platform backend ("beeep"). The backend
// is auto-detected when empty (the default): the platform backend
// when the session can show notifications, the command otherwise -
// explicit config always wins. {count} in the argv is the processed
// entry count, {subjects} the summary as aligned sender/subject/time
// rows (priority entries first, the batch filling the cap at max).
// No command = disabled; the beeep title is the deduped sender list,
// its body the count plus the same rows. The payload never carries
// bodies or ids (F6).
type Notify struct {
	Backend  string   `toml:"backend" enum:"command,beeep"` // empty = auto-detect
	Command  []string `toml:"command"`
	Priority []string `toml:"priority"`
	Max      int      `toml:"max"`
}

// Attachments configures the local attachment download pass
// ([attachments]): the destination folder for categorized attachments.
// Categorization itself is plugin policy - the pass runs only while a
// plugin declares a categorize function. Empty folder = the default.
type Attachments struct {
	Folder string `toml:"folder"`
}

// DefaultAttachFolder is the [attachments] fallback when folder is
// empty, expanded against the home dir at use.
const DefaultAttachFolder = "~/Downloads/Attachments"

// MCP is the [mcp] section: the whitelist of extra tools the stdio
// server may expose beyond the metadata-only defaults (thread_info,
// search, count). The mcp+lua build reads it; other builds ignore it.
// An unknown method name is a startup error in that build, so a typo
// fails loudly instead of silently serving fewer tools.
type MCP struct {
	Allow []string `toml:"allow"`
}

// HeaderRule is one content-based soft-tag rule: a query and the tags it
// adds when it matches.
type HeaderRule struct {
	Query string   `toml:"query"`
	Add   []string `toml:"add"`
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
	Cursor        string `toml:"cursor"`
	ProgressFill  string `toml:"progress_fill"`
	ProgressEmpty string `toml:"progress_empty"`
	BorderTL      string `toml:"border_tl"`
	BorderTR      string `toml:"border_tr"`
	BorderBL      string `toml:"border_bl"`
	BorderBR      string `toml:"border_br"`
	BorderH       string `toml:"border_h"`
	BorderV       string `toml:"border_v"`
	// the tree glyphs: the thread root marker, one level of indentation,
	// and the branch/leaf markers. ASCII defaults ([ui] glyph-set swaps
	// in box-drawing): ambiguous-width box-drawing glyphs drift the slot
	// math on wide terminals, so the 2-cells-per-level invariant must
	// hold in any font.
	Tree       string `toml:"tree"`
	TreeChild  string `toml:"tree_child"`
	TreeBranch string `toml:"tree_branch"`
	TreeLeaf   string `toml:"tree_leaf"`
}

// Style is one theme style: palette names or raw hex for fg/bg, a
// fixed attr subset for attrs. Empty fields inherit from normal (R11).
type Style struct {
	Fg    string   `toml:"fg"`
	Bg    string   `toml:"bg"`
	Attrs []string `toml:"attrs" enum:"bold,italic,underline,reverse"`
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

func (p *Palette) UnmarshalTOML(v any) error {
	raw, ok := v.(map[string]any)
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
		case map[string]any:
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
	Tabbar    TabbarStyleTable
	Compose   ComposeStyleTable
	Index     IndexStyleTable
	Pager     PagerStyleTable
}

// TabbarStyleTable: the tab strip's bar style at the table's own
// level, the active-tab pill in the nested "active" table (the
// index.tag nested-table shape).
type TabbarStyleTable struct {
	Default Style
	Active  Style
}

// ComposeStyleTable: the compose form's style surface; label is the
// two-column settings label, shared with the prompt dialogue's label,
// divider is the section bar (--- Attachments / --- Preview).
type ComposeStyleTable struct {
	Label   Style
	Divider Style
}

type IndexStyleTable struct {
	Number    Style
	Date      Style
	Author    Style
	Subject   Style
	Flags     Style
	Staged    Style
	Ghost     Style
	Search    Style
	Tree      Style
	Collapsed Style // the C-collapsed thread's marker row (R11)
	Tag       TagStyleTable
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
	// HeaderColors rotate over a header block's lines (headers are an
	// open set - any field can appear - so no per-name styles, the
	// block cycles the list); empty block or empty list falls back to
	// HdrDefault.
	HeaderColors []Style
	Quoted       [6]Style
	Signature    Style
	Attachment   Style
	// Recent tints the five most recent messages of a thread, OtherSide
	// the most recent message from the other side (the pager highlight,
	// whole message block).
	Recent    Style
	OtherSide Style
}

type Theme struct {
	Default  string
	Variants map[string]StyleTable
}

// rawStyle decodes a style table {fg, bg, attrs} from a decoded TOML
// table (map). Every key is checked - strict load.
func rawStyle(v any) (Style, error) {
	raw, ok := v.(map[string]any)
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
			arr, ok := val.([]any)
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
func rawStyleTable(v any, base StyleTable) (StyleTable, error) {
	raw, ok := v.(map[string]any)
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
		case "tabbar":
			tm, ok := val.(map[string]any)
			if !ok {
				return StyleTable{}, fmt.Errorf("tabbar: expected a table")
			}
			if a, ok := tm["active"]; ok {
				s, err := rawStyle(a)
				if err != nil {
					return StyleTable{}, err
				}
				t.Tabbar.Active = s
				delete(tm, "active")
			}
			if len(tm) > 0 {
				s, err := rawStyle(tm)
				if err != nil {
					return StyleTable{}, err
				}
				t.Tabbar.Default = s
			}
		case "compose":
			cm, ok := val.(map[string]any)
			if !ok {
				return StyleTable{}, fmt.Errorf("compose: expected a table")
			}
			if l, ok := cm["label"]; ok {
				s, err := rawStyle(l)
				if err != nil {
					return StyleTable{}, err
				}
				t.Compose.Label = s
				delete(cm, "label")
			}
			if d, ok := cm["divider"]; ok {
				s, err := rawStyle(d)
				if err != nil {
					return StyleTable{}, err
				}
				t.Compose.Divider = s
				delete(cm, "divider")
			}
			for k := range cm {
				return StyleTable{}, fmt.Errorf("compose.%s: unknown key", k)
			}
		case "index":
			im, ok := val.(map[string]any)
			if !ok {
				return StyleTable{}, fmt.Errorf("index: expected a table")
			}
			for ik, iv := range im {
				if ik == "tag" {
					tm, ok := iv.(map[string]any)
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
							arr, ok := tv.([]any)
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
				case "search":
					t.Index.Search = style
				case "tree":
					t.Index.Tree = style
				default:
					return StyleTable{}, fmt.Errorf("index: unknown key %q", ik)
				}
			}
		case "pager":
			pm, ok := val.(map[string]any)
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
				case "header-colors":
					items, ok := pv.([]any)
					if !ok {
						return StyleTable{}, fmt.Errorf("pager.header-colors: expected a list")
					}
					// a named list replaces the variant's colors; an
					// unnamed pager table keeps them (overlay rule)
					t.Pager.HeaderColors = nil
					for i, item := range items {
						s, err := rawStyle(item)
						if err != nil {
							return StyleTable{}, fmt.Errorf("pager.header-colors[%d]: %w", i, err)
						}
						t.Pager.HeaderColors = append(t.Pager.HeaderColors, s)
					}
				case "quoted0", "quoted1", "quoted2", "quoted3", "quoted4", "quoted5":
					t.Pager.Quoted[pk[6]-'0'] = style
				case "signature":
					t.Pager.Signature = style
				case "attachment":
					t.Pager.Attachment = style
				case "recent":
					t.Pager.Recent = style
				case "other-side":
					t.Pager.OtherSide = style
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

func (t *Theme) UnmarshalTOML(v any) error {
	raw, ok := v.(map[string]any)
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
// palette resolution applied: the id-keyed map (normal, indicator,
// status, progress, error, index.number, index.tag.<name>, ...) plus
// the resolved pager header rotation (list order preserved).
func (t Theme) Resolved(p Palette, variant string) (map[string]Style, []Style) {
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
	out["tabbar"] = apply("tabbar", table.Tabbar.Default)
	out["tabbar.active"] = apply("tabbar.active", table.Tabbar.Active)
	out["compose.label"] = apply("compose.label", table.Compose.Label)
	out["compose.divider"] = apply("compose.divider", table.Compose.Divider)
	for id, s := range map[string]Style{
		"index.number": table.Index.Number, "index.date": table.Index.Date,
		"index.author": table.Index.Author, "index.subject": table.Index.Subject,
		"index.flags": table.Index.Flags, "index.staged": table.Index.Staged,
		"index.ghost": table.Index.Ghost, "index.tree": table.Index.Tree,
		"index.collapsed": table.Index.Collapsed,
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
	out["pager.recent"] = apply("pager.recent", table.Pager.Recent)
	out["pager.other-side"] = apply("pager.other-side", table.Pager.OtherSide)
	colors := make([]Style, len(table.Pager.HeaderColors))
	for i, s := range table.Pager.HeaderColors {
		colors[i] = apply(fmt.Sprintf("pager.header-colors[%d]", i), s)
	}
	return out, colors
}

type View struct {
	Query   string `toml:"query"`
	Threads bool   `toml:"threads"`
	// Flat renders the threaded views' rows at the same level - the
	// thread grouping stays, the tree glyphs go (the z key toggles
	// the live view the same way).
	Flat bool `toml:"flat"`
}

// IndexSection is the [index] table (R11): the index surface budgets.
// Thread is the threaded views' tree window (R3): a deep thread renders
// at most MaxRows rows; navigating through the thread slides the window
// under the cursor. Zero MaxRows disables the window (the full tree
// renders).
type IndexSection struct {
	Thread ThreadBudget `toml:"thread"`
}

type ThreadBudget struct {
	MaxRows int `toml:"max-rows"`
	// Sort is the flatten's message order inside a thread: "desc"
	// (the default) reads newest-first like the index, "asc" the
	// notmuch-native oldest-first order.
	Sort string `toml:"sort"`
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
// case: [accounts.atlas] maps to the "atlas" tag directly); the
// pointer distinguishes unset from an explicitly empty value, which is
// a load error. Folders is the detected hard-tag folder map (the
// `notmutt setup` output) - per-account tag -> folder-name for the
// mover's folder resolution. Preset names a built-in provider folder
// map (gmail, generic-imap; unknown names are load errors); Moves
// overrides the preset per tag (tag -> folder candidates, first
// existing wins, '*' globs - afew folder_priorities). ReadOnly
// accounts get folder tags but never physical moves (atlas);
// ReturnInbox enables the trash return-to-inbox rule (the non-standard
// rule in muttrc/afew/config). NoFcc skips the client's sent copy:
// the server keeps one itself (Gmail-family providers), and the
// mbsync-fetched copy is the sent record - writing a fcc would
// duplicate the Message-ID record (one message, two paths).
type Account struct {
	Folder           *string             `toml:"folder"`
	From             string              `toml:"from"`
	DefaultSignature string              `toml:"default_signature"`
	Folders          map[string]string   `toml:"folders"`
	Preset           string              `toml:"preset"`
	Moves            map[string][]string `toml:"moves"`
	ReadOnly         bool                `toml:"readonly"`
	ReturnInbox      bool                `toml:"return_inbox"`
	NoFcc            bool                `toml:"no_fcc"`
}

func (a Account) Tag(name string) string {
	if a.Folder != nil {
		return *a.Folder
	}
	return name
}

// Preset is a provider's tag -> folder-name candidates (R2): the
// default move rule is universal (tag:<t> moves to t's folder), only
// the folder names vary per provider (afew folder_priorities as data).
// Candidates are tried in order, first existing folder wins, '*' is a
// glob. The gmail names are the muttrc afew config reference.
type Preset map[string][]string

// Presets are the built-in provider folder maps. An account's preset
// resolves by name; unknown names are load errors.
var Presets = map[string]Preset{
	"gmail": {
		"archive": {"Archives", "Archive"},
		"deleted": {"[Gmail]/Trash", "Trash", "Deleted Items"},
		"spam":    {"[Gmail]/Spam", "Spam", "Junk*"},
		"pending": {"Pending"},
		"draft":   {"[Gmail]/Drafts", "Drafts"},
		"sent":    {"[Gmail]/Sent Mail", "Sent"},
		"inbox":   {"INBOX"},
	},
	"generic-imap": {
		"archive": {"Archive"},
		"deleted": {"Trash"},
		"spam":    {"Spam"},
		"pending": {"Pending"},
		"draft":   {"Drafts"},
		"sent":    {"Sent"},
		"inbox":   {"INBOX"},
	},
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

// MyAddrs is the identity set: the bare lowercased address of every
// account's from field. A message whose From matches one is authored
// by the user (the pager's other-side highlight); the sent tag is the
// other "me" signal, this catches web-sent mail that never saw the
// client's Sent folder.
func (c Config) MyAddrs() []string {
	var out []string
	for _, a := range c.Accounts {
		if a.From == "" {
			continue
		}
		if p, err := mail.ParseAddress(a.From); err == nil {
			out = append(out, strings.ToLower(strings.TrimSpace(p.Address)))
		}
	}
	return out
}

// mergeSchemes overlays file scheme tables over the embedded base per
// key. BurntSushi merges only the top-level map field; a
// [schemes.vim.index] table replaces the whole context table when
// decoded into the nested map, so the R9 file overlay is applied
// explicitly here (context and key levels merge).
func mergeSchemes(base, over map[string]map[string]map[string]Binding) map[string]map[string]map[string]Binding {
	out := make(map[string]map[string]map[string]Binding, len(base))
	for km, ctxs := range base {
		out[km] = make(map[string]map[string]Binding, len(ctxs))
		for c, keys := range ctxs {
			out[km][c] = maps.Clone(keys)
		}
	}
	for km, ctxs := range over {
		if out[km] == nil {
			out[km] = make(map[string]map[string]Binding)
		}
		for c, keys := range ctxs {
			if out[km][c] == nil {
				out[km][c] = make(map[string]Binding)
			}
			maps.Copy(out[km][c], keys)
		}
	}
	return out
}

// deriveDescriptions is the help vocabulary from the binding entries:
// the first non-empty desc per action wins - the selected scheme's
// entries first (a user desc on a rebound key overrides the scheme
// default), then the other schemes sorted. Actions are shared
// vocabulary: a desc defined once describes the action everywhere.
func deriveDescriptions(schemes map[string]map[string]map[string]Binding, keymap string) map[string]string {
	out := map[string]string{}
	var others []string
	for km := range schemes {
		if km != keymap {
			others = append(others, km)
		}
	}
	sort.Strings(others)
	for _, km := range append([]string{keymap}, others...) {
		for _, c := range sortedKeys(schemes[km]) {
			for _, k := range sortedKeys(schemes[km][c]) {
				e := schemes[km][c][k]
				if e.Desc == "" {
					continue
				}
				if _, seen := out[e.Fun]; !seen {
					out[e.Fun] = e.Desc
				}
			}
		}
	}
	return out
}

// deriveAccountViews derives the per-account views and their goto keys
// from the accounts table (R1: virtual views are tag queries; the
// muttrc folder:/^<folder>\// account-tag pattern as data). Every
// account owns a view over its account tag, numbered by sorted account
// name (g1..gN in the vim index scheme). A user [view] entry with the
// same name wins; a key the user already bound wins. Read-only
// accounts are included - a view is a query, never a write (R2's
// classification rules govern writes only). Runs in Default() (the
// placeholder accounts) and again in Load() over the merged accounts:
// the keys it added last run are removed first, so the numbering
// re-derives from the merged set instead of colliding with the
// Default-time entries.
func deriveAccountViews(cfg *Config) {
	if cfg.DerivedGKeys == nil {
		cfg.DerivedGKeys = map[string]string{}
	}
	index := cfg.Schemes["vim"]["index"]
	if index == nil {
		index = map[string]Binding{}
		cfg.Schemes["vim"]["index"] = index
	}
	for key, tag := range cfg.DerivedGKeys {
		// a user-replaced entry is not ours to remove
		if e, ok := index[key]; ok && e.Fun == "goto-"+tag {
			delete(index, key)
		}
	}
	cfg.DerivedGKeys = map[string]string{}
	i := 0
	for _, name := range sortedKeys(cfg.Accounts) {
		i++
		tag := cfg.Accounts[name].Tag(name)
		if _, ok := cfg.Views[tag]; !ok {
			// per-account views are flat: only inbox/archive thread
			cfg.Views[tag] = View{Query: "tag:" + tag}
		}
		key := fmt.Sprintf("g %d", i)
		if _, ok := index[key]; !ok {
			index[key] = Binding{Fun: "goto-" + tag, Desc: "Show the " + tag + " view", Show: true}
			cfg.DerivedGKeys[key] = tag
		}
	}
}

// defaultView is the startup view: "inbox" when present, else the
// first in sorted name order - never map iteration order.
func defaultView(cfg Config) string {
	if _, ok := cfg.Views["inbox"]; ok {
		return "inbox"
	}
	if names := sortedKeys(cfg.Views); len(names) > 0 {
		return names[0]
	}
	return ""
}

// sortedKeys is the deterministic iteration order map lookups need
// (map iteration is not).
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// bindingsFromScheme flattens a scheme's entries to key -> action
// plus the per-context shown-key set (the keyhint row shows them,
// the help dialog shows every binding): the dispatch surface (the
// tui switches on the action strings). Visibility is opt-in - an
// entry without show = true is hidden from the keyhint. The result
// is always a fresh map set - a caller's rebind never touches the
// store or the next Default.
func bindingsFromScheme(scheme map[string]map[string]Binding) (map[string]map[string]string, map[string]map[string]bool) {
	out := make(map[string]map[string]string, len(scheme))
	shown := make(map[string]map[string]bool, len(scheme))
	for ctx, km := range scheme {
		m := make(map[string]string, len(km))
		s := make(map[string]bool, len(km))
		for k, b := range km {
			m[k] = b.Fun
			if b.Show {
				s[k] = true
			}
		}
		out[ctx] = m
		shown[ctx] = s
	}
	return out, shown
}

func Default() Config {
	cfg := Config{
		UI: UI{
			Keymap:     "vim",
			Language:   "auto",
			GlyphSet:   "ascii",
			SearchOpen: "active",
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
					"forwarded": "↪",
				},
			},
			Glyphs: Glyphs{
				Staged: "*", Cursor: "▌", ProgressFill: "#", ProgressEmpty: "-",
				BorderTL: "╭", BorderTR: "╮", BorderBL: "╰", BorderBR: "╯",
				BorderH: "─", BorderV: "│",
				Tree: "+ ", TreeChild: "| ", TreeBranch: "|-", TreeLeaf: "`-",
			},
		},
		// only inbox/archive are threaded (the rest are flat
		// chronological lists: unread, deleted, search)
		Views: map[string]View{
			"inbox":   {Query: "tag:inbox", Threads: true},
			"unread":  {Query: "tag:unread"},
			"pending": {Query: "tag:pending"},
			"sent":    {Query: "tag:sent"},
			"spam":    {Query: "tag:spam"},
			"deleted": {Query: "tag:deleted"},
			"draft":   {Query: "tag:draft"},
			"archive": {Query: "tag:archive", Threads: true},
		},
		Index: IndexSection{
			Thread: ThreadBudget{MaxRows: 10, Sort: "desc"},
		},
		TagGroups: map[string]core.TagGroup{
			"folder": {Tags: []string{"inbox", "archive", "deleted", "sent", "draft", "pending", "spam"}},
		},
		// the gmail placeholder keeps the default shape; the real
		// accounts come from the user's accounts file (a table that
		// merges over this map)
		Accounts: map[string]Account{
			"gmail": {},
		},
		Send: Send{
			Command: "msmtp",
			Args:    []string{"--read-envelope-from"},
		},
		Refresh: Refresh{
			Interval: 20,
		},
		Filter: Filter{
			Enabled: true,
			DryRun:  true,
		},
		Notify: Notify{
			Max: 3,
		},
		Attachments: Attachments{
			Folder: DefaultAttachFolder,
		},
		MCP: MCP{
			Allow: []string{},
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
	deriveAccountViews(&cfg)
	cfg.ActiveView = defaultView(cfg)
	cfg.Bindings, cfg.Shown = bindingsFromScheme(cfg.Schemes["vim"])
	cfg.Descriptions = deriveDescriptions(cfg.Schemes, "vim")
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
				// the tab strip (tmux2k window-list colors mapped onto
				// onedark): the inactive pills sit on the gray bar, the
				// active pill fills with the accent blue, dark text
				Tabbar: TabbarStyleTable{
					Default: Style{Fg: "base05", Bg: "base01"},
					Active:  Style{Fg: "base00", Bg: "base0D"},
				},
				Compose: ComposeStyleTable{
					Label:   Style{Fg: "base0D"},               // the form's settings labels: onedark author blue
					Divider: Style{Fg: "base05", Bg: "base03"}, // section bar: text on the gray
				},
				Index: IndexStyleTable{
					Number: Style{Fg: "base03"}, Date: Style{Fg: "base0A"},
					Author: Style{Fg: "base0D"}, Subject: Style{Fg: "base05"},
					Flags: Style{Fg: "base08"}, Staged: Style{Fg: "base04", Attrs: []string{"bold"}},
					Ghost: Style{Fg: "base03"}, Search: Style{Fg: "base0A", Attrs: []string{"bold"}},
					Tree: Style{Fg: "base03"}, Collapsed: Style{Fg: "base0A", Attrs: []string{"bold"}},
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
					// the onedark quoted palette as the header
					// rotation (neomutt's quoted_colors_get model: the
					// block cycles the list, wrapping past the end -
					// references/muttrc/theme/onedark.muttrc:46-51)
					HeaderColors: []Style{
						{Fg: "base0B"}, {Fg: "base0C"}, {Fg: "base0D"},
						{Fg: "base0E"}, {Fg: "base0A"}, {Fg: "base08"},
					},
					Quoted: [6]Style{
						{Fg: "base0B"}, {Fg: "base0C"}, {Fg: "base0D"},
						{Fg: "base0E"}, {Fg: "base0A"}, {Fg: "base08"},
					},
					Signature: Style{Fg: "base03"}, Attachment: Style{Fg: "base0E"},
					// the thread-position highlight: the recent-5 tint
					// (cyan) and the last other-side message (purple
					// bold) - the index row landmark of the open message
					Recent: Style{Fg: "base0C"}, OtherSide: Style{Fg: "base0E", Attrs: []string{"bold"}},
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

// Load merges every *.toml file in dir over defaults: the optional
// splits (accounts.toml, filters.toml, ...) merge in sorted name
// order, then config.toml merges LAST and wins any conflict - the
// main file is authoritative, the splits partition its sections, and
// one file remains the degenerate case. Tables merge recursively;
// arrays and scalars replace. Unknown keys are load errors naming
// the file and key (strict load, R8). A missing dir means defaults.
// The merged [schemes.*] tables overlay the embedded base per key
// (mergeSchemes; BurntSushi replaces whole context tables in nested
// maps), so a rebinding names the scheme, context, and key it touches.
func Load(dir string) (Config, error) {
	cfg := Default()
	files, err := filepath.Glob(filepath.Join(dir, "*.toml"))
	if err != nil {
		return cfg, err
	}
	sort.Strings(files)
	files = configLast(files)
	if len(files) == 0 {
		return cfg, nil
	}
	merged := map[string]any{}
	for _, f := range files {
		var probe Config
		md, err := toml.DecodeFile(f, &probe)
		if err != nil {
			return cfg, err
		}
		if keys := undecodedKeys(md); len(keys) > 0 {
			return cfg, fmt.Errorf("%s: unknown key(s): %s", f, strings.Join(keys, ", "))
		}
		var m map[string]any
		if _, err := toml.DecodeFile(f, &m); err != nil {
			return cfg, err
		}
		merged = mergeMaps(merged, m)
	}
	raw, err := toml.Marshal(merged)
	if err != nil {
		return cfg, err
	}
	if _, err := toml.Decode(string(raw), &cfg); err != nil {
		return cfg, err
	}
	applyGlyphSet(&cfg, merged)
	cfg.Schemes = mergeSchemes(baseConfig.Schemes, cfg.Schemes)
	deriveAccountViews(&cfg)
	cfg.ActiveView = defaultView(cfg)
	cfg.Bindings, cfg.Shown = bindingsFromScheme(cfg.Schemes[cfg.UI.Keymap])
	cfg.Descriptions = deriveDescriptions(cfg.Schemes, cfg.UI.Keymap)
	if err := validate(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// undecodedKeys runs the strict check: the keys the struct decode did
// not consume. palette/theme decode through custom unmarshalers that
// consume their whole subtree; BurntSushi does not mark Unmarshaler
// subtrees as decoded, so their inner keys always land here. The
// unmarshalers are strict themselves. A 4-level schemes key is a
// table-form binding entry's field (schemes.<km>.<ctx>.<key>.<fun|desc>)
// - consumed by Binding.UnmarshalTOML, which rejects unknown fields.
func undecodedKeys(md toml.MetaData) []string {
	var keys []string
	for _, k := range md.Undecoded() {
		if k[0] == "palette" || k[0] == "theme" || (k[0] == "schemes" && len(k) == 5) {
			continue
		}
		keys = append(keys, k.String())
	}
	return keys
}

// applyGlyphSet resolves the [ui] glyph-set preset onto the tree
// glyphs after the TOML merge: the preset fills only the glyphs the
// user's files did not set explicitly (per-glyph keys always win).
func applyGlyphSet(cfg *Config, merged map[string]any) {
	ui, _ := merged["ui"].(map[string]any)
	g, _ := ui["glyphs"].(map[string]any)
	apply := func(dst *string, key, glyph string) {
		if _, ok := g[key]; !ok {
			*dst = glyph
		}
	}
	if cfg.UI.GlyphSet == "ascii" {
		apply(&cfg.UI.Glyphs.Tree, "tree", "+ ")
		apply(&cfg.UI.Glyphs.TreeChild, "tree_child", "| ")
		apply(&cfg.UI.Glyphs.TreeBranch, "tree_branch", "|-")
		apply(&cfg.UI.Glyphs.TreeLeaf, "tree_leaf", "`-")
		return
	}
	apply(&cfg.UI.Glyphs.Tree, "tree", "▸ ")
	apply(&cfg.UI.Glyphs.TreeChild, "tree_child", "│ ")
	apply(&cfg.UI.Glyphs.TreeBranch, "tree_branch", "├─")
	apply(&cfg.UI.Glyphs.TreeLeaf, "tree_leaf", "└─")
}

// configLast moves config.toml to the end of the load order: it wins
// conflicts against the optional splits.
func configLast(files []string) []string {
	out := make([]string, 0, len(files))
	var main string
	for _, f := range files {
		if filepath.Base(f) == "config.toml" {
			main = f
			continue
		}
		out = append(out, f)
	}
	if main != "" {
		out = append(out, main)
	}
	return out
}

// mergeMaps overlays b over a: tables merge recursively, arrays and
// scalars replace - later files win per key.
func mergeMaps(a, b map[string]any) map[string]any {
	out := make(map[string]any, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		if av, ok := out[k].(map[string]any); ok {
			if bv, ok := v.(map[string]any); ok {
				out[k] = mergeMaps(av, bv)
				continue
			}
		}
		out[k] = v
	}
	return out
}

func validate(cfg Config) error {
	if _, ok := baseConfig.Schemes[cfg.UI.Keymap]; !ok {
		return fmt.Errorf("keymap: must be vim or emacs, got %q", cfg.UI.Keymap)
	}
	if lang := cfg.UI.Language; lang != "" && lang != "auto" {
		if _, err := language.Parse(lang); err != nil {
			return fmt.Errorf("ui.language: must be \"auto\" or a BCP 47 tag, got %q", lang)
		}
	}
	if cfg.UI.Tags.Max < 1 {
		return fmt.Errorf("ui.tags.max: must be >= 1, got %d", cfg.UI.Tags.Max)
	}
	if cfg.UI.GlyphSet != "ascii" && cfg.UI.GlyphSet != "utf-8" {
		return fmt.Errorf("ui.glyph-set: must be ascii or utf-8, got %q", cfg.UI.GlyphSet)
	}
	if cfg.UI.SearchOpen != "active" && cfg.UI.SearchOpen != "background" {
		return fmt.Errorf("ui.search-open: must be active or background, got %q", cfg.UI.SearchOpen)
	}
	if s := cfg.Index.Thread.Sort; s != "asc" && s != "desc" {
		return fmt.Errorf("index.thread.sort: must be asc or desc, got %q", s)
	}
	if cfg.Refresh.Interval < 0 {
		return fmt.Errorf("refresh.interval: must be >= 0 minutes (0 disables the poll), got %d", cfg.Refresh.Interval)
	}
	for name, argv := range cfg.AttachCommands {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("attach-commands: empty command name")
		}
		if len(argv) == 0 || strings.TrimSpace(argv[0]) == "" {
			return fmt.Errorf("attach-commands.%s: argv must not be empty", name)
		}
	}
	for name, r := range cfg.Lua.Network {
		for _, p := range r.Paths {
			method, glob, ok := strings.Cut(p, " ")
			if !ok || method == "" || !strings.HasPrefix(glob, "/") {
				return fmt.Errorf("lua.network.%s.paths: %q must be \"METHOD /path\" (\"GET /crm/v3/objects/contacts*\")", name, p)
			}
		}
	}
	if len(cfg.Opener) > 0 && strings.TrimSpace(cfg.Opener[0]) == "" {
		return fmt.Errorf("opener: argv must not be empty")
	}
	for d, v := range cfg.Pager.DefaultViews {
		if strings.TrimSpace(d) == "" {
			return fmt.Errorf("pager.default-views: empty domain")
		}
		if v != "plain" && v != "html" {
			return fmt.Errorf("pager.default-views.%s: must be plain or html, got %q", d, v)
		}
	}
	if v := cfg.Pager.ImageProtocol; v != "" && v != "sixel" && v != "kitty" {
		return fmt.Errorf("pager.image-protocol: must be sixel or kitty, got %q", v)
	}
	if b := cfg.Notify.Backend; b != "" && !slices.Contains(enumOf(reflect.TypeOf(Notify{}), "Backend"), b) {
		return fmt.Errorf("notify: unknown backend %q", b)
	}
	if len(cfg.Notify.Command) > 0 && strings.TrimSpace(cfg.Notify.Command[0]) == "" {
		return fmt.Errorf("notify: command argv must not be empty")
	}
	if cfg.Notify.Max < 0 {
		return fmt.Errorf("notify: max must be >= 0")
	}
	for _, t := range cfg.Notify.Priority {
		if strings.TrimSpace(t) == "" {
			return fmt.Errorf("notify: priority tag must not be empty")
		}
	}
	for i, r := range cfg.Filter.HeaderRules {
		if strings.TrimSpace(r.Query) == "" {
			return fmt.Errorf("filter.header-rules[%d]: query must not be empty", i)
		}
		if len(r.Add) == 0 {
			return fmt.Errorf("filter.header-rules[%d]: at least one add tag required", i)
		}
		for _, t := range r.Add {
			if strings.TrimSpace(t) == "" {
				return fmt.Errorf("filter.header-rules[%d]: empty add tag", i)
			}
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
				if strings.TrimSpace(v.Fun) == "" {
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
	if g.Staged == "" || g.Cursor == "" || g.ProgressFill == "" || g.ProgressEmpty == "" ||
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
		if a.Preset != "" {
			if _, ok := Presets[a.Preset]; !ok {
				return fmt.Errorf("accounts.%s: unknown preset %q", name, a.Preset)
			}
		}
		for tag, candidates := range a.Moves {
			if strings.TrimSpace(tag) == "" {
				return fmt.Errorf("accounts.%s.moves: empty tag name", name)
			}
			if len(candidates) == 0 {
				return fmt.Errorf("accounts.%s.moves.%s: at least one candidate required", name, tag)
			}
			for _, c := range candidates {
				if strings.TrimSpace(c) == "" {
					return fmt.Errorf("accounts.%s.moves.%s: empty candidate", name, tag)
				}
				if strings.ContainsAny(c, `"`) {
					return fmt.Errorf("accounts.%s.moves.%s: candidate %q: double quotes break the folder rule query", name, tag, c)
				}
			}
		}
	}
	for name, p := range cfg.AI {
		if p.Type != "anthropic" && p.Type != "openai" {
			return fmt.Errorf("ai.%s: type must be \"anthropic\" or \"openai\"", name)
		}
		if strings.TrimSpace(p.Model) == "" {
			return fmt.Errorf("ai.%s: model must not be blank", name)
		}
		for i, a := range p.PassCmd {
			if strings.TrimSpace(a) == "" {
				return fmt.Errorf("ai.%s: pass_cmd[%d] must not be blank", name, i)
			}
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
		{"index.ghost", t.Index.Ghost}, {"index.tree", t.Index.Tree},
		{"index.tag", t.Index.Tag.Default},
		{"pager.header", t.Pager.Header}, {"pager.hdrdefault", t.Pager.HdrDefault},
		{"pager.signature", t.Pager.Signature}, {"pager.attachment", t.Pager.Attachment},
		{"pager.recent", t.Pager.Recent}, {"pager.other-side", t.Pager.OtherSide},
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
		if !slices.Contains(enumOf(reflect.TypeOf(Style{}), "Attrs"), a) {
			return fmt.Errorf("%s.attrs: unknown attr %q", path, a)
		}
	}
	return nil
}

// enumOf is a struct field's enum tag split on commas; nil when the
// field has none. The allowed values travel with the schema as data -
// validate() consumes the tag, and a future config LSP completes from
// the same one definition (keymap and preset stay data-derived: their
// valid sets are the schemes and presets maps, not a tag).
func enumOf(typ reflect.Type, field string) []string {
	f, ok := typ.FieldByName(field)
	if !ok {
		return nil
	}
	tag, ok := f.Tag.Lookup("enum")
	if !ok {
		return nil
	}
	return strings.Split(tag, ",")
}
