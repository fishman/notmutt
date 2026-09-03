// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

// Stage-2 terminal renderer: consumes lib/html's px Row stream (LayoutBlock)
// and quantizes it to core.Line pager lines. All terminal knowledge lives
// here; lib/html stays px-pure. See docs/superpowers/specs/2026-09-03-html-layout-engine-design.md
// stage 2 + migration, and the stage-2 plan 6.

import (
	"math"
	"strings"

	xhtml "golang.org/x/net/html"

	"notmutt/core"
	"notmutt/lib/html"
)

const (
	// charW is the px width of one terminal cell: the horizontal px<->cell
	// scale. It is forced by the locked TestImageDeclaredSizes (50% of the
	// 80-cell layout = 400px).
	charW = 10
	// lineH is the px height of one pager row; blank quantization divides
	// collapsed margin gaps by it. The base em is 16px, so a 1em gap is one
	// blank row.
	lineH = 16
)

// cellMeter measures text for stage-1 layout in px, where each runewidth
// terminal cell is charW px. Wrapping in px then equals the old cell wrapping.
type cellMeter struct{}

func (cellMeter) Width(s string) int { return html.TextWidth(s) * charW }

func (cellMeter) RuneWidth(r rune) int { return html.RuneWidth(r) * charW }

// renderStage2HTML is the stage-2 facade entry: parse + clamp (mirrors the
// walker renderHTML head) then the engine. width is in cells; renderStage2
// lays out at width*charW px.
func renderStage2HTML(body string, atts []Attachment, width int, labelLinks, dark bool, themeBG string) ([]core.Line, []string) {
	doc, err := xhtml.Parse(strings.NewReader(body))
	if err != nil {
		return nil, nil // x/net/html recovers from malformed input by spec; guard anyway
	}
	if width <= 0 || width > htmlWrapWidth {
		width = htmlWrapWidth
	}
	return renderStage2(doc, atts, width*charW, labelLinks, dark, themeBG)
}

func renderStage2(doc *xhtml.Node, atts []Attachment, widthPx int, labelLinks, dark bool, themeBG string) ([]core.Line, []string) {
	rules := html.ParseStyleSheets(doc)
	boxes := html.Build(doc, rules)
	if len(boxes) == 0 {
		return nil, nil // caller falls back to the raw text
	}
	bs := html.BodyStyle(doc, rules)
	bg, fg := pageColors(bs, dark, themeBG)
	q := &stage2{atts: atts, defaultBG: bg, defaultFG: fg, dark: dark,
		themeBG: themeBG, linesLeft: maxHTMLLines}
	// labelLinks (the F-key) injects numbered labels before layout: Task 5.
	rows := html.LayoutBlock(boxes, widthPx, cellMeter{}, true)
	q.emitRows(rows) // Tasks 3-4 grow the row dispatch
	if q.truncated {
		q.lines = append(q.lines, core.Line{Text: "[content truncated]", Kind: core.LineBody})
	}
	if len(q.lines) == 0 {
		return nil, q.links
	}
	return q.lines, q.links
}

// pageColors resolves the page background and the default foreground for
// unstyled text (the walker's html/body handling): the mail's declared
// bg (light-declared reflects onto themeBG in dark mode; dark-declared
// passes through the luma gate); unstyled text reads the contrast fg on
// that bg in light mode, the theme text ("") in dark mode (the page bg IS
// the theme bg).
func pageColors(bs *html.Style, dark bool, themeBG string) (bg, fg string) {
	if bs != nil && bs.Bg != "" {
		if dark && html.IsLight(bs.Bg) {
			bg = html.AdaptBG(bs.Bg, themeBG)
		} else {
			bg = bs.Bg
		}
	} else if dark {
		bg = themeBG
	} else {
		bg = "#ffffff"
	}
	if bs == nil || !bs.FgSet {
		if dark {
			fg = ""
		} else {
			fg = html.ContrastFG(bg)
		}
	} else {
		fg = bs.Fg
	}
	return bg, fg
}

