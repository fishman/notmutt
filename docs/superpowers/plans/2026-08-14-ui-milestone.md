# M3: UI Milestone Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The TUI becomes a framework-native mail UI: styled index (R11), a pager for reading threads, per-context keybinding data (R9), and an extractable component structure (R5).

**Architecture:** Four tasks in dependency order. Task 1 is a behavior-preserving restructure of the existing single-file model.go into focused components on lipgloss styles (hardcoded onedark defaults). Task 2 replaces the hardcoded styles with R11 config data (palette + theme variants, strict load, live variant switch via the config store). Task 3 adds the pager: an `open` action fetches the thread through the existing worker ActThread, a new `mail` package parses the message files with go-message into F1-clean render lines, and the model renders them in a bubbletea viewport. Task 4 makes keybindings per-context data with vim/emacs default schemes and a derived keyhint bar.

**Tech Stack:** Go, BubbleTea v1.1.0, lipgloss v0.13.0 (both vendored), emersion/go-message v0.18.2 (vendored, the R6 parser), runewidth (vendored).

---

## Task 1: Framework restructure - components + lipgloss styles

The model is one file with raw `\x1b[...]` strings. Split the TUI into
focused files and move the renderer onto lipgloss with a hardcoded
onedark style set (the reference port colors: bg #21252b, fg #abb2bf,
grey #5c6370, status bg #3e4451, red #e06c75, green #98c379, yellow
#e5c07b, blue #61afef, cyan #56b6c2, purple #c678dd). Behavior is
preserved except the cursor line (reverse video becomes the indicator
style) and rows are padded/truncated to the terminal width (the R11
slot-reservation rule: rows never exceed width, alignment never shifts).

**Files:**
- Create: `src/tui/styles.go`
- Create: `src/tui/index.go` (renderRow, flags, flagChars, attachIcon, formatDate, tagGlyphs, stripControls, truncCells, padCellsRight - moved verbatim from model.go)
- Create: `src/tui/statusline.go` (statusLine, progressBar, progressWidth - moved verbatim)
- Modify: `src/tui/model.go` (slim: keep the state machine; render via the styles; pad rows to width)
- Test: `src/tui/model_test.go` (existing tests must pass unchanged), add `TestRowStyled` in a new `src/tui/styles_test.go`

- [ ] **Step 1: Write the failing test**

`src/tui/styles_test.go`:

```go
package tui

import (
	"strings"
	"testing"

	"notmutt/core"
)

func TestRowStyled(t *testing.T) {
	row := core.Row{Msg: &core.Message{
		ID: "m1", ThreadID: "t1", Timestamp: 1755150000,
		Author: "Ann", Subject: "hello", Tags: []string{"inbox"},
	}}
	out := renderRow(1, row, DefaultStyles())
	if !strings.Contains(out, "\x1b[38;2;97;175;239m") { // onedark author blue #61afef
		t.Fatalf("author slot must carry its style:\n%q", out)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("subject missing:\n%q", out)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./tui/ -run TestRowStyled`
Expected: FAIL (renderRow takes no styles argument - compile error).

- [ ] **Step 3: Implement**

`src/tui/styles.go` - the style set and its onedark defaults:

```go
package tui

import "github.com/charmbracelet/lipgloss"

// Styles is the full style surface the TUI renders with. Task 2 makes
// this config-driven; the hardcoded onedark values are the reference
// port (references/muttrc/theme/onedark.muttrc) until then.
type Styles struct {
	Normal    lipgloss.Style
	Indicator lipgloss.Style
	Status    lipgloss.Style
	Progress  lipgloss.Style
	Index     IndexStyles
	Pager     PagerStyles
}

type IndexStyles struct {
	Number  lipgloss.Style
	Date    lipgloss.Style
	Author  lipgloss.Style
	Subject lipgloss.Style
	Flags   lipgloss.Style
	Staged  lipgloss.Style
	Ghost   lipgloss.Style
	Tag     lipgloss.Style
}

type PagerStyles struct {
	Header      lipgloss.Style
	HdrDefault  lipgloss.Style
	Quoted      [6]lipgloss.Style
	Signature   lipgloss.Style
	Attachment  lipgloss.Style
}

func DefaultStyles() Styles {
	c := func(hex string) lipgloss.Color { return lipgloss.Color(hex) }
	return Styles{
		Normal:    lipgloss.NewStyle().Foreground(c("#abb2bf")).Background(c("#21252b")),
		Indicator: lipgloss.NewStyle().Foreground(c("#21252b")).Background(c("#e5c07b")),
		Status:    lipgloss.NewStyle().Foreground(c("#abb2bf")).Background(c("#3e4451")),
		Progress:  lipgloss.NewStyle().Foreground(c("#21252b")).Background(c("#61afef")),
		Index: IndexStyles{
			Number:  lipgloss.NewStyle().Foreground(c("#5c6370")),
			Date:    lipgloss.NewStyle().Foreground(c("#e5c07b")),
			Author:  lipgloss.NewStyle().Foreground(c("#61afef")),
			Subject: lipgloss.NewStyle().Foreground(c("#abb2bf")),
			Flags:   lipgloss.NewStyle().Foreground(c("#e06c75")),
			Staged:  lipgloss.NewStyle().Foreground(c("#565c64")).Bold(true),
			Ghost:   lipgloss.NewStyle().Foreground(c("#5c6370")),
			Tag:     lipgloss.NewStyle().Foreground(c("#c678dd")),
		},
		Pager: PagerStyles{
			Header:     lipgloss.NewStyle().Foreground(c("#61afef")),
			HdrDefault: lipgloss.NewStyle().Foreground(c("#abb2bf")),
			Quoted:     [6]lipgloss.Style{
				lipgloss.NewStyle().Foreground(c("#98c379")),
				lipgloss.NewStyle().Foreground(c("#56b6c2")),
				lipgloss.NewStyle().Foreground(c("#61afef")),
				lipgloss.NewStyle().Foreground(c("#c678dd")),
				lipgloss.NewStyle().Foreground(c("#e5c07b")),
				lipgloss.NewStyle().Foreground(c("#e06c75")),
			},
			Signature:  lipgloss.NewStyle().Foreground(c("#5c6370")),
			Attachment: lipgloss.NewStyle().Foreground(c("#c678dd")),
		},
	}
}
```

`src/tui/index.go` - move renderRow/flags/flagChars/attachIcon/formatDate/
tagGlyphs/stripControls/truncCells/padCellsRight from model.go verbatim,
with these changes: renderRow gains `st Styles` and styles each slot
(flags slot: `st.Index.Flags.Render`, number: `st.Index.Number.Render`,
date: `st.Index.Date.Render`, author: `st.Index.Author.Render`, subject:
`st.Index.Subject.Render`, tag glyphs: `st.Index.Tag.Render`); the
staged branch replaces the hardcoded `\x1b[1m` + glyph with
`st.Index.Staged.Render(" *"+flagChars(tags))`; ghost rows use
`st.Index.Ghost.Render`.

`src/tui/statusline.go` - move statusLine/progressBar/progressWidth
verbatim, except: the status line renders through `st.Status.Render`
(left + padding + right), and the progress bar region renders through
`st.Progress.Render` on the filled cells and Normal on the empty cells
(see the Task 2 statusline for the exact split; keep it simple now:
the whole right region renders with Status, the fill glyphs with
Progress).

`src/tui/model.go` - the Model keeps all state fields; render code
becomes:

```go
func (m Model) View() string {
	if m.rows == nil {
		m.rows = m.view.Rows()
	}
	st := DefaultStyles()
	rows := m.rows
	if len(rows) == 0 {
		if m.progressOn {
			return "empty\n" + m.statusLine(st) + "\n"
		}
		return "empty\n"
	}
	cur := m.CursorIndex()
	listHeight := m.height - 1
	if listHeight < 1 {
		listHeight = 1
	}
	top := cur - listHeight/2
	if top < 0 {
		top = 0
	}
	bottom := top + listHeight
	if bottom > len(rows) {
		bottom = len(rows)
		top = bottom - listHeight
		if top < 0 {
			top = 0
		}
	}
	var b strings.Builder
	for i := top; i < bottom; i++ {
		line := renderRow(i+1, rows[i], st)
		if rows[i].Staged {
			line = st.Index.Staged.Render(line)
		}
		if i == cur {
			line = st.Indicator.Render(line)
		} else if rows[i].Ghost {
			line = st.Index.Ghost.Render(line)
		}
		if m.width > 0 {
			line = padCellsRight(stripStyles(line), m.width)
			// stripStyles: the slot styles are already inside line; pad to
			// width with the ROW style applied after (lipgloss pads and
			// truncates by cells, but the row style must wrap the padding)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	b.WriteString(m.statusLine(st))
	b.WriteByte('\n')
	return b.String()
}
```

Pad-to-width detail: renderRow produces a line with per-slot SGR
sequences. To color the full row background, wrap: compute the plain
width (strip SGRs via a small stripANSI helper in index.go), pad the
PLAIN string to m.width, then apply the row style (indicator for the
cursor row, normal otherwise) LAST, so the background covers the full
width:

```go
// padRow wraps line's padding in the given style: the line's own slot
// styles survive inside; the outer style colors the full row width.
func padRow(line string, w int, outer lipgloss.Style) string {
	plain := lipgloss.NewStyle().Render(line)
	width := runewidth.StringWidth(stripANSI(plain))
	if width >= w {
		return outer.Render(truncateStyled(line, w))
	}
	return outer.Render(plain + strings.Repeat(" ", w-width))
}
```

Keep `stripANSI` (regex-free: scan for ESC, skip until 'm') in
index.go; `truncateStyled` truncates the styled string by visible
cells (walk runes, track SGR runs as zero-width). Both are small
helpers covered by `TestPadRow` below. Where the indicator/staged/
ghost styles previously applied to the raw line, apply padRow last
with the row style so the background covers the width:

```go
if rows[i].Staged {
	line = st.Index.Staged.Render(line)
}
outer := st.Normal
if i == cur {
	outer = st.Indicator
} else if rows[i].Ghost {
	outer = st.Index.Ghost
}
line = padRow(line, m.width, outer)
```

`src/tui/styles_test.go` - add `TestPadRow`:

```go
func TestPadRow(t *testing.T) {
	st := DefaultStyles()
	out := padRow("x", 5, st.Indicator)
	if runewidth.StringWidth(stripANSI(out)) != 5 {
		t.Fatalf("padRow must produce exactly 5 visible cells: %q", out)
	}
	if !strings.Contains(out, "\x1b[48") { // indicator background
		t.Fatalf("outer style must color the padding: %q", out)
	}
}
```

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./tui/`
Expected: all pass, including the pre-existing model tests (the
restructure contract: event handling and content assertions are
unchanged; the F1 leak test at model_test.go:368 still passes because
stripControls survives the move).

- [ ] **Step 5: Commit**

```bash
git add src/tui
git commit -m "feat(tui): split the model into styled framework components"
```

## Task 2: R11 theming - palette and theme data

Replace the hardcoded style set with config data. The TOML shape is
pinned in the spec (section 3): `[palette]` base16 names, optional
`[palette.<variant>]` overrides; `[theme] default` selects the variant
whose `[theme.<variant>.*]` tables hold styles; style values are
`{fg, bg, attrs}` with fg/bg being palette names or raw hex. Rules:
inheritance from normal (field by field), resolution order style hex >
variant palette > base palette, strict load (unknown keys, unknown
palette refs, bad hex, unknown attrs, missing variant all error).

**Files:**
- Modify: `src/config/config.go` (Style, Palette, Theme, StyleTable types + UnmarshalTOML + resolution + validation; Glyphs gains StatuslineSeparator)
- Modify: `src/config/store.go` (SetThemeVariant)
- Modify: `src/app/app.go` (theme subscription, pass theme data to the TUI)
- Modify: `src/tui/styles.go` (config data in, lipgloss.Style out)
- Modify: `src/tui/statusline.go` (the composable segment model, section below)
- Modify: `src/tui/model.go` (hold theme data, re-resolve on ConfigChanged)
- Test: `src/config/config_test.go`, `src/tui/styles_test.go`, `src/tui/statusline_test.go`

- [ ] **Step 1: Write the failing tests**

`src/config/config_test.go`:

```go
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
		"unknown key":       {"[theme.dark]\nnonesuch = { fg = \"base00\" }", "unknown key"},
		"unknown palette":   {"[theme.dark]\nstatus = { fg = \"base99\" }", "base99"},
		"bad hex":           {"[palette]\nbase00 = \"zzz\"", "base00"},
		"bad attr":          {"[theme.dark]\nnormal = { attrs = [\"glow\"] }", "glow"},
		"missing variant":   {"[theme]\ndefault = \"light\"", "light"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(write(name+".toml", "keymap = \"vim\"\n"+tc.body))
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
```

Note: `writeThemeFile` is a small helper (os.WriteFile into t.TempDir)
you add in the test file; the load path needs `keymap` present? No -
keymap defaults to "vim" and is always valid, but validate() requires
at least one view, so the fixture files carry a view table. Actually
Default() already provides the inbox view; only strict-load errors are
introduced by the fixture.

`src/tui/styles_test.go`:

```go
func TestResolveFromConfig(t *testing.T) {
	st := ResolveStyles(config.Theme{Default: "dark", Variants: map[string]config.StyleTable{
		"dark": {Status: config.Style{Fg: "base0A"}},
	}}, config.Palette{Base: map[string]string{"base00": "#21252b", "base05": "#abb2bf", "base0A": "#e5c07b"}})
	if !strings.Contains(st.Status.Render("x"), "38;2;229;192;123") {
		t.Fatalf("status fg must resolve through the base palette: %q", st.Status.Render("x"))
	}
}
```

(Adjust the assertion to whatever lipgloss exposes for reading back a
color; if it cannot be read back, assert the rendered escape string
contains "38;2;229;192;123" for the status fg instead.)

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./config/ ./tui/`
Expected: FAIL (config types do not exist).

- [ ] **Step 3: Implement**

`src/config/config.go` - the data types. The TOML files unmarshal into
these; custom UnmarshalTOML handles the data-driven variant tables
(variant names are not fixed struct fields):

```go
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

type Palette struct {
	Base     map[string]string
	Variants map[string]map[string]string
}

func (p *Palette) UnmarshalTOML(v interface{}) error {
	raw, ok := v.(map[string]interface{})
	if !ok {
		return fmt.Errorf("palette: expected a table")
	}
	p.Base = map[string]string{}
	p.Variants = map[string]map[string]string{}
	for name, val := range raw {
		switch tv := val.(type) {
		case string:
			p.Base[name] = tv
		case map[string]interface{}:
			m := map[string]string{}
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

func (p Palette) Color(name, variant string) string {
	if v, ok := p.Variants[variant][name]; ok {
		return v
	}
	return p.Base[name]
}

type StyleTable struct {
	Normal    Style
	Indicator Style
	Status    Style
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

// TagStyleTable: the spec shape (section 3) is mixed - fg/bg/attrs at
// the tag table's own level are the DEFAULT tag glyph style, other
// keys are per-tag overrides:
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
	Quoted0    Style
	Quoted1    Style
	Quoted2    Style
	Quoted3    Style
	Quoted4    Style
	Quoted5    Style
	Signature  Style
	Attachment Style
}

type Theme struct {
	Default  string
	Variants map[string]StyleTable
}

func (t *Theme) UnmarshalTOML(v interface{}) error {
	raw, ok := v.(map[string]interface{})
	if !ok {
		return fmt.Errorf("theme: expected a table")
	}
	t.Variants = map[string]StyleTable{}
	for name, val := range raw {
		if name == "default" {
			s, ok := val.(string)
			if !ok {
				return fmt.Errorf("theme.default: expected a string")
			}
			t.Default = s
			continue
		}
		var table StyleTable
		if err := decodeTable(val, &table); err != nil {
			return fmt.Errorf("theme.%s: %w", name, err)
		}
		t.Variants[name] = table
	}
	return nil
}

// styleFromRaw decodes a style table {fg, bg, attrs} from the raw
// decoded TOML value.
func styleFromRaw(v interface{}) (Style, error) {
	raw, ok := v.(map[string]interface{})
	if !ok {
		return Style{}, fmt.Errorf("expected a style table")
	}
	var s Style
	for k, val := range raw {
		switch k {
		case "fg", "bg":
			str, ok := val.(string)
			if !ok {
				return Style{}, fmt.Errorf("%s: expected a string", k)
			}
			if k == "fg" {
				s.Fg = str
			} else {
				s.Bg = str
			}
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

// decodeTable uses BurntSushi's generic Decode on a fresh table so the
// struct tags still apply to nested tables.
func decodeTable(v interface{}, out interface{}) error {
	if _, err := toml.Decode(structureToString(v), out); err != nil {
		return err
	}
	return nil
}

// structureToString re-encodes the raw decoded value so toml.Decode can
// parse it into the struct (the BurntSushi Primitive dance is not
// available from inside UnmarshalTOML).
func structureToString(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(b)
}
```

Hmm - do not use that JSON dance. BurntSushi's UnmarshalTOML receives
`v interface{}` as a `map[string]interface{}` tree that `toml.Decode`
CANNOT re-parse. The clean way: make the variant tables `map[string]X`
and let BurntSushi decode nested tables natively - i.e., Theme holds
`Variants map[string]StyleTable` with a custom UnmarshalTOML that
recurses with a tiny typed decoder over the map tree:

```go
// rawStyle decodes a style from a decoded TOML table (map).
func rawStyle(v interface{}) (Style, error) {
	raw, ok := v.(map[string]interface{})
	if !ok {
		return Style{}, fmt.Errorf("expected a style table")
	}
	s := Style{}
	for k, val := range raw {
		switch k {
		case "fg":
			s.Fg, _ = val.(string)
		case "bg":
			s.Bg, _ = val.(string)
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

// rawStyleTable decodes a full style table (normal/status/index/...).
func rawStyleTable(v interface{}) (StyleTable, error) {
	raw, ok := v.(map[string]interface{})
	if !ok {
		return StyleTable{}, fmt.Errorf("expected a style table")
	}
	var t StyleTable
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
					// mixed shape: fg/bg strings at this level are the
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
						style, err := rawStyle(tv)
						if err != nil {
							return StyleTable{}, err
						}
						if tn == "attrs" {
							t.Index.Tag.Default.Attrs = style.Attrs
							continue
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
```

So PagerStyleTable becomes:

```go
type PagerStyleTable struct {
	Header     Style
	HdrDefault Style
	Quoted     [6]Style
	Signature  Style
	Attachment Style
}
```

with `Theme.UnmarshalTOML`:

```go
func (t *Theme) UnmarshalTOML(v interface{}) error {
	raw, ok := v.(map[string]interface{})
	if !ok {
		return fmt.Errorf("theme: expected a table")
	}
	t.Variants = map[string]StyleTable{}
	for name, val := range raw {
		if name == "default" {
			s, ok := val.(string)
			if !ok {
				return fmt.Errorf("theme.default: expected a string")
			}
			t.Default = s
			continue
		}
		table, err := rawStyleTable(val)
		if err != nil {
			return fmt.Errorf("theme.%s: %w", name, err)
		}
		t.Variants[name] = table
	}
	return nil
}
```

`Resolved` - inheritance + palette resolution, pure config (no
lipgloss):

```go
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
```

Config struct gains the fields + validation (in validate()):

```go
type Config struct {
	UI         UI                           `toml:"ui"`
	Views      map[string]View              `toml:"view"`
	TagGroups  map[string]core.TagGroup     `toml:"tag-groups"`
	Bindings   map[string]map[string]string `toml:"bindings"`
	TagActions map[string]string            `toml:"tag-actions"`
	Palette    Palette                      `toml:"palette"`
	Theme      Theme                        `toml:"theme"`
}
```

Default() fills the reference palette and a dark variant with the
spec's onedark styles (the exact tables from the spec section 3;
"normal" = {fg base05, bg base00}, indicator = {fg base00, bg base0A},
status = {fg base05, bg base01}, progress = {fg base00, bg base0D},
index slots per the muttrc port, pager quoted0-5 per the muttrc
quoted colors, and the [ui] tables: `UI{Keymap: "vim", Tags: UITags{Max: 2}, Glyphs: Glyphs{Staged: "*", ProgressFill: "#", ProgressEmpty: "-", StatuslineSeparator: "|"}}`).

Default theme must also be present: `Theme{Default: "dark", Variants: {"dark": ...}}`.

validate() additions: palette colors hex; style fg/bg are hex or base
palette names; attrs subset of {bold, italic, underline, reverse};
theme default variant exists.

`src/config/store.go`:

```go
func (s *Store) SetThemeVariant(name string) error {
	s.mu.Lock()
	if _, ok := s.cfg.Theme.Variants[name]; !ok {
		s.mu.Unlock()
		return fmt.Errorf("theme: no variant %q", name)
	}
	s.cfg.Theme.Default = name
	s.mu.Unlock()
	s.notify("theme")
	return nil
}
```

`src/app/app.go` - subscribe the theme section and pass the theme data:

```go
st.Subscribe("theme", func() { bus.Publish(core.ConfigChanged{Section: "theme"}) })
...
prog := tea.NewProgram(tui.New(view, busCh, cfg.Bindings, cfg.TagActions, bus, cfg.Theme, cfg.Palette, cfg.UI), tea.WithAltScreen())
```

`src/tui/styles.go` - the config-to-lipgloss converter (replaces the
hardcoded set):

```go
// ResolveStyles converts the config theme data into the render style
// set. Style ids the config does not define resolve to normal. An
// empty theme (no config file provided one) falls back to the
// hardcoded onedark defaults - the reference port.
func ResolveStyles(theme config.Theme, palette config.Palette) Styles {
	if theme.Default == "" || len(theme.Variants) == 0 {
		return DefaultStyles()
	}
	ids := theme.Resolved(palette, theme.Default)
	to := func(id string, base lipgloss.Style) lipgloss.Style {
		s, ok := ids[id]
		if !ok {
			return base
		}
		if s.Fg != "" {
			base = base.Foreground(lipgloss.Color(s.Fg))
		}
		if s.Bg != "" {
			base = base.Background(lipgloss.Color(s.Bg))
		}
		for _, a := range s.Attrs {
			switch a {
			case "bold":
				base = base.Bold(true)
			case "italic":
				base = base.Italic(true)
			case "underline":
				base = base.Underline(true)
			case "reverse":
				base = base.Reverse(true)
			}
		}
		return base
	}
	normal := to("normal", lipgloss.NewStyle())
	return Styles{
		Normal:    normal,
		Indicator: to("indicator", normal),
		Status:    to("status", normal),
		Progress:  to("progress", normal),
		Index: IndexStyles{
			Number: to("index.number", normal), Date: to("index.date", normal),
			Author: to("index.author", normal), Subject: to("index.subject", normal),
			Flags: to("index.flags", normal), Staged: to("index.staged", normal),
			Ghost: to("index.ghost", normal),
			Tag:   func(name string) lipgloss.Style {
				if s, ok := ids["index.tag."+name]; ok {
					return to("index.tag."+name, normal)
				}
				return to("index.tag", normal)
			},
		},
		Pager: PagerStyles{
			Header: to("pager.header", normal), HdrDefault: to("pager.hdrdefault", normal),
			Quoted: [6]lipgloss.Style{
				to("pager.quoted0", normal), to("pager.quoted1", normal),
				to("pager.quoted2", normal), to("pager.quoted3", normal),
				to("pager.quoted4", normal), to("pager.quoted5", normal),
			},
			Signature: to("pager.signature", normal), Attachment: to("pager.attachment", normal),
		},
	}
}
```

IndexStyles.Tag becomes a func (per-tag styles, R11) - update
index.go's tagGlyphs to take `st.Index.Tag` and call it per tag:

```go
func tagGlyphs(tags []string, max int, tagStyle func(string) lipgloss.Style) string
```

The existing model_test.go call sites of tui.New must be updated to
the new signature (mechanical; assertions stay unchanged).

`src/tui/model.go` - Model gains `theme config.Theme, palette
config.Palette, ui config.UI` and a `styles Styles` field; New
computes `styles: ResolveStyles(theme, palette)`; View() and
statusLine use `m.styles` instead of DefaultStyles(); renderRow and
statusLine gain `ui config.UI` and consume the config-data glyphs
(the staged glyph from ui.Glyphs.Staged in the staged flags slot, the
tag slot max from ui.Tags.Max in tagGlyphs, the progress fill/empty
from ui.Glyphs in progressBar - R11: never hardcoded); the Update
loop adds:

`src/tui/statusline.go` - the composable segment model (the
powerline-go pattern, adapted to the current UI model):

```go
// statusSegment is one composable cell of the status line: content,
// style, and a drop priority (powerline-go Segment, cut to notmutt).
// The lower the priority, the earlier the segment drops when the
// row exceeds the terminal width.
type statusSegment struct {
	content  string
	style    lipgloss.Style // zero value inherits the status style
	priority int
}
```

statusLine(st, ui, d statusData) builds the segments from a small
data struct, joins the LEFT group with the separator glyph
(ui.Glyphs.StatuslineSeparator, default "|"), right-aligns the RIGHT
group (the R15 progress region; lowest priority), and pads the gaps
with the status background so the row always covers the full width
(R11 slot reservation):

```go
type statusData struct {
	view    string
	visible int
	prog    *core.Progress // nil = no job on
	on      bool
}
```

Composition rules (powerline-go's, sized down):

- Left group: one segment per datum (view name, visible count;
  future segments - keymap indicator, notification - append the same
  way). Segments join with the separator glyph; the seam renders fg
  = previous segment's bg on bg = next segment's bg (the powerline
  chevron); when the two bgs are equal the separator renders in the
  previous segment's fg instead - a plain "|" on the shared status
  background. HideSeparators is not needed (no powerline symbol
  templates); the right group has no separator, it abuts the gap.
- Width fitting follows truncateRow: when the composed row exceeds
  the terminal width, the lowest-priority segments drop first -
  progress region (0), then the visible count (5); the view name
  (10) always survives. This is the old narrow-terminal bar-drop
  semantics, data-driven.
- The status bar's own content never leaves the status style: a
  segment's zero style inherits st.Status (per-segment styles land
  when a segment needs one - a notification segment, R4 era).

statusLine builds the segments from the data struct and returns the
composed, width-fitted row (progressBar keeps its pure function; the
segment machinery composes it into the row).

`src/tui/statusline_test.go`:

```go
func TestStatusLineSegments(t *testing.T) {
	ui := config.Default().UI
	row := statusLine(DefaultStyles(), ui, statusData{view: "inbox", visible: 5})
	if !strings.Contains(row, "inbox") || !strings.Contains(row, "5") {
		t.Fatalf("segments must render: %q", row)
	}
	if !strings.Contains(row, ui.Glyphs.StatuslineSeparator) {
		t.Fatalf("segments must join with the separator: %q", row)
	}
}

func TestStatusLineDropsLowPriorityOnNarrow(t *testing.T) {
	ui := config.Default().UI
	d := statusData{view: "inbox", visible: 5, prog: &core.Progress{Done: 1, Total: 5}, on: true}
	full := statusLine(DefaultStyles(), ui, d)
	if !strings.Contains(full, ui.Glyphs.ProgressFill) {
		t.Fatalf("progress segment must render at full width: %q", full)
	}
	narrow := statusLineWidth(DefaultStyles(), ui, d, 8)
	if strings.Contains(narrow, ui.Glyphs.ProgressFill) {
		t.Fatalf("progress must drop first on a narrow terminal: %q", narrow)
	}
	if !strings.Contains(narrow, "inbox") {
		t.Fatalf("the view name must survive: %q", narrow)
	}
}
```

(statusLineWidth is statusLine with an explicit width parameter for
testability; the model calls it with m.width.)

```go
case core.ConfigChanged:
	if e.Section == "theme" {
		m.styles = ResolveStyles(m.theme, m.palette)
	}
```

(statusLine signature: `statusLine(st Styles) string` - it already
styles the progress region; apply Progress on the fill glyphs and
Normal on the empty cells.)

Keep `DefaultStyles()` for the tests that call renderRow directly
(simplest: leave DefaultStyles as the fallback set used when the
theme data is empty - ResolveStyles with an empty Theme returns the
normal-derived styles, so tests can keep using it; drop DefaultStyles
only if ResolveStyles covers the old call sites).

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./config/ ./tui/ ./app/`
Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(config): R11 theme data with palette resolution and live variants"
```

## Task 3: Pager and open flow

`o` on a thread row opens the pager: the thread's headers + bodies
rendered in a scrollable viewport. Content loads on open only (R13
two-step). The worker already has ActThread (show --body=false,
headers + paths); the new `mail` package parses the message FILES with
go-message (R6) into F1-clean render lines.

**Files:**
- Create: `src/mail/thread.go` + `src/mail/thread_test.go` (the render pipeline, no TUI imports)
- Create: `src/tui/pager.go`
- Modify: `src/core/bus.go` (ThreadLoaded event)
- Modify: `src/app/app.go` (open handler wiring)
- Modify: `src/tui/model.go` (mode state, open action, ThreadLoaded handling)
- Test: `src/tui/model_test.go` (pager mode tests)

- [ ] **Step 1: Write the failing tests**

`src/mail/thread_test.go`:

```go
package mail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"notmutt/core"
)

// fixture writes a minimal multipart/alternative message and returns
// its path. Mail content in tests is synthetic; no real mail is used.
func fixture(t *testing.T, body string) string {
	t.Helper()
	msg := "From: a@example.com\nTo: b@example.com\n" +
		"Subject: hello\nDate: Tue, 01 Jan 2019 00:00:00 +0000\n" +
		"MIME-Version: 1.0\nContent-Type: text/plain; charset=utf-8\n\n" +
		body
	p := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(p, []byte(msg), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRenderThread(t *testing.T) {
	body := "line one\n> quoted a\n> > quoted deep\n-- \nsig line\n"
	msgs := []core.Message{{ID: "m1", ThreadID: "t1", Paths: []string{fixture(t, body)}}}
	lines, err := RenderThread(msgs)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(lines, "\n")
	for _, want := range []string{"hello", "a@example.com", "line one", "quoted a", "quoted deep", "sig line"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}
}

func TestRenderThreadStripsControls(t *testing.T) {
	body := "evil\x1b[31mred\x07\n"
	msgs := []core.Message{{ID: "m1", ThreadID: "t1", Paths: []string{fixture(t, body)}}}
	lines, err := RenderThread(msgs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(lines, "\n"), "\x1b") {
		t.Fatalf("control chars leaked into the pager content:\n%v", lines)
	}
}

func TestRenderThreadMissingFile(t *testing.T) {
	msgs := []core.Message{{ID: "m1", ThreadID: "t1", Paths: []string{"/nonexistent"}}}
	if _, err := RenderThread(msgs); err == nil {
		t.Fatal("missing file must error")
	}
}
```

`src/tui/model_test.go` - add pager mode tests (extend the existing
fake worker pattern; open the cursor row):

```go
func TestOpenSwitchesToPager(t *testing.T) {
	// the existing harness shape (model_test.go): build the model with
	// bindings {"index": {"o": "open"}}, register a fake open handler
	// that publishes a ThreadLoaded for one fixture message, then:
	m.Update(tea.KeyMsg{Runes: []rune("o")})
	if m.mode != "pager" {
		t.Fatalf("open must switch to pager mode, mode=%q", m.mode)
	}
	if m.pager == nil || len(m.pager.lines) == 0 {
		t.Fatal("pager content missing")
	}
}
```

The harness: the model's `openHandler` field holds the tui.SetOpenHandler
callback (mirror of SetApplyHandler). The test sets it to a func that
publishes `core.ThreadLoaded{ThreadID: "t1", Msgs: []core.Message{{ID:
"m1", ThreadID: "t1", Paths: []string{fixturePath}}}}` on the model's
bus (or calls a helper that injects the event directly); the fixture
message file is written in the test with the same shape as
mail/thread_test.go's fixture.

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./mail/ ./tui/`
Expected: FAIL (mail package does not exist; open is unbound).

- [ ] **Step 3: Implement**

`src/mail/thread.go` - the content pipeline. Pure mail logic (R6, the
library is the parser):

```go
package mail

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/emersion/go-message/mail"

	"notmutt/core"
)

