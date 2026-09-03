// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

// Metrics measures text in CSS px so stage 1 needs no terminal
// knowledge. The terminal backend provides monospace metrics (one cell
// is one px advance of the chosen char width, wide runes double); a PDF
// backend later provides real font metrics. Break opportunities are
// rune boundaries today; proportional fonts that need grapheme-level
// cuts extend the interface when that backend lands.
type Metrics interface {
	Width(s string) int // px advance of s
}

// RuneStepper is implemented by Metrics that can also advance one rune at
// a time. char-break uses it to carry a running px width instead of
// re-measuring prefixes (quadratic on a giant unbroken token). Width and
// the per-rune widths must agree (a monospace meter satisfies this).
type RuneStepper interface {
	RuneWidth(r rune) int
}
