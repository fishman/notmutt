package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"notmutt/setup"
)

// TestResolveSetupFolders: the setup resolution follows the mover's own
// machinery - the account folder space plus the detected folder map,
// first existing wins, else the first candidate - and an account whose
// folders resolve to nothing fails setup.
func TestResolveSetupFolders(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "mail")
	for _, d := range []string{"gmail/INBOX", "gmail/Archives", "flint/Sent"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	accs := []setup.Account{
		{Name: "gmail", Template: "gmail", Folders: map[string]string{
			"archive": "Archives", "inbox": "INBOX", "sent": "[Gmail]/Sent Mail"}},
		{Name: "flint", Template: "icloud", Folders: map[string]string{"sent": "Sent"}},
		{Name: "unmatched", Template: ""},
	}
	resolved, err := resolveSetupFolders(root, accs)
	if err != nil {
		t.Fatal(err)
	}
	want := "archive=" + filepath.Join(root, "gmail", "Archives") +
		" inbox=" + filepath.Join(root, "gmail", "INBOX") +
		" sent=" + filepath.Join(root, "gmail", "[Gmail]", "Sent Mail")
	if got := strings.Join(resolved["gmail"], " "); got != want {
		t.Fatalf("gmail = %q, want %q", got, want)
	}
	if got := strings.Join(resolved["flint"], " "); got != "sent="+filepath.Join(root, "flint", "Sent") {
		t.Fatalf("flint = %q", got)
	}
	if _, ok := resolved["unmatched"]; ok {
		t.Fatal("unmatched accounts must not resolve")
	}

	accs[0].Folders = map[string]string{}
	if _, err := resolveSetupFolders(root, accs); err == nil || !strings.Contains(err.Error(), "gmail") {
		t.Fatalf("want gmail resolution failure, got %v", err)
	}
}