// RenderThread parses each message's file and produces the pager's
// render lines: per message a header block, then the body with quoted
// levels and signature, then attachment lines. All text is stripped of
// C0/DEL/C1 control chars before it leaves this package (F1) - the
// TUI never sees raw mail content.
func RenderThread(msgs []core.Message) ([]string, error) {
	var lines []string
	for i, m := range msgs {
		if i > 0 {
			lines = append(lines, "")
		}
		if len(m.Paths) == 0 {
			return nil, fmt.Errorf("message %s: no path", m.ID)
		}
		parsed, err := ParseMessage(m.Paths[0])
		if err != nil {
			return nil, err
		}
		lines = append(lines, renderMessage(parsed)...)
	}
	return lines, nil
}

type Message struct {
	From string
	Date string
	Subject string
	Parts []Part
	Attachments []Attachment
}

type Part struct {
	Body     string
	Quoted   int   // 0..5, capped
	Signature bool
}

type Attachment struct {
	Name string
	Size int64
}

// ParseMessage opens one mail file and reads its structure with
// go-message: the text/plain inline parts become body parts (quoted
// depth + signature split), other inline parts are skipped, and
// attachment parts are listed with their sizes.
func ParseMessage(path string) (*Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	mr, err := mail.CreateReader(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	hdr := mr.Header
	m := &Message{}
	if addrs, err := hdr.AddressList("From"); err == nil && len(addrs) > 0 {
		m.From = addrs[0].Address
	}
	m.Date = hdr.Get("Date")
	m.Subject = hdr.Get("Subject")
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		switch h := p.Header.(type) {
		case *mail.InlineHeader:
			if ct, _, _ := h.ContentType(); ct == "text/plain" {
				data, err := io.ReadAll(p.Body)
				if err != nil {
					return nil, err
				}
				m.Parts = append(m.Parts, splitBody(string(data)))
			}
		case *mail.AttachmentHeader:
			name, _ := h.Filename()
			if name == "" {
				name = "attachment"
			}
			size, err := io.Copy(io.Discard, p.Body)
			if err != nil {
				return nil, err
			}
			m.Attachments = append(m.Attachments, Attachment{Name: name, Size: size})
		}
	}
	return m, nil
}