type stage2 struct {
	atts      []Attachment
	lines     []core.Line
	links     []string
	linesLeft int
	truncated bool
	defaultBG string
	defaultFG string
	dark      bool
	themeBG   string
	firstRow  bool // first content row's gap drops (D5)
}

// emitRows turns the block row stream into pager lines. Task 2 handles
// only plain single-line text rows; cells/hr/markers/image rows land in
// Tasks 3-4 and are skipped here without consuming the first-row drop.
func (q *stage2) emitRows(rows []html.Row) {
	for _, r := range rows {
		if q.truncated {
			return
		}
		q.emitTextRow(r)
	}
}

// emitTextRow emits one plain text row; false when the row needs the
// Task 3-4 machinery (cells, hr, markers, images) and is left for them.
func (q *stage2) emitTextRow(r html.Row) bool {
	if len(r.Cells) > 0 || r.HR || len(r.Markers) > 0 {
		return false
	}
	for _, a := range r.Line.Atoms {
		if a.Img != nil {
			return false // Task 3: image rows
		}
	}
	// D5: the first content row's gap drops; later gaps quantize to blanks
	if !q.firstRow {
		q.firstRow = true
	} else {
		q.blankLines(r.Gap)
	}
	var runs []core.Run
	if lead := int(math.Round(float64(r.X) / charW)); lead > 0 {
		runs = append(runs, core.Run{Text: strings.Repeat(" ", lead)})
	}
	for _, a := range r.Line.Atoms {
		text := sanitize(a.Text) // F1: DOM text is raw
		if text == "" {
			continue
		}
		rn := q.runFor(a.St)
		if rn.Fg == "" {
			rn.Fg = q.defaultFG
		}
		rn.Text = text
		if len(runs) > 0 && rn == runs[len(runs)-1] {
			runs[len(runs)-1].Text += text
			continue
		}
		runs = append(runs, rn)
	}
	q.addLine(core.Line{Kind: core.LineBody, Text: joinRunText(runs), Runs: runs, Bg: q.defaultBG})
	return true
}

// blankRows quantizes a collapsed margin gap to whole pager blank rows
// (the base em line height).
func blankRows(gapPx int) int {
	return int(math.Round(float64(gapPx) / lineH))
}

// blankLines appends the gap's blank rows, each carrying the mail bg.
func (q *stage2) blankLines(gapPx int) {
	for n := blankRows(gapPx); n > 0; n-- {
		q.addLine(core.Line{Bg: q.defaultBG})
	}
}

// addLine appends one line and decrements the render budget.
func (q *stage2) addLine(l core.Line) {
	if q.linesLeft <= 0 {
		q.truncated = true
		return
	}
	q.linesLeft--
	q.lines = append(q.lines, l)
}

// runFor maps a computed style to its run representation (the walker's
// dark/theme adaptation, ported): a light-declared bg reflects onto
// themeBG and its fg inverts to keep the hue and the readability; a
// dark-declared bg is not inverted into light. "" fg/bg and zero attrs
// for the unstyled base (run equality gates run merging).
func (q *stage2) runFor(st *html.Style) core.Run {
	var r core.Run
	if st == nil {
		return r
	}
	r.Fg, r.Bg = st.Fg, st.Bg
	if q.dark {
		onLight := true
		if st.Bg != "" {
			onLight = html.IsLight(st.Bg)
			if onLight {
				r.Bg = html.AdaptBG(st.Bg, q.themeBG)
			}
		}
		if st.Fg != "" && onLight {
			ref := r.Bg
			if ref == "" {
				ref = q.defaultBG
			}
			r.Fg = html.AdaptFG(st.Fg, ref)
		}
	}
	if st.Bold {
		r.Attrs |= core.AttrBold
	}
	if st.Italic {
		r.Attrs |= core.AttrItalic
	}
	if st.Underline {
		r.Attrs |= core.AttrUnderline
	}
	return r
}
