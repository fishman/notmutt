package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"

	"notmutt/mail"
)

// pager holds the open thread's render lines and the scroll window.
// The content is bounded (one thread), so the window owns the scroll
// state (the index stays windowed - 129k rows must never flatten).
type pager struct {
	threadID string
	lines    []mail.Line
	vp       pagerViewport
	width    int
}

func newPager(threadID string, lines []mail.Line) *pager {
	return &pager{threadID: threadID, lines: lines}
}

func (p *pager) setSize(w, h int) {
	p.width = w
	p.vp.setSize(w, h)
}

// render maps the structured lines to styled text and hands it to the
// window: subject -> header, from/date -> hdrdefault, body -> quotedN
// by depth, signature -> signature, attachment -> attachment. Every
// line pads to the window width with its own style (the R11
// slot-reservation rule - alignment never shifts per line).
func (p *pager) render(st Styles) string {
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
	return p.vp.View()
}

// pagerViewport is the pager's line-based scroll window, hand-rolled
// like the index windowing: bubbletea v1.1.0 carries no viewport
// package (it moved to the separate bubbles module), and the pager
// needs only line/half-page/top/bottom scrolling - the R7 supply-chain
// bar keeps it dependency-free.
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

func (v *pagerViewport) LineDown(n int) { v.offset += n; v.clamp() }
func (v *pagerViewport) LineUp(n int)   { v.offset -= n; v.clamp() }
func (v *pagerViewport) HalfPageDown()  { v.LineDown(v.height / 2) }
func (v *pagerViewport) HalfPageUp()    { v.LineUp(v.height / 2) }
func (v *pagerViewport) GotoTop()       { v.offset = 0 }
func (v *pagerViewport) GotoBottom()    { v.offset = len(v.lines); v.clamp() }

// View renders the visible window as newline-joined lines.
func (v *pagerViewport) View() string {
	if v.height == 0 {
		return ""
	}
	var b strings.Builder
	last := v.offset + v.height
	if last > len(v.lines) {
		last = len(v.lines)
	}
	for i := v.offset; i < last; i++ {
		b.WriteString(v.lines[i])
		b.WriteByte('\n')
	}
	return b.String()
}
