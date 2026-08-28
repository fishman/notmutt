// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"notmutt/core"
)

// fixture writes a minimal text/plain message and returns its path.
// Mail content in tests is synthetic; no real mail is used.
func fixture(t *testing.T, body string) string {
	t.Helper()
	msg := "From: a@example.com\nTo: b@example.com\n" +
		"Subject: hello\nDate: Tue, 01 Jan 2019 00:00:00 +0000\n" +
		"MIME-Version: 1.0\nContent-Type: text/plain; charset=utf-8\n\n" +
		body
	p := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(p, []byte(msg), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func joinText(lines []core.Line) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestRenderThread(t *testing.T) {
	body := "line one\n> quoted a\n> > quoted deep\n-- \nsig line\n"
	msgs := []core.Message{{ID: "m1", ThreadID: "t1", Paths: []string{fixture(t, body)}}}
	lines, _, _, err := RenderThread(msgs, core.RenderHTML, false, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	// the curated header block tops every pager: Date/From/To/Subject,
	// labels padded to one column (the %-8s alignment)
	for i, want := range []string{
		"Date:    Tue, 01 Jan 2019 00:00:00 +0000",
		"From:    a@example.com",
		"To:      b@example.com",
		"Subject: hello",
	} {
		if lines[i].Text != want {
			t.Fatalf("header line %d = %q, want %q", i, lines[i].Text, want)
		}
	}
	joined := joinText(lines)
	for _, want := range []string{"hello", "a@example.com", "line one", "quoted a", "quoted deep", "sig line"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in:\n%s", want, joined)
		}
	}
}

func TestRenderThreadStripsControls(t *testing.T) {
	body := "evil\x1b[31mred\x07\n"
	msgs := []core.Message{{ID: "m1", ThreadID: "t1", Paths: []string{fixture(t, body)}}}
	lines, _, _, err := RenderThread(msgs, core.RenderHTML, false, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(joinText(lines), "\x1b") {
		t.Fatalf("control chars leaked into the pager content:\n%v", lines)
	}
}

func TestRenderThreadAttachment(t *testing.T) {
	msg := "From: a@example.com\nTo: b@example.com\nSubject: attach\n" +
		"Date: Tue, 01 Jan 2019 00:00:00 +0000\nMIME-Version: 1.0\n" +
		"Content-Type: multipart/mixed; boundary=x\n\n" +
		"--x\nContent-Type: text/plain; charset=utf-8\n\nbody\n" +
		"--x\nContent-Type: application/octet-stream\n" +
		"Content-Disposition: attachment; filename=\"report.pdf\"\n\ndata\n" +
		"--x--\n"
	p := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(p, []byte(msg), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, _, _, err := RenderThread([]core.Message{{ID: "m1", ThreadID: "t1", Paths: []string{p}}}, core.RenderHTML, false, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	joined := joinText(lines)
	if !strings.Contains(joined, "attachment: report.pdf (application/pdf, 4 bytes)") {
		t.Fatalf("attachment line must show the extension-refined mime:\n%s", joined)
	}
	if !strings.Contains(joined, "body") {
		t.Fatalf("inline part missing:\n%s", joined)
	}
}

func TestRenderThreadMissingFile(t *testing.T) {
	msgs := []core.Message{{ID: "m1", ThreadID: "t1", Paths: []string{"/nonexistent"}}}
	lines, _, _, err := RenderThread(msgs, core.RenderHTML, false, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	joined := joinText(lines)
	if !strings.Contains(joined, "failed to parse message") {
		t.Fatalf("missing file must render an error line:\n%s", joined)
	}
}

func TestRenderThreadNoPath(t *testing.T) {
	lines, _, _, err := RenderThread([]core.Message{{ID: "m1", ThreadID: "t1"}}, core.RenderHTML, false, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	joined := joinText(lines)
	if !strings.Contains(joined, "no path") {
		t.Fatalf("missing path must render an error line:\n%s", joined)
	}
}

func TestRenderThreadEmpty(t *testing.T) {
	if _, _, _, err := RenderThread(nil, core.RenderHTML, false, 0, false); err == nil {
		t.Fatal("an empty thread must error - nothing to show")
	}
}

func TestRenderThreadPartialOnBadMessage(t *testing.T) {
	good := fixture(t, "good body\n")
	bad := filepath.Join(t.TempDir(), "bad")
	if err := os.WriteFile(bad, []byte("this is not a mail message at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	msgs := []core.Message{
		{ID: "m1", ThreadID: "t1", Paths: []string{bad}},
		{ID: "m2", ThreadID: "t1", Paths: []string{good}},
	}
	lines, _, _, err := RenderThread(msgs, core.RenderHTML, false, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	joined := joinText(lines)
	if !strings.Contains(joined, "good body") || !strings.Contains(joined, "failed to parse message") {
		t.Fatalf("a bad message must not kill the thread's content:\n%s", joined)
	}
}

func TestRenderThreadUnknownCharset(t *testing.T) {
	msg := "From: a@example.com\nTo: b@example.com\nSubject: mixed\n" +
		"Date: Tue, 01 Jan 2019 00:00:00 +0000\nMIME-Version: 1.0\n" +
		"Content-Type: multipart/mixed; boundary=x\n\n" +
		"--x\nContent-Type: text/plain; charset=x-mystery\n\nraw bytes\n" +
		"--x\nContent-Type: text/plain; charset=utf-8\n\nvalid part\n" +
		"--x--\n"
	p := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(p, []byte(msg), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, _, _, err := RenderThread([]core.Message{{ID: "m1", ThreadID: "t1", Paths: []string{p}}}, core.RenderHTML, false, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	joined := joinText(lines)
	if !strings.Contains(joined, "raw bytes") {
		t.Fatalf("the unknown-charset part must render raw, not abort:\n%s", joined)
	}
	if !strings.Contains(joined, "valid part") {
		t.Fatalf("the valid part must still render:\n%s", joined)
	}
}

func TestRenderThreadBodyTruncated(t *testing.T) {
	big := strings.Repeat("x", maxPartBytes+1024)
	lines, _, _, err := RenderThread([]core.Message{{ID: "m1", ThreadID: "t1", Paths: []string{fixture(t, big)}}}, core.RenderHTML, false, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	joined := joinText(lines)
	if !strings.Contains(joined, "[content truncated]") {
		t.Fatalf("an oversized body must be marked truncated:\n%s", joined)
	}
	if len(joined) > maxPartBytes+4096 {
		t.Fatalf("an oversized body must not reach the renderer whole")
	}
}

func TestRenderThreadAttachmentTruncated(t *testing.T) {
	big := strings.Repeat("x", maxPartBytes+1024)
	msg := "From: a@example.com\nTo: b@example.com\nSubject: big\n" +
		"Date: Tue, 01 Jan 2019 00:00:00 +0000\nMIME-Version: 1.0\n" +
		"Content-Type: multipart/mixed; boundary=x\n\n" +
		"--x\nContent-Type: text/plain; charset=utf-8\n\nbody\n" +
		"--x\nContent-Type: application/octet-stream\n" +
		"Content-Disposition: attachment; filename=\"big.bin\"\n\n" + big + "\n" +
		"--x--\n"
	p := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(p, []byte(msg), 0o600); err != nil {
		t.Fatal(err)
	}
	lines, _, _, err := RenderThread([]core.Message{{ID: "m1", ThreadID: "t1", Paths: []string{p}}}, core.RenderHTML, false, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	joined := joinText(lines)
	if !strings.Contains(joined, "big.bin") || !strings.Contains(joined, "truncated") {
		t.Fatalf("an oversized attachment must be marked truncated:\n%s", joined)
	}
}

func TestSplitBodyQuotedAndSignature(t *testing.T) {
	parts := splitBody("plain\n> q1\n> > q2\n-- \nsig\n")
	if len(parts) != 5 || parts[1].Quoted != 1 || parts[2].Quoted != 2 || !parts[4].Signature {
		t.Fatalf("splitBody: %+v", parts)
	}
}

func TestSplitBodyQuotedDepthCap(t *testing.T) {
	parts := splitBody("> > > > > > deep\n")
	if parts[0].Quoted != 5 || parts[0].Body != "> > > > > > deep" {
		t.Fatalf("depth must cap at 5, markers kept: %+v", parts[0])
	}
}

func TestSplitBodyCRLF(t *testing.T) {
	parts := splitBody("a\r\nb\r\n")
	if len(parts) != 2 || parts[0].Body != "a" || parts[1].Body != "b" {
		t.Fatalf("CRLF must be stripped: %+v", parts)
	}
	for _, p := range parts {
		if strings.ContainsRune(p.Body, '\r') {
			t.Fatalf("CR left in body: %+v", parts)
		}
	}
}

// TestExpandTabs pins the 8-column tab stop: tabs expand to spaces
// (one rune per column), since the sanitize pass would drop them.
func TestExpandTabs(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a\tb", "a       b"},             // col 1 -> stop 8
		{"ab\tcd", "ab      cd"},          // col 2 -> stop 8
		{"abcdefg\th", "abcdefg h"},       // col 7 -> stop 8
		{"\tlead", "        lead"},        // col 0 -> stop 8
		{"no tabs", "no tabs"},            // pass-through
		{"12\tx\ty", "12      x       y"}, // two stops in one line
	}
	for _, c := range cases {
		if got := expandTabs(c.in); got != c.want {
			t.Fatalf("expandTabs(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	parts := splitBody("a\tb\n")
	if parts[0].Body != "a       b" {
		t.Fatalf("splitBody must expand tabs: %+v", parts)
	}
}

// fixtureMail is a fabricated message - never real mail content.
const fixtureMail = `From: Alice <alice@example.com>
To: Bob <bob@example.com>, Carol <carol@example.org>
Cc: Dave <dave@example.net>
Subject: hello
Message-Id: <abc123@example.com>
Date: Tue, 14 Aug 2026 10:00:00 +0000

body line one
body line two
`

func TestParseMessageHeaders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fixture.eml")
	if err := os.WriteFile(path, []byte(fixtureMail), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := ParseMessage(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.MessageID != "<abc123@example.com>" {
		t.Fatalf("MessageID = %q", m.MessageID)
	}
	if m.From != "alice@example.com" {
		t.Fatalf("From = %q", m.From)
	}
	if len(m.To) != 2 || m.To[0] != "bob@example.com" || m.To[1] != "carol@example.org" {
		t.Fatalf("To = %v", m.To)
	}
	if len(m.Cc) != 1 || m.Cc[0] != "dave@example.net" {
		t.Fatalf("Cc = %v", m.Cc)
	}
	if len(m.Parts) != 2 || m.Parts[0].Body != "body line one" {
		t.Fatalf("Parts = %+v", m.Parts)
	}
}

func TestRenderThreadTextView(t *testing.T) {
	body := "<p>hello <b>bold</b></p>\n"
	msg := "From: a@example.com\nTo: b@example.com\n" +
		"Subject: hello\nDate: Tue, 01 Jan 2019 00:00:00 +0000\n" +
		"MIME-Version: 1.0\nContent-Type: text/html; charset=utf-8\n\n" +
		body
	p := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(p, []byte(msg), 0o600); err != nil {
		t.Fatal(err)
	}
	msgs := []core.Message{{ID: "m1", ThreadID: "t1", Paths: []string{p}}}

	lines, _, _, err := RenderThread(msgs, core.RenderHTML, false, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(joinText(lines), "<p>") {
		t.Fatalf("the html view must render, not show markup:\n%s", joinText(lines))
	}

	lines, _, _, err = RenderThread(msgs, core.RenderPlain, false, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(joinText(lines), "hello bold") || strings.Contains(joinText(lines), "<p>hello <b>bold</b></p>") {
		t.Fatalf("the text view must show the html as text, not the markup:\n%s", joinText(lines))
	}

	// the raw markup is the source view's alone
	lines, _, _, err = RenderThread(msgs, core.RenderSource, false, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(joinText(lines), "<p>hello <b>bold</b></p>") {
		t.Fatalf("the source view must show the raw markup:\n%s", joinText(lines))
	}
}

// TestRenderThreadAlternative pins the part-selection toggle: a
// multipart/alternative text+html message keeps both parts, the plain
// view renders the plain part only, the html view the html part only.
func TestRenderThreadAlternative(t *testing.T) {
	msg := "From: a@example.com\nTo: b@example.com\n" +
		"Subject: alt\nDate: Tue, 01 Jan 2019 00:00:00 +0000\n" +
		"MIME-Version: 1.0\n" +
		"Content-Type: multipart/alternative; boundary=x\n\n" +
		"--x\nContent-Type: text/plain; charset=utf-8\n\n" +
		"plain body line\n" +
		"--x\nContent-Type: text/html; charset=utf-8\n\n" +
		"<p>html body</p>\n" +
		"--x--\n"
	p := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(p, []byte(msg), 0o600); err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseMessage(p)
	if err != nil {
		t.Fatal(err)
	}
	hasPlain, hasHTML := false, false
	for _, part := range parsed.Parts {
		hasPlain = hasPlain || !part.HTML
		hasHTML = hasHTML || part.HTML
	}
	if !hasPlain || !hasHTML {
		t.Fatalf("both halves of the alternative must survive the parse: %+v", parsed.Parts)
	}

	msgs := []core.Message{{ID: "m1", ThreadID: "t1", Paths: []string{p}}}
	lines, _, _, err := RenderThread(msgs, core.RenderPlain, false, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if out := joinText(lines); !strings.Contains(out, "plain body line") || strings.Contains(out, "<p>html body</p>") {
		t.Fatalf("the plain view must render the plain part only:\n%s", out)
	}

	lines, _, _, err = RenderThread(msgs, core.RenderHTML, false, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if out := joinText(lines); strings.Contains(out, "plain body line") || strings.Contains(out, "<p>") {
		t.Fatalf("the html view must render the html part only:\n%s", out)
	}

	quoted := QuoteParts(parsed.Parts, 0)
	if len(quoted) == 0 || quoted[0].HTML {
		t.Fatalf("the quote must carry the plain part only, never the markup: %+v", quoted)
	}
}

// TestRenderThreadSourceView pins the ctrl+u view: the raw html source
// renders as plain lines and the mime label says what is on screen.
// The three views of an html-only mail are distinct: plain unstyled,
// html styled, source raw.
func TestRenderThreadSourceView(t *testing.T) {
	alt := "From: a@example.com\nTo: b@example.com\n" +
		"Subject: alt\nDate: Tue, 01 Jan 2019 00:00:00 +0000\n" +
		"MIME-Version: 1.0\n" +
		"Content-Type: multipart/alternative; boundary=x\n\n" +
		"--x\nContent-Type: text/plain; charset=utf-8\n\n" +
		"plain body line\n" +
		"--x\nContent-Type: text/html; charset=utf-8\n\n" +
		"<p>html body</p>\n" +
		"--x--\n"
	p := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(p, []byte(alt), 0o600); err != nil {
		t.Fatal(err)
	}
	msgs := []core.Message{{ID: "m1", ThreadID: "t1", Paths: []string{p}}}

	lines, mime, _, err := RenderThread(msgs, core.RenderSource, false, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if out := joinText(lines); !strings.Contains(out, "<p>html body</p>") {
		t.Fatalf("the source view must show the raw markup:\n%s", out)
	}
	if mime != "text/html" {
		t.Fatalf("the source view must label text/html, got %q", mime)
	}

	htmlOnly := "From: a@example.com\nSubject: h\n" +
		"Content-Type: text/html; charset=utf-8\n\n<p>only</p>\n"
	p2 := filepath.Join(t.TempDir(), "msg2")
	if err := os.WriteFile(p2, []byte(htmlOnly), 0o600); err != nil {
		t.Fatal(err)
	}
	msgs2 := []core.Message{{ID: "m1", ThreadID: "t1", Paths: []string{p2}}}
	lines, mime, _, err = RenderThread(msgs2, core.RenderPlain, false, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	out := joinText(lines)
	if !strings.Contains(out, "only") || strings.Contains(out, "<p>only</p>") {
		t.Fatalf("the plain view of an html-only mail must show the html text, not the markup:\n%s", out)
	}
	for i := range lines {
		if len(lines[i].Runs) != 0 {
			t.Fatalf("the html-only plain view must be unstyled, line %d carries runs", i)
		}
	}
	if mime != "text/html" {
		t.Fatalf("the html-only plain view must label text/html, got %q", mime)
	}

	// the same message in the source view keeps the raw markup - the
	// views are distinct
	lines, mime, _, err = RenderThread(msgs2, core.RenderSource, false, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if out := joinText(lines); !strings.Contains(out, "<p>only</p>") {
		t.Fatalf("the source view of an html-only mail must show the raw markup:\n%s", out)
	}
	if mime != "text/html" {
		t.Fatalf("the html-only source view must label text/html, got %q", mime)
	}
}

// TestExtractAttachment pins the attachment demand path (the v
// dialog's view/save): the ordinal-th bytes + name come back,
// out-of-range errors, and the render sanitizes into body lines (F1).
func TestExtractAttachment(t *testing.T) {
	msg := "From: a@example.com\nTo: b@example.com\nSubject: atts\n" +
		"Date: Tue, 01 Jan 2019 00:00:00 +0000\nMIME-Version: 1.0\n" +
		"Content-Type: multipart/mixed; boundary=x\n\n" +
		"--x\nContent-Type: text/plain; charset=utf-8\n\nbody\n" +
		"--x\nContent-Type: application/pdf\nContent-Disposition: attachment; filename=\"report.pdf\"\n\n%PDF-1.4 fake\n" +
		"--x\nContent-Type: text/plain\nContent-Disposition: attachment; filename=\"notes.txt\"\n\nnote one\nnote two\n" +
		"--x--\n"
	p := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(p, []byte(msg), 0o600); err != nil {
		t.Fatal(err)
	}
	name, typ, data, err := ExtractAttachment(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if name != "report.pdf" || typ != "application/pdf" || string(data) != "%PDF-1.4 fake" {
		t.Fatalf("attachment 0 = %q %q %q", name, typ, data)
	}
	name, typ, data, err = ExtractAttachment(p, 1)
	if err != nil {
		t.Fatal(err)
	}
	if name != "notes.txt" || typ != "text/plain" || string(data) != "note one\nnote two" {
		t.Fatalf("attachment 1 = %q %q %q", name, typ, data)
	}
	if _, _, _, err := ExtractAttachment(p, 9); err == nil {
		t.Fatal("an out-of-range ordinal must error")
	}
	// F1: the ESC byte is stripped; the sequence text stays
	lines := RenderAttachment([]byte("a\x1b[31mb\nc"))
	if len(lines) != 2 || lines[0].Text != "a[31mb" || lines[1].Text != "c" {
		t.Fatalf("the render must sanitize into body lines: %+v", lines)
	}
}

// TestRenderThreadMimeLabel pins the label against the message's real
// parts: a plain-only mail renders plain in every view and says so.
func TestRenderThreadMimeLabel(t *testing.T) {
	plain := "From: a@example.com\nSubject: p\n" +
		"Content-Type: text/plain; charset=utf-8\n\nplain body\n"
	p := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(p, []byte(plain), 0o600); err != nil {
		t.Fatal(err)
	}
	msgs := []core.Message{{ID: "m1", ThreadID: "t1", Paths: []string{p}}}
	for _, mode := range []core.RenderMode{core.RenderPlain, core.RenderHTML, core.RenderSource} {
		lines, mime, _, err := RenderThread(msgs, mode, false, 0, false)
		if err != nil {
			t.Fatal(err)
		}
		if out := joinText(lines); !strings.Contains(out, "plain body") {
			t.Fatalf("the plain-only mail must render plain in every view (mode %d):\n%s", mode, out)
		}
		if mime != "text/plain" {
			t.Fatalf("the plain-only mail must label text/plain in mode %d, got %q", mode, mime)
		}
	}
}

// TestRenderThreadHeaders pins the h toggle: the full raw header block
// replaces the envelope in the plain view - every field in file order,
// delivery headers included, verbatim.
func TestRenderThreadHeaders(t *testing.T) {
	msg := "Return-Path: <bounce@example.com>\n" +
		"Received: from mx.example.com by mail.example.com\n\tfor <alpha@example.com>\n" +
		"DKIM-Signature: v=1; a=rsa-sha256; d=example.com\n" +
		"From: alpha@example.com\nTo: beta@example.com\n" +
		"Subject: =?utf-8?Q?caf=C3=A9?=\n" +
		"Message-ID: <abc@example.com>\n" +
		"Date: Tue, 01 Jan 2019 00:00:00 +0000\n" +
		"Content-Type: text/plain; charset=utf-8\n\nbody\n"
	p := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(p, []byte(msg), 0o600); err != nil {
		t.Fatal(err)
	}
	msgs := []core.Message{{ID: "m1", ThreadID: "t1", Paths: []string{p}, Subject: "caf\xc3\xa9"}}

	lines, _, _, err := RenderThread(msgs, core.RenderPlain, true, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	out := joinText(lines)
	// file-ordered and verbatim: delivery headers included, the folded
	// Received unfolded, the encoded subject as stored; go-message
	// canonicalizes field names (DKIM-Signature -> Dkim-Signature)
	block := []string{
		"Return-Path: <bounce@example.com>",
		"Received: from mx.example.com by mail.example.com for <alpha@example.com>",
		"Dkim-Signature: v=1; a=rsa-sha256; d=example.com",
		"From: alpha@example.com",
		"To: beta@example.com",
		"Subject: =?utf-8?Q?caf=C3=A9?=",
		"Message-Id: <abc@example.com>",
		"Date: Tue, 01 Jan 2019 00:00:00 +0000",
		"Content-Type: text/plain; charset=utf-8",
	}
	pos := -1
	for _, want := range block {
		i := strings.Index(out, want)
		if i < 0 {
			t.Fatalf("the header block must show %q:\n%s", want, out)
		}
		if i < pos {
			t.Fatalf("the header block must stay in file order, %q out of place:\n%s", want, out)
		}
		pos = i
	}
	if !strings.Contains(out, "body") {
		t.Fatalf("the body must follow the header block:\n%s", out)
	}

	// without the toggle the envelope carries no Reply-To row
	lines, _, _, err = RenderThread(msgs, core.RenderPlain, false, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if out := joinText(lines); strings.Contains(out, "Reply-To:") {
		t.Fatalf("the envelope must not carry the header rows:\n%s", out)
	}
}

// TestEnvelopeShowsCc pins the curated pager envelope: a Cc header renders
// as a Cc: row (between To and Subject) by default, absent when no Cc.
func TestEnvelopeShowsCc(t *testing.T) {
	msg := "From: alpha@example.com\nTo: beta@example.com\nCc: carol@example.com\n" +
		"Subject: cc test\nDate: Tue, 01 Jan 2019 00:00:00 +0000\n" +
		"Content-Type: text/plain; charset=utf-8\n\nbody\n"
	p := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(p, []byte(msg), 0o600); err != nil {
		t.Fatal(err)
	}
	msgs := []core.Message{{ID: "m1", ThreadID: "t1", Paths: []string{p}, Subject: "cc test"}}

	lines, _, _, err := RenderThread(msgs, core.RenderPlain, false, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	out := joinText(lines)
	to := strings.Index(out, "To:")
	cc := strings.Index(out, "Cc:")
	subj := strings.Index(out, "Subject:")
	if cc < 0 || !strings.Contains(out, "carol@example.com") {
		t.Fatalf("the envelope must show the Cc row by default:\n%s", out)
	}
	if cc < to || subj < cc {
		t.Fatalf("Cc must sit between To and Subject:\n%s", out)
	}

	// a message without Cc must not emit the row
	plain := "From: alpha@example.com\nTo: beta@example.com\n" +
		"Subject: no cc\nDate: Tue, 01 Jan 2019 00:00:00 +0000\n" +
		"Content-Type: text/plain; charset=utf-8\n\nbody\n"
	p2 := filepath.Join(t.TempDir(), "msg2")
	if err := os.WriteFile(p2, []byte(plain), 0o600); err != nil {
		t.Fatal(err)
	}
	msgs2 := []core.Message{{ID: "m2", ThreadID: "t2", Paths: []string{p2}, Subject: "no cc"}}
	lines, _, _, err = RenderThread(msgs2, core.RenderPlain, false, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if out := joinText(lines); strings.Contains(out, "Cc:") {
		t.Fatalf("a message without Cc must not emit a Cc row:\n%s", out)
	}
}

// TestRenderThreadHeadersHTML pins the h toggle on an html mail: the full
// raw header block must show in the html view too, not only text/plain.
func TestRenderThreadHeadersHTML(t *testing.T) {
	msg := "Return-Path: <bounce@example.com>\n" +
		"From: alpha@example.com\n" +
		"Content-Type: multipart/alternative; boundary=\"altb\"\n\n" +
		"--altb\nContent-Type: text/plain\n\nplain body\n" +
		"--altb\nContent-Type: text/html\n\n<html><body>html body</body></html>\n" +
		"--altb--\n"
	p := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(p, []byte(msg), 0o600); err != nil {
		t.Fatal(err)
	}
	msgs := []core.Message{{ID: "m1", ThreadID: "t1", Paths: []string{p}, Subject: "html"}}

	for _, mode := range []core.RenderMode{core.RenderPlain, core.RenderHTML} {
		lines, _, _, err := RenderThread(msgs, mode, true, 0, false)
		if err != nil {
			t.Fatal(err)
		}
		out := joinText(lines)
		if !strings.Contains(out, "Return-Path: <bounce@example.com>") {
			t.Fatalf("mode %d: the h toggle must show the full header block on html mail:\n%s", mode, out)
		}
		if !strings.Contains(out, "From: alpha@example.com") {
			t.Fatalf("mode %d: the header block must carry From:\n%s", mode, out)
		}
	}
}

// TestHTMLPartListsAsAttachment pins the html part as a download entry:
// both walks count it with the same ordinal, so the v dialog's "N.
// html" and the extract/save seams index the same part stream.
func TestHTMLPartListsAsAttachment(t *testing.T) {
	msg := "From: a@example.com\nTo: b@example.com\nSubject: html\n" +
		"Date: Tue, 01 Jan 2019 00:00:00 +0000\nMIME-Version: 1.0\n" +
		"Content-Type: multipart/alternative; boundary=x\n\n" +
		"--x\nContent-Type: text/plain; charset=utf-8\n\nplain\n" +
		"--x\nContent-Type: text/html; charset=utf-8\n\n<h1>hi</h1>\n" +
		"--x--\n"
	p := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(p, []byte(msg), 0o600); err != nil {
		t.Fatal(err)
	}
	// the boundary terminator consumes the part's trailing \n, so the
	// parsed body is "<h1>hi</h1>" without it
	m, err := ParseMessage(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Attachments) != 1 || m.Attachments[0].Name != "html" || m.Attachments[0].Size != int64(len("<h1>hi</h1>")) {
		t.Fatalf("attachments = %+v, want the html part", m.Attachments)
	}
	name, typ, data, err := ExtractAttachment(p, 0)
	if err != nil {
		t.Fatal(err)
	}
	if name != "html" || typ != "text/html" || string(data) != "<h1>hi</h1>" {
		t.Fatalf("extract = %q %q %q, want the raw html part", name, typ, data)
	}
}

// TestParseMessageMimeType pins the content type on every attachment
// entry: attachment headers carry their declared type, the html part
// of an alternative pair text/html (the categorize hooks match on it);
// entries stay ordinal-aligned with ExtractAttachment.
func TestParseMessageMimeType(t *testing.T) {
	msg := "From: a@example.com\nSubject: atts\n" +
		"Date: Tue, 01 Jan 2019 00:00:00 +0000\nMIME-Version: 1.0\n" +
		"Content-Type: multipart/mixed; boundary=x\n\n" +
		"--x\nContent-Type: text/plain; charset=utf-8\n\nbody\n" +
		"--x\nContent-Type: application/pdf\nContent-Disposition: attachment; filename=\"report.pdf\"\n\n%PDF-1.4 fake\n" +
		"--x\nContent-Type: text/plain\nContent-Disposition: attachment; filename=\"notes.txt\"\n\nnote one\n" +
		"--x--\n"
	p := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(p, []byte(msg), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := ParseMessage(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Attachments) != 2 ||
		m.Attachments[0].MimeType != "application/pdf" ||
		m.Attachments[1].MimeType != "text/plain" {
		t.Fatalf("attachments = %+v, want pdf + txt with their declared types", m.Attachments)
	}
	if _, typ, _, err := ExtractAttachment(p, 0); err != nil || typ != m.Attachments[0].MimeType {
		t.Fatalf("extract 0 = %q, %v - the ordinal alignment must hold", typ, err)
	}

	alt := "From: a@example.com\nSubject: html\n" +
		"Date: Tue, 01 Jan 2019 00:00:00 +0000\nMIME-Version: 1.0\n" +
		"Content-Type: multipart/alternative; boundary=x\n\n" +
		"--x\nContent-Type: text/plain; charset=utf-8\n\nplain\n" +
		"--x\nContent-Type: text/html; charset=utf-8\n\n<h1>hi</h1>\n" +
		"--x--\n"
	pa := filepath.Join(t.TempDir(), "msg")
	if err := os.WriteFile(pa, []byte(alt), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err = ParseMessage(pa)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Attachments) != 1 || m.Attachments[0].MimeType != "text/html" {
		t.Fatalf("the html entry must carry text/html: %+v", m.Attachments)
	}
}
