package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"notmutt/config"
)

// Truecolor is the baseline, no 256-color mapping (R11). Pin the renderer
// profile so colors never degrade: profile detection reads the terminal
// behind stdout, which is a pipe under `go test` - an Ascii profile would
// silently drop every style and break the render contract.
func init() {
	lipgloss.SetColorProfile(termenv.TrueColor)
}

// Styles is the full style surface the TUI renders with. ResolveStyles
// builds it from config data; the hardcoded onedark values in
// DefaultStyles are the reference port (muttrc/theme/onedark.muttrc)
// fallback.
type Styles struct {
	Normal         lipgloss.Style
	Indicator      lipgloss.Style
	Status         lipgloss.Style
	View           lipgloss.Style // statusline view pill
	Count          lipgloss.Style // statusline count pill
	Account        lipgloss.Style // statusline account pill (R2)
	Progress       lipgloss.Style
	Error          lipgloss.Style
	Tabbar         lipgloss.Style // tab strip bar (inactive tabs, padding)
	TabActive      lipgloss.Style // tab strip active-tab pill
	ComposeLabel   lipgloss.Style // compose settings label (the two-column form + the dialogue box)
	ComposeDivider lipgloss.Style // compose section bar (--- Attachments / --- Preview)
	Index          IndexStyles
	Pager          PagerStyles
	sgr            sgrSet // precomputed hot-path fragments (index rows, pager lines)
}

type IndexStyles struct {
	Number  lipgloss.Style
	Date    lipgloss.Style
	Author  lipgloss.Style
	Subject lipgloss.Style
	Flags   lipgloss.Style
	Staged  lipgloss.Style
	Ghost   lipgloss.Style
	Tag     func(name string) lipgloss.Style // per-tag styles (R11)
}

type PagerStyles struct {
	Header     lipgloss.Style
	HdrDefault lipgloss.Style
	Quoted     [6]lipgloss.Style
	Signature  lipgloss.Style
	Attachment lipgloss.Style
}

// sgr is a style's precomputed render fragments: the SGR open sequence
// and its reset. sgrSetOf computes them once at style resolution time;
// the render hot paths (index rows, pager lines) join open + text +
// close with string ops instead of calling Style.Render per slot (the
// measured 58% of the frame build: per-call hex parse, border
// pipeline, and grapheme splits).
type sgr struct {
	open  string
	close string
}

func sgrOf(st lipgloss.Style) sgr {
	open := strings.TrimSuffix(st.Render(""), "\x1b[0m")
	if open == "" {
		return sgr{}
	}
	return sgr{open: open, close: "\x1b[0m"}
}

// render joins the SGR open, the text, and the reset - byte-identical
// to Style.Render for the single-line, unpadded styles the hot paths
// use (lipgloss styles each line of a multi-line input separately, so
// this stays single-line; mail content is sanitized and line-split
// before it gets here). Tabs expand to 4 spaces like lipgloss's
// Render, so direct constructions match too.
func (g sgr) render(text string) string {
	if strings.ContainsRune(text, '\t') {
		text = strings.ReplaceAll(text, "\t", "    ")
	}
	if g.open == "" {
		return text
	}
	return g.open + text + g.close
}

// sgrSet precomputes the SGR fragments of the render hot paths.
type sgrSet struct {
	normal, indicator, ghost                     sgr
	stagedNormal, stagedIndicator, stagedGhost   sgr
	border                                       sgr // the popup border: the indicator's fg over the normal bg (no fill)
	number, flags, date, author, subject, staged sgr
	tag                                          func(name string) sgr
	pagerHdr                                     sgr
	pagerDef                                     sgr
	pagerSig                                     sgr
	pagerAtt                                     sgr
	pagerErr                                     sgr
	pagerQuoted                                  [6]sgr
	pagerKey                                     string // fingerprint of the pager-relevant opens (styleKey)
}

