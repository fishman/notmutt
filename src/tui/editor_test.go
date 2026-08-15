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

	path, err := writeEditorBuffer(*s)
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

	got, err := applyEditorResult(*s, path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != "hello" || got.Body != "body text" {
		t.Fatalf("round trip: %q %q", got.Subject, got.Body)
	}
	if got.SignatureBody != "bob" {
		t.Fatalf("signature must survive the round trip: %q", got.SignatureBody)
	}
}

func TestApplyEditorResultParsesEdits(t *testing.T) {
	s := compose.NewCompose("gmail", "Bob <bob@example.com>", "gmail", "bob")
	s.To = []string{"a@b.c"}
	s.Subject = "old"
	s.Body = "old body"

	path := filepath.Join(t.TempDir(), "buf")
	content := "To: x@y.z\nCc: \nSubject: new subject\n\nnew body\n\n-- \nnew sig"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	got, err := applyEditorResult(*s, path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Subject != "new subject" || got.Body != "new body\n\n-- \nnew sig" {
		t.Fatalf("edits = %q %q", got.Subject, got.Body)
	}
	if len(got.To) != 1 || got.To[0] != "x@y.z" {
		t.Fatalf("to = %v", got.To)
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
