package config

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"notmutt/core"
)

type Config struct {
	UI         UI                           `toml:"ui"`
	Views      map[string]View              `toml:"view"`
	TagGroups  map[string]core.TagGroup     `toml:"tag-groups"`
	Bindings   map[string]map[string]string `toml:"bindings"`
	TagActions map[string]string            `toml:"tag-actions"`
}

type UI struct {
	Keymap string `toml:"keymap"`
}

type View struct {
	Query   string `toml:"query"`
	Threads bool   `toml:"threads"`
}

func Default() Config {
	return Config{
		UI: UI{Keymap: "vim"},
		Views: map[string]View{
			"inbox": {Query: "tag:inbox", Threads: true},
		},
		TagGroups: map[string]core.TagGroup{
			"folder": {Tags: []string{"inbox", "archive", "deleted", "sent", "draft", "pending", "spam"}},
		},
		Bindings: map[string]map[string]string{
			"index": {
				"j": "cursor-down", "k": "cursor-up", "q": "quit",
				"r": "toggle-read", "a": "archive", "d": "delete",
				"u": "undo", "$": "apply",
			},
		},
		TagActions: map[string]string{
			"toggle-read": "unread",
			"archive":     "archive",
			"delete":      "deleted",
		},
	}
}

// TagGroupList returns the groups sorted by name - the deterministic
// order the resolver consumes (map iteration is not).
func (c Config) TagGroupList() []core.TagGroup {
	names := make([]string, 0, len(c.TagGroups))
	for n := range c.TagGroups {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]core.TagGroup, 0, len(names))
	for _, n := range names {
		out = append(out, c.TagGroups[n])
	}
	return out
}

// Load merges file values over defaults. Unknown keys are load errors
// naming the key (strict load, R8). A missing file means defaults.
func Load(path string) (Config, error) {
	cfg := Default()
	md, err := toml.DecodeFile(path, &cfg)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, err
	}
	if und := md.Undecoded(); len(und) > 0 {
		keys := make([]string, len(und))
		for i, k := range und {
			keys[i] = k.String()
		}
		return cfg, fmt.Errorf("%s: unknown key(s): %s", path, strings.Join(keys, ", "))
	}
	if err := validate(cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func validate(cfg Config) error {
	if cfg.UI.Keymap != "vim" && cfg.UI.Keymap != "emacs" {
		return fmt.Errorf("keymap: must be vim or emacs, got %q", cfg.UI.Keymap)
	}
	if len(cfg.Views) == 0 {
		return fmt.Errorf("at least one view required")
	}
	for name, v := range cfg.Views {
		if strings.TrimSpace(v.Query) == "" {
			return fmt.Errorf("view %q: query must not be empty", name)
		}
	}
	seen := map[string]bool{}
	for name, g := range cfg.TagGroups {
		if len(g.Tags) == 0 {
			return fmt.Errorf("tag-groups.%s: at least one tag required", name)
		}
		for _, t := range g.Tags {
			if strings.TrimSpace(t) == "" {
				return fmt.Errorf("tag-groups.%s: empty tag name", name)
			}
			if seen[t] {
				return fmt.Errorf("tag %q in multiple groups", t)
			}
			seen[t] = true
		}
	}
	for name, km := range cfg.Bindings {
		if len(km) == 0 {
			return fmt.Errorf("bindings.%s: at least one binding required", name)
		}
		for k, v := range km {
			if strings.TrimSpace(k) == "" {
				return fmt.Errorf("bindings.%s: empty key", name)
			}
			if strings.TrimSpace(v) == "" {
				return fmt.Errorf("bindings.%s: empty action for key %q", name, k)
			}
		}
	}
	for name, tag := range cfg.TagActions {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("tag-actions: empty action name")
		}
		if strings.TrimSpace(tag) == "" {
			return fmt.Errorf("tag-actions.%s: empty tag value", name)
		}
	}
	return nil
}
