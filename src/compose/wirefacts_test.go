package compose

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestAssemblePartWireFacts pins the wire facts the compose UI's
// attachment rows display (tui wireEncodingInline/wireEncodingAttach/
// wireCharsetInline): the inline part is quoted-printable with the
// explicit charset, an attachment rides base64 and the detected
// Content-Type. If a go-message upgrade changes any of these, this
// test fails and the display constants must follow.
func TestAssemblePartWireFacts(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "note-*.md")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("# hello\n")
	f.Close()
	st := &State{From: "a@b.c", To: []string{"d@e.f"}, Subject: "s"}
	st.AddAttachment(f.Name())
	if got := st.Attachments[0].MimeType; got != "text/markdown" {
		t.Fatalf("MimeTypeOf(.md) = %q, want text/markdown", got)
	}
	var buf bytes.Buffer
	if err := st.Assemble(&buf); err != nil {
		t.Fatal(err)
	}
	raw := buf.String()
	for _, want := range []string{
		"Content-Transfer-Encoding: quoted-printable",
		"Content-Transfer-Encoding: base64",
		"Content-Type: text/markdown",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("the wire must carry %q:\n%s", want, raw)
		}
	}
}

func TestMimeTypeOf(t *testing.T) {
	for name, want := range map[string]string{
		"notes.md":     "text/markdown",
		"x.PDF":        "application/pdf",
		"no-extension": "application/octet-stream",
		"x.unknownx":   "application/octet-stream",
	} {
		if got := MimeTypeOf(name); got != want {
			t.Fatalf("MimeTypeOf(%q) = %q, want %q", name, got, want)
		}
	}
}
