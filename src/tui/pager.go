package tui

import (
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"notmutt/mail"
)

// pager holds the open thread's render lines, the scroll window, and
// the READ POSITION: a cursor line inside the visible page. j/k move
// the position with the window holding still; only when the position
// crosses a page edge does the window jump a full page (down: cursor
// lands on the new page's first line, up: on its last line) - reading
// flows through the thread without the window churning on every key.
// The content is bounded (one thread), so the window owns the scroll
// state (the index stays windowed - 129k rows must never flatten).
// Lines are styled once per open/resize (style), never on the repaint
// path - the TUI repaints on every event, so per-frame re-styling
// would throw away the work 5+ times per second. Long lines are
// truncated to the window width, never wrapped (R11 alignment; the
// truncation is a pinned limitation, wrapping is future work).
type pager struct {
	threadID string
	lines    []mail.Line
	vp       pagerViewport
	width    int
	st       Styles
	cursor   int // the read position: the absolute line under the indicator
}

func newPager(threadID string, lines []mail.Line) *pager {
	return &pager{threadID: threadID, lines: lines}
}

func (p *pager) setSize(w, h int, st Styles) {
	p.width = w
	p.st = st
	p.vp.setSize(w, h)
	p.style(st)
	p.clampCursor()
}

// clampCursor keeps the read position on a real line after the content
// or the window shrank.
func (p *pager) clampCursor() {
	if n := len(p.lines); p.cursor > n-1 {
		p.cursor = n - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
}

// style maps the structured lines to styled text once per load (open
// or resize) and hands it to the window: subject -> header, from/date
// -> hdrdefault, body -> quotedN by depth, signature -> signature,
// attachment -> attachment, error -> error. Every line pads to the
// window width with its own style (the R11 slot-reservation rule -
// alignment never shifts per line).
func (p *pager) style(st Styles) {
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
		case mail.LineError:
			s = st.Error
		default:
			s = st.Normal
		}
		text := s.Render(l.Text)
		if p.width > 0 {
			text = padRow(text, p.width, s)
		}
		b.WriteString(text)
		b.WriteByte('\n')
	}
	p.vp.SetContent(b.String())
}

// scrollDown advances the read position n lines (j / down / a count).
// The window holds still until the position passes the bottom edge,
// then jumps a full page and lands the position on the new page's
// first line - continuous reading flow, the window only ever moves a
// full page.
func (p *pager) scrollDown(n int) {
	for i := 0; i < n && p.cursor < len(p.lines)-1; i++ {
		p.cursor++
		if p.cursor > p.vp.offset+p.vp.height-1 {
			p.vp.offset += p.vp.height
			p.vp.clamp()
			p.cursor = p.vp.offset
		}
	}
}

// scrollUp mirrors scrollDown: at the top edge the window jumps a full
// page up and the position lands on the new page's last line.
func (p *pager) scrollUp(n int) {
	for i := 0; i < n && p.cursor > 0; i++ {
		p.cursor--
		if p.cursor < p.vp.offset {
			p.vp.offset -= p.vp.height
			p.vp.clamp()
			p.cursor = p.vp.offset + p.vp.height - 1
		}
	}
}

// scrollTop/scrollBottom jump the position and the window absolutely
// (g / G).
func (p *pager) scrollTop() {
	p.cursor = 0
	p.vp.offset = 0
}

func (p *pager) scrollBottom() {
	p.cursor = len(p.lines) - 1
	p.vp.offset = len(p.lines)
	p.vp.clamp()
}

// pageDown/pageUp move the position by half a window (ctrl+d/ctrl+u,
// vim's default) through the same edge machinery.
func (p *pager) pageDown() { p.scrollDown(p.vp.height / 2) }
func (p *pager) pageUp()   { p.scrollUp(p.vp.height / 2) }

// render returns the styled window with the read position's line
// wrapped in the indicator style (R11); the repaint path never
// re-styles. The window is copied so the indicator wrap never persists
// into the styled lines.
func (p *pager) render() string {
	win := p.vp.window()
	if len(win) == 0 {
		return ""
	}
	if idx := p.cursor - p.vp.offset; idx >= 0 && idx < len(win) {
		win[idx] = padRow(win[idx], p.width, p.st.Indicator)
	}
	return strings.Join(win, "\n") + "\n"
}

// pagerViewport is the pager's line-based window, hand-rolled like the
// index windowing: bubbletea v1.1.0 carries no viewport package (it
// moved to the separate bubbles module), and the pager needs only an
// offset into styled lines - the movement logic lives on the pager
// (R7 supply-chain bar keeps it dependency-free).
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

// SetContent replaces the window's lines (render output, already
// styled and width-padded).
func (v *pagerViewport) SetContent(s string) {
	v.lines = strings.Split(s, "\n")
	if n := len(v.lines); n > 0 && v.lines[n-1] == "" {
		v.lines = v.lines[:n-1]
	}
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

// window returns a copy of the visible line range: the pager's render
// wraps the read-position line in the indicator style, and the copy
// keeps that wrap out of the styled lines (a later render must see the
// clean line, not an ever-growing nest of indicator sequences).
func (v *pagerViewport) window() []string {
	last := v.offset + v.height
	if last > len(v.lines) {
		last = len(v.lines)
	}
	return slices.Clone(v.lines[v.offset:last])
}