// splitBody splits the raw text into parts: quoted depth by leading
// ">" count (capped at 5), signature after the first standalone "-- ".
func splitBody(text string) []Part {
	var parts []Part
	sig := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if !sig && line == "-- " {
			sig = true
		}
		depth := 0
		for depth < 5 && strings.HasPrefix(line, ">") {
			depth++
			line = line[1:]
		}
		line = strings.TrimPrefix(line, " ")
		parts = append(parts, Part{Body: line, Quoted: depth, Signature: sig})
	}
	return parts
}

// stripControls drops C0/DEL/C1 runes (F1; the same policy as the
// TUI's index renderer, enforced here at the mail boundary).
func stripControls(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || (r >= 0x7F && r <= 0x9F) }) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 0x20 || (r >= 0x7F && r <= 0x9F) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func renderMessage(m *Message) []string {
	var lines []string
	add := func(s string) { lines = append(lines, stripControls(s)) }
	add(m.Subject)
	add(m.From + "  " + m.Date)
	for _, p := range m.Parts {
		body := p.Body
		if p.Signature {
			body = "-- " + body
		}
		add(body)
	}
	for _, a := range m.Attachments {
		add(fmt.Sprintf("attachment: %s (%d bytes)", a.Name, a.Size))
	}
	return lines
}
```

`src/core/bus.go`:

```go
type ThreadLoaded struct {
	ThreadID string
	Msgs     []core.Message
	Err      error
}
```

`src/tui/model.go`:

- Model gains `mode string` ("index" default), `pager *pager`.
- New signature: `New(view, ch, bindings, tagActions, bus, theme, palette)` (bindings becomes the per-context map; Task 4 finalizes).
- Update: on KeyMsg, dispatch on `m.keys[m.mode]`; new action "open" in index mode:
  - `tui.SetOpenHandler` (mirror of SetApplyHandler) - the app does the fetch and publishes ThreadLoaded.
  - Actually the model can't call the worker; keep the handler pattern:

```go
case "open":
	if m.mode == "index" {
		row, ok := m.view.CursorRow()
		if !ok {
			break
		}
		tid := row.ThreadID
		if tid == "" && row.Msg != nil {
			tid = row.Msg.ThreadID
		}
		if tid != "" {
			m.openHandler(tid)
		}
	}
