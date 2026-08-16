package setup

import (
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"
)

func mkdirs(t *testing.T, root string, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0700); err != nil {
			t.Fatal(err)
		}
	}
}

// TestDetectGmail pins the gmail template: the INBOX + [Gmail] system
// folders match, the hard-tag map extracts the exact folder names,
// and maildir internals (cur/new/tmp) never count as folders.
func TestDetectGmail(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root,
		"gmail/INBOX/cur", "gmail/INBOX/new", "gmail/INBOX/tmp",
		"gmail/Archives/cur", "gmail/Pending/cur",
		"gmail/[Gmail]/Drafts/cur", "gmail/[Gmail]/Sent Mail/cur",
		"gmail/[Gmail]/Spam/cur", "gmail/[Gmail]/Trash/cur")
	accs, err := Detect(root, Templates)
	if err != nil {
		t.Fatal(err)
	}
	if len(accs) != 1 {
		t.Fatalf("want exactly one account, got %d: %+v", len(accs), accs)
	}
	a := accs[0]
	if a.Name != "gmail" || a.Template != "gmail" {
		t.Fatalf("got %+v, want gmail matched by the gmail template", a)
	}
	want := map[string]string{
		"inbox": "INBOX", "draft": "[Gmail]/Drafts", "sent": "[Gmail]/Sent Mail",
		"spam": "[Gmail]/Spam", "deleted": "[Gmail]/Trash",
		"archive": "Archives", "pending": "Pending",
	}
	if !maps.Equal(a.Folders, want) {
		t.Fatalf("folders = %v, want %v", a.Folders, want)
	}
}

// TestDetectFlatImapUnmatched pins the discriminator: a flat IMAP
// layout (system folders at the account root) fails the required
// [Gmail] names and stays unmatched; a plain file is not an account.
func TestDetectFlatImapUnmatched(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root,
		"jelveh/INBOX", "jelveh/Drafts", "jelveh/Sent",
		"jelveh/Spam", "jelveh/Trash", "jelveh/Archive")
	if err := os.WriteFile(filepath.Join(root, "notes"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	accs, err := Detect(root, Templates)
	if err != nil {
		t.Fatal(err)
	}
	if len(accs) != 1 || accs[0].Name != "jelveh" || accs[0].Template != "" {
		t.Fatalf("flat imap must not match the gmail template: %+v", accs)
	}
}

// TestDetectGmailVariant pins the fallbacks: an account with the
// [Gmail] system folders but Archive instead of Archives still
// matches, resolves the fallback, and omits absent optional tags.
func TestDetectGmailVariant(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root,
		"toptal/INBOX", "toptal/Archive",
		"toptal/[Gmail]/Drafts", "toptal/[Gmail]/Sent Mail",
		"toptal/[Gmail]/Spam", "toptal/[Gmail]/Trash")
	accs, err := Detect(root, Templates)
	if err != nil {
		t.Fatal(err)
	}
	if len(accs) != 1 || accs[0].Template != "gmail" {
		t.Fatalf("the variant must match the gmail template: %+v", accs)
	}
	a := accs[0]
	if a.Folders["archive"] != "Archive" {
		t.Fatalf("archive must resolve the fallback, got %q", a.Folders["archive"])
	}
	if _, ok := a.Folders["pending"]; ok {
		t.Fatal("absent optional tags must stay out of the map")
	}
}

// TestDetectSorted pins determinism: accounts sort by name.
func TestDetectSorted(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root,
		"zeta/INBOX", "zeta/[Gmail]/Drafts", "zeta/[Gmail]/Sent Mail",
		"zeta/[Gmail]/Spam", "zeta/[Gmail]/Trash",
		"alpha/INBOX", "alpha/[Gmail]/Drafts", "alpha/[Gmail]/Sent Mail",
		"alpha/[Gmail]/Spam", "alpha/[Gmail]/Trash")
	accs, err := Detect(root, Templates)
	if err != nil {
		t.Fatal(err)
	}
	if len(accs) != 2 || accs[0].Name != "alpha" || accs[1].Name != "zeta" {
		t.Fatalf("accounts must sort by name: %+v", accs)
	}
}

// TestDetectTemplateSet pins the merge contract: a contributed
// template replaces the built-in of the same name, a new name adds to
// the detection set (first match wins - the set is tried in order).
func TestDetectTemplateSet(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root,
		"gmail/INBOX", "gmail/[Gmail]/Drafts", "gmail/[Gmail]/Sent Mail",
		"gmail/[Gmail]/Spam", "gmail/[Gmail]/Trash",
		"exchange/INBOX", "exchange/Sent Items", "exchange/Deleted Items")
	templates := []Template{
		{Name: "gmail", Required: map[string][]string{
			"inbox": {"INBOX"}, "draft": {"[Gmail]/Drafts"}, "sent": {"[Gmail]/Sent Mail"},
			"spam": {"[Gmail]/Spam"}, "deleted": {"[Gmail]/Trash"},
		}},
		{Name: "exchange", Required: map[string][]string{
			"inbox": {"INBOX"}, "sent": {"Sent Items"}, "deleted": {"Deleted Items"},
		}},
	}
	accs, err := Detect(root, templates)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, a := range accs {
		got[a.Name] = a.Template
	}
	if got["gmail"] != "gmail" || got["exchange"] != "exchange" {
		t.Fatalf("the merged set must detect both accounts: %v", got)
	}
}

func TestDetectMissingRoot(t *testing.T) {
	if _, err := Detect(filepath.Join(t.TempDir(), "nope"), Templates); err == nil {
		t.Fatal("a missing root must error")
	}
}

// TestGenerateValid pins the generated file: valid TOML, quoted
// folder names, sorted tags, and no entries for unmatched accounts.
func TestGenerateValid(t *testing.T) {
	accs := []Account{
		{Name: "zeta", Template: "gmail", Folders: map[string]string{
			"inbox": "INBOX", "draft": "[Gmail]/Drafts",
		}},
		{Name: "jelveh"},
	}
	got := Generate(accs)
	if strings.Contains(got, "jelveh") {
		t.Fatalf("unmatched accounts must not be generated:\n%s", got)
	}
	if !strings.Contains(got, "draft = \"[Gmail]/Drafts\"") {
		t.Fatalf("folder names must quote through TOML:\n%s", got)
	}
	var parsed struct {
		Accounts map[string]struct {
			Folder  string            `toml:"folder"`
			Folders map[string]string `toml:"folders"`
		} `toml:"accounts"`
	}
	if _, err := toml.Decode(got, &parsed); err != nil {
		t.Fatalf("generated output must parse as TOML: %v\n%s", err, got)
	}
	z := parsed.Accounts["zeta"]
	if z.Folder != "zeta" || z.Folders["inbox"] != "INBOX" || z.Folders["draft"] != "[Gmail]/Drafts" {
		t.Fatalf("parsed account = %+v, want folder + the hard-tag map", z)
	}
}
