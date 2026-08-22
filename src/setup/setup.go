// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

// Package setup detects accounts on disk by folder structure and
// generates the accounts config (`notmutt setup`): template-driven,
// an account matches when every required tag resolves to an existing
// folder. Only directory names are read - never mail content.
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
// folders that gate the template (never below the root); Folders
// maps each hard tag to candidate paths in priority order (the afew
// folder_priorities shape) - first present wins, nested paths like
// "[Gmail]/Drafts" give the mover its real path, absent tags stay
// out. NoFcc marks providers that store sent copies server-side
// (gmail): the account gets no_fcc = true, no fcc copy is written.
type Template struct {
	Name    string
	Match   []string
	Folders map[string][]string
	NoFcc   bool
}

// Templates is the built-in detection set, tried in order: first
// template whose top-level Match names all resolve wins. The gmail
// discriminator is the top-level [Gmail] folder (mbsync syncs it as
// one dir; its presence marks gmail system folders even when
// subfolders were never synced); flat layouts without it fall to
// generic shapes. Candidates are priority-ordered: standard name
// first, flat fallback after system folders. The lua build evaluates
// the same templates from app/lua/templates/*.lua - this Go set is
// the no-Lua fallback, pinned equal by TestBuiltinTemplatesMatchGoData.
// Contributed templates in <configdir>/lua/templates, enabled by name
// in [setup] templates, override these seeds.
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
		// zoho discriminates by its Snoozed folder; without it a flat
		// account falls to outlook (no no_fcc - set by hand). Stores
		// sent copies server-side like gmail.
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

// Detect walks the notmuch mail root: top-level directories with at
// least one folder are candidates; the first template whose Match
// resolves wins. Only directory names are read - never mail content;
// empty/unreadable dirs are not accounts; results sort by name.
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

// folders is the account's folder set: paths relative to the account
// root, at most two levels deep; Maildir internals (cur/new/tmp) and
// dot dirs are excluded.
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
// must be a TOP-LEVEL folder (the [Gmail] discriminator); each tag
// resolves to its first present candidate anywhere, nested included -
// the mover needs the real "[Gmail]/Drafts".
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

// Generate renders detected accounts as TOML: one [accounts.<name>]
// entry per matched account with its hard-tag folder map;
// from/signature are not detectable and stay out. Folders quote via
// strconv (valid TOML); accounts and tags sort for a stable file.
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
