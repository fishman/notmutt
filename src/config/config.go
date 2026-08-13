package config

import (
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
)

type Config struct {
	UI    UI              `toml:"ui"`
	Views map[string]View `toml:"view"`
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
	}
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
	return nil
}
