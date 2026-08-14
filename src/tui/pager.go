package tui

import (
	"slices"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"notmutt/mail"
)

// pager holds the open thread's render lines and the scroll window.
// Scrolling moves the WINDOW by line (the glow/less model): j/k shift
// the offset one line, so every press changes every visible line and
// the renderer repaints the whole window - no read-position indicator
// whose style-only change the diff can drop (the pre-glow model that
// made the first press render nothing). Content is styled once per
// open/resize (style), never on the repaint path - the TUI repaints on
// every event, so per-frame re-styling would throw away the work 5+
// times per second. The content is bounded (one thread), so the window
// owns the scroll state (the index stays windowed - 129k rows must
// never flatten). Long lines are truncated to the window width, never
// wrapped (R11 alignment; the truncation is a pinned limitation,
// wrapping is future work).
type pager struct {
	threadID string
	lines    []mail.Line
	vp       pagerViewport
	width    int
	st       Styles
}

func newPager(threadID string, lines []mail.Line) *pager {
	return &pager{threadID: threadID, lines: lines}
}

func (p *pager) setSize(w, h int, st Styles) {
	p.width = w
	p.st = st
	p.vp.setSize(w, h)
	p.style(st)
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
