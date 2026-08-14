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
	if _, err := RenderThread(msgs); err == nil {
		t.Fatal("missing file must error")
	}
}

func TestSplitBodyQuotedAndSignature(t *testing.T) {
	parts := splitBody("plain\n> q1\n> > q2\n-- \nsig\n")
	if len(parts) != 5 || parts[1].Quoted != 1 || parts[2].Quoted != 2 || !parts[4].Signature {
		t.Fatalf("splitBody: %+v", parts)
	}
}
