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
// Scrolling moves the WINDOW by line (the glow/less model): every
// press changes every visible line, so the renderer repaints the whole
// window - no style-only indicator the diff can drop (the pre-glow
// double-press bug). Content is styled LAZILY: only the visible window
// plus a margin (ensureStyled), never the whole document - the old
// style() pass re-styled 20k lines on every resize (the 385ms stall).
// Styled lines stay cached; a resize or theme switch (the width/
// styleKey invalidation) re-styles only the window. The content is
// bounded (one thread), so the window owns the scroll state (the index
// stays windowed - 129k rows must never flatten). Long lines truncate
// to the window width, never wrap (R11; wrapping is future work) - the
// h/l keys pan horizontally past the truncation instead. The one
// exception is the streamed AI summary (appendText): it wraps to the
// window width as it arrives.
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
	// x is the horizontal pan offset in cells (the h/l keys); maxX the
	// widest content line. The clip lands at render time on the styled
	// window, so panning never invalidates the styled cache.
	x    int
	maxX int
	st   Styles
	vp   viewport
}

func newPager(threadID, msgID string, lines []core.Line) *pager {
	p := &pager{threadID: threadID, msgID: msgID}
	p.setLines(lines)
	return p
}

// setLines replaces the pager content (the compose preview after a
// body edit, the AI summary swap): the expanded layout drops with the
// lines - a stale doc maps old rows into the new list; ensureStyled
// rebuilds it.
// setSMIME prepends the S/MIME verdict banner (R10) when the opened message
// carried a signature. Crypto validity and signer identity are separate: the
// banner names the cert, the user judges it against the From header. No-op
// for unsigned messages.
func (p *pager) setSMIME(s *core.SMIMEStatus) {
	if s == nil || !s.Present {
		return
	}
	banner := core.Line{Text: "[S/MIME] invalid or untrusted signature", Kind: core.LineSecurity}
	switch {
	case s.Err != "":
		banner.Text = "[S/MIME] could not verify: " + s.Err
	case s.Valid:
		banner.Text = "[S/MIME] valid signature from " + s.Signer
		if s.Checked && s.Revoked {
			banner.Text += " (revoked)"
		}
		banner.OK = true
	}
	p.lines = append([]core.Line{banner}, p.lines...)
	p.setLines(p.lines)
}

func (p *pager) setLines(lines []core.Line) {
	p.lines = lines
	p.doc = nil
	p.styled = nil
	p.vp.offset = 0
	p.x = 0
	p.maxX = 0
	for _, l := range lines {
		if w := runewidth.StringWidth(l.Text); w > p.maxX {
			p.maxX = w
		}
	}
}

// pad is the style-time truncation boundary: the window width plus the pan offset, so a panned line still carries the cells the clip will show.
func (p *pager) pad() int { return p.width + p.x }

func (p *pager) setSize(w, h int, st Styles) {
	p.width = w
	p.st = st
	p.vp.setSize(w, h)
	// the styled cache is pad- and style-dependent: a resize, a theme
	// switch, or a pan that moved the boundary invalidates it;
	// ensureStyled re-styles only the visible window (same-width
	// resizes and height changes keep the cached range untouched)
	if key := st.sgr.pagerKey; p.pad() != p.styleWidth || key != p.styleKey {
		p.styleWidth, p.styleKey = p.pad(), key
		clear(p.styled)
		clear(p.doc)
	}
	p.ensureStyled()
}

