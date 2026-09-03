// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

// The HTML flow renderer facade (docs/html-rendering-analysis.md): the
// stage-2 engine in html_stage2.go parses (x/net/html, the fuzz-exercised
// trust boundary), builds the lib/html box tree, lays out the px row
// stream, and quantizes it to pager lines. This file keeps the public
// entry points, the render budgets, and the F1 sanitize gate.

import (
	"notmutt/core"
)

const (
	htmlWrapWidth = 120  // no TUI width at render; shr.el's 120-cap reference, the plain path wraps at the same width
	maxHTMLLines  = 5000 // render budget: a hostile doc cannot balloon the thread
)

// RenderHTML renders an HTML mail body to pager lines (light mode -
// today's verbatim colors, white default); nil for an empty result
// (the caller falls back to the raw text). width caps at htmlWrapWidth.
// The dark render flows through RenderThread (the open job resolves the
// [html] dark-mode setting there).
func RenderHTML(body string, atts []Attachment, width int) []core.Line {
	lines, _ := renderHTML(body, atts, width, false, false, "")
	return lines
}

// RenderHTMLWithLinks is the pager F key's link mode (easyjump-style):
// every link - anchor href or bare URL word - gets an inline "[N]"
// label; the label order is the returned list (label N opens
// Links[N-1]), both in document order.
func RenderHTMLWithLinks(body string, atts []Attachment, width int) ([]core.Line, []string) {
	return renderHTML(body, atts, width, true, false, "")
}

// renderHTML routes to the stage-2 engine (html_stage2.go); renderStage2HTML
// parses, clamps width, and quantizes the lib/html row stream.
func renderHTML(body string, atts []Attachment, width int, labelLinks, dark bool, themeBG string) ([]core.Line, []string) {
	return renderStage2HTML(body, atts, width, labelLinks, dark, themeBG)
}

// sanitize is the F1 gate: DOM text is raw - ESC/C0 never reach the pager.
func sanitize(s string) string {
	return core.SanitizeControls(s)
}
