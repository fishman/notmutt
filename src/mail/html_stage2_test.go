// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

// The stage-2 engine is exercised through its own facade entry
// (renderStage2HTML); the locked html_*_test.go suite is the walker's
// contract until the Task-6 cutover. These tests pin the stage-2-only
// decisions: blank quantization, page background, punctuation binding, tab
// expansion, image own-line/inline, list marker gutters, and table strips.

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/png"
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
	// text starts at or after the first cell's text end (abutment tolerated, D12)
	lines, _ := renderStage2HTML(`<table><tr><td>aa</td><td>bb</td></tr></table>`, nil, 0, false, false, "")
	if len(lines) != 1 {
		t.Fatalf("want one strip line, got %d: %q", len(lines), renderText(lines))
	}
	text := lines[0].Text
	i, j := strings.Index(text, "aa"), strings.Index(text, "bb")
	if i < 0 || j <= i+1 {
		t.Fatalf("cells must both render, second at or after the first's text end: %q", text)
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

func TestStage2LabelsAreOwnRuns(t *testing.T) {
	// the label "[1]" precedes the anchor text as its OWN run (never merged
	// with "alpha") and never merges with non-label text
	lines, links := renderStage2HTML(`<p><a href="https://a.example.com/x">alpha</a> and beta</p>`, nil, 80, true, false, "")
	if len(links) != 1 || links[0] != "https://a.example.com/x" {
		t.Fatalf("links must list the anchor href: %v", links)
	}
	joined := renderText(lines)
	if !strings.Contains(joined, "[1]alpha") {
		t.Fatalf("label must sit at the link start: %q", joined)
	}
	labelRuns := 0
	alphaRuns := 0
	for _, l := range lines {
		for _, r := range l.Runs {
			if r.Label {
				labelRuns++
				if r.Text != "[1]" {
					t.Fatalf("label run text must be exactly [1], got %q", r.Text)
				}
				continue
			}
			if strings.Contains(r.Text, "alpha") {
				alphaRuns++
			}
		}
	}
	if labelRuns != 1 {
		t.Fatalf("want exactly one label run, got %d", labelRuns)
	}
	if alphaRuns == 0 {
		t.Fatalf("the anchor text must render in a non-label run: %q", joined)
	}
}

func TestStage2BareUrlLabeledInDocumentOrder(t *testing.T) {
	// a bare URL token in text gets its own [N] and KEEPS its text (the old
	// walker added the label AND the token); anchors and bare URLs number in
	// document order into the links list
	body := `<p><a href="https://a.example.com/x">alpha</a> see https://b.example.com/y now</p>`
	lines, links := renderStage2HTML(body, nil, 80, true, false, "")
	if len(links) != 2 || links[0] != "https://a.example.com/x" || links[1] != "https://b.example.com/y" {
		t.Fatalf("links must be [anchor, bare URL] in document order: %v", links)
	}
	joined := renderText(lines)
	if !strings.Contains(joined, "[2]https://b.example.com/y") {
		t.Fatalf("bare URL keeps its text behind its label: %q", joined)
	}
}

func TestStage2UnlabeledRenderCarriesNoLabels(t *testing.T) {
	body := `<p><a href="https://a.example.com/x">alpha</a> see https://b.example.com/y now</p>`
	lines, links := renderStage2HTML(body, nil, 80, false, false, "")
	if links != nil {
		t.Fatalf("the unlabeled render must return no links: %v", links)
	}
	joined := renderText(lines)
	if strings.Contains(joined, "[") {
		t.Fatalf("no label may reach the unlabeled render: %q", joined)
	}
	for _, l := range lines {
		for _, r := range l.Runs {
			if r.Label {
				t.Fatalf("no label run may exist in the unlabeled render")
			}
		}
	}
}

func TestStage2WholeCardAnchorLabelLeadsItsOwnLine(t *testing.T) {
	// a whole-card anchor (uniformly block-level content) must render its [N]
	// as its own leading line: a bare RoleText leaf among block children lays
	// out to zero rows and the label would vanish (Task-6 parity shape)
	body := `<a href="https://x.example.com/"><h3>Card title</h3><p>card body</p></a>`
	lines, links := renderStage2HTML(body, nil, 80, true, false, "")
	if len(links) != 1 || links[0] != "https://x.example.com/" {
		t.Fatalf("links must list the card anchor: %v", links)
	}
	found := false
	for _, l := range lines {
		if strings.HasPrefix(l.Text, "[1]") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("the [1] label must lead its own line before the card content: %q", renderText(lines))
	}
}

func TestStage2PreTextBareUrlNotLabeled(t *testing.T) {
	// preserve-white-space text keeps its line structure: a bare URL inside
	// <pre> is never bare-URL labeled (the walker never linkifies in pre), so
	// the labeled render matches the unlabeled path line for line
	body := "<pre>line1\nhttps://x.example.com/\nline2</pre>"
	lines, links := renderStage2HTML(body, nil, 80, true, false, "")
	if links != nil {
		t.Fatalf("bare URLs inside preserve text must not be labeled: %v", links)
	}
	if got := linesText(lines); got != "line1/https://x.example.com//line2" {
		t.Fatalf("pre line structure must survive the labeled render: %q", got)
	}
	for _, l := range lines {
		for _, r := range l.Runs {
			if r.Label {
				t.Fatalf("no label run may exist inside preserve text")
			}
		}
	}
}

func TestStage2AdjacentLabelsStaySeparateRuns(t *testing.T) {
	// two adjacent anchors yield [1] and [2] as separate runs: the pager
	// highlights a label only on exact r.Text == linkSel, so a merged
	// "[1][2]" run would drop both highlights
	body := `<p><a href="https://a.example.com/"></a><a href="https://b.example.com/">z</a></p>`
	lines, links := renderStage2HTML(body, nil, 80, true, false, "")
	if len(links) != 2 {
		t.Fatalf("links must list both anchors: %v", links)
	}
	has1, has2, merged := false, false, false
	for _, l := range lines {
		for _, r := range l.Runs {
			if r.Label {
				switch r.Text {
				case "[1]":
					has1 = true
				case "[2]":
					has2 = true
				}
				if r.Text == "[1][2]" {
					merged = true
				}
			}
		}
	}
	if !has1 || !has2 || merged {
		t.Fatalf("labels must render as separate runs [1],[2]: %q", renderText(lines))
	}
}

func TestStage2AllEmptyStripSkips(t *testing.T) {
	// a grid row whose fragments all drop (a lone tracking pixel) must render
	// nothing and consume no gap: it must not stop margin collapse between the
	// surrounding paragraphs (TODO.org all-empty-strips).
	body := `<p>a</p><table><tr><td><img width="1" height="1"></td></tr></table><p>b</p>`
	lines, _ := renderStage2HTML(body, nil, 0, false, false, "")
	if got := linesText(lines); got != "a//b" {
		t.Fatalf("tracking-pixel table must drop with no blank line: %q", got)
	}
}

func TestStage2MainFlowAllPixelsSkip(t *testing.T) {
	// a lone 1x1 tracking pixel between paragraphs drops and consumes no gap:
	// the surrounding paragraphs keep their single blank (allTrackingPixels).
	// Without the skip the pixel's row would add a spurious blank line.
	body := `<p>a</p><img width="1" height="1"><p>b</p>`
	lines, _ := renderStage2HTML(body, nil, 0, false, false, "")
	if got := linesText(lines); got != "a//b" {
		t.Fatalf("main-flow tracking pixel must drop with no extra line: %q", got)
	}
}

func TestStage2DarkSmokeAdapts(t *testing.T) {
	// dark=true: a light-declared page bg reflects onto the theme bg and
	// rides every content line (pageColors + runFor dark adaptation, the
	// luma gate). Mirrors the locked html_dark_test.go fixture shape.
	// #33373f is AdaptBG's reflection of the fixture's light #f4f4f4 onto
	// the theme bg #282c34, not a wiring constant.
	lines, _ := renderStage2HTML(`<body style="background:#f4f4f4">x</body>`, nil, 0, false, true, "#282c34")
	if len(lines) == 0 || lines[0].Bg != "#33373f" {
		t.Fatalf("light body bg = %q, want reflected #33373f", firstBG(lines))
	}
	lines, _ = renderStage2HTML(`<body style="background:#111111">x</body>`, nil, 0, false, true, "#282c34")
	if len(lines) == 0 || lines[0].Bg != "#111111" {
		t.Fatalf("dark body bg = %q, want #111111 unchanged", firstBG(lines))
	}
	// a bgcolor table cell reflects its light bg through runFor's dark path
	lines, _ = renderStage2HTML(`<table><tr><td bgcolor="#f4f4f4">x</td></tr></table>`, nil, 0, false, true, "#282c34")
	found := false
	for _, l := range lines {
		for _, r := range l.Runs {
			if r.Bg == "#33373f" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("bgcolor cell must carry the reflected #33373f run: %+v", lines)
	}
}

func TestStage2DecimalMarkerShows(t *testing.T) {
	lines, _ := renderStage2HTML(`<ol><li>one</li></ol>`, nil, 0, false, false, "")
	if got := renderText(lines); !strings.Contains(got, "1.") || !strings.Contains(got, "one") {
		t.Fatalf("ol item must show its decimal marker and text: %q", got)
	}
}

func TestStage2EmptyLiShowsDisc(t *testing.T) {
	lines, _ := renderStage2HTML(`<ul><li></li></ul>`, nil, 0, false, false, "")
	if got := renderText(lines); !strings.Contains(got, "•") {
		t.Fatalf("a lone li must show its disc: %q", got)
	}
}

func TestStage2HRYieldsRuleLine(t *testing.T) {
	lines, _ := renderStage2HTML(`<hr>`, nil, 0, false, false, "")
	if got := renderText(lines); !strings.Contains(got, "─") {
		t.Fatalf("an hr must yield a rule-glyph line: %q", got)
	}
}

// TestStage2ImagesOnResolvesIntrinsicSizes pins the images-on render: an
// undeclared-size inline image is glued mid-text at images=false (markers,
// intrinsic 0), but images=true sizes it from its bytes so it occupies real
// geometry and no longer shares the text row with the words around it. The
// data: URI is measured by the worker's DecodeConfig loader (dimensions
// only); the http src takes its px from the imgSizes map and stays markers
// while unsized (the refine path seats it only once fetched/measured).
func TestStage2ImagesOnResolvesIntrinsicSizes(t *testing.T) {
	data := pngURI(t, 360, 180)
	dataBody := `<p>alpha <img src="` + data + `"> beta</p>`
	httpBody := `<p>alpha <img src="https://x.example.com/big.png"> beta</p>`
	remote := "https://x.example.com/big.png"
	for _, tc := range []struct {
		name string
		body string
		img  func() ([]core.Line, []string)
		want string // relayout: glued keeps the shared inline row; sized pushes the image off it
	}{
		{
			// markers when off: the undeclared image stays a zero-width
			// placeholder glued into the single text row (locked behavior)
			name: "embedded off",
			body: dataBody,
			img:  func() ([]core.Line, []string) { return renderStage2HTML(dataBody, nil, 30, false, false, "") },
			want: "glued",
		},
		{
			// images on: the data bytes measure 360x180, the image lays at
			// real width and wraps the words off its row
			name: "embedded on",
			body: dataBody,
			img: func() ([]core.Line, []string) {
				return renderStage2Full(dataBody, nil, 30, false, false, "", true, nil)
			},
			want: "sized",
		},
		{
			// remote not yet fetched: no imgSizes entry, stays markers
			name: "remote on unsized",
			body: httpBody,
			img: func() ([]core.Line, []string) {
				return renderStage2Full(httpBody, nil, 30, false, false, "", true, nil)
			},
			want: "glued",
		},
		{
			// remote fetched + measured: the TUI's imgSizes seats it at real px
			name: "remote on sized",
			body: httpBody,
			img: func() ([]core.Line, []string) {
				return renderStage2Full(httpBody, nil, 30, false, false, "", true, map[string]core.ImgSize{remote: {W: 360, H: 180}})
			},
			want: "sized",
		},
	} {
		lines, _ := tc.img()
		text := linesText(lines)
		switch tc.want {
		case "glued":
			if len(lines) != 1 || !strings.Contains(text, "alpha ") || !strings.Contains(text, " beta") {
				t.Fatalf("%s: image must stay glued into one shared text row, got %d lines %q", tc.name, len(lines), text)
			}
		case "sized":
			// the image lays at real geometry: no text trails it on its own
			// row (the off-mode trailing word wrapped below), and content
			// follows on a later line
			if len(lines) < 2 {
				t.Fatalf("%s: images on must re-layout around the image, got %d lines %q", tc.name, len(lines), text)
			}
			found := false
			for _, l := range lines {
				if l.Image != nil || len(l.Imgs) > 0 {
					found = true
					if strings.Contains(l.Text, " beta") {
						t.Fatalf("%s: text must not trail the sized image on its row, got %q", tc.name, linesText(lines))
					}
				}
			}
			if !found {
				t.Fatalf("%s: a sized image must render, got %d lines %q", tc.name, len(lines), text)
			}
			if !strings.Contains(text, "beta") {
				t.Fatalf("%s: the wrapped word must still render below, got %q", tc.name, text)
			}
		}
	}
}

// pngURI encodes a w x h NRGBA image to a data:image/png;base64 URI.
func pngURI(t *testing.T, w, h int) string {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())
}

func firstBG(lines []core.Line) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[0].Bg
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