// relayout rebuilds the expanded document: an image line (only when
// images is on AND the image has decoded dims) spans Image.Rows empty
// rows - the terminal paint fills them - every other line maps 1:1.
// The styled cache survives: doc rows resolve to lines via imgFrom,
// so the toggle and a decode-gained resize never re-style.
func (p *pager) relayout() {
	// image-carrying lines re-style on every layout: their cached text holds the placeholder or the blanked run - the decode state (Rows) decides which
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

// imgRowSpan is a text line's inline-image expansion rows: the tallest decoded image (1 when none are decoded).
func imgRowSpan(l *core.Line) int {
	rows := 1
	for _, im := range l.Imgs {
		rows = max(rows, im.Image.Rows)
	}
	return rows
}

// ensureStyled styles the visible window plus a margin above and
// below, so small scrolls never touch the styled lines; lines outside
// the range stay unstyled until scrolled into it. The styled slice
// doubles as the viewport's content, so the clamp and window math see
// the full document. Image rows carry no text - the terminal paint
// fills them.
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
			p.styled[li] = p.styleLine(li)
		}
		p.doc[i] = p.styled[li]
	}
	p.vp.setLines(p.doc)
}

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
// token stream): complete lines (ending in \n) wrap to the window width
// and append as rows; a trailing partial extends the last line in place
// and wraps it when it overflows - a token-per-event stream renders as
// flowing wrapped text, never one row per token. The summary is the
// only streamed content: mail, html, and preview lines arrive
// producer-wrapped and never re-wrap (R11).
func (p *pager) appendText(text string) {
	for len(text) > 0 {
		i := strings.IndexByte(text, '\n')
		if i < 0 {
			// trailing partial: extend the open last line (or start
			// one), then wrap it in place if it overflows
			if n := len(p.lines); n > 0 && !strings.HasSuffix(p.lines[n-1].Text, "\n") {
				p.lines[n-1].Text += text
			} else {
				p.append(core.Line{Text: text, Kind: core.LineBody})
			}
			p.wrapLast(false)
			return
		}
		seg, rest := text[:i], text[i+1:]
		if n := len(p.lines); n > 0 && !strings.HasSuffix(p.lines[n-1].Text, "\n") {
			// the newline closes the open line: extend it with the
			// segment's text and mark it complete - appending a fresh
			// row leaves the open line dangling and the next delta's
			// text merges into it
			p.lines[n-1].Text += seg
			p.wrapLast(true)
		} else {
			p.appendWrapped(text[:i+1])
		}
		text = rest
	}
}

// appendWrapped wraps one complete line (ends in \n) to the window
// width and appends its rows; the last row keeps the \n marker so the
// next delta starts a fresh line.
func (p *pager) appendWrapped(line string) {
	line = strings.TrimSuffix(line, "\n")
	if line == "" {
		p.append(core.Line{Text: "\n", Kind: core.LineBody})
		return
	}
	rows := wrapText(line, p.width)
	for i, r := range rows {
		if i == len(rows)-1 {
			r += "\n"
		}
		p.append(core.Line{Text: r, Kind: core.LineBody})
	}
}

// wrapLast wraps the open last line (no trailing \n) in place when it
// overflows the window width: the first row keeps the slot, the rest
// append. With close the final row gets the \n - the line is complete
// and the next delta starts fresh; without, the last row stays the open
// continuation the next delta extends. The styled slot always drops -
// the text changed whether or not it split.
func (p *pager) wrapLast(close bool) {
	n := len(p.lines)
	if n == 0 || strings.HasSuffix(p.lines[n-1].Text, "\n") {
		return
	}
	rows := wrapText(p.lines[n-1].Text, p.width)
	if close {
		rows[len(rows)-1] += "\n"
	}
	p.lines[n-1].Text = rows[0]
	if n-1 < len(p.styled) {
		p.styled[n-1] = ""
	}
	for _, r := range rows[1:] {
		p.append(core.Line{Text: r, Kind: core.LineBody})
	}
	p.ensureStyled()
}

// wrapText word-wraps line to width display cells (wcwidth, R11): runs
// of whitespace collapse to a single space separator, a word wider than
// the row hard-breaks on a rune boundary. width <= 0 returns the line
// as a single row (no wrap).
//
// The line's edge separator runs survive (collapsed to one space): the
// streaming splitter leaves a trailing space on an open line, and
// dropping it would concatenate the next token's word to the last one
// ("brown" + "fox " + "jumps" -> "brown foxjumps").
func wrapText(line string, width int) []string {
	if width <= 0 {
		return []string{line}
	}
	head := ""
	if l := strings.TrimLeft(line, " \t"); l != line {
		head = " "
		line = l
	}
	tail := ""
	if l := strings.TrimRight(line, " \t"); l != line {
		tail = " "
		line = l
	}
	words := strings.Fields(line)
	if len(words) == 0 {
		return []string{head + line + tail}
	}
	var rows []string
	var row []rune
	col := 0
	flush := func() {
		if len(row) > 0 {
			rows = append(rows, string(row))
			row = nil
			col = 0
		}
	}
	for _, word := range words {
		runes := []rune(word)
		for runewidth.StringWidth(string(runes)) > width {
			// hard-break: take cells up to the width, commit, repeat
			var piece []rune
			wc := 0
			for _, r := range runes {
				w := runewidth.RuneWidth(r)
				if wc+w > width {
					break
				}
				wc += w
				piece = append(piece, r)
			}
			if len(piece) == 0 {
				piece = runes[:1] // a single rune wider than the row
			}
			flush()
			rows = append(rows, string(piece))
			runes = runes[len(piece):]
		}
		if len(runes) == 0 {
			continue
		}
		if col > 0 && col+1+runewidth.StringWidth(string(runes)) > width {
			flush()
		}
		if col > 0 {
			row = append(row, ' ')
			col++
		}
		row = append(row, runes...)
		col += runewidth.StringWidth(string(runes))
	}
	flush()
	if head != "" && len(rows) > 0 {
		rows[0] = head + rows[0]
	}
	if tail != "" && len(rows) > 0 {
		rows[len(rows)-1] += tail
	}
	return rows
}

