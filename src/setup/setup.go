// Package setup detects the accounts on disk by their folder
// structure and generates the accounts config (the `notmutt setup`
// subcommand). Detection is template-driven: a template names each
// hard tag's candidate folder names; an account matches when every
// required tag resolves to an existing folder. Only directory names
// are read - never mail content.
package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Template is a provider folder shape: Match names the TOP-LEVEL
// folders that gate the template (an account matches when every one
// is present - the match only ever applies to top-level folders), and
// Folders maps each hard tag to its candidate folder paths in
// priority order (the afew folder_priorities shape). A tag's folder
// is its first present candidate - the gmail candidates nest
// ("[Gmail]/Drafts") for the mover's real path - and tags with no
// present candidate stay out of the account's folder map. NoFcc
// marks providers that store sent copies server-side (gmail): the
// generated account gets no_fcc = true so the client writes no fcc
// copy.
type Template struct {
	Name    string
	Match   []string
	Folders map[string][]string
	NoFcc   bool
}

// Templates is the built-in detection set, tried in order: an account
// matches the first template whose top-level Match names all resolve.
// The gmail discriminator is the top-level [Gmail] folder (mbsync
// syncs it as one dir; its presence says "gmail system folders", so
// an account that carries it is a gmail account even when some
// [Gmail] subfolders were never synced); flat IMAP layouts without it
// (Drafts/Sent/Trash at the account root) fall to the generic
// shapes. Candidate lists are priority-ordered: the standard name
// first (Archives before Archive, Spam before Junk), the flat
// fallback after the provider's system folders (a [Gmail]-marked
// account synced flat, or a flat account with Trash instead of
// Deleted Items, still resolves). The lua build evaluates the same templates from
// app/lua/templates/*.lua (the shipped examples users copy from);
// this Go set is the no-Lua fallback, pinned equal by
// TestBuiltinTemplatesMatchGoData. The provider shapes are seeds -
// contributed templates in <configdir>/lua/templates, enabled by name
// in [setup] templates, override them.
var Templates = []Template{
	{
		Name:  "gmail",
		Match: []string{"INBOX", "[Gmail]"},
		NoFcc: true, // gmail stores sent copies server-side
		Folders: map[string][]string{
			"inbox":   {"INBOX"},
			"draft":   {"[Gmail]/Drafts", "Drafts"},
			"sent":    {"[Gmail]/Sent Mail", "Sent"},
			"spam":    {"[Gmail]/Spam", "Spam", "Junk"},
			"deleted": {"[Gmail]/Trash", "Trash"},
			"archive": {"Archives", "Archive"},
			"pending": {"Pending"},
		},
	},
	{
		Name:  "exchange",
		Match: []string{"INBOX", "Sent Items"},
		Folders: map[string][]string{
			"inbox":   {"INBOX"},
			"sent":    {"Sent Items"},
			"deleted": {"Trash", "Deleted Items"},
			"draft":   {"Drafts"},
			"archive": {"Archives", "Archive"},
			"spam":    {"Spam", "Junk Email", "Junk"},
			"pending": {"Pending"},
		},
	},
	{
		Name:  "icloud",
		Match: []string{"INBOX", "Sent Messages"},
		Folders: map[string][]string{
			"inbox":   {"INBOX"},
			"sent":    {"Sent Messages"},
			"deleted": {"Trash"},
			"draft":   {"Drafts"},
			"archive": {"Archives", "Archive"},
			"spam":    {"Spam", "Junk"},
			"pending": {"Pending"},
		},
	},
	{
		// zoho discriminates by its Snoozed system folder; without it
		// a flat zoho account falls to outlook (same shape, but no
		// no_fcc - set the flag by hand). zoho stores sent copies
		// server-side like gmail.
		Name:  "zoho",
		Match: []string{"INBOX", "Sent", "Snoozed"},
		NoFcc: true,
		Folders: map[string][]string{
			"inbox":   {"INBOX"},
			"sent":    {"Sent"},
			"deleted": {"Trash"},
			"draft":   {"Drafts"},
			"spam":    {"Spam", "Junk"},
			"archive": {"Archive", "Archives"},
			"pending": {"Pending"},
		},
	},
	{
		Name:  "outlook",
		Match: []string{"INBOX", "Sent"},
		Folders: map[string][]string{
			"inbox":   {"INBOX"},
			"sent":    {"Sent"},
			"deleted": {"Trash", "Deleted Items"},
			"draft":   {"Drafts"},
			"archive": {"Archives", "Archive"},
			"spam":    {"Spam", "Junk"},
			"pending": {"Pending"},
		},
	},
}

