// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"notmutt/config"
)

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
	Search  lipgloss.Style
	Tree    lipgloss.Style                   // the thread tree glyphs (R3)
	Tag     func(name string) lipgloss.Style // per-tag styles (R11)
}

type PagerStyles struct {
	Header     lipgloss.Style
	HdrDefault lipgloss.Style
	// HeaderColors are the resolved header rotation (config order):
	// a header block cycles the list, wrapping at the end.
	HeaderColors []config.Style
	Quoted       [6]lipgloss.Style
	Signature    lipgloss.Style
	Attachment   lipgloss.Style
}

// styleOf converts a resolved config style to a lipgloss style
// (fg/bg/attrs; resolved styles are concrete, no inheritance left).
func styleOf(s config.Style) lipgloss.Style {
	base := lipgloss.NewStyle()
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
	// v1 reset was "\x1b[0m"; v2 emits the abbreviated "\x1b[m"
	open := strings.TrimSuffix(st.Render(""), "\x1b[0m")
	open = strings.TrimSuffix(open, "\x1b[m")
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
	stagedNormal, stagedGhost                    sgr
	border                                       sgr // the popup border: the indicator's fg over the normal bg (no fill)
	number, flags, date, author, subject, staged sgr
	search, tree                                 sgr
	tag                                          func(name string) sgr
	pagerHdr                                     sgr
	pagerDef                                     sgr
	pagerHdrColors                               []sgr
	pagerSig                                     sgr
	pagerAtt                                     sgr
	pagerErr                                     sgr
	pagerQuoted                                  [6]sgr
	pagerKey                                     string // fingerprint of the pager-relevant opens (styleKey)
}

// pagerHdrColor picks the header rotation color for the n-th line of a
// header block (n starts at 0), wrapping at the end of the list; an
// empty list falls back to the hdrdefault style.
func (sg sgrSet) pagerHdrColor(n int) sgr {
	if len(sg.pagerHdrColors) == 0 {
		return sg.pagerDef
	}
	return sg.pagerHdrColors[n%len(sg.pagerHdrColors)]
}

func sgrSetOf(st Styles) sgrSet {
	tagStyle := st.Index.Tag
	cache := make(map[string]sgr)
	sg := sgrSet{
		normal:       sgrOf(st.Normal),
		indicator:    sgrOf(st.Indicator),
		ghost:        sgrOf(st.Index.Ghost),
		stagedNormal: sgrOf(st.Index.Staged.Inherit(st.Normal)),
		stagedGhost:  sgrOf(st.Index.Staged.Inherit(st.Index.Ghost)),
		border:       sgrOf(st.Normal.Foreground(st.Indicator.GetBackground())),
		number:       sgrOf(st.Index.Number),
		flags:        sgrOf(st.Index.Flags),
		date:         sgrOf(st.Index.Date),
		author:       sgrOf(st.Index.Author),
		subject:      sgrOf(st.Index.Subject),
		search:       sgrOf(st.Index.Search),
		tree:         sgrOf(st.Index.Tree),
		staged:       sgrOf(st.Index.Staged),
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
	for _, c := range st.Pager.HeaderColors {
		sg.pagerHdrColors = append(sg.pagerHdrColors, sgrOf(styleOf(c)))
	}
	opens := []sgr{sg.normal, sg.pagerHdr, sg.pagerDef, sg.pagerSig, sg.pagerAtt, sg.pagerErr}
	opens = append(opens, sg.pagerHdrColors...)
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
	c := func(hex string) color.Color { return lipgloss.Color(hex) }
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
		ComposeDivider: lipgloss.NewStyle().Foreground(c("#abb2bf")).Background(c("#5c6370")),
		Index: IndexStyles{
			Number:  lipgloss.NewStyle().Foreground(c("#5c6370")),
			Date:    lipgloss.NewStyle().Foreground(c("#e5c07b")),
			Author:  lipgloss.NewStyle().Foreground(c("#61afef")),
			Subject: lipgloss.NewStyle().Foreground(c("#abb2bf")),
			Flags:   lipgloss.NewStyle().Foreground(c("#e06c75")),
			Staged:  lipgloss.NewStyle().Foreground(c("#565c64")).Bold(true),
			Ghost:   lipgloss.NewStyle().Foreground(c("#5c6370")),
			Search:  lipgloss.NewStyle().Foreground(c("#e5c07b")).Bold(true),
			Tree:    lipgloss.NewStyle().Foreground(c("#5c6370")),
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
	ids, hdrColors := theme.Resolved(palette, theme.Default)
	to := func(id string, base lipgloss.Style) lipgloss.Style {
		s, ok := ids[id]
		if !ok {
			return base
		}
		return styleOf(s)
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
			Ghost: to("index.ghost", normal), Tree: to("index.tree", normal),
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
			HeaderColors: hdrColors,
		},
	}
	st.sgr = sgrSetOf(st)
	return st
}