// setLinkSel points the easyjump highlight at the label marker under
// entry; the styled cache drops so the window re-renders the reversed
// marker on the next paint.
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

// setImages switches the image expansion (the render-images key): on expands decoded image lines to Image.Rows rows, off collapses them to their Alt row.
func (p *pager) setImages(on bool) {
	if p.images == on {
		return
	}
	p.images = on
	p.relayout()
}

// imgBlock is one visible image block: the source line, its doc row
// (first expanded row - the paint crop anchor), the cell dims (0
// until the decode runs), the image (the block or one of its inline
// rows) and its cell offset.
type imgBlock struct {
	line int
	doc  int
	cols int
	rows int
	img  *core.Image
	x    int
}

// visibleImages lists the window's image lines, one block per image:
// the expanded block (rows walked back to the first doc row for the
// anchor) or the collapsed Alt row (Rows 0 - listed as the decoder's
// trigger, painted as text). The dims may still be 0. A standalone
// image (alone on its line) centers in the window; an inline image
// keeps its flow offset.
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
			out = append(out, imgBlock{line: li, doc: doc, cols: l.Image.Cols, rows: l.Image.Rows,
				img: l.Image, x: max(0, (p.width-l.Image.Cols)/2)})
		}
		for _, im := range l.Imgs {
			x := im.X
			if p.standaloneLine(li, im.Image) {
				x = max(0, (p.width-im.Image.Cols)/2)
			}
			out = append(out, imgBlock{line: li, doc: doc, cols: im.Image.Cols, rows: im.Image.Rows, img: im.Image, x: x})
		}
	}
	return out
}

// standaloneLine reports whether img is the sole content of its line (a
// lone own-line image, or an inline placeholder on an otherwise empty
// text row). Such an image fills the text column and centers instead of
// holding its authored disp size - a mail that sizes a chart for a
// 600px browser column must not leave it half the width of a 120-cell
// terminal. The inline check blanks the Alt placeholder the flow glued
// in: whatever text remains is what truly shares the row.
func (p *pager) standaloneLine(li int, img *core.Image) bool {
	l := &p.lines[li]
	if l.Image == img {
		return true
	}
	if len(l.Imgs) != 1 || l.Imgs[0].Image != img {
		return false
	}
	t := strings.ReplaceAll(l.Text, img.Alt, "")
	return strings.TrimSpace(t) == ""
}

// styleLine maps one structured line to styled text (subject ->
// header, from/date -> hdrdefault, body -> quotedN by depth, etc.).
// Every line pads to the style boundary (the window width plus the pan
// offset) with its own style (the R11 slot-reservation rule); the
// styles' SGR fragments are precomputed (p.st.sgr), so a line is plain
// string joins, never a Style.Render.
//
// quoteColor maps the quote depth to the style table: depth 0 (plain
// body text) is the normal color - a plain mail must not share a
// custom color with the first reply layer quote; depths 1-5 keep their
// own colors (quoted0-4).
func quoteColor(sg sgrSet, quoted int) sgr {
	if quoted <= 0 {
		return sg.normal
	}
	return sg.pagerQuoted[quoted-1]
}

