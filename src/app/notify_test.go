package app

import (
	"os"
	"path/filepath"
	"testing"

	"notmutt/config"
)

func TestNotifyNewMail(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Notify.Command = []string{"touch", filepath.Join(dir, "m{count}x")}
	notifyNewMail(cfg, 3)
	if _, err := os.Stat(filepath.Join(dir, "m3x")); err != nil {
		t.Fatalf("notify argv: %v", err)
	}
	cfg.Notify.Command = nil
	notifyNewMail(cfg, 3) // disabled: no-op
	cfg.Notify.Command = []string{"touch", filepath.Join(dir, "n{count}")}
	notifyNewMail(cfg, 0) // no entries: no-op
	if _, err := os.Stat(filepath.Join(dir, "n0")); err == nil {
		t.Fatalf("entries=0 still ran the command")
	}
}