```

  In app.go: `tui.SetOpenHandler(func(threadID string) { go func() { rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActThread, ThreadID: threadID}); if err != nil { bus.Publish(core.ThreadLoaded{ThreadID: threadID, Err: err}); return }; bus.Publish(core.ThreadLoaded{ThreadID: threadID, Msgs: rpl.Msgs}) }() })`. The open handler is idempotent on the model side: a ThreadLoaded for the ALREADY-OPEN thread re-renders it (pagerThreadID guard below is `!=` for this reason - re-opening the same thread is a reload).

- Update: on `core.ThreadLoaded`:

```go
case core.ThreadLoaded:
	if e.Err != nil {
		m.mode = "index"
		m.pager = nil
		break
	}
	if e.ThreadID != pagerThreadID(m.pager) {
		lines, err := mail.RenderThread(e.Msgs)
		if err != nil {
			m.mode = "index"
			break
		}
		m.pager = newPager(e.ThreadID, lines)
	}
	m.mode = "pager"
```

`src/tui/pager.go`:

```go
package tui

import (
	"strings"

	"github.com/charmbracelet/bubbletea/viewport"
	"github.com/charmbracelet/lipgloss"

	"notmutt/core"
)

// pager holds the open thread's render lines and the scroll viewport.
// The content is bounded (one thread), so the viewport component owns
// the scroll state (the index stays windowed - 129k rows must never
// flatten).
type pager struct {
	threadID string
	lines    []string
	vp       viewport.Model
}

