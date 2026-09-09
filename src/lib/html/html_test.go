// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

// Fuzz targets for the CSS boundary (AGENTS.md: parser-adjacent code
// must pass SECURITY.md's fuzz targets).

import (
	"testing"

	"golang.org/x/net/html"
)

// TestDisplayNotInherited pins the CSS rule: display is not inherited, so
// a block's content computes the tag default, not the parent's display.
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

// TestMediaQueryRulesDoNotApply pins the at-rule policy: a media query
// is a device query, not a style, so its whole body drops - including
// rules after the first inner rule (the flat brace scan used to leak
// them as active, so a real marketing mail's 480px block demoted its
// .responsive-td cells to inline and collapsed the whole render).
func TestMediaQueryRulesDoNotApply(t *testing.T) {
	cases := []string{
		"@media only screen and (max-width: 480px) { .a { display: block; } }",
		"@media only screen and (max-width: 480px) { .a { display: block; } .b { display: block; } }",
		"@media (prefers-color-scheme: dark) { .a { color: #ffffff; } }\n.x { color: #ff0000; }",
	}
	for _, css := range cases {
		rules := ParseStyleSheet(css)
		for _, r := range rules {
			if d := r.decls["display"]; d != "" {
				t.Errorf("%q: media rule leaked: display=%q", css, d)
			}
		}
	}
	if got := ParseStyleSheet("@media x { .a { color: red; } } .b { color: red; }"); len(got) != 1 {
		t.Fatalf("non-media rules must survive the at-rule skip, got %d rules", len(got))
	}
}

// TestImportantSuffixStripped pins declaration parsing: the !important
// suffix is cascade machinery, never part of the value - a display of
// "block !important" would fail roleOf and demote a table to inline.
func TestImportantSuffixStripped(t *testing.T) {
	d := ParseDecls("color: red !important; display: inline-table !important")
	if d["color"] != "red" || d["display"] != "inline-table" {
		t.Fatalf("!important leaked into values: %v", d)
	}
	bs := buildBody(`<table style="display: inline-table !important"><tr><td>hello</td></tr></table>`)
	if len(bs) == 0 || bs[0].Role != RoleTable || bs[0].Tbl != "table" {
		t.Fatalf("an inline-table declaration must keep grid identity, got %+v", bs)
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
// control 0.
func TestRuneWidth(t *testing.T) {
	if RuneWidth('a') != 1 || RuneWidth('界') != 2 || RuneWidth(0x01) != 0 {
		t.Fatalf("RuneWidth wide/control mismatch")
	}
}
