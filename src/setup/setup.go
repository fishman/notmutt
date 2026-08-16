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

// Template is a provider folder shape: per hard tag, the candidate
// folder names in priority order (the afew folder_priorities shape).
// An account matches when every required tag resolves; optional tags
// are extracted when present.
type Template struct {
	Name     string
	Required map[string][]string
	Optional map[string][]string
}

// Templates is the built-in detection set, tried in order: an account
// matches the first template whose required tags all resolve. The
// gmail shape is INBOX plus the [Gmail] system folders; flat IMAP
// layouts (Drafts/Sent/Trash at the account root) fail the required
// [Gmail] names and stay unmatched. The lua build evaluates the same
// templates from app/lua/templates/*.lua (the shipped examples users
// copy from); this Go set is the no-Lua fallback, pinned equal by
// TestBuiltinTemplatesMatchGoData. The provider shapes are seeds -
// contributed templates in <configdir>/lua/templates override them by
// name.
var Templates = []Template{
	{
		Name: "gmail",
		Required: map[string][]string{
			"inbox":   {"INBOX"},
			"draft":   {"[Gmail]/Drafts"},
			"sent":    {"[Gmail]/Sent Mail"},
			"spam":    {"[Gmail]/Spam"},
			"deleted": {"[Gmail]/Trash"},
		},
		Optional: map[string][]string{
			"archive": {"Archives", "Archive"},
			"pending": {"Pending"},
		},
	},
	{
		Name: "exchange",
		Required: map[string][]string{
			"inbox":   {"INBOX"},
			"sent":    {"Sent Items"},
			"deleted": {"Deleted Items"},
		},
		Optional: map[string][]string{
			"archive": {"Archive"},
			"draft":   {"Drafts"},
			"spam":    {"Junk Email", "Junk"},
		},
	},
	{
		Name: "icloud",
		Required: map[string][]string{
			"inbox":   {"INBOX"},
			"sent":    {"Sent Messages"},
			"deleted": {"Trash"},
		},
		Optional: map[string][]string{
			"archive": {"Archive"},
			"draft":   {"Drafts"},
			"spam":    {"Junk"},
		},
	},
	{
		Name: "outlook",
		Required: map[string][]string{
			"inbox":   {"INBOX"},
			"sent":    {"Sent"},
			"deleted": {"Deleted Items"},
		},
		Optional: map[string][]string{
			"archive": {"Archive"},
			"draft":   {"Drafts"},
			"spam":    {"Junk"},
		},
	},
}

// Account is one detected top-level directory: its template (empty
// when no template matched) and the resolved hard-tag folder map.
type Account struct {
	Name     string
	Template string
	Folders  map[string]string
}

// Detect walks the notmuch mail root: every top-level directory is an
// account candidate; a candidate whose folder set resolves a
// template's required tags matches that template. Templates are tried
// in order - the first match wins. Unreadable and folder-less
// directories stay unmatched. Accounts sort by name.
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
		a := Account{Name: e.Name()}
		if set, err := folders(filepath.Join(root, e.Name())); err == nil {
			for i := range templates {
				if f, ok := resolve(&templates[i], set); ok {
					a.Template, a.Folders = templates[i].Name, f
					break
				}
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

// resolve maps each tag to its first present candidate; match is true
// when every required tag resolved.
func resolve(t *Template, set map[string]bool) (map[string]string, bool) {
	folders := map[string]string{}
	for _, tags := range [2]map[string][]string{t.Required, t.Optional} {
		for tag, cands := range tags {
			for _, c := range cands {
				if set[c] {
					folders[tag] = c
					break
				}
			}
		}
	}
	for tag := range t.Required {
		if _, ok := folders[tag]; !ok {
			return nil, false
		}
	}
	return folders, true
}

// Generate renders the detected accounts as TOML: one [accounts.<name>]
// entry per matched account with its hard-tag folder map. The file is
// detection output - from/sent_folder/signature are not detectable
// and stay out. Folders quote through strconv (valid TOML basic
// strings), accounts and tags sort for a stable file.
func Generate(accounts []Account) string {
	var b strings.Builder
	b.WriteString("# generated by notmutt setup - detection output.\n")
	b.WriteString("# The [accounts.<name>.folders] table is the hard-tag folder map\n")
	b.WriteString("# (R2 tag-groups): each tag's mail lives in that folder under the\n")
	b.WriteString("# account. Only structure is detected - from/sent_folder/signature\n")
	b.WriteString("# are yours to fill in. Edit freely, re-run to regenerate.\n")
	for _, a := range accounts {
		if a.Template == "" {
			continue
		}
		b.WriteString("\n[accounts." + a.Name + "]\n")
		b.WriteString("folder = " + strconv.Quote(a.Name) + "\n")
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