// Account is one detected top-level directory: its template (empty
// when no template matched), the resolved hard-tag folder map, and
// the template's no_fcc flag.
type Account struct {
	Name     string
	Template string
	Folders  map[string]string
	NoFcc    bool
}

// Detect walks the notmuch mail root: every top-level directory with
// at least one folder is an account candidate; a candidate whose
// top-level folders resolve a template's Match names matches that
// template (first match wins). Directory names only - never mail
// content. Empty and unreadable directories are not accounts (a
// stray maildir under construction is not a detection result).
// Accounts sort by name.
func Detect(root string, templates []Template) ([]Account, error) {
	top, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("detect: read %s: %w", root, err)
	}
	var out []Account
	for _, e := range top {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		set, err := folders(filepath.Join(root, e.Name()))
		if err != nil || len(set) == 0 {
			continue
		}
		a := Account{Name: e.Name()}
		for i := range templates {
			if f, ok := resolve(&templates[i], set); ok {
				a.Template, a.Folders = templates[i].Name, f
				a.NoFcc = templates[i].NoFcc
				break
			}
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// folders is the account's folder set: directory paths relative to
// the account root, at most two levels deep (the deepest pattern is
// "[Gmail]/Drafts"). Maildir internals (cur/new/tmp) and dot
// directories are not folders.
func folders(root string) (map[string]bool, error) {
	set := map[string]bool{}
	top, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	for _, e := range top {
		if !e.IsDir() || maildirInternal(e.Name()) || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		set[e.Name()] = true
		sub, err := os.ReadDir(filepath.Join(root, e.Name()))
		if err != nil {
			continue
		}
		for _, s := range sub {
			if !s.IsDir() || maildirInternal(s.Name()) || strings.HasPrefix(s.Name(), ".") {
				continue
			}
			set[e.Name()+"/"+s.Name()] = true
		}
	}
	return set, nil
}

func maildirInternal(name string) bool {
	return name == "cur" || name == "new" || name == "tmp"
}

// resolve matches a template against the folder set: every Match name
// must be a TOP-LEVEL folder (the match never looks below the account
// root - a [Gmail] dir at the top level is the gmail discriminator).
// The extracted folder map resolves each tag to its first present
// candidate anywhere in the set (nested paths included - the mover
// needs the real "[Gmail]/Drafts").
func resolve(t *Template, set map[string]bool) (map[string]string, bool) {
	for _, name := range t.Match {
		if !set[name] {
			return nil, false
		}
	}
	folders := map[string]string{}
	for tag, cands := range t.Folders {
		for _, c := range cands {
			if set[c] {
				folders[tag] = c
				break
			}
		}
	}
	return folders, true
}

// Generate renders the detected accounts as TOML: one [accounts.<name>]
// entry per matched account with its hard-tag folder map. The file is
// detection output - from/signature are not detectable
// and stay out. Folders quote through strconv (valid TOML basic
// strings), accounts and tags sort for a stable file.
func Generate(accounts []Account) string {
	var b strings.Builder
	b.WriteString("# generated by notmutt setup - detection output.\n")
	b.WriteString("# The [accounts.<name>.folders] table is the hard-tag folder map\n")
	b.WriteString("# (R2 tag-groups): each tag's mail lives in that folder under the\n")
	b.WriteString("# account. Only structure is detected - from/signature\n")
	b.WriteString("# are yours to fill in. Edit freely, re-run to regenerate.\n")
	for _, a := range accounts {
		if a.Template == "" {
			continue
		}
		b.WriteString("\n[accounts." + a.Name + "]\n")
		b.WriteString("folder = " + strconv.Quote(a.Name) + "\n")
		if a.NoFcc {
			b.WriteString("# the provider stores sent copies server-side; the client writes no fcc copy\n")
			b.WriteString("no_fcc = true\n")
		}
		b.WriteString("\n[accounts." + a.Name + ".folders]\n")
		for _, tag := range sortedKeys(a.Folders) {
			b.WriteString(tag + " = " + strconv.Quote(a.Folders[tag]) + "\n")
		}
	}
	return b.String()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
