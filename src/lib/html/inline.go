// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import (
	"strings"
	"unicode/utf8"
)

// wsB is the layout behavior of one white-space class.
type wsB struct {
	wrap         bool // text may wrap at break opportunities
	collapse     bool // collapsible whitespace collapses
	newlineBreak bool // LF is a forced break (the pre family)
}

// wsBehavior maps a white-space class to its behavior (weasyprint
// text/line_break.py): normal/pre-wrap/pre-line wrap; normal/nowrap/
// pre-line collapse; the pre family breaks on LF.
func wsBehavior(w WS) wsB {
	switch w {
	case WSNowrap:
		return wsB{collapse: true}
	case WSPre:
		return wsB{newlineBreak: true}
	case WSPreWrap:
		return wsB{wrap: true, newlineBreak: true}
	case WSPreLine:
		return wsB{wrap: true, collapse: true, newlineBreak: true}
	default: // WSNormal
		return wsB{wrap: true, collapse: true}
	}
}

// atom is one laid-out fragment of inline content. Exactly one of
// text / img / br is live; sep marks a single collapsible space.
type atom struct {
	st   *Style
	ws   WS // effective white-space of the text that made this atom
	text string
	img  *Box // atomic image (px width resolved by the image plan)
	br   bool // <br> or preserved newline: forces a break
	sep  bool // one collapsed space (a break point iff the text wraps)
}

func (a atom) width(m Metrics) int {
	if a.img != nil {
		return 0
	}
	return m.Width(a.text)
}

// collapsible reports a CSS-collapsible rune (space, tab, LF, CR, FF).
func collapsible(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\f':
		return true
	}
	return false
}

// normalizeLF folds CRLF and lone CR to LF so a break never double-fires.
func normalizeLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// atomizeText collapses one text leaf under its effective white-space
// class into atoms. Collapse modes split into words separated by single
// space atoms (sep); the pre family keeps text verbatim, splitting only
// on newlines (a trailing LF drops; blank source lines leave only their
// break, mirroring the walker's empty-line drop).
func atomizeText(txt string, st *Style, ws WS) []atom {
	b := wsBehavior(ws)
	txt = normalizeLF(txt)
	if !b.collapse {
		var out []atom
		for i, ln := range strings.Split(strings.TrimSuffix(txt, "\n"), "\n") {
			if i > 0 {
				out = append(out, atom{st: st, ws: ws, br: true})
			}
			if ln == "" {
				continue
			}
			out = append(out, atom{st: st, ws: ws, text: ln})
		}
		return out
	}
	var out []atom
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			out = append(out, atom{st: st, ws: ws, text: word.String()})
			word.Reset()
		}
	}
	sep := false
	for _, r := range txt {
		if collapsible(r) {
			if b.newlineBreak && r == '\n' { // pre-line: LF is a kept break
				flush()
				out = append(out, atom{st: st, ws: ws, br: true})
				sep = false
			} else {
				sep = true
			}
			continue
		}
		if sep {
			flush() // the prior word atomizes before its break space
			out = append(out, atom{st: st, ws: ws, text: " ", sep: true})
			sep = false
		}
		word.WriteRune(r)
	}
	flush()
	if sep {
		out = append(out, atom{st: st, ws: ws, text: " ", sep: true})
	}
	return out
}

// flattenInline turns a block's inline-level children into one ordered
// atom stream. RoleInline boxes flatten transparently: their text leaves
// already carry the effective white-space class (promoted by the box
// builder), so the tag is irrelevant at layout time. Consecutive sep
// atoms from adjacent leaves collapse at fill (one rendered space), not
// here.
func flattenInline(cs []*Box) []atom {
	var out []atom
	for _, c := range cs {
		switch c.Role {
		case RoleInline:
			out = append(out, flattenInline(c.Children)...)
		case RoleText:
			out = append(out, atomizeText(c.Text, c.St, c.WS)...)
		case RoleImg:
			out = append(out, atom{st: c.St, ws: c.WS, img: c})
		case RoleBR:
			out = append(out, atom{st: c.St, ws: c.WS, br: true})
		}
	}
	return out
}

// LineBox is one filled line: its laid-out atoms in order.
type LineBox struct {
	Atoms []atom
	Width int // content px width (trailing space already dropped)
	X     int // lead offset for block text-align (px), 0 = flush left
}

