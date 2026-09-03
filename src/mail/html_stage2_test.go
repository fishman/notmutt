// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

// The stage-2 engine is exercised through its own facade entry
// (renderStage2HTML); the locked html_*_test.go suite is the walker's
// contract until the Task-6 cutover. These tests pin the stage-2-only
// decisions: blank quantization, page background, punctuation binding, tab
// expansion, image own-line/inline, list marker gutters, and table strips.

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

// TestStage2EmptySpanAfterControlDoesNotDoubleSpace proves the F1 output
// through the pipeline: the control char never reaches the pager and the
// spacing stays single. It does NOT exercise the emptied-span branch in
// emitRowContent - collapse-mode input already dropped the zero-width
// atom in lib/html, so that branch is a preserve-mode defense only.
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

func TestStage2InlineImageXAtPlaceholderColumns(t *testing.T) {
	// Two inline images share one line: each ImagePos.X must be the cell
	// column where that image's placeholder text starts in Line.Text (col
	// tracks the concatenated run-text end, not the image's used px). The
	// walk below recomputes each placeholder column from the preceding run
	// texts, so the pin locks the invariant the pager relies on.
	lines, _ := renderStage2HTML(`<p>before <img src="https://x.example.com/i.png" width="24" height="24"> <img src="https://x.example.com/j.png" width="24" height="24"> after</p>`, nil, 80, false, false, "")
	var line *core.Line
	for i := range lines {
		if len(lines[i].Imgs) > 0 {
			line = &lines[i]
			break
		}
	}
	if line == nil {
		t.Fatalf("want one line holding both inline images")
	}
	if len(line.Imgs) != 2 {
		t.Fatalf("want 2 inline images, got %d in %q", len(line.Imgs), line.Text)
	}
	col, img := 0, 0
	for _, r := range line.Runs {
		if r.Image != nil {
			if img >= len(line.Imgs) {
				t.Fatalf("image runs outnumber ImagePos entries")
			}
			if line.Imgs[img].X != col {
				t.Fatalf("image %d placeholder starts at col %d (run walk of %q), ImagePos.X=%d", img, col, line.Text, line.Imgs[img].X)
			}
			img++
		}
		col += html.TextWidth(r.Text)
	}
	if img != len(line.Imgs) {
		t.Fatalf("image runs undercount ImagePos entries: %d runs, %d imgs", img, len(line.Imgs))
	}
	if line.Text != "before [image] [image] after" {
		t.Fatalf("unexpected line text %q", line.Text)
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

func TestStage2CellBackgroundPaintsItsRuns(t *testing.T) {
	lines, _ := renderStage2HTML(`<table bgcolor="#dddddd"><tr><td>cell</td></tr></table>`, nil, 0, false, false, "")
	found := false
	for _, l := range lines {
		for _, r := range l.Runs {
			if r.Bg == "#dddddd" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("the bgcolor table's cell runs must carry the table bg: %+v", renderText(lines))
	}
}

func TestStage2MultiColumnStripPlacesCells(t *testing.T) {
	// two narrow cells: both texts present on one strip line, second cell's
	// text starts strictly after the first's text end
	lines, _ := renderStage2HTML(`<table><tr><td>aa</td><td>bb</td></tr></table>`, nil, 0, false, false, "")
	if len(lines) != 1 {
		t.Fatalf("want one strip line, got %d: %q", len(lines), renderText(lines))
	}
	text := lines[0].Text
	i, j := strings.Index(text, "aa"), strings.Index(text, "bb")
	if i < 0 || j <= i+1 {
		t.Fatalf("cells must both render, second strictly after the first: %q", text)
	}
}

func TestStage2NestedStripRendersInnerCells(t *testing.T) {
	// a cell hosting a table renders its inner cells on the strip too
	body := `<table><tr><td>a<table><tr><td>in</td><td>ner</td></tr></table></td><td>b</td></tr></table>`
	lines, _ := renderStage2HTML(body, nil, 0, false, false, "")
	text := renderText(lines)
	for _, want := range []string{"a", "in", "ner", "b"} {
		if !strings.Contains(text, want) {
			t.Fatalf("nested table cell %q missing: %q", want, text)
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