func (p *pager) styleLine(li int) string {
	sg := p.st.sgr
	l := p.lines[li]
	var g sgr
	switch l.Kind {
	case core.LineSubject:
		g = sg.pagerHdr
	case core.LineHeader:
		// the rotation position: how far this line sits into its contiguous header run (a run resets after a non-header line)
		n := 0
		for j := li; j >= 0 && p.lines[j].Kind == core.LineHeader; j-- {
			n++
		}
		g = sg.pagerHdrColor(n - 1)
	case core.LineBody:
		g = quoteColor(sg, l.Quoted)
	case core.LineSignature:
		g = sg.pagerSig
	case core.LineAttachment:
		g = sg.pagerAtt
	case core.LineError:
		g = sg.pagerErr
	case core.LineSecurity:
		// the S/MIME verdict (R10): green when the signature + chain to
		// the pinned roots held, red on a failed verify or no roots
		if l.OK {
			g = sg.pagerSig
		} else {
			g = sg.pagerErr
		}
	default:
		g = sg.normal
	}
	// the line's default background (the html view's mail-declared body color) covers the pad and blank rows; a trailing run background extends over the pad too
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
		text = padRowSGR(text, p.pad(), outer)
	}
	return text
}

// styleRuns joins the run fragments into one styled string: each run
// emits its own SGR open + text, and a reset closes a styled run only
// (an unstyled run's open would be redundant). padRowSGR re-applies
// the line's base style after every reset, so the quoted/header color
// covers the gaps and the pad; a trailing bg run closes with a reset
// so its bg does not leak into the pad.
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
		// a decoded inline image's placeholder run blanks: the pixels paint at its offset, the words would render under them
		if r.Image != nil && p.images && r.Image.Rows > 0 {
			b.WriteString(strings.Repeat(" ", runewidth.StringWidth(r.Text)))
		} else {
			b.WriteString(r.Text)
		}
	}
	// the trailing reset closes any style visible on a space: bg, underline
	// (the easyjump link leak) and reverse (the selected marker) - bold and
	// italic have no glyph on a pad, a colored one is invisible, so they leak free
	last := p.runSel(runs[len(runs)-1])
	if last.Bg != "" || last.Attrs&(core.AttrUnderline|core.AttrReverse) != 0 {
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

// runSGR maps a run's style to its SGR open (truecolor fg/bg, attr bits); "" when the run carries no style, so the line style shows through.
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

// hexRGB parses a #rrggbb color to its "r;g;b" channel form; "" for anything unparseable - a bad color never reaches the terminal.
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

// scrollDown/scrollUp move the window by n lines (j / k / a count). Every press changes every visible line, so the renderer repaints the window - nothing can diff to zero.
func (p *pager) scrollDown(n int) {
	p.vp.offset += n
	p.vp.clamp()
}

func (p *pager) scrollUp(n int) {
	p.vp.offset -= n
	p.vp.clamp()
}

// scrollLeft/scrollRight pan the window horizontally (the h/l keys):
// the offset moves in scrollStep cells, clamped to the content width
// minus the window (a right pan at the end is a no-op). The pan moves
// the style boundary - the styled cache truncates to pad(), so a moved
// boundary invalidates it (the next render re-styles at the new pad).
func (p *pager) scrollLeft() {
	p.x = max(0, p.x-scrollStep)
	if p.pad() != p.styleWidth {
		p.styleWidth, p.styleKey = p.pad(), p.st.sgr.pagerKey
		clear(p.styled)
		clear(p.doc)
	}
}

func (p *pager) scrollRight() {
	p.x = min(p.x+scrollStep, max(0, p.maxX-p.width))
	if p.pad() != p.styleWidth {
		p.styleWidth, p.styleKey = p.pad(), p.st.sgr.pagerKey
		clear(p.styled)
		clear(p.doc)
	}
}

// pageDown/pageUp move a full window (pgdown/pgup); halfPageDown/Up half one (ctrl+d/ctrl+u, vim's default). The clamp pins the last page to the tail, so repeated page-down ends on the bottom.
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
// it): short content pads with blank styled rows so the keyhint/status
// rows stay anchored at the bottom (the R11 rule applied to the frame
// itself).
func (p *pager) render() string {
	p.ensureStyled()
	win := p.vp.window()
	if p.width == 0 {
		win = nil
	}
	if p.x > 0 && p.width > 0 {
		// the horizontal pan: skip the offset, re-pad to the window.
		// The styled lines carry their own per-line base style inside
		// (styleLine's pad), so the re-wrap uses the plain normal - the
		// SGR runs survive the skip and the outer wrap covers the tail.
		for i, l := range win {
			win[i] = padRowSGR(skipStyled(l, p.x), p.width, p.st.sgr.normal)
		}
	}
	for len(win) < p.vp.height {
		win = append(win, padRow("", p.width, p.st.Normal))
	}
	return strings.Join(win, "\n")
}
