// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

// Fuzz targets for the CSS boundary (AGENTS.md: parser-adjacent code
// must pass SECURITY.md's fuzz targets). Under test: panic-freedom
// and determinism on hostile stylesheet text.

import (
	"testing"

	"golang.org/x/net/html"
)

// TestDisplayNotInherited pins the CSS rule: display is not inherited,
// so a block element's content computes the tag default (""), not the
// parent's display.
func TestDisplayNotInherited(t *testing.T) {
	parent := &Style{Display: "block"}
	for _, tag := range []string{"img", "span", "a"} {
		n := &html.Node{Type: html.ElementNode, Data: tag}
		if got := StyleOf(n, parent, nil).Display; got != "" {
			t.Fatalf("<%s> must compute the tag default display, got %q", tag, got)
		}
	}
}

// TestBackgroundShorthand pins the shorthand: the first color token
// becomes the background-color; the longhand wins when both are present.
func TestBackgroundShorthand(t *testing.T) {
	cases := []struct{ css, want string }{
		{"background: #111111", "#111111"},
		{"background: #fff url(bg.png) no-repeat", "#ffffff"},
		{"background: url(bg.png) #fff", "#ffffff"},
		{"background: transparent", ""},
		{"background-color: #222", "#222222"},
		{"background: #111; background-color: #222", "#222222"},
	}
	for _, c := range cases {
		s := Style{}
		s.apply(ParseDecls(c.css))
		if s.Bg != c.want {
			t.Errorf("%s: got %q, want %q", c.css, s.Bg, c.want)
		}
	}
}

func FuzzCSSDeclarations(f *testing.F) {
	f.Add("color: red; font-weight: bold")
	f.Add("background-color: #fff; text-align: center")
	f.Add("p { color: red } .x { font-style: italic }")
	f.Add("/* c */ a { color: rgb(1,2,3) }")
	f.Fuzz(func(t *testing.T, s string) {
		cssColor(s)
		ParseDecls(s)
		ParseStyleSheet(s)
	})
}

// TestRuneWidth pins the rune-level cell width: ASCII 1, wide 2, C0
// control 0 - the measure cellMeter steps per rune with.
func TestRuneWidth(t *testing.T) {
	if RuneWidth('a') != 1 || RuneWidth('界') != 2 || RuneWidth(0x01) != 0 {
		t.Fatalf("RuneWidth wide/control mismatch")
	}
}
