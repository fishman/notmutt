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
	c.Palette.Base = maps.Clone(s.cfg.Palette.Base)
	c.Palette.Variants = make(map[string]map[string]string, len(s.cfg.Palette.Variants))
	for v, m := range s.cfg.Palette.Variants {
		c.Palette.Variants[v] = maps.Clone(m)
	}
	c.Theme.Default = s.cfg.Theme.Default
	c.Theme.Variants = make(map[string]StyleTable, len(s.cfg.Theme.Variants))
	for v, table := range s.cfg.Theme.Variants {
		table.Index.Tag.Tags = maps.Clone(table.Index.Tag.Tags)
		c.Theme.Variants[v] = table
	}
	return c
}

func (s *Store) Subscribe(section string, fn func()) {
	s.mu.Lock()
	s.subs[section] = append(s.subs[section], fn)
	s.mu.Unlock()
}

func (s *Store) SetKeymap(k string) error {
	if k != "vim" && k != "emacs" {
		return fmt.Errorf("keymap: must be vim or emacs, got %q", k)
	}
	s.mu.Lock()
	s.cfg.UI.Keymap = k
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
