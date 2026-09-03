// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

// The stage-2 engine is exercised through its own facade entry
// (renderStage2HTML); the locked html_*_test.go suite is the walker's
// contract until the Task-6 cutover. These tests pin the stage-2-only
// decisions: blank quantization, page background, punctuation binding, tab
// expansion, image own-line/inline, and list marker gutters.

import (
	"strings"
	"testing"

	"notmutt/core"
	"notmutt/lib/html"
)

func TestStage2ParagraphBlankAndBackground(t *testing.T) {
	lines, _ := renderStage2HTML(`<body style="background-color:#f0f0f0"><p>a</p><p>b</p></body>`, nil, 0, false, false, "")
	// first row drops its gap: no leading blank
	if len(lines) == 0 || lines[0].Text != "a" {
		t.Fatalf("first line = %q, want content (no leading blank)", firstText(lines))
	}
	// exactly one blank between the paragraphs, carrying the mail bg
	if len(lines) != 3 || lines[1].Text != "" || lines[1].Bg != "#f0f0f0" || lines[2].Text != "b" {
		t.Fatalf("want a,b with one blank between: %q", linesText(lines))
	}
	if lines[0].Bg != "#f0f0f0" || lines[2].Bg != "#f0f0f0" {
		t.Fatalf("content lines must carry the mail bg")
	}
}

func TestStage2NoTrailingBlank(t *testing.T) {
	lines, _ := renderStage2HTML("<p>one</p>", nil, 0, false, false, "")
	if len(lines) != 1 || lines[0].Text != "one" {
		t.Fatalf("a lone paragraph renders one line, no trailing blank: %q", linesText(lines))
	}
}

func TestStage2BindsSourceSpaceBeforePunctuation(t *testing.T) {
	// a source space before a comma hugs in the terminal (deliberate
	// divergence from weasyprint; the old walker's bindsLeft)
	lines, _ := renderStage2HTML(`<p>Reply <span>alpha</span> <span>, beta</span> now</p>`, nil, 0, false, false, "")
	text := renderText(lines)
	if strings.Contains(text, "alpha ,") || !strings.Contains(text, "alpha, beta") {
		t.Fatalf("space before punctuation must hug: %q", text)
	}
}

func TestStage2TabExpandsInPreservedText(t *testing.T) {
	lines, _ := renderStage2HTML("<pre>a\tb</pre>", nil, 0, false, false, "")
	text := renderText(lines)
	if strings.Contains(text, "\t") {
		t.Fatalf("a literal tab must not reach the pager (F1): %q", text)
	}
	if !strings.Contains(text, "a       b") { // tab to the 8-column stop
		t.Fatalf("tab must expand to the 8-column stop: %q", text)
	}
}

func TestStage2EmptySpanAfterControlDoesNotDoubleSpace(t *testing.T) {
	lines, _ := renderStage2HTML("<p>lead \x01 tail</p>", nil, 0, false, false, "")
	text := renderText(lines)
	if strings.Contains(text, "  ") || strings.Contains(text, "\x01") {
		t.Fatalf("a sanitized-empty control node must leave single spacing: %q", text)
	}
}

func TestStage2ImageOwnLineAndInline(t *testing.T) {
	// isolated image -> own Image line; image sharing its line -> inline ImagePos
	lines, _ := renderStage2HTML(`<p>before <img src="https://x.example.com/i.png" width="24" height="24"> after</p>`, nil, 80, false, false, "")
	own, inline := 0, 0
	for _, l := range lines {
		if l.Image != nil {
			own++
		}
		inline += len(l.Imgs)
	}
	if own != 0 || inline != 1 {
		t.Fatalf("a shared-line image must be inline (0 own, 1 inline), got own=%d inline=%d", own, inline)
	}
	lines, _ = renderStage2HTML(`<img src="https://x.example.com/j.png" width="200" height="100">`, nil, 80, false, false, "")
	own, inline = 0, 0
	for _, l := range lines {
		if l.Image != nil {
			own++
		}
		inline += len(l.Imgs)
	}
	if own != 1 || inline != 0 {
		t.Fatalf("an isolated image must be its own line (1 own, 0 inline), got own=%d inline=%d", own, inline)
	}
}

func TestStage2MarkerGutterAndIndent(t *testing.T) {
	lines, _ := renderStage2HTML(`<ul><li>one</li><li>two</li></ul>`, nil, 0, false, false, "")
	// each item is one contiguous line (no blank between); the text starts at
	// col 4 (the 40px ul gutter at charW 10) and its hanging marker occupies
	// the gutter cell just before (col 3 for a 1-cell disc). The disc is 3
	// UTF-8 bytes for one cell, so the col is measured in cells, not bytes.
	if len(lines) != 2 {
		t.Fatalf("want two contiguous item lines: %q", renderText(lines))
	}
	for i, want := range []string{"one", "two"} {
		text := lines[i].Text
		j := strings.Index(text, want)
		if j < 0 {
			t.Fatalf("line %d = %q: %q missing", i, text, want)
		}
		if col := html.TextWidth(text[:j]); col != 4 {
			t.Fatalf("line %d = %q: %q must start at col 4 (40px gutter), found col %d", i, text, want, col)
		}
		if j == 0 || !strings.HasSuffix(text[:j], "•") {
			t.Fatalf("line %d = %q: the hanging disc must sit in the gutter cell before the text", i, text)
		}
	}
}

func firstText(lines []core.Line) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[0].Text
}

func linesText(lines []core.Line) string {
	texts := make([]string, len(lines))
	for i, l := range lines {
		texts[i] = l.Text
	}
	return strings.Join(texts, "/")
}
