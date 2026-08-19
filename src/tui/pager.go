// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mattn/go-runewidth"

	"notmutt/core"
)

// pager holds the open thread's render lines and the scroll window.
// Scrolling moves the WINDOW by line (the glow/less model): j/k shift
// the offset one line, so every press changes every visible line and
// the renderer repaints the whole window - no read-position indicator
// whose style-only change the diff can drop (the pre-glow model that
// made the first press render nothing). Content is styled LAZILY: only
// the visible window plus a margin above and below (ensureStyled),
// never the whole document - the old style() pass re-styled 20k lines
// on every resize (the 385ms resize stall). Styled lines stay cached;
// a scroll into an unstyled region styles it on demand, and a resize
// or theme switch (the width/styleKey invalidation) re-styles only the
// window at the new width. The content is bounded (one thread), so the
// window owns the scroll state (the index stays windowed - 129k rows
// must never flatten). Long lines are truncated to the window width,
// never wrapped (R11 alignment; the truncation is a pinned limitation,
// wrapping is future work).
type pager struct {
	threadID string
	// msgID is the opened message: the pager shows that message's
	// render only, never the whole thread. The identity guards the
	// reloads - a same-message re-render replaces the content.
	msgID string
	lines []core.Line
	// linkSel is the F key's selected label marker ("[N]", "" = none):
	// that run renders reversed - the easyjump highlight follows the
	// digits live
	linkSel    string
	styled     []string // styled text per line; "" = not styled yet
	doc        []string // the expanded document: image lines span Image.Rows rows
	imgFrom    []int    // per doc row: the source line index
	imgRow     []bool   // per doc row: an image expansion row (no text)
	images     bool     // the render-images toggle: expand image lines
	styleKey   string   // the style set the cache was built with (sgr opens)
	width      int
	styleWidth int // the width the cache was styled at (0 = none)
	st         Styles
	vp         viewport
}

func newPager(threadID, msgID string, lines []core.Line) *pager {
	return &pager{threadID: threadID, msgID: msgID, lines: lines}
}

// setLines replaces the pager content (the compose preview after a
// body edit, the AI summary swap): the expanded layout drops with the
// lines - a stale doc maps old rows into the new line list, and
// ensureStyled rebuilds it from the new content.
func (p *pager) setLines(lines []core.Line) {
	p.lines = lines
	p.doc = nil
	p.styled = nil
	p.vp.offset = 0
}

func (p *pager) setSize(w, h int, st Styles) {
	p.width = w
	p.st = st
	p.vp.setSize(w, h)
	// the styled cache is width- and style-dependent: a resize or a
	// theme switch invalidates it; ensureStyled re-styles only the
	// visible window (same-width resizes and height changes keep the
	// cached range untouched)
	if key := st.sgr.pagerKey; w != p.styleWidth || key != p.styleKey {
		p.styleWidth, p.styleKey = w, key
		clear(p.styled)
		clear(p.doc)
	}
	p.ensureStyled()
}

// relayout rebuilds the expanded document: an image line (only when
// images is on AND the image has decoded dims) spans Image.Rows empty
// rows - the terminal paint fills them - every other line maps 1:1.
// The per-line styled cache survives: doc rows resolve to lines via
// imgFrom, so the toggle and a decode-gained resize never re-style.
func (p *pager) relayout() {
	// image-carrying lines re-style on every layout: their cached text
	// holds the placeholder or the blanked run - the decode state
	// (Rows) decides which, and it changed
	for i := range p.styled {
		if l := &p.lines[i]; l.Image != nil || len(l.Imgs) > 0 {
			p.styled[i] = ""
		}
	}
	n := len(p.lines)
	if p.images {
		for i := range p.lines {
			l := &p.lines[i]
			if l.Image != nil && l.Image.Rows > 0 {
				n += l.Image.Rows - 1
			} else if s := imgRowSpan(l); s > 1 {
				n += s - 1
			}
		}
	}
	doc := make([]string, n)
	from := make([]int, n)
	isImg := make([]bool, n)
	j := 0
	for i := range p.lines {
		l := &p.lines[i]
		rows := 1
		blank := false
		if p.images {
			if l.Image != nil && l.Image.Rows > 0 {
				rows, blank = l.Image.Rows, true
			} else if s := imgRowSpan(l); s > 1 {
				// an inline image row keeps its own text row; only
				// the expansion rows below it blank
				rows, blank = s, true
			}
		}
		for r := 0; r < rows; r++ {
			from[j] = i
			isImg[j] = blank && (l.Image != nil || r > 0)
			j++
		}
	}
	p.doc, p.imgFrom, p.imgRow = doc, from, isImg
	p.vp.setLines(doc)
	p.vp.clamp()
}

