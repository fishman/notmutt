// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

// Layout pin for testing/semianalysis.html (the stripped sample corpus):
// the fixture's render must keep its structural shape - forwarded header
// top right, title/authors/buttons left, READ IN APP right, joined list
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
	body, err := os.ReadFile("../../testing/semianalysis.html")
	if err != nil {
		t.Skip(err)
	}
	lines := RenderHTML(string(body), nil, 0)
	narrow := RenderHTML(string(body), nil, 40)
	for _, l := range narrow {
		if cells := html.TextWidth(l.Text); cells > 40 {
			t.Fatalf("a narrow render must wrap at the requested width, got %d cells", cells)
		}
	}

	lead := func(l core.Line) int { return len(l.Text) - len(strings.TrimLeft(l.Text, " ")) }
	trim := func(l core.Line) string { return strings.TrimSpace(l.Text) }

	var forwarded, title, readInApp, sources, last int
	var buttonLines, marks []int
	right := func(i int) bool { return lead(lines[i]) > 50 }
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
		// the header's action icons are inline: they join their row's
		// words as placeholders (one line, READ IN APP right)
		if n := strings.Count(l.Text, "[image]"); n == 4 {
			buttonLines = append(buttonLines, i)
		}
		if m := trim(l); len(m) >= 2 && m[0] >= '1' && m[0] <= '9' && m[1] == '.' {
			marks = append(marks, i)
		}
		last = i
	}

	if !right(forwarded) {
		t.Fatalf("forwarded header must be right-aligned, lead=%d", lead(lines[forwarded]))
	}
	if !left(title) {
		t.Fatalf("title must be left-aligned, lead=%d", lead(lines[title]))
	}
	if len(buttonLines) != 1 {
		t.Fatalf("the 4 action buttons must join into one line, got %d lines", len(buttonLines))
	}
	if !left(buttonLines[0]) {
		t.Fatalf("the buttons line must be left-aligned, lead=%d", lead(lines[buttonLines[0]]))
	}
	if !right(readInApp) || lead(lines[readInApp]) <= lead(lines[buttonLines[0]]) {
		t.Fatalf("READ IN APP must be the rightmost line, lead=%d vs buttons %d",
			lead(lines[readInApp]), lead(lines[buttonLines[0]]))
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
