// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"fmt"
	"maps"
	"strings"
	"sync"

	"notmutt/core"
)

// Store is the single write path for runtime config mutations. Setters
// validate, mutate, and notify section observers, which publish
// ConfigChanged on the bus (wired in app).
type Store struct {
	mu   sync.Mutex
	cfg  Config
	subs map[string][]func()
}

func NewStore(cfg Config) *Store {
	return &Store{cfg: cfg, subs: map[string][]func(){}}
}

func (s *Store) Config() Config {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.cfg
	c.Views = maps.Clone(s.cfg.Views)
	c.TagGroups = make(map[string]core.TagGroup, len(s.cfg.TagGroups))
	for n, g := range s.cfg.TagGroups {
		g.Tags = append([]string(nil), g.Tags...)
		c.TagGroups[n] = g
	}
	c.Bindings = maps.Clone(s.cfg.Bindings)
	for ctx, km := range c.Bindings {
		c.Bindings[ctx] = maps.Clone(km)
	}
	c.TagActions = maps.Clone(s.cfg.TagActions)
	c.Descriptions = maps.Clone(s.cfg.Descriptions)
	c.Palette.Base = maps.Clone(s.cfg.Palette.Base)
	c.Palette.Variants = make(map[string]map[string]string, len(s.cfg.Palette.Variants))
	for v, m := range s.cfg.Palette.Variants {
		c.Palette.Variants[v] = maps.Clone(m)
	}
	c.Theme.Default = s.cfg.Theme.Default
	c.Theme.Variants = make(map[string]StyleTable, len(s.cfg.Theme.Variants))
	for v, table := range s.cfg.Theme.Variants {
		c.Theme.Variants[v] = cloneStyleTable(table)
	}
	return c
}

// cloneStyleTable deep-copies one theme variant: every style's Attrs
// slice gets its own backing array and the tag map is cloned (the same
// discipline as TagGroups above - a Config() caller mutating the copy
// must never touch the store).
func cloneStyleTable(t StyleTable) StyleTable {
	for _, s := range []*Style{
		&t.Normal, &t.Indicator, &t.Status, &t.Progress, &t.Error,
		&t.Compose.Label,
		&t.Index.Number, &t.Index.Date, &t.Index.Author, &t.Index.Subject,
		&t.Index.Flags, &t.Index.Staged, &t.Index.Ghost, &t.Index.Tag.Default,
		&t.Pager.Header, &t.Pager.HdrDefault, &t.Pager.Signature, &t.Pager.Attachment,
		&t.Pager.Recent, &t.Pager.OtherSide,
	} {
		if s.Attrs != nil {
			s.Attrs = append([]string(nil), s.Attrs...)
		}
	}
	for i := range t.Pager.Quoted {
		if t.Pager.Quoted[i].Attrs != nil {
			t.Pager.Quoted[i].Attrs = append([]string(nil), t.Pager.Quoted[i].Attrs...)
		}
	}
	t.Index.Tag.Tags = maps.Clone(t.Index.Tag.Tags)
	for n, s := range t.Index.Tag.Tags {
		if s.Attrs != nil {
			s.Attrs = append([]string(nil), s.Attrs...)
		}
		t.Index.Tag.Tags[n] = s
	}
	return t
}

func (s *Store) Subscribe(section string, fn func()) {
	s.mu.Lock()
	s.subs[section] = append(s.subs[section], fn)
	s.mu.Unlock()
}

func (s *Store) SetKeymap(k string) error {
	if _, ok := baseConfig.Schemes[k]; !ok {
		return fmt.Errorf("keymap: must be vim or emacs, got %q", k)
	}
	s.mu.Lock()
	s.cfg.UI.Keymap = k
	s.cfg.Bindings, s.cfg.Shown = bindingsFromScheme(s.cfg.Schemes[k])
	s.cfg.Descriptions = deriveDescriptions(s.cfg.Schemes, k)
	s.mu.Unlock()
	s.notify("ui")
	return nil
}

func (s *Store) SetThemeVariant(name string) error {
	s.mu.Lock()
	if _, ok := s.cfg.Theme.Variants[name]; !ok {
		s.mu.Unlock()
		return fmt.Errorf("theme: no variant %q", name)
	}
	s.cfg.Theme.Default = name
	s.mu.Unlock()
	s.notify("theme")
	return nil
}

// SetActiveView moves the active-view pointer (the view switch's
// single write path, R8): the refresher re-reads the entry and
// full-reloads the new query. The views themselves are config data -
// this only selects among them.
func (s *Store) SetActiveView(name string) error {
	s.mu.Lock()
	if _, ok := s.cfg.Views[name]; !ok {
		s.mu.Unlock()
		return fmt.Errorf("view %q: no such view", name)
	}
	s.cfg.ActiveView = name
	s.mu.Unlock()
	s.notify("view")
	return nil
}

func (s *Store) SetViewQuery(name, q string) error {
	if strings.TrimSpace(q) == "" {
		return fmt.Errorf("view %q: query must not be empty", name)
	}
	s.mu.Lock()
	v, ok := s.cfg.Views[name]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("view %q: no such view", name)
	}
	v.Query = q
	s.cfg.Views[name] = v
	s.mu.Unlock()
	s.notify("view")
	return nil
}

func (s *Store) notify(section string) {
	s.mu.Lock()
	fns := append([]func(){}, s.subs[section]...)
	s.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}
