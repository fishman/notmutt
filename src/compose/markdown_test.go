// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package compose

import (
	"strings"
	"testing"

	"notmutt/core"
	"notmutt/mail"
)

// The html alternative must keep the line structure the plain alternative
// keeps; a quoted original folded into one re-flowed paragraph is the
// regression (an html-only original replied to in html mode).
func TestMarkdownHTMLKeepsQuotedLines(t *testing.T) {
	orig := core.Message{ID: "m1", Timestamp: 1756720800, Author: "Ann <ann@example.com>"}
	parsed := &mail.Message{Parts: []mail.Part{{HTML: true,
		Body: "<div>alpha one</div><div>beta two</div><div>gamma three</div>"}}}
	st := Reply(orig, parsed, "example", "Bob <bob@example.com>", "", "")

	body := 0
	for _, l := range strings.Split(st.Body, "\n") {
		if strings.TrimSpace(l) != "" {
			body++
		}
	}
	if body < 4 {
		t.Fatalf("prefill lost the quote: %d body lines", body)
	}

	html, err := MarkdownHTML(st.Body)
	if err != nil {
		t.Fatal(err)
	}
	rendered := 0
	for _, l := range mail.RenderHTML(string(html), nil, 80) {
		if strings.TrimSpace(l.Text) != "" {
			rendered++
		}
	}
	if rendered != body {
		t.Fatalf("html alternative folds the quote: %d rendered lines, want %d", rendered, body)
	}
}

// The reported regression: a multipart/alternative whose plain half is the
// html flattened to one unwrapped line. The quote must keep the original's
// line structure (the html render supplies it), and the html alternative
// must render those lines instead of re-flowing them into one paragraph.
func TestReplyToFlattenedAlternativeKeepsLines(t *testing.T) {
	orig := core.Message{ID: "m1", Timestamp: 1756720800, Author: "Ann <ann@example.com>"}
	parsed := &mail.Message{Parts: []mail.Part{
		{Body: strings.Repeat("alpha one beta two ", 12)},
		{HTML: true, Body: "<div>alpha one</div><div>beta two</div><div>gamma three</div>"},
	}}
	st := Reply(orig, parsed, "example", "Bob <bob@example.com>", "", "")

	lines := 0
	for _, l := range strings.Split(st.Body, "\n") {
		if strings.TrimSpace(l) != "" {
			lines++
		}
	}
	if lines != 4 {
		t.Fatalf("the quote folded the flattened alternative: %d body lines, want 4", lines)
	}

	html, err := MarkdownHTML(st.Body)
	if err != nil {
		t.Fatal(err)
	}
	rendered := 0
	for _, l := range mail.RenderHTML(string(html), nil, 80) {
		if strings.TrimSpace(l.Text) != "" {
			rendered++
		}
	}
	if rendered != 4 {
		t.Fatalf("the html alternative re-flowed the quote: %d rendered lines, want 4", rendered)
	}
}
