// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

// Underline-boundary regression (never mail content): CSS underlines (and
// UA link underlines) must stop at the underlined word's edge - the space
// after <u>word</u> belongs to the following plain leaf, never merged into
// the underlined run ("word ").

import (
	"strings"
	"testing"

	"notmutt/core"
)

func TestUnderlineStopsBeforeSpace(t *testing.T) {
	lines := RenderHTML(`<p><u>word</u> tail</p>`, nil, 0)
	if len(lines) == 0 {
		t.Fatal("no lines")
	}
	found := false
	for _, r := range lines[0].Runs {
		if r.Attrs&core.AttrUnderline == 0 {
			continue
		}
		found = true
		if strings.ContainsAny(r.Text, " \t") {
			t.Fatalf("underline run %q bleeds onto a following space; want it to stop at \"word\"", r.Text)
		}
	}
	if !found {
		t.Fatal("no underlined run rendered")
	}
}

func TestUnderlineHoldsAcrossInternalSpace(t *testing.T) {
	lines := RenderHTML(`<p><u>foo bar</u></p>`, nil, 0)
	if len(lines) == 0 {
		t.Fatal("no lines")
	}
	for _, r := range lines[0].Runs {
		if r.Attrs&core.AttrUnderline != 0 && r.Text != "foo bar" {
			t.Fatalf("underline run %q, want single \"foo bar\"", r.Text)
		}
	}
}