// LayoutInline lays out one block's uniformly-inline children at the
// given content width. m measures text; norm enables normalize mode
// (wrap every class, char-break words that still overflow). Vertical
// flow is block.go's job. The block's text-align offsets each line.
func LayoutInline(block *Box, width int, m Metrics, norm bool) []LineBox {
	atoms := flattenInline(block.Children)
	var lines []LineBox
	var cur []atom
	cw := 0
	curBreak := false // the trailing atom is a legal break point
	emit := func(a atom) {
		cur = append(cur, a)
		cw += a.width(m)
		curBreak = false
	}
	flush := func() {
		if n := len(cur); n > 0 && cur[n-1].sep {
			cw -= cur[n-1].width(m)
			cur = cur[:n-1] // a line never renders a trailing space
		}
		if len(cur) == 0 {
			return // breaks with nothing on the line emit nothing
		}
		lines = append(lines, LineBox{Atoms: cur, Width: cw})
		cur = nil
		cw = 0
		curBreak = false
	}
	// charBreak emits a text atom in width-wide pieces, one per line
	// (normalize's overflow-wrap:anywhere).
	charBreak := func(a atom) {
		for a.text != "" {
			head, tail := cutText(a.text, width, m)
			a.text = tail
			emit(atom{st: a.st, ws: a.ws, text: head})
			if a.text != "" {
				flush()
			}
		}
	}
	for _, a := range atoms {
		if a.br {
			flush()
			continue
		}
		if a.img != nil {
			emit(a)
			continue
		}
		if a.sep {
			// one space between words on the same line; a separator
			// that opens a line (or follows one) drops here or at flush
			if len(cur) > 0 && !cur[len(cur)-1].sep {
				emit(a)
			}
			curBreak = wsBehavior(a.ws).wrap || norm
			continue
		}
		b := wsBehavior(a.ws)
		if norm {
			b.wrap = true // normalize wraps every class
		}
		if !b.collapse && b.wrap {
			// preserved text that wraps at interior spaces (pre-wrap in
			// author mode; pre and pre-wrap under normalize)
			rest := a.text
			for rest != "" {
				if m.Width(rest) <= width-cw {
					emit(atom{st: a.st, ws: a.ws, text: rest})
					break
				}
				if head, tail, ok := breakAtSpace(rest, width-cw, m); ok {
					emit(atom{st: a.st, ws: a.ws, text: head})
					flush()
					rest = tail
					continue
				}
				if len(cur) > 0 {
					flush()
					continue
				}
				if norm {
					charBreak(atom{st: a.st, ws: a.ws, text: rest})
				} else {
					emit(atom{st: a.st, ws: a.ws, text: rest}) // overflow whole
				}
				break
			}
			continue
		}
		aw := a.width(m)
		if aw == 0 {
			continue
		}
		if len(cur) > 0 && cw+aw > width && curBreak {
			flush() // break before the word; the trailing space drops
		}
		if len(cur) > 0 && cw+aw > width && norm {
			if width-cw <= 0 {
				flush() // line exactly full: break at the token's leading edge
			} else {
				head, tail := cutText(a.text, width-cw, m)
				a.text = tail
				aw = a.width(m)
				emit(atom{st: a.st, ws: a.ws, text: head})
				flush()
			}
			// tail (or the whole token, when budget was 0) continues from a
			// fresh line: the len(cur)==0 && aw>width gate handles it below
		}
		if len(cur) == 0 && aw > width {
			if norm {
				charBreak(a)
			} else {
				emit(a) // author mode: an over-wide word overflows
			}
			continue
		}
		emit(a)
	}
	flush()
	applyAlign(lines, block, width)
	return lines
}

// breakAtSpace splits preserved text at the last space whose preceding
// run fits the budget, dropping the break space itself (neither line
// keeps it). ok is false when no such space exists.
func breakAtSpace(s string, budget int, m Metrics) (head, tail string, ok bool) {
	best := -1
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' || i == 0 {
			continue
		}
		if m.Width(s[:i]) <= budget {
			best = i
		}
	}
	if best < 0 {
		return "", s, false
	}
	return s[:best], s[best+1:], true
}

// cutText cuts the longest rune prefix of s that fits the px budget,
// always returning at least one rune so char-break makes progress. When
// the meter steps rune-by-rune the running width is carried, so cutting
// a giant token is O(n); otherwise prefixes are re-measured (the
// proportional-metrics fallback, correct but not linear).
func cutText(s string, budget int, m Metrics) (head, tail string) {
	if s == "" {
		return "", ""
	}
	if st, ok := m.(RuneStepper); ok {
		w := 0
		i := 0
		for i < len(s) {
			r, n := utf8.DecodeRuneInString(s[i:])
			if w > 0 && w+st.RuneWidth(r) > budget {
				break
			}
			w += st.RuneWidth(r)
			i += n
		}
		return s[:i], s[i:]
	}
	_, size := utf8.DecodeRuneInString(s)
	if m.Width(s[:size]) > budget {
		return s[:size], s[size:]
	}
	i := size
	for i < len(s) {
		_, n := utf8.DecodeRuneInString(s[i:])
		if m.Width(s[:i+n]) > budget {
			break
		}
		i += n
	}
	return s[:i], s[i:]
}

// applyAlign offsets each line for the block's text-align (center and
// right pad in px; a line wider than the content box stays flush left).
func applyAlign(lines []LineBox, block *Box, width int) {
	align := ""
	if block.St != nil {
		align = block.St.Align
	}
	for i := range lines {
		switch align {
		case "center":
			if x := (width - lines[i].Width) / 2; x > 0 {
				lines[i].X = x
			}
		case "right":
			if x := width - lines[i].Width; x > 0 {
				lines[i].X = x
			}
		}
	}
}
