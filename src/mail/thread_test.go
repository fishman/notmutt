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

func joinText(lines []Line) string {
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
	lines, err := RenderThread(msgs)
	if err != nil {
		t.Fatal(err)
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
	lines, err := RenderThread(msgs)
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
	lines, err := RenderThread([]core.Message{{ID: "m1", ThreadID: "t1", Paths: []string{p}}})
	if err != nil {
		t.Fatal(err)
	}
	joined := joinText(lines)
	if !strings.Contains(joined, "report.pdf") {
		t.Fatalf("attachment line missing:\n%s", joined)
	}
	if !strings.Contains(joined, "body") {
		t.Fatalf("inline part missing:\n%s", joined)
	}
}

func TestRenderThreadMissingFile(t *testing.T) {
	msgs := []core.Message{{ID: "m1", ThreadID: "t1", Paths: []string{"/nonexistent"}}}
	lines, err := RenderThread(msgs)
	if err != nil {
		t.Fatal(err)
	}
	joined := joinText(lines)
	if !strings.Contains(joined, "failed to parse message") {
		t.Fatalf("missing file must render an error line:\n%s", joined)
	}
}

func TestRenderThreadNoPath(t *testing.T) {
	lines, err := RenderThread([]core.Message{{ID: "m1", ThreadID: "t1"}})
	if err != nil {
		t.Fatal(err)
	}
	joined := joinText(lines)
	if !strings.Contains(joined, "no path") {
		t.Fatalf("missing path must render an error line:\n%s", joined)
	}
}

func TestRenderThreadEmpty(t *testing.T) {
	if _, err := RenderThread(nil); err == nil {
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
	lines, err := RenderThread(msgs)
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
	lines, err := RenderThread([]core.Message{{ID: "m1", ThreadID: "t1", Paths: []string{p}}})
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
	lines, err := RenderThread([]core.Message{{ID: "m1", ThreadID: "t1", Paths: []string{fixture(t, big)}}})
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
	lines, err := RenderThread([]core.Message{{ID: "m1", ThreadID: "t1", Paths: []string{p}}})
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
	if parts[0].Quoted != 5 || parts[0].Body != "> deep" {
		t.Fatalf("depth must cap at 5: %+v", parts[0])
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
