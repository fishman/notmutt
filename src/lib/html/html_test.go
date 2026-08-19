// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

// Fuzz targets for the CSS boundary (AGENTS.md: parser-adjacent code
// passes the fuzz targets in SECURITY.md before it is accepted). The
// properties under test are panic-freedom and determinism on hostile
// stylesheet text.

import "testing"

// TestBackgroundShorthand pins the background shorthand (the commonest
// way mail declares a body color): the first color token becomes the
// background-color, and the longhand wins when both are present.
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
