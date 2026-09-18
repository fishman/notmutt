package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"notmutt/config"
	"notmutt/core"
)

func TestWatchConfigReloadsCompleteFileSet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[ui]\nkeymap = \"vim\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	store := config.NewStore(cfg)
	changed := make(chan struct{}, 1)
	store.Subscribe("ui", func() { changed <- struct{}{} })
	old := configWatchInterval
	configWatchInterval = time.Millisecond
	t.Cleanup(func() { configWatchInterval = old })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go watchConfig(ctx, dir, store, core.NewBus())
	time.Sleep(10 * time.Millisecond)
	if err := os.WriteFile(path, []byte("[ui]\nkeymap = \"emacs\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
		if got := store.Config().UI.Keymap; got != "emacs" {
			t.Fatalf("keymap = %q", got)
		}
	case <-time.After(time.Second):
		t.Fatal("configuration change was not reloaded")
	}
}
