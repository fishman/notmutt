// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

// Inline-boundary spacing regression: the field tokenizer splits a text
// node on whitespace, so a punctuation node ("', or '") split from its
// word by an inline element boundary becomes its own field and the word
// join inserts a space the source lacked ("alpha ," instead of "alpha,").
// Punctuation binds left; so does an underscore-leading fragment split
// across spans ("email" + "_source" renders "email_source").
// Fabricated content, never mail.

import (
	"strings"
	"testing"

	"notmutt/core"
)

func TestHTMLInlineBoundaryHugsPunctuation(t *testing.T) {
	lines := RenderHTML(`<p>Reply <a href="x">alpha</a>, or <a href="y">atlas</a>.</p>`, nil, 0)
	text := renderText(lines)
	if strings.Contains(text, "alpha ,") || strings.Contains(text, "atlas .") {
		t.Fatalf("inline boundary must not space the punctuation: %q", text)
	}
	if !strings.Contains(text, "alpha, or") || !strings.Contains(text, "atlas.") {
		t.Fatalf("punctuation must hug its word: %q", text)
	}
}

func TestHTMLUnderscoreFragmentHugs(t *testing.T) {
	lines := RenderHTML(`<p>see <span>email</span><span>_source</span> here</p>`, nil, 0)
	text := renderText(lines)
	if strings.Contains(text, "email _source") {
		t.Fatalf("underscore fragment must hug: %q", text)
	}
	if !strings.Contains(text, "email_source") {
		t.Fatalf("fragments must merge: %q", text)
	}
}

// TestHTMLControlNodeDoesNotPanic: a C0 control char survives x/net/html
// but sanitizes to "" (F1). The inline-boundary join must not index the
// empty word (the bindsLeft panic the html fuzz target would hit).
func TestHTMLControlNodeDoesNotPanic(t *testing.T) {
	for _, c := range []byte{0x01, 0x08, 0x1b, 0x7f} {
		lines := RenderHTML("<p>lead "+string(c)+" tail</p>", nil, 0)
		if len(lines) == 0 {
			t.Fatalf("control char %#x must not empty the render", c)
		}
		if strings.Contains(renderText(lines), string(c)) {
			t.Fatalf("control char %#x must be stripped (F1)", c)
		}
	}
}

func renderText(lines []core.Line) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Text)
		b.WriteString("\n")
	}
	return b.String()
}
