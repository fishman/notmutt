// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

// Layout pin for testdata/html/ucsfhealth.html: the render keeps the
// real content. Before the CSS parser fix, a media-query block leaked
// its .responsive-td rules (display:block !important) so every cell
// demoted to inline and the whole 84-table body collapsed to a single
// line - the display:none preheader. Assertions are
// presence signatures only, never mail content.

import (
	"os"
	"strings"
	"testing"
)

func TestRenderUcsfLayout(t *testing.T) {
	body, err := os.ReadFile("../testdata/html/ucsfhealth.html")
	if err != nil {
		t.Fatal(err)
	}
	lines := RenderHTML(string(body), nil, 0)
	if len(lines) < 30 {
		t.Fatalf("the body content must render, got %d lines", len(lines))
	}
	var text []string
	for _, l := range lines {
		text = append(text, l.Text)
	}
	joined := strings.Join(text, "\n")
	if strings.Contains(joined, "fight allergies naturally") {
		t.Fatal("the display:none preheader must not render")
	}
	for _, want := range []string{"Bill pay", "Save my spot", "Prevent fall flare-ups", "Cole Valley", "Health Library", "Unsubscribe"} {
		if !strings.Contains(joined, want) {
			t.Errorf("render lost %q", want)
		}
	}
}
