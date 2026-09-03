// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

// The stage-2 engine is exercised through its own facade entry
// (renderStage2HTML); the locked html_*_test.go suite is the walker's
// contract until the Task-6 cutover. These tests pin the stage-2-only frame
// decisions (blank quantization, page background) before tables/images/labels
// land.

import (
	"strings"
	"testing"

	"notmutt/core"
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
