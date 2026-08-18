// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func htmlFixture(t *testing.T, full string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(p, []byte(full), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHTMLParseShapes(t *testing.T) {
	base := "From: a@example.com\nTo: b@example.com\nSubject: html\n" +
		"Date: Tue, 01 Jan 2019 00:00:00 +0000\nMIME-Version: 1.0\n"
	cases := map[string]string{
		"single html": base + "Content-Type: text/html; charset=utf-8\n\n<p>hi</p>\n",
		"alt html only": base + "Content-Type: multipart/alternative; boundary=x\n\n" +
			"--x\nContent-Type: text/html; charset=utf-8\n\n<p>hi</p>\n--x--\n",
		"alt plain+html": base + "Content-Type: multipart/alternative; boundary=x\n\n" +
			"--x\nContent-Type: text/plain; charset=utf-8\n\nplain\n" +
			"--x\nContent-Type: text/html; charset=utf-8\n\n<p>hi</p>\n--x--\n",
		"alt html base64": base + "Content-Type: multipart/alternative; boundary=x\n\n" +
			"--x\nContent-Type: text/html; charset=utf-8\nContent-Transfer-Encoding: base64\n\n" +
			"PGg+aGk8L2g+Cg==\n--x--\n",
		"related html+image": base + "Content-Type: multipart/related; boundary=x\n\n" +
			"--x\nContent-Type: text/html; charset=utf-8\n\n<p><img src=\"cid:im\"></p>\n" +
			"--x\nContent-Type: image/png\nContent-ID: <im>\n\n\x89PNG\r\n\x1a\n--x--\n",
		"broken boundary": base + "Content-Type: multipart/alternative; boundary=x\n\n" +
			"--x\nContent-Type: text/html\n\n<p>hi</p>\n",
		"truncated base64": base + "Content-Type: text/html; charset=utf-8\n" +
			"Content-Transfer-Encoding: base64\n\nPGh0bWw+PGJvZHk+PGgxPkhlbGxv\n",
	}
	for name, msg := range cases {
		m, err := ParseMessage(htmlFixture(t, msg))
		if err != nil {
			t.Logf("%s: ERROR %v", name, err)
			continue
		}
		t.Logf("%s: parts=%d atts=%d", name, len(m.Parts), len(m.Attachments))
	}
}

// TestRenderHTMLLinks pins the F key's label render (easyjump style):
// every link - an anchor href or a bare URL word - gets an inline
// "[N]" label, and the labels' document order is the returned target
// list (label N opens Links[N-1]). An anchor without an href is plain
// text, never a label. The unlabeled render returns no links and
// carries no labels - the labels are mode-scoped, never a permanent
// decoration of the html view.
func TestRenderHTMLLinks(t *testing.T) {
	body := "<p>see <a href=\"https://alpha.example.com/x\">alpha</a>" +
		" and <a href=\"https://beta.example.com/b\">beta</a></p>\n" +
		"<p>bare https://delta.example.com/d word</p>\n" +
		"<p><a>no href</a> and <a href=\"https://gamma.example.com\"></a></p>\n" +
		"<table><tr><td><a href=\"https://table.example.com/t\">table link</a>" +
		" and bare https://cell.example.com/w word</td></tr></table>\n"
	lines, links := RenderHTMLWithLinks(body, nil, 80)
	want := []string{
		"https://alpha.example.com/x",
		"https://beta.example.com/b",
		"https://delta.example.com/d",
		"https://gamma.example.com",
		"https://table.example.com/t",
		"https://cell.example.com/w",
	}
	if len(links) != len(want) {
		t.Fatalf("links = %v, want %v", links, want)
	}
	for i, w := range want {
		if links[i] != w {
			t.Fatalf("links[%d] = %q, want %q (document order)", i, links[i], w)
		}
	}
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Text)
		b.WriteByte('\n')
	}
	joined := b.String()
	prev := -1
	for i := range want {
		idx := strings.Index(joined, fmt.Sprintf("[%d]", i+1))
		if idx < 0 {
			t.Fatalf("label [%d] missing from render:\n%s", i+1, joined)
		}
		if idx < prev {
			t.Fatalf("labels out of document order:\n%s", joined)
		}
		prev = idx
	}
	plain := RenderHTML(body, nil, 80)
	for _, l := range plain {
		if strings.Contains(l.Text, "[") {
			t.Fatalf("the unlabeled render must carry no labels: %q", l.Text)
		}
	}
}

// TestRenderHTMLBackground pins the html view's background contract: a
// mail-declared background (CSS or the bgcolor attribute) becomes the
// lines' default bg, a mail without one gets the light default - the
// html view never renders on the theme's dark surface.
func TestRenderHTMLBackground(t *testing.T) {
	lines := RenderHTML("<p>hi</p>", nil, 0)
	if len(lines) == 0 || lines[0].Bg != "#ffffff" {
		t.Fatalf("the no-background mail must default to white: %+v", lines)
	}

	for name, body := range map[string]string{
		"css body":     `<body style="background-color:#f0f0f0"><p>hi</p></body>`,
		"bgcolor body": `<body bgcolor="#e8e8e8"><p>hi</p></body>`,
	} {
		lines = RenderHTML(body, nil, 0)
		if len(lines) == 0 || lines[0].Bg == "" || lines[0].Bg == "#ffffff" {
			t.Fatalf("%s: the declared background must be respected: %+v", name, lines[0])
		}
	}
	// a nested colored block (bgcolor table) paints its OWN runs over
	// the region default: the cell run carries the table's bg
	tbl := RenderHTML(`<table bgcolor="#dddddd"><tr><td>cell</td></tr></table>`, nil, 0)
	found := false
	for _, l := range tbl {
		for _, r := range l.Runs {
			if r.Bg == "#dddddd" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("the bgcolor table's cells must carry their own bg: %+v", tbl)
	}
	if !strings.Contains(RenderHTML("<p>hi</p>", nil, 0)[0].Bg, "#") {
		t.Fatal("the bg must be a hex color")
	}

	// the body background propagates to blank lines too (the block
	// spacing row between paragraphs carries it)
	lines = RenderHTML("<body style=\"background-color:#f0f0f0\"><p>a</p><p>b</p></body>", nil, 0)
	blanks := 0
	for _, l := range lines {
		if l.Text == "" {
			blanks++
			if l.Bg != "#f0f0f0" {
				t.Fatalf("a blank line must carry the mail background: %+v", l)
			}
		}
	}
	if blanks == 0 {
		t.Fatalf("the fixture must produce a blank block-spacing line: %+v", lines)
	}

	// a nested colored block still paints its own runs over the base
	lines = RenderHTML(`<body style="background-color:#ffffff"><p style="background-color:#dddddd">x</p></body>`, nil, 0)
	nested := false
	for _, l := range lines {
		for _, r := range l.Runs {
			if r.Bg == "#dddddd" {
				nested = true
			}
		}
	}
	if !nested {
		t.Fatalf("the nested block's run must carry its own bg: %+v", lines)
	}
}
