// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import "strings"

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
	sep := false
	for _, r := range txt {
		if collapsible(r) {
			if b.newlineBreak && r == '\n' { // pre-line: LF is a kept break
				out = append(out, atom{st: st, ws: ws, br: true})
				sep = false
			} else {
				sep = true
			}
			continue
		}
		if sep {
			out = append(out, atom{st: st, ws: ws, text: " ", sep: true})
			sep = false
		}
		if n := len(out); n > 0 && !out[n-1].sep && !out[n-1].br {
			out[n-1].text += string(r)
		} else {
			out = append(out, atom{st: st, ws: ws, text: string(r)})
		}
	}
	if sep {
		out = append(out, atom{st: st, ws: ws, text: " ", sep: true})
	}
	return out
}
