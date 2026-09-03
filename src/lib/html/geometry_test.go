// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import "testing"

func TestParseMarginLengths(t *testing.T) {
	cases := map[string]int{
		"12px": 12, "0": 0, "0px": 0, "auto": 0,
		"1em": 16, "1.5em": 24, "2em": 32, "0.67em": 11,
	}
	for in, want := range cases {
		got, ok := parseLen(in)
		if !ok || got != want {
			t.Errorf("parseLen(%q) = %d,%v want %d,true", in, got, ok, want)
		}
	}
	for _, in := range []string{"10%", "inherit", "initial", "xx", "1.5px", "-2px"} {
		if _, ok := parseLen(in); ok {
			t.Errorf("parseLen(%q) accepted an unsupported value", in)
		}
	}
}

func TestApplyMarginShorthandSetsSides(t *testing.T) {
	var s Style
	s.apply(ParseDecls("margin: 1px 2px 3px 4px"))
	if s.MarginTop != 1 || s.MarginRight != 2 || s.MarginBottom != 3 || s.MarginLeft != 4 {
		t.Fatalf("4-value margin = %d/%d/%d/%d", s.MarginTop, s.MarginRight, s.MarginBottom, s.MarginLeft)
	}
	for _, set := range []bool{s.MarginTopSet, s.MarginRightSet, s.MarginBottomSet, s.MarginLeftSet} {
		if !set {
			t.Fatal("margin shorthand must mark all four sides set")
		}
	}
	s = Style{}
	s.apply(ParseDecls("margin: 1em 0"))
	if s.MarginTop != 16 || s.MarginBottom != 16 || s.MarginRight != 0 || s.MarginLeft != 0 {
		t.Fatalf("2-value margin = %d/%d/%d/%d", s.MarginTop, s.MarginRight, s.MarginBottom, s.MarginLeft)
	}
}

func TestApplyMarginLonghandOverridesShorthand(t *testing.T) {
	var s Style
	s.apply(ParseDecls("margin: 1px; margin-top: 2em"))
	if s.MarginTop != 32 || s.MarginTopSet != true {
		t.Fatalf("longhand override top = %d", s.MarginTop)
	}
	if s.MarginBottom != 1 {
		t.Fatalf("shorthand bottom leaked = %d", s.MarginBottom)
	}
}

func TestMarginsDoNotInherit(t *testing.T) {
	parent := &Style{MarginTop: 16, MarginTopSet: true, MarginBottom: 16, PadLeft: 40}
	child := StyleOf(el("p"), parent, nil)
	if child.MarginTop != 0 || child.MarginBottom != 0 || child.PadLeft != 0 {
		t.Fatalf("geometry inherited: t=%d b=%d pl=%d", child.MarginTop, child.MarginBottom, child.PadLeft)
	}
	if child.MarginTopSet || child.MarginBottomSet {
		t.Fatal("margin set flags must not inherit")
	}
}

func TestUAMarginsFillUnsetSides(t *testing.T) {
	cases := []struct {
		tag        string
		depth      int
		t, r, b, l int
		pl         int
	}{
		{"p", 0, 16, 0, 16, 0, 0},
		{"ul", 0, 16, 0, 16, 0, 40},
		{"ul", 1, 0, 0, 0, 0, 40}, // nested list drops its vertical margins
		{"ol", 0, 16, 0, 16, 0, 40},
		{"li", 0, 0, 0, 0, 0, 0},
		{"blockquote", 0, 16, 40, 16, 40, 0},
		{"dd", 0, 16, 0, 16, 40, 0},
		{"hr", 0, 8, 0, 8, 0, 0},
		{"h1", 0, 21, 0, 21, 0, 0},
		{"h4", 0, 21, 0, 21, 0, 0},
		{"h6", 0, 25, 0, 25, 0, 0},
		{"span", 0, 0, 0, 0, 0, 0},
	}
	for _, tc := range cases {
		var s Style
		uaMargins(tc.tag, tc.depth, &s)
		if s.MarginTop != tc.t || s.MarginRight != tc.r || s.MarginBottom != tc.b || s.MarginLeft != tc.l || s.PadLeft != tc.pl {
			t.Errorf("uaMargins(%s,%d) = %d/%d/%d/%d pl%d, want %d/%d/%d/%d pl%d",
				tc.tag, tc.depth, s.MarginTop, s.MarginRight, s.MarginBottom, s.MarginLeft, s.PadLeft,
				tc.t, tc.r, tc.b, tc.l, tc.pl)
		}
	}
}

func TestUAMarginsDoNotOverrideAuthor(t *testing.T) {
	var s Style
	s.apply(ParseDecls("margin-bottom: 3px"))
	uaMargins("p", 0, &s)
	if s.MarginBottom != 3 || s.MarginTop != 16 {
		t.Fatalf("UA clobbered author: b=%d t=%d", s.MarginBottom, s.MarginTop)
	}
}