// imgRowSpan is the expansion row count of a text line's inline
// images: the tallest decoded image (1 when none are decoded).
func imgRowSpan(l *core.Line) int {
	rows := 1
	for _, im := range l.Imgs {
		rows = max(rows, im.Image.Rows)
	}
	return rows
}

// ensureStyled styles the visible window plus a margin above and
// below, so small scroll movements never touch the styled lines; lines
// outside the range stay unstyled until scrolled into it. The styled
// slice doubles as the viewport's content, so the clamp and window
// math see the full document. Image expansion rows carry no text and
// are never styled - the terminal paint fills them.
func (p *pager) ensureStyled() {
	if p.doc == nil {
		p.relayout()
	}
	if len(p.styled) != len(p.lines) {
		p.styled = make([]string, len(p.lines))
	}
	first := max(0, p.vp.offset-pagerStyleMargin)
	last := min(len(p.doc), p.vp.offset+p.vp.height+pagerStyleMargin)
	for i := first; i < last; i++ {
		if p.imgRow[i] {
			continue
		}
		li := p.imgFrom[i]
		if p.styled[li] == "" {
			p.styled[li] = p.styleLine(p.lines[li])
		}
		p.doc[i] = p.styled[li]
	}
	p.vp.setLines(p.doc)
}

// setLinkSel points the easyjump highlight at the label marker under
// entry; the styled cache drops so the visible window re-renders with
// the reversed marker on the next paint.
// append adds a line to the pager (the AI summary stream): the styled
// cache extends and the document grows with the line - a streamed
// line is plain text, one row, never an image expansion. The viewport
// offset survives, so a scrolled summary does not jump - the bottom
// only follows when the view was already pinned there.
func (p *pager) append(l core.Line) {
	p.lines = append(p.lines, l)
	if p.doc != nil {
		p.doc = append(p.doc, "")
		p.imgFrom = append(p.imgFrom, len(p.lines)-1)
		p.imgRow = append(p.imgRow, false)
	}
	p.ensureStyled()
}

// appendText merges a streamed delta into the pager (the AI summary's
// token stream): whole lines split off and append, a trailing partial
// extends the last line in place - a token-per-event stream renders as
// flowing text, never one row per token. A partial after a completed
// line starts a fresh line.
func (p *pager) appendText(text string) {
	for len(text) > 0 {
		i := strings.IndexByte(text, '\n')
		if i < 0 {
			n := len(p.lines)
			if n > 0 && !strings.HasSuffix(p.lines[n-1].Text, "\n") {
				p.lines[n-1].Text += text
				if n-1 < len(p.styled) {
					p.styled[n-1] = ""
				}
				p.ensureStyled()
				return
			}
			p.append(core.Line{Text: text, Kind: core.LineBody})
			return
		}
		p.append(core.Line{Text: text[:i+1], Kind: core.LineBody})
		text = text[i+1:]
	}
}

func (p *pager) setLinkSel(sel string) {
	if p.linkSel == sel {
		return
	}
	p.linkSel = sel
	clear(p.styled)
	if p.doc != nil {
		p.ensureStyled()
	}
}

// setImages switches the image expansion (the render-images key): on
// expands decoded image lines to Image.Rows rows, off collapses them
// to their single Alt row.
func (p *pager) setImages(on bool) {
	if p.images == on {
		return
	}
	p.images = on
	p.relayout()
}

// imgBlock is one visible image block: the source line, its doc row
// (first expanded row - the paint crop anchor), the cell dims (0
// until the decode runs), the image (the line's block or one of its
// inline row) and its cell offset.
type imgBlock struct {
	line int
	doc  int
	cols int
	rows int
	img  *core.Image
	x    int
}

