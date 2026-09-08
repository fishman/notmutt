// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

// Floated-table column regression on a real Stripo drop mail (fixture
// content is never asserted - only the layout the user reported): sibling
// tables that float (es-left/es-right, align + inline float CSS) must lay
// side by side - the header's store logo and "VIEW IN BROWSER" share one
// line above the hero, and the footer's three icon+caption columns render
// as three columns on one strip, not stacked.

import (
	"os"
	"strings"
	"testing"

	"notmutt/core"
)

func tomorrowland() []core.Line {
	body, err := os.ReadFile("../testdata/tomorrowland.html")
	if err != nil {
		panic(err)
	}
	return RenderHTML(string(body), nil, 60)
}

func TestTomorrowlandFloatedColumnsBand(t *testing.T) {
	lines := tomorrowland()
	if len(lines) == 0 {
		t.Fatal("no lines rendered")
	}
	// header: the store logo (an inline image) shares its line with VIEW IN BROWSER
	header := -1
	for i, ln := range lines {
		if strings.Contains(ln.Text, "VIEW IN BROWSER") {
			header = i
			break
		}
	}
	if header < 0 {
		t.Fatal("VIEW IN BROWSER not rendered")
	}
	inline := false
	for _, r := range lines[header].Runs {
		if r.Image != nil {
			inline = true
		}
	}
	if !inline {
		t.Fatalf("store logo did not share the header line with VIEW IN BROWSER:\n%s", lines[header].Text)
	}
	// footer: the three feature captions land on one shared line
	footer := -1
	for _, ln := range lines {
		if strings.Contains(ln.Text, "OFFICIAL TOMORROWLAND") &&
			strings.Contains(ln.Text, "NEXT DAY") &&
			strings.Contains(ln.Text, "30 DAY RETURN") {
			footer++
			break
		}
	}
	if footer < 0 {
		var got []string
		for _, ln := range lines {
			got = append(got, ln.Text)
		}
		t.Fatalf("footer columns stacked vertically; want one line holding all three captions:\n%v", got)
	}
	// and the three column images render on one strip too
	icons := false
	for _, ln := range lines {
		if len(ln.Imgs) >= 3 {
			icons = true
		}
	}
	if !icons {
		t.Fatal("the footer's three column images did not share one line")
	}
}
