// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

// Stage-2 terminal renderer: consumes lib/html's px Row stream (LayoutBlock)
// and quantizes it to core.Line pager lines. All terminal knowledge lives
// here; lib/html stays px-pure. See docs/superpowers/specs/2026-09-03-html-layout-engine-design.md
// stage 2 + migration, and docs/superpowers/plans/2026-09-04-html-stage2-wiring.md.

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
	// The F-key render injects a numbered [N] marker box before every link
	// (anchor href and bare URL) so labels flow into line building like any
	// word; labelLinks=false never injects, so no label reaches the pager.
	if labelLinks {
		boxes = q.injectLinkLabels(boxes)
	}
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

// injectLinkLabels numbers every link (anchor href and bare URL) in document
// order with a synthesized [N] marker box (D13), mirroring the old walker's
// F-key render. Runs before layout so the markers flow into line building.
// Style.Label is set only on these synthesized boxes; the unlabeled render
// never calls this.
func (q *stage2) injectLinkLabels(bs []*html.Box) []*html.Box {
	return q.injectInto(bs)
}

// injectInto walks one child list pre-order (Build order mirrors DOM order).
// An anchor box gets its marker at its lead inline position; a text box that
// holds a bare URL splits into pieces around each labeled token. The split
// can grow a list, so the walk returns the replacement slice.
func (q *stage2) injectInto(bs []*html.Box) []*html.Box {
	var out []*html.Box
	for _, b := range bs {
		switch {
		case b.Tag == "a" && b.Node != nil:
			if href := sanitize(html.Attr(b.Node, "href")); href != "" {
				q.links = append(q.links, href)
				label := labelBox(b.St, fmt.Sprintf("[%d]", len(q.links)))
				switch {
				case len(b.Children) > 0 && b.Children[0].Role == html.RoleBlock && b.Children[0].Tag == "":
					// a blockified anchor whose content leads with an anonymous
					// inline run: the label joins that run's inline content
					run := b.Children[0]
					run.Children = append([]*html.Box{label}, run.Children...)
				case len(b.Children) > 0 && (b.Children[0].Role == html.RoleBlock || b.Children[0].Role == html.RoleTable):
					// uniformly block-level content (whole-card anchor): the
					// label needs its own anonymous run so the children stay
					// uniformly block-level and the marker leads its own line
					run := &html.Box{Role: html.RoleBlock, St: b.St, WS: b.WS, Children: []*html.Box{label}}
					b.Children = append([]*html.Box{run}, b.Children...)
				default:
					b.Children = append([]*html.Box{label}, b.Children...)
				}
			}
		case b.Role == html.RoleText && !preserveWS(b.WS) && urlText(b.Text):
			out = append(out, q.splitTextURLs(b)...)
			continue // replacement leaves carry the whole text node
		}
		if len(b.Children) > 0 {
			b.Children = q.injectInto(b.Children)
		}
		out = append(out, b)
	}
	return out
}

// splitTextURLs splits one text box at its first bare-URL token into
// [before][label][token][after], recursing into after for further URLs. The
// pieces keep the original text (spaces included) so collapse still yields
// single inter-word spaces and the token itself still renders behind its
// label. All pieces share the source style pointer; only the label's copy
// carries Label.
func (q *stage2) splitTextURLs(b *html.Box) []*html.Box {
	pos := 0
	for _, f := range strings.Fields(b.Text) {
		start := pos + strings.Index(b.Text[pos:], f)
		if ls := html.Links(f, true); len(ls) > 0 {
			q.links = append(q.links, ls[0])
			var out []*html.Box
			if start > 0 {
				out = append(out, textBox(b.St, b.WS, b.Text[:start]))
			}
			out = append(out, labelBox(b.St, fmt.Sprintf("[%d]", len(q.links))))
			out = append(out, textBox(b.St, b.WS, f))
			if tail := b.Text[start+len(f):]; tail != "" {
				rest := textBox(b.St, b.WS, tail)
				if urlText(tail) {
					out = append(out, q.splitTextURLs(rest)...)
				} else {
					out = append(out, rest)
				}
			}
			return out
		}
		pos = start + len(f)
	}
	return nil // urlText gated the call: a URL field exists
}

// urlText reports whether a text leaf holds a bare URL token (a whitespace
// field html.Links recognizes) - the split's trigger.
func urlText(s string) bool {
	for _, f := range strings.Fields(s) {
		if ls := html.Links(f, true); len(ls) > 0 {
			return true
		}
	}
	return false
}

// preserveWS reports a white-space class in the pre family. Bare-URL splitting
// must skip these for walker parity (the walker never linkifies inside pre);
// only WSPre/WSPreWrap keep whitespace verbatim - WSPreLine collapses but is
// gated here for policy uniformity. Field-splitting pre text also strands a
// newline in a before piece, whose trailing LF atomizeText trims, collapsing
// two pre lines into one.
func preserveWS(w html.WS) bool {
	switch w {
	case html.WSPre, html.WSPreWrap, html.WSPreLine:
		return true
	}
	return false
}

