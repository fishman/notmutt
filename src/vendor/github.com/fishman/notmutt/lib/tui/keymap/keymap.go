// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package keymap

import (
	"fmt"
	"slices"
	"strings"
)

// Binding is one Notmutt-style TOML key entry.
type Binding struct {
	Fun, Desc     string
	Show, Inherit bool
}

func (b *Binding) UnmarshalTOML(value any) error {
	*b = Binding{}
	switch item := value.(type) {
	case string:
		b.Fun = item
	case []any:
		if len(item) != 2 {
			return fmt.Errorf("binding: expected [fun, desc]")
		}
		fun, funOK := item[0].(string)
		desc, descOK := item[1].(string)
		if !funOK || !descOK {
			return fmt.Errorf("binding: fun and desc must be strings")
		}
		b.Fun, b.Desc = fun, desc
	case map[string]any:
		fun, ok := item["fun"].(string)
		if !ok {
			return fmt.Errorf("binding: fun must be a string")
		}
		b.Fun = fun
		for key, value := range item {
			switch key {
			case "fun":
			case "desc":
				desc, ok := value.(string)
				if !ok {
					return fmt.Errorf("binding: desc must be a string")
				}
				b.Desc = desc
			case "show":
				show, ok := value.(bool)
				if !ok {
					return fmt.Errorf("binding: show must be a boolean")
				}
				b.Show = show
			case "inherit":
				inherit, ok := value.(bool)
				if !ok {
					return fmt.Errorf("binding: inherit must be a boolean")
				}
				b.Inherit = inherit
			default:
				return fmt.Errorf("binding: unknown key %q", key)
			}
		}
	default:
		return fmt.Errorf("binding: expected a string, [fun, desc], or table")
	}
	if strings.TrimSpace(b.Fun) == "" {
		return fmt.Errorf("binding: action is required")
	}
	return nil
}

type Entry struct {
	Key, Fun, Desc string
	Show           bool
}

// Table contains dispatch bindings and context-local hint visibility.
type Table struct {
	Bindings map[string]map[string]string
	Shown    map[string]map[string]bool
	entries  map[string][]Entry
}

// Compile applies only explicitly inherited parent keys; child keys win.
func Compile(scheme map[string]map[string]Binding, parents map[string]string) (Table, error) {
	result := Table{Bindings: make(map[string]map[string]string, len(scheme)), Shown: make(map[string]map[string]bool, len(scheme)), entries: make(map[string][]Entry, len(scheme))}
	for context, bindings := range scheme {
		if context == "" || len(bindings) == 0 {
			return Table{}, fmt.Errorf("keymap: empty context %q", context)
		}
		for key, binding := range bindings {
			if key == "" || strings.TrimSpace(binding.Fun) == "" {
				return Table{}, fmt.Errorf("keymap: invalid binding in %q", context)
			}
		}
	}
	resolved := make(map[string]map[string]Binding, len(scheme))
	visiting := make(map[string]bool, len(scheme))
	var resolve func(string) (map[string]Binding, error)
	resolve = func(context string) (map[string]Binding, error) {
		if bindings, ok := resolved[context]; ok {
			return bindings, nil
		}
		if visiting[context] {
			return nil, fmt.Errorf("keymap: context inheritance cycle at %q", context)
		}
		own, ok := scheme[context]
		if !ok {
			return nil, fmt.Errorf("keymap: unknown context %q", context)
		}
		visiting[context] = true
		bindings := make(map[string]Binding, len(own))
		if parent := parents[context]; parent != "" {
			inherited, err := resolve(parent)
			if err != nil {
				return nil, err
			}
			for key, binding := range inherited {
				if binding.Inherit {
					bindings[key] = binding
				}
			}
		}
		for key, binding := range own {
			bindings[key] = binding
		}
		visiting[context] = false
		resolved[context] = bindings
		return bindings, nil
	}
	for context := range scheme {
		bindings, err := resolve(context)
		if err != nil {
			return Table{}, err
		}
		keys := make([]string, 0, len(bindings))
		for key := range bindings {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		result.Bindings[context] = make(map[string]string, len(keys))
		result.Shown[context] = make(map[string]bool)
		for _, key := range keys {
			binding := bindings[key]
			result.Bindings[context][key] = binding.Fun
			shown := scheme[context][key].Show
			result.entries[context] = append(result.entries[context], Entry{Key: key, Fun: binding.Fun, Desc: binding.Desc, Show: shown})
			if shown {
				result.Shown[context][key] = true
			}
		}
	}
	return result, nil
}

func (t Table) Action(context, key string) (string, bool) {
	action, ok := t.Bindings[context][key]
	return action, ok
}
func (t Table) Entries(context string) []Entry { return slices.Clone(t.entries[context]) }
func (t Table) Hints(context string) []Entry {
	var entries []Entry
	for _, entry := range t.entries[context] {
		if t.Shown[context][entry.Key] {
			entries = append(entries, entry)
		}
	}
	return entries
}
func (t Table) KeyFor(context, action string) string {
	for _, entry := range t.entries[context] {
		if entry.Fun == action {
			return entry.Key
		}
	}
	return ""
}

// Resolve preserves case for typed runes and falls back to named keys.
func Resolve(bindings map[string]string, typed, canonical string) string {
	if typed != "" {
		if action, ok := bindings[typed]; ok {
			return action
		}
	}
	return bindings[canonical]
}