func newPager(threadID string, lines []string) *pager {
	return &pager{threadID: threadID, lines: lines}
}

func (p *pager) setSize(w, h int, st Styles) {
	p.vp = viewport.New(w, h)
	p.vp.Style = st.Normal
	p.vp.SetContent(strings.Join(p.lines, "\n"))
}

func (p *pager) scroll(delta int) {
	p.vp.LineDown(delta)
}

// render styles the content lines with the pager styles (quoted
// levels, signature, attachment) and hands the styled text to the
// viewport.
func (p *pager) render(st Styles) string {
	var b strings.Builder
	for _, l := range p.lines {
		b.WriteString(styleLine(l, st))
		b.WriteByte('\n')
	}
	p.vp.SetContent(b.String())
	return p.vp.View()
}

func styleLine(l mail.Line, st Styles) string {
	switch l.Kind {
	case mail.LineSubject:
		return st.Pager.Header.Render(l.Text)
	case mail.LineHeader:
		return st.Pager.HdrDefault.Render(l.Text)
	case mail.LineBody:
		return st.Pager.Quoted[l.Quoted].Render(l.Text)
	case mail.LineSignature:
		return st.Pager.Signature.Render(l.Text)
	case mail.LineAttachment:
		return st.Pager.Attachment.Render(l.Text)
	}
	return st.Normal.Render(l.Text)
}
```

The quoting styles need to come from the mail parse (Part.Quoted is
not carried into the render lines as written). Fix: RenderThread
returns STRUCTURED lines (a `Line` type with the style id), so the
pager can style them (styleLine above consumes them):

```go
// mail/thread.go
type LineKind int

