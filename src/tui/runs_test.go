// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

// The styled-runs contract: an HTML-sourced line carries per-run SGR
// fragments inside the line's base style. The runs path emits each
// run's truecolor open + text, resets only after a styled run, and
// leaves the pad region in the base style (padRowSGR re-applies the
// base open after every reset).

import (
	"strings"
	"testing"

	"notmutt/core"
)

func TestStyleRuns(t *testing.T) {
	runs := []core.Run{
		{Text: "plain "},
		{Text: "red ", Fg: "#ff0000"},
		{Text: "bold", Fg: "#ff0000", Attrs: core.AttrBold},
	}
	got := (&pager{}).styleRuns(runs)
	want := "plain " + "\x1b[38;2;255;0;0m" + "red " + "\x1b[0m" +
		"\x1b[38;2;255;0;0m" + "\x1b[1m" + "bold"
	if got != want {
		t.Fatalf("styleRuns:\n got %q\nwant %q", got, want)
	}
}

func TestStyleRunsTrailingBgResetsForPad(t *testing.T) {
	runs := []core.Run{
		{Text: "hi", Bg: "#21252b"},
	}
	got := (&pager{}).styleRuns(runs)
	if !strings.HasSuffix(got, "\x1b[0m") {
		t.Fatalf("trailing bg run must close with a reset, got %q", got)
	}
}

// TestStyleRunsTrailingUnderlineResetsForPad pins the link-under pad leak:
// a trailing underlined run (no bg, no selection) leaves its SGR open into
// padRowSGR's padding spaces - underline is visible on a space, unlike a
// bold or italic one, so it must close before the pad.
func TestStyleRunsTrailingUnderlineResetsForPad(t *testing.T) {
	runs := []core.Run{
		{Text: "link", Attrs: core.AttrUnderline},
	}
	got := (&pager{}).styleRuns(runs)
	if !strings.HasSuffix(got, "\x1b[0m") {
		t.Fatalf("trailing underline run must close with a reset, got %q", got)
	}
}

// TestSkipStyled pins the horizontal-pan cut: the first x visible
// cells drop (a rune starting exactly at the cut renders), the last
// completed SGR open re-emits when the cut lands inside its run, a
// reset in the skipped region closes the tracked open, and past the
// cut the sequences pass through whole. A wide char straddling the
// cut drops.
func TestSkipStyled(t *testing.T) {
	red := "\x1b[31m"
	reset := "\x1b[0m"
	cases := []struct {
		in   string
		x    int
		want string
	}{
		{"0123456789", 4, "456789"},
		{"0123456789", 0, "0123456789"},
		{red + "abc" + reset + "def", 2, red + "c" + reset + "def"},
		{red + "ab" + reset + "cdef", 3, "def"}, // the reset closes the tracked open
		{"ab界d", 4, "d"},                        // wide char fully skipped
		{"a界b", 2, "b"},                         // the straddling wide char drops
		{"0123456789", 20, ""},                  // fully scrolled past: blank, never the head
	}
	for _, c := range cases {
		if got := skipStyled(c.in, c.x); got != c.want {
			t.Errorf("skipStyled(%q, %d) = %q, want %q", c.in, c.x, got, c.want)
		}
	}
}

func TestHexRGB(t *testing.T) {
	cases := map[string]string{
		"#ff0000": "255;0;0",
		"#000000": "0;0;0",
		"#0a1f2e": "10;31;46",
		"":        "",
		"red":     "",
		"#fff":    "",
		"#gg0000": "",
	}
	for in, want := range cases {
		if got := hexRGB(in); got != want {
			t.Errorf("hexRGB(%q) = %q, want %q", in, got, want)
		}
	}
}
