package tui

// The styled-runs contract: an HTML-sourced line carries per-run SGR
// fragments inside the line's base style. The runs path emits each
// run's truecolor open + text, resets only after a styled run, and
// leaves the pad region in the base style (padRowSGR re-applies the
// base open after every reset, so a quoted/header line's color covers
// the gaps).

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
