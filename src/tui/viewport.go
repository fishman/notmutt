package tui

import "slices"

// viewport is the pager widget: a scroll window over a pre-rendered
// line list (R5 - the widget the mail pager, the help dialog, and the
// compose form share). Offset math only; the content's styling stays
// at the call sites. Hand-rolled like the index windowing: the bubbles
// viewport package is not a dependency (R7 supply-chain bar).
type viewport struct {
	lines  []string
	offset int
	width  int
	height int
}

func (v *viewport) setLines(lines []string) {
	v.lines = lines
	v.clamp()
}

func (v *viewport) setSize(w, h int) {
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
func (v *viewport) clamp() {
	if max := len(v.lines) - v.height; v.offset > max {
		v.offset = max
	}
	if v.offset < 0 {
		v.offset = 0
	}
}

// window returns the visible line range as a copy: the pager's render
// pads short content, and the copy keeps the padding out of the lines
// (a later render must see the clean content, not an ever-growing pile
// of blank rows).
func (v *viewport) window() []string {
	last := v.offset + v.height
	if last > len(v.lines) {
		last = len(v.lines)
	}
	return slices.Clone(v.lines[v.offset:last])
}

// scrollDown/scrollUp move the window by n lines (j / k / a count).
func (v *viewport) scrollDown(n int) {
	v.offset += n
	v.clamp()
}

func (v *viewport) scrollUp(n int) {
	v.offset -= n
	v.clamp()
}

// pageDown/pageUp move a full window (pgdown/pgup); halfPageDown/Up
// half a window (ctrl+d/ctrl+u, vim's default). The clamp pins the
// last page to the tail, so repeated page-down ends on the bottom.
func (v *viewport) pageDown()     { v.offset += v.height; v.clamp() }
func (v *viewport) pageUp()       { v.offset -= v.height; v.clamp() }
func (v *viewport) halfPageDown() { v.offset += v.height / 2; v.clamp() }
func (v *viewport) halfPageUp()   { v.offset -= v.height / 2; v.clamp() }

// scrollTop/scrollBottom jump the window absolutely (g / G).
func (v *viewport) scrollTop() {
	v.offset = 0
}

func (v *viewport) scrollBottom() {
	v.offset = len(v.lines) - v.height
	v.clamp()
}

// ensureVisible scrolls the window so row is on screen (the compose
// form's follow-cursor): above the window scrolls up, below scrolls
// down to it.
func (v *viewport) ensureVisible(row int) {
	if row < v.offset {
		v.offset = row
	}
	if v.height > 0 && row >= v.offset+v.height {
		v.offset = row - v.height + 1
	}
	v.clamp()
}