func sgrSetOf(st Styles) sgrSet {
	tagStyle := st.Index.Tag
	cache := make(map[string]sgr)
	sg := sgrSet{
		normal:          sgrOf(st.Normal),
		indicator:       sgrOf(st.Indicator),
		ghost:           sgrOf(st.Index.Ghost),
		stagedNormal:    sgrOf(st.Index.Staged.Inherit(st.Normal)),
		stagedIndicator: sgrOf(st.Index.Staged.Inherit(st.Indicator)),
		stagedGhost:     sgrOf(st.Index.Staged.Inherit(st.Index.Ghost)),
		border:          sgrOf(st.Normal.Foreground(st.Indicator.GetBackground())),
		number:          sgrOf(st.Index.Number),
		flags:           sgrOf(st.Index.Flags),
		date:            sgrOf(st.Index.Date),
		author:          sgrOf(st.Index.Author),
		subject:         sgrOf(st.Index.Subject),
		staged:          sgrOf(st.Index.Staged),
		tag: func(name string) sgr {
			if g, ok := cache[name]; ok {
				return g
			}
			g := sgrOf(tagStyle(name))
			cache[name] = g
			return g
		},
		pagerHdr:    sgrOf(st.Pager.Header),
		pagerDef:    sgrOf(st.Pager.HdrDefault),
		pagerSig:    sgrOf(st.Pager.Signature),
		pagerAtt:    sgrOf(st.Pager.Attachment),
		pagerErr:    sgrOf(st.Error),
		pagerQuoted: [6]sgr{sgrOf(st.Pager.Quoted[0]), sgrOf(st.Pager.Quoted[1]), sgrOf(st.Pager.Quoted[2]), sgrOf(st.Pager.Quoted[3]), sgrOf(st.Pager.Quoted[4]), sgrOf(st.Pager.Quoted[5])},
	}
	opens := []sgr{sg.normal, sg.pagerHdr, sg.pagerDef, sg.pagerSig, sg.pagerAtt, sg.pagerErr}
	opens = append(opens, sg.pagerQuoted[:]...)
	key := make([]string, len(opens))
	for i, g := range opens {
		key[i] = g.open
	}
	// the join separator is a byte that can never appear in an SGR
	// sequence, so keys cannot collide
	sg.pagerKey = strings.Join(key, "\x00")
	return sg
}

func DefaultStyles() Styles {
	c := func(hex string) lipgloss.Color { return lipgloss.Color(hex) }
	st := Styles{
		Normal:    lipgloss.NewStyle().Foreground(c("#abb2bf")).Background(c("#21252b")),
		Indicator: lipgloss.NewStyle().Foreground(c("#21252b")).Background(c("#e5c07b")),
		Status:    lipgloss.NewStyle().Foreground(c("#abb2bf")).Background(c("#3e4451")),
		View:      lipgloss.NewStyle().Foreground(c("#21252b")).Background(c("#98c379")),
		Count:     lipgloss.NewStyle().Foreground(c("#21252b")).Background(c("#e5c07b")),
		Account:   lipgloss.NewStyle().Foreground(c("#21252b")).Background(c("#61afef")),
		Progress:  lipgloss.NewStyle().Foreground(c("#21252b")).Background(c("#61afef")),
		Error:     lipgloss.NewStyle().Foreground(c("#e06c75")),
		Tabbar:    lipgloss.NewStyle().Foreground(c("#abb2bf")).Background(c("#3e4451")),
		TabActive: lipgloss.NewStyle().Foreground(c("#21252b")).Background(c("#61afef")),
		// the background must be set - the label cell's width padding
		// fills with it (colorWhitespace), so the column seam never
		// leaks the terminal default background
		ComposeLabel:   lipgloss.NewStyle().Foreground(c("#61afef")).Background(c("#21252b")),
		ComposeDivider: lipgloss.NewStyle().Foreground(c("#21252b")).Background(c("#5c6370")),
		Index: IndexStyles{
			Number:  lipgloss.NewStyle().Foreground(c("#5c6370")),
			Date:    lipgloss.NewStyle().Foreground(c("#e5c07b")),
			Author:  lipgloss.NewStyle().Foreground(c("#61afef")),
			Subject: lipgloss.NewStyle().Foreground(c("#abb2bf")),
			Flags:   lipgloss.NewStyle().Foreground(c("#e06c75")),
			Staged:  lipgloss.NewStyle().Foreground(c("#565c64")).Bold(true),
			Ghost:   lipgloss.NewStyle().Foreground(c("#5c6370")),
			Tag: func(string) lipgloss.Style {
				return lipgloss.NewStyle().Foreground(c("#c678dd"))
			},
		},
		Pager: PagerStyles{
			Header:     lipgloss.NewStyle().Foreground(c("#61afef")),
			HdrDefault: lipgloss.NewStyle().Foreground(c("#abb2bf")),
			Quoted: [6]lipgloss.Style{
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
	st.sgr = sgrSetOf(st)
	return st
}

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
	st := Styles{
		Normal:         normal,
		Indicator:      to("indicator", normal),
		Status:         to("status", normal),
		View:           to("status.view", normal),
		Count:          to("status.count", normal),
		Account:        to("status.account", normal),
		Progress:       to("progress", normal),
		Error:          to("error", normal),
		Tabbar:         to("tabbar", normal),
		TabActive:      to("tabbar.active", normal),
		ComposeLabel:   to("compose.label", normal),
		ComposeDivider: to("compose.divider", normal),
		Index: IndexStyles{
			Number: to("index.number", normal), Date: to("index.date", normal),
			Author: to("index.author", normal), Subject: to("index.subject", normal),
			Flags: to("index.flags", normal), Staged: to("index.staged", normal),
			Ghost: to("index.ghost", normal),
			Tag: func(name string) lipgloss.Style {
				if _, ok := ids["index.tag."+name]; ok {
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
	st.sgr = sgrSetOf(st)
	return st
}
