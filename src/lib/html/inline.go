// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

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