const (
	LineSubject LineKind = iota
	LineHeader
	LineBody
	LineSignature
	LineAttachment
)

type Line struct {
	Text  string
	Kind  LineKind
	Quoted int // LineBody only, 0..5
}
```

RenderThread returns []Line (stripControls still applied at build);
renderMessage appends Line{...} per source. `pager.render` maps Line ->
styled string:

```go
func (p *pager) render(st Styles) string {
	var b strings.Builder
	for _, l := range p.lines {
		var s lipgloss.Style
		switch l.Kind {
		case mail.LineSubject:
			s = st.Pager.Header
		case mail.LineHeader:
			s = st.Pager.HdrDefault
		case mail.LineBody:
			s = st.Pager.Quoted[l.Quoted]
		case mail.LineSignature:
			s = st.Pager.Signature
		case mail.LineAttachment:
			s = st.Pager.Attachment
		}
		b.WriteString(s.Render(l.Text))
		b.WriteByte('\n')
	}
	p.vp.SetContent(b.String())
	return p.vp.View()
}
```

The viewport needs a size: on WindowSizeMsg in pager mode, call
`m.pager.setSize(m.width, pagerHeight, m.styles)`; in View() in pager
mode, `m.pager.render(m.styles)`. The viewport height: height - 2
(status + keyhint rows are reserved; Task 4 adds the keyhint row - for
this task reserve 2: status + keyhint placeholder empty).

View():

```go
func (m Model) View() string {
	if m.mode == "pager" && m.pager != nil {
		var b strings.Builder
		b.WriteString(m.pager.render(m.styles))
		b.WriteString("\n")
		b.WriteString(m.statusLine(m.styles))
		b.WriteByte('\n')
		return b.String()
	}
	... existing index path ...
}
```

WindowSizeMsg: `if m.mode == "pager" && m.pager != nil { m.pager.setSize(m.width, m.height-2, m.styles) }`.

The pager "back" action (index context has none): add "back" to the
pager context's action set (Task 4 defines the contexts; here, bind
q -> "back" in the pager table wired through the existing keys map,
and the model handles "back" -> mode = index). This needs Task 4's
per-context dispatch to be in place... so ORDER: the model must
already dispatch on `m.bindings[m.mode]` for the pager keys to work.
Put the per-context dispatch in THIS task (it is a prerequisite of the
pager), and Task 4 then only adds the emacs schemes, validation and
the keyhint bar.

Model bindings field: `bindings map[string]map[string]string` (per
context; New's `bindings` argument, Task 2's signature). Update
dispatch:

```go
case tea.KeyMsg:
	km := m.bindings[m.mode]
	if km == nil {
		km = m.bindings["index"]
	}
	switch actionForKey(msg, km) {
	...
	}
