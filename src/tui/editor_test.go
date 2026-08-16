package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"notmutt/compose"
)

func TestEditorBufferRoundTrip(t *testing.T) {
	s := compose.NewCompose("gmail", "Bob <bob@example.com>", "gmail", "bob")
	s.To = []string{"a@b.c"}
	s.Subject = "hello"
	s.Body = "body text"

	path, err := writeEditorBuffer(*s, "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0600 {
		t.Fatalf("buffer perms = %v, want 0600 (F5)", fi.Mode().Perm())
	}
	// the buffer holds ONLY the mail content (mutt's msgbody shape) -
	// the email header is built from the dialogue fields, never the
	// editor
	if raw, _ := os.ReadFile(path); strings.Contains(string(raw), "Subject:") || strings.Contains(string(raw), "To:") {
		t.Fatalf("the editor buffer must not carry the email header:\n%s", raw)
	}

	got, err := applyEditorResult(*s, path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "body text" {
		t.Fatalf("round trip: %q", got.Body)
	}
	if got.SignatureBody != "bob" {
		t.Fatalf("signature must survive the round trip: %q", got.SignatureBody)
	}
	if got.To[0] != "a@b.c" || got.Subject != "hello" {
		t.Fatalf("header fields are dialogue-owned, must survive untouched: %v %q", got.To, got.Subject)
	}
}

func TestApplyEditorResultParsesEdits(t *testing.T) {
	s := compose.NewCompose("gmail", "Bob <bob@example.com>", "gmail", "bob")
	s.Body = "old body"

	path := filepath.Join(t.TempDir(), "buf")
	content := "new body\n\n-- \nnew sig"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := applyEditorResult(*s, path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != "new body\n\n-- \nnew sig" {
		t.Fatalf("edits = %q", got.Body)
	}
	// the edited signature tail no longer matches "bob": it stays as
	// body text and the signature detaches
	if !strings.Contains(got.Body, "new sig") || got.SignatureBody != "" {
		t.Fatalf("edited tail must stay as text, signature detach: %q %q", got.Body, got.SignatureBody)
	}
}

func TestEditorCmd(t *testing.T) {
	t.Setenv("EDITOR", "emacs -nw")
	cmd := editorCmd("/tmp/buf")
	if cmd.Path == "" || cmd.Args[len(cmd.Args)-1] != "/tmp/buf" {
		t.Fatalf("cmd = %+v", cmd)
	}
	if cmd.Args[1] != "-nw" {
		t.Fatalf("middle args = %+v", cmd.Args)
	}
	t.Setenv("EDITOR", "")
	if cmd := editorCmd("/tmp/buf"); cmd.Args[0] != "vi" {
		t.Fatalf("fallback editor = %+v", cmd)
	}
}
