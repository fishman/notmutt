// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

// Layout pin for testdata/html/semianalysis.html: the render keeps its
// structural shape - forwarded header and title/authors/buttons left,
// the action icons and READ IN APP share one table strip (the walker's
// right-cell split is a spec-accepted flatten deletion), joined list
// marks, centered sources. Assertions are alignment signatures only,
// never mail content.

import (
	"os"
	"strings"
	"testing"

	"notmutt/core"
	"notmutt/lib/html"
)

func TestRenderSemianalysisLayout(t *testing.T) {
	body, err := os.ReadFile("../testdata/html/semianalysis.html")
	if err != nil {
		t.Fatal(err)
	}
	lines := RenderHTML(string(body), nil, 0)
	narrow := RenderHTML(string(body), nil, 40)
	for _, l := range narrow {
		cells := html.TextWidth(l.Text)
		// text-only lines wrap at the requested width; an image-carrying
		// strip overruns only by the inline-image placeholder allowance
		// (recorded divergence J: a 7-cell "[image]" placeholder stands in
		// for an 18px image whose used width is 2 cells, so each placeholder
		// adds up to 5 cells beyond the measured width).
		if n := strings.Count(l.Text, "[image]"); n == 0 {
			if cells > 40 {
				t.Fatalf("a text-only narrow line must wrap at 40 cells, got %d: %q", cells, l.Text)
			}
		} else if cells > 40+5*n {
			t.Fatalf("an image-carrying narrow line overruns the placeholder allowance, got %d cells for %d placeholders: %q", cells, n, l.Text)
		}
	}

	lead := func(l core.Line) int { return len(l.Text) - len(strings.TrimLeft(l.Text, " ")) }
	trim := func(l core.Line) string { return strings.TrimSpace(l.Text) }

	var forwarded, title, readInApp, sources, last int
	var buttonLines, marks []int
	left := func(i int) bool { return lead(lines[i]) < 20 }
	for i, l := range lines {
		switch {
		case strings.HasPrefix(trim(l), "Forwarded"):
			forwarded = i
		case strings.HasPrefix(trim(l), "$12B of US"):
			title = i
		case strings.Contains(l.Text, "READ IN APP"):
			readInApp = i
		case strings.HasPrefix(trim(l), "Sources:"):
			sources = i
		}
		// the header's action icons are inline: the 4 buttons and the
		// READ IN APP text-icon join one strip as placeholders
		if n := strings.Count(l.Text, "[image]"); n == 5 {
			buttonLines = append(buttonLines, i)
		}
		if m := trim(l); len(m) >= 2 && m[0] >= '1' && m[0] <= '9' && m[1] == '.' {
			marks = append(marks, i)
		}
		last = i
	}

	if !left(forwarded) {
		t.Fatalf("forwarded header must be left-aligned, lead=%d", lead(lines[forwarded]))
	}
	if !left(title) {
		t.Fatalf("title must be left-aligned, lead=%d", lead(lines[title]))
	}
	if len(buttonLines) != 1 {
		t.Fatalf("the 4 action buttons and READ IN APP icon must join into one strip, got %d lines", len(buttonLines))
	}
	if !left(buttonLines[0]) {
		t.Fatalf("the buttons line must be left-aligned, lead=%d", lead(lines[buttonLines[0]]))
	}
	if readInApp != buttonLines[0] {
		t.Fatalf("READ IN APP must share the buttons strip, READ IN APP line=%d buttons line=%d",
			readInApp, buttonLines[0])
	}
	for _, n := range []byte{'1', '2', '3'} {
		found := false
		for _, i := range marks {
			if trim(lines[i])[0] == n {
				found = true
			}
		}
		if !found {
			t.Fatalf("list entry %c. must render with its mark joined", n)
		}
	}
	if c := lead(lines[sources]); c <= 10 || c >= 60 {
		t.Fatalf("Sources must be centered, lead=%d", c)
	}
	if last < sources {
		t.Fatalf("the closing paragraph after Sources is missing")
	}
}