```

with

```go
// actionForKey resolves the pressed key: runes first (plain keys),
// then BubbleTea's canonical name ("ctrl+n", "alt+v", ...) so
// control keys are bindable.
func actionForKey(msg tea.KeyMsg, km map[string]string) string {
	if a, ok := km[string(msg.Runes)]; ok {
		return a
	}
	return km[msg.String()]
}
```

Pager bindings (vim defaults, config.Default()):

```go
"pager": {
	"j": "scroll-down", "k": "scroll-up",
	"ctrl+d": "page-down", "ctrl+u": "page-up",
	"g": "scroll-top", "G": "scroll-bottom",
	"q": "back",
},
```

and the model pager key handling:

```go
case "scroll-down":
	if m.mode == "pager" && m.pager != nil {
		m.pager.vp.LineDown(1)
	}
case "scroll-up":
	if m.mode == "pager" && m.pager != nil {
		m.pager.vp.LineUp(1)
	}
case "page-down":
	if m.mode == "pager" && m.pager != nil {
		m.pager.vp.HalfPageDown()
	}
case "page-up":
	if m.mode == "pager" && m.pager != nil {
		m.pager.vp.HalfPageUp()
	}
case "scroll-top":
	if m.mode == "pager" && m.pager != nil {
		m.pager.vp.GotoTop()
	}
case "scroll-bottom":
	if m.mode == "pager" && m.pager != nil {
		m.pager.vp.GotoBottom()
	}
case "back":
	if m.mode == "pager" {
		m.mode = "index"
	}
```

`src/tui/hooks.go` gains `SetOpenHandler` (mirror the apply handler
shape).

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./mail/ ./tui/ ./app/ ./core/`
Expected: all pass; `go test -race ./tui/ ./app/` clean.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(tui): pager - open a thread and read it"
```

## Task 4: R9 keybindings - schemes, contexts, keyhints

Keybinding data finalization: vim and emacs default schemes selected
by `[ui] keymap`, per-context tables, per-context validation, and the
keyhint bar derived from the active context's binding map.

**Files:**
- Modify: `src/config/config.go` (scheme defaults + merge, [ui] tags/glyphs tables)
- Modify: `src/app/app.go` (per-context validation)
- Modify: `src/tui/model.go` (keyhint row in View)
- Create: `src/tui/keyhints.go`
- Test: `src/config/config_test.go`, `src/tui/model_test.go`

- [ ] **Step 1: Write the failing tests**

`src/config/config_test.go`:

```go
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
}
```

(CORRECTED on delivery: the scheme swap is a LOAD-time selection, not
a Default() mutation - Default() returns the vim scheme; Load() nils
the Bindings field BEFORE decode so the file's `[bindings.*]` tables
overlay the selected scheme only. The earlier draft mutated
cfg.UI.Keymap after Default(), impossible with value semantics, and
would have leaked vim defaults into the emacs scheme.)

`src/tui/model_test.go`:

```go
func TestKeyhintBar(t *testing.T) {
	km := map[string]string{"j": "cursor-down", "q": "quit"}
	hint := keyhintRow(km, 30)
	if !strings.Contains(hint, "j cursor-down") || !strings.Contains(hint, "q quit") {
		t.Fatalf("hint must derive from the binding map: %q", hint)
	}
	if runewidth.StringWidth(hint) > 30 {
		t.Fatalf("hint exceeds width: %q", hint)
	}
}

func TestPagerKeysOnlyInPager(t *testing.T) {
	// keys: index {"o": "open"}, pager {"q": "back"}; cursor on a row.
	// Pressing q in INDEX mode must not go back (nothing happens).
	// After o, q returns to index.
}
```

- [ ] **Step 2: Run to verify they fail**

Expected: FAIL (keyhintRow missing; schemes not implemented).

- [ ] **Step 3: Implement**

`src/config/config.go`:

- The [ui] tables:

```go
type UI struct {
	Keymap string   `toml:"keymap"`
	Tags   UITags   `toml:"tags"`
	Glyphs Glyphs   `toml:"glyphs"`
}