// labelBox is a synthesized F-key marker: a RoleText box whose style is a
// copy of the link/text style with Label set, so stage-2 run building keeps
// it a separate run (D13).
func labelBox(st *html.Style, text string) *html.Box {
	cp := *st // do not mutate the shared style pointer
	cp.Label = true
	return &html.Box{Role: html.RoleText, St: &cp, Text: text}
}

// textBox is a RoleText leaf sharing its source's style pointer - the shape
// the box builder gives text leaves. Split pieces must share the style so run
// colors stay uniform across a split token.
func textBox(st *html.Style, ws html.WS, s string) *html.Box {
	return &html.Box{Role: html.RoleText, St: st, WS: ws, Text: s}
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
// strips (Task 4) flow through it too, so strip rows and text rows
// cannot drift apart. Tracking-pixel rows emit nothing and consume no gap.
func (q *stage2) emitRows(rows []html.Row) {
	for _, r := range rows {
		if q.truncated {
			return
		}
		if q.skipFor(r) || q.contentFreeStrip(r) { // dropped rows consume no gap (D9 + all-empty strips)
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
		case len(r.Cells) > 0:
			q.emitStrip(r)
		case len(r.Line.Atoms) > 0:
			q.emitTextRow(r)
		default:
			q.emitMarkerRow(r) // marker-only (empty li / textless nested)
		}
	}
}

// skipFor reports a row whose atoms are all declared 1x1 pixels (tracking
// beacons, D9) with only inter-word separators between them: it emits
// nothing and consumes no gap.
func (q *stage2) skipFor(r html.Row) bool {
	if len(r.Markers) != 0 {
		return false
	}
	any := false
	for _, a := range r.Line.Atoms {
		switch {
		case a.Img != nil:
			w, h := a.Img.ImgDisp()
			if w != 1 || h != 1 {
				return false
			}
			any = true
		case a.Sep:
		default:
			return false
		}
	}
	return any
}

// contentFreeStrip reports a table grid row whose fragments all render
// nothing (lone tracking pixels, hr cells, or nested all-empty strips).
// Such a strip must drop before the row preamble consumes its gap, else a
// spacer/tracking table emits an empty defaultBG line and stops the margin
// collapse around it (spacer tables are endemic in HTML mail).
func (q *stage2) contentFreeStrip(r html.Row) bool {
	if len(r.Cells) == 0 {
		return false
	}
	for _, f := range r.Cells {
		if !q.fragmentContentFree(f) {
			return false
		}
	}
	return true
}

// fragmentContentFree reports a strip fragment that renders no visible
// content: an hr (swallowed on strips by appendRow), a lone tracking
// pixel, or a nested all-empty strip.
func (q *stage2) fragmentContentFree(r html.Row) bool {
	if r.HR {
		return true
	}
	if len(r.Markers) > 0 {
		return false
	}
	if len(r.Cells) > 0 {
		for _, f := range r.Cells {
			if !q.fragmentContentFree(f) {
				return false
			}
		}
		return true
	}
	return q.skipFor(r)
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
// effective style (everything but Text) matches. Labels never merge with
// each other: the pager finds a link marker by exact run text, so "[1][2]"
// would drop both highlights.
func (a *acc) add(r core.Run) {
	if n := len(a.runs); n > 0 {
		last := &a.runs[n-1]
		if last.Fg == r.Fg && last.Bg == r.Bg && last.Attrs == r.Attrs &&
			last.Label == r.Label && last.Image == r.Image && !(last.Label && r.Label) {
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
// (D9). A row that holds only isolated images emits one own-line per
// image (the walker's per-image own-line render, pinned by
// TestImageDeclaredSizes); an image sharing its row with text stays inline.
func (q *stage2) emitTextRow(r html.Row) {
	if q.allImages(r) {
		for _, sp := range r.Line.Atoms {
			if sp.Img == nil {
				continue // a separator between isolated images
			}
			if w, h := sp.Img.ImgDisp(); w == 1 && h == 1 {
				continue // tracking pixel (D9)
			}
			if img := q.boxImage(sp.Img); img != nil {
				q.addLine(core.Line{Image: img, Text: img.Alt, Kind: core.LineBody, Bg: q.defaultBG})
			} else {
				rn := q.runForBox(sp.Img)
				rn.Text = sanitize(imgAlt(sp.Img))
				q.addLine(core.Line{Text: rn.Text, Runs: []core.Run{rn}, Kind: core.LineBody, Bg: q.defaultBG})
			}
		}
		return
	}
	var a acc
	q.emitRowContent(&a, r)
	q.addLine(core.Line{Text: joinRunText(a.runs), Runs: a.runs, Imgs: a.imgs, Kind: core.LineBody, Bg: q.defaultBG})
}

// allImages reports a text row that holds only image spans and inter-word
// separators (no text, no markers): each image is isolated, so it renders
// as its own line rather than an inline placeholder.
func (q *stage2) allImages(r html.Row) bool {
	if len(r.Markers) != 0 {
		return false
	}
	has := false
	for _, sp := range r.Line.Atoms {
		switch {
		case sp.Img != nil:
			has = true
		case sp.Sep:
		default:
			return false // a text span shares the line
		}
	}
	return has
}

// emitMarkerRow emits a marker-only row (an empty li, or a nested item
// whose content produced no row): the hanging glyphs, then the line ends.
func (q *stage2) emitMarkerRow(r html.Row) {
	var a acc
	q.layMarkers(&a, r)
	q.addLine(core.Line{Text: joinRunText(a.runs), Runs: a.runs, Kind: core.LineBody, Bg: q.defaultBG})
}

// emitStrip renders one table grid row (Row.Cells) as one horizontal line
// (D12): each fragment renders at its own absolute px X through appendRow;
// recursion places nested-table strips inline at their shifted columns. A
// cell's declared background rides its content runs (the td style carries
// the bg down the cascade), so no region machinery: blank interior columns
// of a bg'd cell render line-default (accepted, not a bug to "fix"). The
// strip line carries defaultBG. The row-level preamble in emitRows already
// applied the gap.
func (q *stage2) emitStrip(r html.Row) {
	var a acc
	for _, f := range r.Cells {
		q.appendRow(&a, f)
	}
	q.addLine(core.Line{Text: joinRunText(a.runs), Runs: a.runs, Imgs: a.imgs, Kind: core.LineBody, Bg: q.defaultBG})
}

// appendRow places one strip fragment (a Row) into the accumulator. A
// fragment with Cells hosts a nested table: its fragments are already
// shifted to absolute X (shiftRow), so render them in directly. A cell is
// table flow or inline content at the strip level (cellRows separates), so
// a fragment is never both (guard on Cells first). A strip has no own-line
// image; every cell image is inline on the strip (D9). emitRowContent pads
// the fragment to its own column. A non-table non-inline fragment (an hr
// row) drops here: emitRowContent ignores HR, matching the walker's
// no-lines output for a lone hr cell.
func (q *stage2) appendRow(a *acc, f html.Row) {
	if len(f.Cells) > 0 {
		for _, inner := range f.Cells {
			q.appendRow(a, inner)
		}
		return
	}
	q.emitRowContent(a, f)
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

// emitRowContent lays one row's content into the accumulator. It owns
// the content lead: after hanging gutter markers it pads to the row's
// content column (round(r.X/charW)); callers must not pre-pad. Images
// render inline here (the all-image own-line case is emitTextRow's).
func (q *stage2) emitRowContent(a *acc, r html.Row) {
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
			// col tracks the concatenated run-text end (the pager blanks runs
			// over Text), so ImagePos.X is the placeholder's text column.
			a.imgs = append(a.imgs, core.ImagePos{Image: img, X: a.col})
			a.col += html.TextWidth(rn.Text)
			started = true
		case sp.Sep:
			pending = true
		default:
			text := sanitize(expandTabs(sp.Text))
			if text == "" {
				// Preserve-mode pure-control corner only: collapse-mode
				// input already dropped the zero-width atom in lib/html
				// (inline.go). Clear the pending space so a control char
				// never doubles the gap.
				pending = false
				continue
			}
			q.emitTextPiece(a, &pending, &started, text, sp.St)
		}
	}
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
	if st.Label {
		r.Label = true
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

// imgAlt is an image box's placeholder text: a non-blank alt attribute,
// else "[image]". Blank alts still reserve the bracket text so an image
// run always advances at least one cell.
func imgAlt(b *html.Box) string {
	if b != nil && b.Node != nil {
		if alt := strings.TrimSpace(html.Attr(b.Node, "alt")); alt != "" {
			return alt
		}
	}
	return "[image]"
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

// joinRunText is the line's plain text (the Lua render contract reads
// Line.Text; runs are the styled overlay).
func joinRunText(runs []core.Run) string {
	var b strings.Builder
	for _, r := range runs {
		b.WriteString(r.Text)
	}
	return b.String()
}

// bindsLeft reports a word that must hug the preceding word: a comma or
// period split from its word by an inline boundary ("GitHub ," and
// "unsubscribe ." artifacts - the field join adds a space the source
// lacked) and an underscore-leading fragment (a URL split across spans
// into "email" + "_source"). These bind left typographically anyway.
func bindsLeft(s string) bool {
	if s == "" {
		return false // a control-only node sanitizes to ""; the join must not index it
	}
	switch s[0] {
	case ',', '.', ';', ':', '!', '?', '%', '_', ')', ']', '}':
		return true
	}
	return false
}
