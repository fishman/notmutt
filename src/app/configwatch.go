package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"notmutt/config"
	"notmutt/core"
)

var configWatchInterval = time.Second

// watchConfig reloads a complete TOML file set after an atomic-save, rename,
// create, or deletion. Invalid edits leave the live store unchanged.
func watchConfig(ctx context.Context, dir string, store *config.Store, bus *core.Bus) {
	last := configFingerprint(dir)
	tick := time.NewTicker(configWatchInterval)
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			next := configFingerprint(dir)
			if next == last {
				continue
			}
			cfg, err := config.Load(dir)
			if err != nil {
				bus.Publish(core.JobError{Job: "config", Err: err})
				continue
			}
			last = next
			store.Replace(cfg)
		}
	}
}

func configFingerprint(dir string) string {
	files, err := filepath.Glob(filepath.Join(dir, "*.toml"))
	if err != nil {
		return ""
	}
	sort.Strings(files)
	var b strings.Builder
	for _, path := range files {
		info, err := os.Stat(path)
		if err == nil {
			fmt.Fprintf(&b, "%s\x00%d\x00%d\n", filepath.Base(path), info.Size(), info.ModTime().UnixNano())
		}
	}
	return b.String()
}
