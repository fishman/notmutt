// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"testing"

	"notmutt/core"
)

// The dark-mode walker contract (the html spec section 6): the theme
// background substitutes the mail's assumed white, declared colors map
// by reflection/hue-preserving inversion, and the luma gate keeps a
// dark-declared mail dark.

func TestRenderHTMLDark(t *testing.T) {
	// unstyled mail: the theme bg, unstyled text carries the theme fg ("")
	lines, _ := renderHTML("<html><body><p>hello</p></body></html>", nil, 0, false, true, "#282c34")
	if len(lines) == 0 || lines[0].Bg != "#282c34" {
		t.Fatalf("unstyled dark line bg = %q, want the theme bg", lineBG(lines))
	}
	if fg := firstRunFg(lines); fg != "" {
		t.Errorf("unstyled dark fg = %q, want theme text (\"\")", fg)
	}
}

func TestRenderHTMLDarkDeclared(t *testing.T) {
	// a light-declared page bg reflects onto the theme bg
	lines, _ := renderHTML(`<body style="background:#f4f4f4">x</body>`, nil, 0, false, true, "#282c34")
	if got := lineBG(lines); got != "#33373f" {
		t.Errorf("light body bg = %q, want reflected #33373f", got)
	}
	// a dark-declared page bg passes through (the luma gate)
	lines, _ = renderHTML(`<body style="background:#111111">x</body>`, nil, 0, false, true, "#282c34")
	if got := lineBG(lines); got != "#111111" {
		t.Errorf("dark body bg = %q, want #111111 unchanged", got)
	}
	// a bgcolor table cell reflects its light bg
	lines, _ = renderHTML(`<table><tr><td bgcolor="#f4f4f4">x</td></tr></table>`, nil, 0, false, true, "#282c34")
	if !hasRunBG(lines, "#33373f") {
		t.Errorf("bgcolor cell = %v, want a run with the reflected #33373f", lineBGs(lines))
	}
}

func TestRenderHTMLDarkOff(t *testing.T) {
	// dark-mode off: today's render - declared colors verbatim, white default
	lines, _ := renderHTML(`<p style="color:#0066cc">blue</p>`, nil, 0, false, false, "")
	if got := firstRunFg(lines); got != "#0066cc" {
		t.Errorf("light-mode fg = %q, want verbatim #0066cc", got)
	}
	if len(lines) == 0 || lines[0].Bg != "#ffffff" {
		t.Errorf("light-mode bg = %q, want the white default", lineBG(lines))
	}
}

func lineBG(lines []core.Line) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[0].Bg
}

func firstRunFg(lines []core.Line) string {
	for _, l := range lines {
		for _, r := range l.Runs {
			if r.Fg != "" {
				return r.Fg
			}
		}
	}
	return ""
}

func lineBGs(lines []core.Line) []string {
	var out []string
	for _, l := range lines {
		out = append(out, l.Bg)
	}
	return out
}

func hasRunBG(lines []core.Line, want string) bool {
	for _, l := range lines {
		for _, r := range l.Runs {
			if r.Bg == want {
				return true
			}
		}
	}
	return false
}
