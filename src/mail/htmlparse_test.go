package mail

import (
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

// TestRenderHTMLBackground pins the html view's background contract: a
// mail-declared background (CSS or the bgcolor attribute) becomes the
// lines' default bg, a mail without one gets the light default - the
// html view never renders on the theme's dark surface.
func TestRenderHTMLBackground(t *testing.T) {
	lines := RenderHTML("<p>hi</p>", nil)
	if len(lines) == 0 || lines[0].Bg != "#ffffff" {
		t.Fatalf("the no-background mail must default to white: %+v", lines)
	}

	for name, body := range map[string]string{
		"css body":     `<body style="background-color:#f0f0f0"><p>hi</p></body>`,
		"bgcolor body": `<body bgcolor="#e8e8e8"><p>hi</p></body>`,
	} {
		lines = RenderHTML(body, nil)
		if len(lines) == 0 || lines[0].Bg == "" || lines[0].Bg == "#ffffff" {
			t.Fatalf("%s: the declared background must be respected: %+v", name, lines[0])
		}
	}
	// a nested colored block (bgcolor table) paints its OWN runs over
	// the region default: the cell run carries the table's bg
	tbl := RenderHTML(`<table bgcolor="#dddddd"><tr><td>cell</td></tr></table>`, nil)
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
	if !strings.Contains(RenderHTML("<p>hi</p>", nil)[0].Bg, "#") {
		t.Fatal("the bg must be a hex color")
	}

	// the body background propagates to blank lines too (the block
	// spacing row between paragraphs carries it)
	lines = RenderHTML("<body style=\"background-color:#f0f0f0\"><p>a</p><p>b</p></body>", nil)
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
	lines = RenderHTML(`<body style="background-color:#ffffff"><p style="background-color:#dddddd">x</p></body>`, nil)
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
