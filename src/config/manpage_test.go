// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

// TestManPageCoversSchema keeps notmutt-config(5) honest: the page is
// hand-written (docs/man/notmutt-config.5.md, converted by `make man`),
// so nothing but this check stops a new config key from shipping
// undocumented. It fails with the missing key's dotted path.
func TestManPageCoversSchema(t *testing.T) {
	doc, err := os.ReadFile("../../docs/man/notmutt-config.5.md")
	if err != nil {
		t.Fatalf("read the man page source: %v", err)
	}
	documented := documentedPaths(string(doc))
	missing := 0
	for path := range schemaPaths() {
		if !documented[normalizePath(path)] {
			t.Errorf("undocumented config key: %s", path)
			missing++
		}
	}
	if missing > 0 {
		t.Fatalf("%d key(s) missing from docs/man/notmutt-config.5.md", missing)
	}
}

var (
	headingRe = regexp.MustCompile(`^#{1,2}\s*\[([^\]]+)\]`)
	entryRe   = regexp.MustCompile(`\*\*([^*]+)\*\*`)
)

// documentedPaths parses the page's shape: a `## [table]` heading sets
// the table, and every bold run on a line is an entry under it (one
// entry line can name several keys).
func documentedPaths(doc string) map[string]bool {
	out := map[string]bool{}
	table := ""
	for _, line := range strings.Split(doc, "\n") {
		if m := headingRe.FindStringSubmatch(line); m != nil {
			table = m[1]
			out[normalizePath(table)] = true
			continue
		}
		if strings.HasPrefix(line, "# ") {
			table = "" // a prose section (TOP LEVEL and friends)
			continue
		}
		entry := strings.TrimSpace(line)
		if !strings.HasPrefix(entry, "**") {
			continue
		}
		for _, m := range entryRe.FindAllStringSubmatch(entry, -1) {
			name := strings.TrimSpace(m[1])
			if name == "" {
				continue
			}
			path := name
			if table != "" {
				path = table + "." + name
			}
			out[normalizePath(path)] = true
		}
	}
	return out
}

// schemaPaths walks the default config the way the page is laid out:
// every toml-tagged field, tables by dotted path, map keys as the
// placeholder the page uses (accounts.<name>). Slice-of-struct elements
// share their table's path (filter.header-rules.query).
func schemaPaths() map[string]bool {
	out := map[string]bool{}
	var walk func(path string, t reflect.Type, seen map[reflect.Type]bool)
	walk = func(path string, t reflect.Type, seen map[reflect.Type]bool) {
		if t.Kind() != reflect.Struct || seen[t] {
			return
		}
		seen[t] = true
		defer delete(seen, t)
		for i := range t.NumField() {
			f := t.Field(i)
			name, ok := tomlFieldName(f)
			if !ok {
				continue
			}
			at := joinPath(path, name)
			switch f.Type.Kind() {
			case reflect.Struct:
				if isOpaque(f.Type) {
					out[at] = true
					continue
				}
				walk(at, f.Type, seen)
			case reflect.Map:
				elem := f.Type.Elem()
				if elem.Kind() == reflect.Struct && !isOpaque(elem) {
					walk(joinPath(at, "<key>"), elem, seen)
					continue
				}
				out[at] = true
			case reflect.Slice, reflect.Array:
				if f.Type.Elem().Kind() == reflect.Struct && !isOpaque(f.Type.Elem()) {
					walk(at, f.Type.Elem(), seen)
					continue
				}
				out[at] = true
			case reflect.Ptr:
				if f.Type.Elem().Kind() == reflect.Struct {
					walk(at, f.Type.Elem(), seen)
					continue
				}
				out[at] = true
			default:
				out[at] = true
			}
		}
	}
	walk("", reflect.TypeOf(Default()), map[reflect.Type]bool{})
	return out
}

// isOpaque marks the types whose TOML shape is a custom unmarshaler's:
// the page documents their shape in prose, not key by key.
func isOpaque(t reflect.Type) bool {
	switch t.String() {
	case "config.Binding", "core.TagGroup":
		return true
	}
	return false
}

func tomlFieldName(f reflect.StructField) (string, bool) {
	tag := f.Tag.Get("toml")
	if tag == "" {
		return "", false
	}
	name, _, _ := strings.Cut(tag, ",")
	if name == "" || name == "-" {
		return "", false
	}
	return name, true
}

func joinPath(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// normalizePath drops the chosen-key placeholders: the schema walk says
// <key> where the page spells out <name>.
func normalizePath(p string) string {
	for {
		i := strings.IndexByte(p, '<')
		if i < 0 {
			return p
		}
		j := strings.IndexByte(p[i:], '>')
		if j < 0 {
			return p
		}
		p = p[:i] + p[i+j+1:]
	}
}
