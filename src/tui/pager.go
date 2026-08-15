package tui

import (
	"slices"
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
	styleKey   string   // the style set the cache was built with (sgr opens)
	width      int
	styleWidth int // the width the cache was styled at (0 = none)
	st         Styles
	vp         pagerViewport
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
	}
	p.ensureStyled()
}

// ensureStyled styles the visible window plus a margin above and
// below, so small scroll movements never touch the styled lines; lines
// outside the range stay unstyled until scrolled into it. The styled
// slice doubles as the viewport's content, so the clamp and window
// math see the full document.
func (p *pager) ensureStyled() {
	if len(p.styled) != len(p.lines) {
		p.styled = make([]string, len(p.lines))
	}
	first := p.vp.offset - pagerStyleMargin
	if first < 0 {
		first = 0
	}
	last := p.vp.offset + p.vp.height + pagerStyleMargin
	if last > len(p.lines) {
		last = len(p.lines)
	}
	for i := first; i < last; i++ {
		if p.styled[i] == "" {
			p.styled[i] = p.styleLine(p.lines[i])
		}
	}
	p.vp.lines = p.styled
}

// styleLine maps one structured line to styled text: subject ->
// header, from/date -> hdrdefault, body -> quotedN by depth,
// signature -> signature, attachment -> attachment, error -> error.
// Every line pads to the window width with its own style (the R11
// slot-reservation rule - alignment never shifts per line). The
// styles' SGR fragments are precomputed (p.st.sgr), so a line is
// plain string joins, never a Style.Render.
func (p *pager) styleLine(l core.Line) string {
	sg := p.st.sgr
	var g sgr
	switch l.Kind {
	case core.LineSubject:
		g = sg.pagerHdr
	case core.LineHeader:
		g = sg.pagerDef
	case core.LineBody:
		g = sg.pagerQuoted[l.Quoted]
	case core.LineSignature:
		g = sg.pagerSig
	case core.LineAttachment:
		g = sg.pagerAtt
	case core.LineError:
		g = sg.pagerErr
	default:
		g = sg.normal
	}
	text := g.render(l.Text)
	if p.width > 0 {
		text = padRowSGR(text, p.width, g)
	}
	return text
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

// pagerViewport is the pager's line-based window, hand-rolled like the
// index windowing: the bubbles viewport package is not a dependency
// (R7 supply-chain bar), and the pager needs only an offset into
// styled lines - the movement logic lives on the pager.
type pagerViewport struct {
	lines  []string
	offset int
	width  int
	height int
}

func (v *pagerViewport) setSize(w, h int) {
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	v.width, v.height = w, h
	v.clamp()
}

// clamp keeps the offset inside [0, len-lines-height]; a window taller
// than the content pins to the top.
func (v *pagerViewport) clamp() {
	if max := len(v.lines) - v.height; v.offset > max {
		v.offset = max
	}
	if v.offset < 0 {
		v.offset = 0
	}
}

// window returns the visible line range as a copy: the pager's render
// pads short content, and the copy keeps the padding out of the styled
// lines (a later render must see the clean content, not an ever-growing
// pile of blank rows).
func (v *pagerViewport) window() []string {
	last := v.offset + v.height
	if last > len(v.lines) {
		last = len(v.lines)
	}
	return slices.Clone(v.lines[v.offset:last])
}
