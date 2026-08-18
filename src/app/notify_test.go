// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"os"
	"path/filepath"
	"testing"

	"notmutt/config"
	"notmutt/filter"
)

func TestNotifyNewMail(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Default()
	cfg.Notify.Command = []string{"touch", filepath.Join(dir, "m{count}x")}
	notifyNewMail(cfg, "command", 3, nil)
	if _, err := os.Stat(filepath.Join(dir, "m3x")); err != nil {
		t.Fatalf("notify argv: %v", err)
	}
	cfg.Notify.Command = nil
	notifyNewMail(cfg, "command", 3, nil) // disabled: no-op
	cfg.Notify.Command = []string{"touch", filepath.Join(dir, "n{count}")}
	notifyNewMail(cfg, "command", 0, nil) // no entries: no-op
	if _, err := os.Stat(filepath.Join(dir, "n0")); err == nil {
		t.Fatalf("entries=0 still ran the command")
	}
}

func TestResolveNotifyBackend(t *testing.T) {
	cfg := config.Default()
	if got := resolveNotifyBackend(cfg, func() bool { return true }); got != "beeep" {
		t.Fatalf("auto with daemon = %q, want beeep", got)
	}
	if got := resolveNotifyBackend(cfg, func() bool { return false }); got != "command" {
		t.Fatalf("auto without daemon = %q, want command", got)
	}
	cfg.Notify.Backend = "beeep"
	if got := resolveNotifyBackend(cfg, func() bool { return false }); got != "beeep" {
		t.Fatalf("explicit beeep overridden = %q", got)
	}
	cfg.Notify.Backend = "command"
	if got := resolveNotifyBackend(cfg, func() bool { return true }); got != "command" {
		t.Fatalf("explicit command overridden = %q", got)
	}
}

func TestExpandNotifyTokens(t *testing.T) {
	got := expandNotifyTokens([]string{"sh", "-c", "echo {count}: {subjects}"}, 2, []string{"one", "two"})
	if got[2] != "echo 2: one\ntwo" {
		t.Fatalf("tokens: %q", got[2])
	}
	got = expandNotifyTokens([]string{"touch", "x{other}"}, 1, nil)
	if got[1] != "x{other}" {
		t.Fatalf("unknown token rewritten: %q", got[1])
	}
}

func TestPrioritySubjects(t *testing.T) {
	cfg := config.Default()
	cfg.Notify.Max = 2
	rep := &filter.Report{Entries: []filter.Entry{
		{Subject: "a", Priority: false},
		{Subject: "b", Priority: true},
		{Subject: "", Priority: true},
		{Subject: "c", Priority: true},
		{Subject: "d", Priority: true},
	}}
	got := prioritySubjects(cfg, rep)
	if len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Fatalf("subjects: %v", got)
	}
	cfg.Notify.Max = 0
	if got := prioritySubjects(cfg, rep); len(got) != 0 {
		t.Fatalf("max=0: %v", got)
	}
}
