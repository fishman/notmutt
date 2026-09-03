// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

// Stage-2 terminal renderer: consumes lib/html's px Row stream (LayoutBlock)
// and quantizes it to core.Line pager lines. All terminal knowledge lives
// here; lib/html stays px-pure. See docs/superpowers/specs/2026-09-03-html-layout-engine-design.md
// stage 2 + migration, and the stage-2 plan 6.

import (
	"encoding/base64"
	"fmt"
	"math"
	"sort"
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

// emitRows turns the block row stream into pager lines. The D5 gap
// preamble runs once per emitted content row at the row level; cell
// strips (Task 4) and tracking-pixel rows own their rows instead.
func (q *stage2) emitRows(rows []html.Row) {
	for _, r := range rows {
		if q.truncated {
			return
		}
		if len(r.Cells) > 0 {
			// Task 4: emitStrip(r)
			continue
		}
		if q.skipFor(r) { // tracking-pixel-only row (D9): no blanks, no line
			continue
		}
		if !q.firstRow {
			q.firstRow = true // first content row drops its gap (D5)
		} else if r.Gap > 0 {
			q.blankLines(r.Gap)
		}
		switch {
		case r.HR:
			q.emitHR(r)
		case len(r.Line.Atoms) > 0:
			q.emitTextRow(r)
		default:
			q.emitMarkerRow(r) // marker-only (empty li / textless nested)
		}
	}
}

// skipFor reports a row whose only content is one declared 1x1 pixel
// (a tracking beacon, D9): it emits nothing and consumes no gap.
func (q *stage2) skipFor(r html.Row) bool {
	if len(r.Markers) != 0 || len(r.Line.Atoms) != 1 || r.Line.Atoms[0].Img == nil {
		return false
	}
	w, h := r.Line.Atoms[0].Img.ImgDisp()
	return w == 1 && h == 1
}

// acc is one horizontal pager line under construction. Runs sit at
// absolute cell columns; pad() materializes the blanks before a column
// as a space run so core.Line.Text stays the concatenation of the runs'
// text (the pager paints runs over Line.Text). Blank lines have Text ""
// and only a Bg.
type acc struct {
	col  int // current text-end column
	runs []core.Run
	imgs []core.ImagePos
}

func (a *acc) pad(to int) {
	if to <= a.col {
		return
	}
	a.runs = append(a.runs, core.Run{Text: strings.Repeat(" ", to-a.col)})
	a.col = to
}

// add appends a run, merging it into the previous run when their
// effective style (everything but Text) matches.
func (a *acc) add(r core.Run) {
	if n := len(a.runs); n > 0 {
		last := &a.runs[n-1]
		if last.Fg == r.Fg && last.Bg == r.Bg && last.Attrs == r.Attrs &&
			last.Label == r.Label && last.Image == r.Image {
			last.Text += r.Text
			return
		}
	}
	a.runs = append(a.runs, r)
}

// space appends the inter-word one-space run styled like the preceding
// run (the walker's space-inherits-preceding), merging when equal.
func (a *acc) space() {
	var fg, bg string
	var attrs core.LineAttrs
	if n := len(a.runs); n > 0 {
		last := a.runs[n-1]
		fg, bg, attrs = last.Fg, last.Bg, last.Attrs
	}
	a.add(core.Run{Text: " ", Fg: fg, Bg: bg, Attrs: attrs})
	a.col++
}

// emitTextRow emits one content row: hanging markers (D10), then its
// spans with binding (D6), tab expansion + F1 sanitize, and images
// (D9). An isolated image the row owns outright becomes an image line.
func (q *stage2) emitTextRow(r html.Row) {
	var a acc
	if img := q.emitRowContent(&a, r, true); img != nil {
		q.addLine(core.Line{Image: img, Text: img.Alt, Kind: core.LineBody, Bg: q.defaultBG})
		return
	}
	q.addLine(core.Line{Text: joinRunText(a.runs), Runs: a.runs, Imgs: a.imgs, Kind: core.LineBody, Bg: q.defaultBG})
}

// emitMarkerRow emits a marker-only row (an empty li, or a nested item
// whose content produced no row): the hanging glyphs, then the line ends.
func (q *stage2) emitMarkerRow(r html.Row) {
	var a acc
	q.layMarkers(&a, r)
	q.addLine(core.Line{Text: joinRunText(a.runs), Runs: a.runs, Kind: core.LineBody, Bg: q.defaultBG})
}

// emitHR emits one horizontal-rule row (D11): a run of rule glyphs over
// the rule's content width.
func (q *stage2) emitHR(r html.Row) {
	var a acc
	a.pad(int(math.Round(float64(r.X) / charW)))
	rn := q.runForBox(r.Box)
	rn.Text = strings.Repeat(ruleGlyph, int(math.Round(float64(r.W)/charW)))
	a.add(rn)
	q.addLine(core.Line{Text: joinRunText(a.runs), Runs: a.runs, Kind: core.LineBody, Bg: q.defaultBG})
}

// emitRowContent lays one row's content into the accumulator: its
// hanging gutter markers, then its spans. ownImages lets a top-level
// row claim an isolated image (the sole span, no markers) as an
// own-line image, which it returns non-nil; strip fragments always pass
// false so their images stay inline. The caller reaches the content
// column with a.pad.
func (q *stage2) emitRowContent(a *acc, r html.Row, ownImages bool) *core.Image {
	if ownImages && len(r.Markers) == 0 && len(r.Line.Atoms) == 1 && r.Line.Atoms[0].Img != nil {
		if img := q.boxImage(r.Line.Atoms[0].Img); img != nil {
			return img
		}
	}
	q.layMarkers(a, r)
	a.pad(int(math.Round(float64(r.X) / charW)))
	pending := false
	started := false // a text/image run already landed: a sep then spaces words
	for _, sp := range r.Line.Atoms {
		switch {
		case sp.Img != nil:
			if w, h := sp.Img.ImgDisp(); w == 1 && h == 1 {
				continue // tracking pixel (D9): drops, leaving any pending space
			}
			img := q.boxImage(sp.Img)
			if img == nil {
				// unresolved src: the alt renders as plain text, not an image
				q.emitTextPiece(a, &pending, &started, sanitize(imgAlt(sp.Img)), sp.Img.St)
				continue
			}
			if pending && started {
				a.space()
			}
			pending = false
			rn := q.runFor(sp.Img.St)
			if rn.Fg == "" {
				rn.Fg = q.defaultFG
			}
			rn.Text = img.Alt
			rn.Image = img
			a.add(rn)
			a.imgs = append(a.imgs, core.ImagePos{Image: img, X: a.col})
			a.col += imageCells(sp.Img)
			started = true
		case sp.Sep:
			pending = true
		default:
			text := sanitize(expandTabs(sp.Text))
			if text == "" {
				pending = false // an emptied span clears the pending space (no doubled gap)
				continue
			}
			q.emitTextPiece(a, &pending, &started, text, sp.St)
		}
	}
	return nil
}

// emitTextPiece appends one text piece: a pending inter-word space
// commits unless the piece binds left (D6) or opens the line (a dropped
// pixel's trailing space collapses like CSS leading whitespace); then
// the styled run lands.
func (q *stage2) emitTextPiece(a *acc, pending, started *bool, text string, st *html.Style) {
	if *pending && *started && !bindsLeft(text) {
		a.space()
	}
	*pending = false
	rn := q.runFor(st)
	if rn.Fg == "" {
		rn.Fg = q.defaultFG
	}
	rn.Text = text
	a.add(rn)
	a.col += html.TextWidth(text)
	*started = true
}

// layMarkers hangs a row's markers in their gutters, each glyph ending
// one cell before its owning item's content edge. A textless li whose
// first content row is a nested list hangs both markers on one row, so
// they lay in ascending gutter order (pad only moves forward).
func (q *stage2) layMarkers(a *acc, r html.Row) {
	ms := r.Markers
	if len(ms) > 1 {
		ms = append([]html.RowMarker(nil), ms...)
		sort.Slice(ms, func(i, j int) bool { return ms[i].X < ms[j].X })
	}
	for _, mk := range ms {
		gc := glyphCells(mk)
		a.pad(int(math.Round(float64(mk.X)/charW)) - gc)
		rn := q.runForBox(r.Box)
		rn.Text = glyphText(mk)
		a.add(rn)
		a.col += gc
	}
}

// runForBox is runFor on a box's computed style, folding in the default
// foreground for unstyled content.
func (q *stage2) runForBox(b *html.Box) core.Run {
	var r core.Run
	if b != nil {
		r = q.runFor(b.St)
	}
	if r.Fg == "" {
		r.Fg = q.defaultFG
	}
	return r
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

// boxImage resolves an image box to its core.Image: cid:/data: bytes
// or a remote URL-only placeholder (the render never decodes - the TUI
// does on the render-images key). DispW/DispH are the DECLARED display
// px from the box (stage-1 resolved, % against the actual containing
// width); nil when the src resolves to nothing.
func (q *stage2) boxImage(b *html.Box) *core.Image {
	if b == nil || b.Node == nil {
		return nil
	}
	src := html.Attr(b.Node, "src")
	var data []byte
	var url string
	switch {
	case strings.HasPrefix(src, "cid:"):
		id := strings.Trim(strings.TrimSpace(src[4:]), "<>")
		for _, a := range q.atts {
			if strings.EqualFold(strings.Trim(a.ContentID, "<>"), id) {
				data = a.Data
				break
			}
		}
	case strings.HasPrefix(src, "data:image/") && strings.Contains(src, ";base64,"):
		data, _ = base64.StdEncoding.DecodeString(src[strings.IndexByte(src, ',')+1:])
	case strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://"):
		url = src
	}
	if len(data) == 0 && url == "" {
		return nil
	}
	img := &core.Image{Data: data, URL: url, Alt: sanitize(imgAlt(b))}
	img.DispW, img.DispH = b.ImgDisp()
	return img
}

// imgAlt is an image box's placeholder text: its alt attribute, else
// "[image]".
func imgAlt(b *html.Box) string {
	if b != nil && b.Node != nil {
		if alt := html.Attr(b.Node, "alt"); alt != "" {
			return alt
		}
	}
	return "[image]"
}

// imageCells is an inline image's occupied cell width: its used px at
// the last layout, the declared px fallback, else one cell.
func imageCells(b *html.Box) int {
	if w, _, ok := b.ImgUsed(); ok && w > 0 {
		return int(math.Ceil(float64(w) / charW))
	}
	if w, _ := b.ImgDisp(); w > 0 {
		return int(math.Ceil(float64(w) / charW))
	}
	return 1
}

var ruleGlyph = "─"

var markerGlyphs = map[string]string{
	"disc":   "•",
	"circle": "◦",
	"square": "▪",
}

// glyphText renders one marker's display text: the numbered "N." for an
// ordered item (Ord is 1-based; 0 defended as 1 since stage-1 always
// stamps it), the glyph-map shape otherwise.
func glyphText(mk html.RowMarker) string {
	if mk.Type == "decimal" {
		if mk.Ord == 0 {
			mk.Ord = 1
		}
		return fmt.Sprintf("%d.", mk.Ord)
	}
	return markerGlyphs[mk.Type]
}

// glyphCells is a marker glyph's terminal width: its hang ends one cell
// before the owning item's content edge, so the glyph occupies
// [col-glyphCells, col) in the gutter.
func glyphCells(mk html.RowMarker) int { return html.TextWidth(glyphText(mk)) }
