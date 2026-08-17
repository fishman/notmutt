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
	if !a.NoFcc {
		t.Fatal("gmail stores sent copies server-side: the account must carry no_fcc")
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

// TestDetectFlatImap pins the gmail discriminator and the flat layout
// (the flat shape): system folders at the account root, no top-level
// [Gmail] - not gmail, it falls to the generic shape whose Match names
// resolve (INBOX + Sent). Every hard tag resolves from the flat names
// (Trash, Pending), and the priority lists pick the standard name when
// both exist (Archives over Archive, Spam over Junk). A plain file is
// never an account.
func TestDetectFlatImap(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root,
		"flint/INBOX", "flint/Drafts", "flint/Sent",
		"flint/Spam", "flint/Junk", "flint/Trash",
		"flint/Archive", "flint/Archives", "flint/Pending")
	if err := os.WriteFile(filepath.Join(root, "notes"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	accs, err := Detect(root, Templates)
	if err != nil {
		t.Fatal(err)
	}
	if len(accs) != 1 {
		t.Fatalf("want one account, got %+v", accs)
	}
	a := accs[0]
	if a.Name != "flint" || a.Template != "outlook" {
		t.Fatalf("flat imap must match the generic shape, got %+v", a)
	}
	want := map[string]string{
		"inbox": "INBOX", "sent": "Sent", "deleted": "Trash",
		"draft": "Drafts", "archive": "Archives", "spam": "Spam", "pending": "Pending",
	}
	if !maps.Equal(a.Folders, want) {
		t.Fatalf("folders = %v, want %v", a.Folders, want)
	}
	if a.Template == "gmail" {
		t.Fatal("flat imap must never match gmail")
	}
}

// TestDetectGmailVariant pins the fallbacks: an account with the
// [Gmail] system folders but Archive instead of Archives still
// matches, resolves the fallback, and omits absent optional tags.
func TestDetectGmailVariant(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root,
		"gull/INBOX", "gull/Archive",
		"gull/[Gmail]/Drafts", "gull/[Gmail]/Sent Mail",
		"gull/[Gmail]/Spam", "gull/[Gmail]/Trash")
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

// TestDetectGmailFlatFallbacks pins the [Gmail]-marked flat account
// (the flat gmail shape): the [Gmail] marker is present but the system
// subfolders were never synced - sent/deleted resolve to the flat
// names at the account root instead of staying unmapped.
func TestDetectGmailFlatFallbacks(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root,
		"lark/INBOX", "lark/Archive", "lark/Sent", "lark/Trash",
		"lark/[Gmail]")
	accs, err := Detect(root, Templates)
	if err != nil {
		t.Fatal(err)
	}
	if len(accs) != 1 || accs[0].Template != "gmail" {
		t.Fatalf("the marker must match gmail with flat fallbacks: %+v", accs)
	}
	a := accs[0]
	if a.Folders["sent"] != "Sent" || a.Folders["deleted"] != "Trash" || a.Folders["archive"] != "Archive" {
		t.Fatalf("flat fallbacks must resolve, got %v", a.Folders)
	}
	if _, ok := a.Folders["draft"]; ok {
		t.Fatal("absent tags must stay out of the map")
	}
}

// TestDetectZoho pins the zoho template: the Snoozed system folder
// discriminates zoho from the flat outlook shape, the hard-tag map
// resolves, and the account carries no_fcc (zoho stores sent copies
// server-side).
func TestDetectZoho(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root,
		"zoho/INBOX/cur", "zoho/Sent/cur", "zoho/Snoozed/cur",
		"zoho/Drafts/cur", "zoho/Trash/cur", "zoho/Spam/cur",
		"zoho/Archive/cur", "zoho/Pending/cur")
	accs, err := Detect(root, Templates)
	if err != nil {
		t.Fatal(err)
	}
	if len(accs) != 1 || accs[0].Template != "zoho" {
		t.Fatalf("got %+v, want the zoho template", accs)
	}
	if !accs[0].NoFcc {
		t.Fatal("zoho stores sent copies server-side: the account must carry no_fcc")
	}
	want := map[string]string{
		"inbox": "INBOX", "sent": "Sent", "deleted": "Trash", "draft": "Drafts",
		"spam": "Spam", "archive": "Archive", "pending": "Pending",
	}
	if !maps.Equal(accs[0].Folders, want) {
		t.Fatalf("folders = %v, want %v", accs[0].Folders, want)
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
		{Name: "gmail", Match: []string{"INBOX", "[Gmail]"}, Folders: map[string][]string{
			"inbox": {"INBOX"}, "draft": {"[Gmail]/Drafts"}, "sent": {"[Gmail]/Sent Mail"},
			"spam": {"[Gmail]/Spam"}, "deleted": {"[Gmail]/Trash"},
		}},
		{Name: "exchange", Match: []string{"INBOX", "Sent Items"}, Folders: map[string][]string{
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

// TestDetectTopLevelOnly pins the match rule: only TOP-LEVEL folders
// gate a template. A [Gmail] dir nested below another folder never
// counts, while an account whose top-level [Gmail] carries just one
// subfolder still matches (the sync may skip system folders - the
// presence of [Gmail] itself says gmail).
func TestDetectTopLevelOnly(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root,
		"nested/INBOX", "nested/stuff/[Gmail]", "nested/stuff/[Gmail]/Drafts",
		"partial/INBOX", "partial/[Gmail]/Drafts")
	accs, err := Detect(root, Templates)
	if err != nil {
		t.Fatal(err)
	}
	if len(accs) != 2 {
		t.Fatalf("both dirs are account candidates (they hold folders): %+v", accs)
	}
	byName := map[string]Account{}
	for _, a := range accs {
		byName[a.Name] = a
	}
	if byName["nested"].Template != "" {
		t.Fatalf("a nested [Gmail] must never match gmail: %+v", accs)
	}
	if byName["partial"].Template != "gmail" {
		t.Fatalf("a top-level [Gmail] must match gmail: %+v", accs)
	}
	if byName["partial"].Folders["inbox"] != "INBOX" || byName["partial"].Folders["draft"] != "[Gmail]/Drafts" {
		t.Fatalf("extraction must still resolve the nested paths: %v", byName["partial"].Folders)
	}
}

// TestDetectSkipsEmptyDir pins the drafts case: an empty directory is
// not an account - no detection noise from a stray maildir under
// construction.
func TestDetectSkipsEmptyDir(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "drafts")
	accs, err := Detect(root, Templates)
	if err != nil {
		t.Fatal(err)
	}
	if len(accs) != 0 {
		t.Fatalf("an empty dir must not be an account: %+v", accs)
	}
}

func TestDetectMissingRoot(t *testing.T) {
	if _, err := Detect(filepath.Join(t.TempDir(), "nope"), Templates); err == nil {
		t.Fatal("a missing root must error")
	}
}

// TestGenerateValid pins the generated file: valid TOML, quoted
// folder names, sorted tags, and no entries for unmatched accounts.
// A no_fcc account (gmail) generates the flag with the reason
// comment; a plain account generates no flag.
func TestGenerateValid(t *testing.T) {
	accs := []Account{
		{Name: "zeta", Template: "gmail", NoFcc: true, Folders: map[string]string{
			"inbox": "INBOX", "draft": "[Gmail]/Drafts",
		}},
		{Name: "wren"},
	}
	got := Generate(accs)
	if strings.Contains(got, "wren") {
		t.Fatalf("unmatched accounts must not be generated:\n%s", got)
	}
	if !strings.Contains(got, "draft = \"[Gmail]/Drafts\"") {
		t.Fatalf("folder names must quote through TOML:\n%s", got)
	}
	if !strings.Contains(got, "no_fcc = true") || !strings.Contains(got, "stores sent copies server-side") {
		t.Fatalf("a no_fcc account must generate the flag with the reason comment:\n%s", got)
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