// visibleImages lists the window's image lines, one block per image:
// the expanded block (its rows walked back to the first doc row for
// the anchor) or the collapsed Alt row (Rows 0 - listed as the
// decoder's trigger, painted as text). The dims may still be 0 - the
// decode pass fills them.
func (p *pager) visibleImages() []imgBlock {
	if p.imgRow == nil {
		return nil
	}
	var out []imgBlock
	seen := map[int]bool{}
	for i := p.vp.offset; i < len(p.imgRow) && i < p.vp.offset+p.vp.height; i++ {
		li := p.imgFrom[i]
		if seen[li] {
			continue
		}
		l := &p.lines[li]
		if l.Image == nil && len(l.Imgs) == 0 {
			continue
		}
		seen[li] = true
		doc := i
		for doc > 0 && p.imgFrom[doc-1] == li {
			doc--
		}
		if l.Image != nil {
			out = append(out, imgBlock{line: li, doc: doc, cols: l.Image.Cols, rows: l.Image.Rows, img: l.Image})
		}
		for _, im := range l.Imgs {
			out = append(out, imgBlock{line: li, doc: doc, cols: im.Image.Cols, rows: im.Image.Rows, img: im.Image, x: im.X})
		}
	}
	return out
}

// styleLine maps one structured line to styled text: subject ->
// header, from/date -> hdrdefault, body -> quotedN by depth,
// signature -> signature, attachment -> attachment, error -> error.
// Every line pads to the window width with its own style (the R11
// slot-reservation rule - alignment never shifts per line). The
// styles' SGR fragments are precomputed (p.st.sgr), so a line is
// plain string joins, never a Style.Render.
//
// quoteColor maps the line's quote depth to the style table: depth 0
// (plain body text) is the normal text color - a plain mail must not
// share a custom color with the first reply layer quote; depths 1-5
// keep their own colors (quoted0-4, the mutt surface).
func quoteColor(sg sgrSet, quoted int) sgr {
	if quoted <= 0 {
		return sg.normal
	}
	return sg.pagerQuoted[quoted-1]
}

func (p *pager) styleLine(l core.Line) string {
	sg := p.st.sgr
	var g sgr
	switch l.Kind {
	case core.LineSubject:
		g = sg.pagerHdr
	case core.LineHeader:
		g = sg.pagerDef
	case core.LineBody:
		g = quoteColor(sg, l.Quoted)
	case core.LineSignature:
		g = sg.pagerSig
	case core.LineAttachment:
		g = sg.pagerAtt
	case core.LineError:
		g = sg.pagerErr
	default:
		g = sg.normal
	}
	// the line's default background (the html view's mail-declared
	// body color) covers the pad and the blank rows; a trailing run
	// background (a colored block) extends over the pad too
	outer := g
	if l.Bg != "" {
		outer.open += "\x1b[48;2;" + hexRGB(l.Bg) + "m"
	}
	var text string
	if len(l.Runs) > 0 {
		text = p.styleRuns(l.Runs)
		if last := l.Runs[len(l.Runs)-1]; last.Bg != "" {
			outer.open += "\x1b[48;2;" + hexRGB(last.Bg) + "m"
		}
	} else {
		text = outer.render(l.Text)
	}
	if p.width > 0 {
		text = padRowSGR(text, p.width, outer)
	}
	return text
}

