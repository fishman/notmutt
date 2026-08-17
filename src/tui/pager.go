package tui

import (
	"fmt"
	"strconv"
	"strings"

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
	threadID   string
	lines      []core.Line
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

func newPager(threadID string, lines []core.Line) *pager {
	return &pager{threadID: threadID, lines: lines}
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
	n := len(p.lines)
	if p.images {
		for _, l := range p.lines {
			if l.Image != nil && l.Image.Rows > 0 {
				n += l.Image.Rows - 1
			}
		}
	}
	doc := make([]string, n)
	from := make([]int, n)
	isImg := make([]bool, n)
	j := 0
	for i := range p.lines {
		expanded := p.images && p.lines[i].Image != nil && p.lines[i].Image.Rows > 0
		rows := 1
		if expanded {
			rows = p.lines[i].Image.Rows
		}
		for r := 0; r < rows; r++ {
			from[j] = i
			isImg[j] = expanded
			j++
		}
	}
	p.doc, p.imgFrom, p.imgRow = doc, from, isImg
	p.vp.setLines(doc)
	p.vp.clamp()
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
// (first expanded row - the paint crop anchor), and the cell dims
// (0 until the decode runs).
type imgBlock struct {
	line int
	doc  int
	cols int
	rows int
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
		if l.Image == nil {
			continue
		}
		seen[li] = true
		doc := i
		for doc > 0 && p.imgFrom[doc-1] == li {
			doc--
		}
		out = append(out, imgBlock{line: li, doc: doc, cols: l.Image.Cols, rows: l.Image.Rows})
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
			prev := runs[i-1]
			if prev.Fg != "" || prev.Bg != "" || prev.Attrs != 0 {
				b.WriteString("\x1b[0m")
			}
		}
		if open := runSGR(r); open != "" {
			b.WriteString(open)
		}
		b.WriteString(r.Text)
	}
	if last := runs[len(runs)-1]; last.Bg != "" {
		b.WriteString("\x1b[0m")
	}
	return b.String()
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