type UITags struct {
	Max int `toml:"max"`
}

type Glyphs struct {
	Staged              string `toml:"staged"`
	ProgressFill        string `toml:"progress_fill"`
	ProgressEmpty       string `toml:"progress_empty"`
	StatuslineSeparator string `toml:"statusline_separator"`
}
```

- Scheme tables (package vars):

```go
var vimScheme = map[string]map[string]string{
	"index": {
		"j": "cursor-down", "k": "cursor-up", "o": "open", "q": "quit",
		"r": "toggle-read", "a": "archive", "d": "delete",
		"u": "undo", "$": "apply",
	},
	"pager": {
		"j": "scroll-down", "k": "scroll-up",
		"ctrl+d": "page-down", "ctrl+u": "page-up",
		"g": "scroll-top", "G": "scroll-bottom",
		"q": "back",
	},
}

var emacsScheme = map[string]map[string]string{
	"index": {
		"ctrl+n": "cursor-down", "ctrl+p": "cursor-up", "o": "open",
		"q": "quit", "r": "toggle-read", "a": "archive", "d": "delete",
		"u": "undo", "$": "apply",
	},
	"pager": {
		"ctrl+n": "scroll-down", "ctrl+p": "scroll-up",
		"ctrl+v": "page-down", "alt+v": "page-up",
		"ctrl+g": "back", "q": "quit",
	},
}
```

- Default() drops the hardcoded Bindings literal and builds the
  scheme's tables (with file overlay handled in Load): Default sets
  Bindings from the scheme selected by cfg.UI.Keymap; Load applies
  the same selection AFTER decoding so a file-set keymap switches the
  defaults; file tables overlay per key:

```go
func scheme(keymap string) map[string]map[string]string {
	if keymap == "emacs" {
		return emacsScheme
	}
	return vimScheme
}

// mergeBindings overlays the file's per-key bindings on the scheme
// defaults; a context table missing from the file is the whole scheme
// table.
func mergeBindings(file map[string]map[string]string, keymap string) map[string]map[string]string {
	out := map[string]map[string]string{}
	for ctx, km := range scheme(keymap) {
		merged := map[string]string{}
		for k, v := range km {
			merged[k] = v
		}
		for k, v := range file[ctx] {
			merged[k] = v
		}
		out[ctx] = merged
	}
	return out
}
```

In Load(): after DecodeFile and before validate:
`cfg.Bindings = mergeBindings(cfg.Bindings, cfg.UI.Keymap)`.

Validate: `[ui]` tables validated (max >= 0, glyphs non-empty).

`src/app/app.go` - validateBindings becomes per-context:

```go
func validateBindings(cfg *config.Config) error {
	used := map[string]bool{}
	for ctx, km := range cfg.Bindings {
		for key, action := range km {
			if tui.Actions[ctx][action] {
				continue
			}
			if ctx != "index" {
				return fmt.Errorf("bindings.%s: unknown action %q", ctx, action)
			}
			if _, ok := cfg.TagActions[action]; !ok {
				return fmt.Errorf("bindings.%s: unknown action %q", ctx, action)
			}
			used[action] = true
		}
	}
	for name := range cfg.TagActions {
		if tui.Actions["index"][name] {
			return fmt.Errorf("tag action %q collides with a builtin action", name)
		}
		if !used[name] {
			return fmt.Errorf("tag action %q is not bound", name)
		}
	}
	return nil
}
```

`src/tui/model.go` - the Actions vocabulary becomes per-context:

```go
var Actions = map[string]map[string]bool{
	"index": {
		"cursor-down": true, "cursor-up": true, "open": true,
		"quit": true, "undo": true, "apply": true,
	},
	"pager": {
		"scroll-down": true, "scroll-up": true,
		"page-down": true, "page-up": true,
		"scroll-top": true, "scroll-bottom": true,
		"back": true, "quit": true,
	},
}
```

View() renders the keyhint row above the status line (both modes):

```go
b.WriteString(keyhintRow(m.bindings[m.mode], m.width))
b.WriteString(m.statusLine(m.styles))
b.WriteByte('\n')
```

(the index list height becomes height-2; the pager viewport height
becomes height-2.)

`src/tui/keyhints.go`:

```go
package tui

import (
	"sort"
	"strings"
)

// keyhintRow renders the active context's bindings as "key action"
// pairs, sorted by key and truncated to the terminal width (R11 slot
// reservation: the row never shifts with content). Labels are the
// action names - config data, never hardcoded.
func keyhintRow(km map[string]string, w int) string {
	keys := make([]string, 0, len(km))
	for k := range km {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, k+" "+km[k])
	}
	line := strings.Join(parts, "  ")
	if w > 0 {
		return truncCells(line, w)
	}
	return line
}
```

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./...`; `go test -race ./tui/ ./app/ ./core/` clean.

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(tui): R9 per-context keymaps with emacs scheme and keyhints"
```

DELIVERED (2026-08-14, commits 24eb1222 + b7563e54 + 1be1d979):
- `[bindings.*]` is the section name (the draft's `[binding.*]` was a
  typo; the singular form is a pinned strict-load error).
- Delivered in addition to the tests above: TestKeymapFileOverlay
  (per-key overlay on the scheme, no replacement, no vim leakage),
  TestLoadUnknownBindingContext/TestValidateUnknownBindingContext
  (unknown binding contexts are Load errors - R8 strict load
  extended to the context level, since the section-level
  undecoded-key check cannot see inside the map), TestPagerQuitKeyExits
  (emacs pager q = quit exits; vim pager q = back returns), plus
  app-level per-context validation cases.
- The keyhint row renders in ALL three render paths (index list,
  pager, empty+progress); index list height is height-2; labels are
  the action names from the active context's binding map.
## Task 5: docs pin + soak

- [ ] **Step 1: Pin the milestone**

Add a section to `docs/superpowers/specs/2026-08-14-ui-milestone-design.md`
noting the delivered shapes (scheme tables, [ui] tables, Line kind
model, per-context validation) if any deviated from the spec, and mark
the acceptance items. Commit with the `AI-assisted: deepseek` trailer:

```bash
git commit -m "docs: pin the M3 delivered shapes in spec and plan

AI-assisted: deepseek"
```

- [ ] **Step 2: Run the full suite + manual acceptance**

`go test ./...` and `go test -race ./...` clean; run the client
against the live mailbox and walk the manual acceptance items
(spec section 1, items 5-7): open a real thread and scroll; switch
the theme variant (via a temporary call in app.go or the config
file); the bar and hints never shift.