// styleRuns joins the run fragments into one styled string: each run
// emits its own SGR open + text, and a reset closes a styled run only
// (the open of an unstyled run would be redundant). padRowSGR re-applies
// the line's base style after every reset, so the quoted/header color
// covers the gaps and the pad; a trailing background run closes with a
// reset so its bg does not leak into the pad region.
func (p *pager) styleRuns(runs []core.Run) string {
	var b strings.Builder
	for i, r := range runs {
		if i > 0 {
			prev := p.runSel(runs[i-1])
			if prev.Fg != "" || prev.Bg != "" || prev.Attrs != 0 {
				b.WriteString("\x1b[0m")
			}
		}
		r = p.runSel(r)
		if open := runSGR(r); open != "" {
			b.WriteString(open)
		}
		// a decoded inline image's placeholder run blanks: the pixels
		// paint at its offset, the words would render under them
		if r.Image != nil && p.images && r.Image.Rows > 0 {
			b.WriteString(strings.Repeat(" ", runewidth.StringWidth(r.Text)))
		} else {
			b.WriteString(r.Text)
		}
	}
	// the trailing reset covers the selected marker's reverse too - a
	// reverse pad row is a visible artifact, unlike a colored one
	last := p.runSel(runs[len(runs)-1])
	if last.Bg != "" || last.Attrs != runs[len(runs)-1].Attrs {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// runSel applies the easyjump selection to a run: the marker of the
// link under entry renders reversed (the live highlight). The overlay
// rides the attrs so the reset logic sees it like any styled run.
func (p *pager) runSel(r core.Run) core.Run {
	if p.linkSel != "" && r.Label && r.Text == p.linkSel {
		r.Attrs |= core.AttrReverse
	}
	return r
}

// runSGR maps a run's style to its SGR open (truecolor fg/bg, the attr
// bits); "" when the run carries no style, so the line style shows
// through.
func runSGR(r core.Run) string {
	var b strings.Builder
	if h := hexRGB(r.Fg); h != "" {
		b.WriteString("\x1b[38;2;")
		b.WriteString(h)
		b.WriteString("m")
	}
	if h := hexRGB(r.Bg); h != "" {
		b.WriteString("\x1b[48;2;")
		b.WriteString(h)
		b.WriteString("m")
	}
	if r.Attrs&core.AttrBold != 0 {
		b.WriteString("\x1b[1m")
	}
	if r.Attrs&core.AttrItalic != 0 {
		b.WriteString("\x1b[3m")
	}
	if r.Attrs&core.AttrUnderline != 0 {
		b.WriteString("\x1b[4m")
	}
	if r.Attrs&core.AttrReverse != 0 {
		b.WriteString("\x1b[7m")
	}
	return b.String()
}

// hexRGB parses a #rrggbb color to its "r;g;b" channel form; "" for
// anything unparseable - a bad color drops, never reaches the terminal.
func hexRGB(hex string) string {
	if len(hex) != 7 || hex[0] != '#' {
		return ""
	}
	n, err := strconv.ParseUint(hex[1:], 16, 32)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d;%d;%d", n>>16&255, n>>8&255, n&255)
}

// scrollDown/scrollUp move the window by n lines (j / k / a count).
// Every press changes every visible line, so the renderer repaints the
// window - there is nothing that can diff to zero.
func (p *pager) scrollDown(n int) {
	p.vp.offset += n
	p.vp.clamp()
}

func (p *pager) scrollUp(n int) {
	p.vp.offset -= n
	p.vp.clamp()
}

// pageDown/pageUp move a full window (pgdown/pgup); halfPageDown/Up
// half a window (ctrl+d/ctrl+u, vim's default). The clamp pins the
// last page to the tail, so repeated page-down ends on the bottom.
func (p *pager) pageDown()     { p.vp.offset += p.vp.height; p.vp.clamp() }
func (p *pager) pageUp()       { p.vp.offset -= p.vp.height; p.vp.clamp() }
func (p *pager) halfPageDown() { p.vp.offset += p.vp.height / 2; p.vp.clamp() }
func (p *pager) halfPageUp()   { p.vp.offset -= p.vp.height / 2; p.vp.clamp() }

// scrollTop/scrollBottom jump the window absolutely (g / G).
func (p *pager) scrollTop() {
	p.vp.offset = 0
}

func (p *pager) scrollBottom() {
	p.vp.offset = len(p.vp.lines) - p.vp.height
	p.vp.clamp()
}

// render returns the styled window, ALWAYS exactly vp.height lines
// (no trailing newline - the frame's keyhint/status composition adds
// it): short content is padded with blank styled rows so the frame
// keeps its full height and the keyhint/status rows stay anchored at
// the bottom (the R11 slot-reservation rule applied to the frame
// itself).
func (p *pager) render() string {
	p.ensureStyled()
	win := p.vp.window()
	if p.width == 0 {
		win = nil
	}
	for len(win) < p.vp.height {
		win = append(win, padRow("", p.width, p.st.Normal))
	}
	return strings.Join(win, "\n")
}
