// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"notmutt/config"
	"notmutt/core"
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
	got := expandNotifyTokens([]string{"sh", "-c", "echo {count}: {subjects}"}, 2, []core.NotifyHeadline{
		{Sender: "Ann", Subject: "one", Timestamp: time.Now().Unix()},
		{Sender: "Bob", Subject: "two", Timestamp: time.Now().Unix()},
	})
	if !strings.Contains(got[2], "2:") || !strings.Contains(got[2], "Ann") || !strings.Contains(got[2], "one") || !strings.Contains(got[2], "two") {
		t.Fatalf("tokens: %q", got[2])
	}
	got = expandNotifyTokens([]string{"touch", "x{other}"}, 1, nil)
	if got[1] != "x{other}" {
		t.Fatalf("unknown token rewritten: %q", got[1])
	}
}

func TestNotifyHeadlines(t *testing.T) {
	cfg := config.Default()
	cfg.Notify.Max = 2
	rep := &filter.Report{Entries: []filter.Entry{
		{Subject: "a", Sender: "Zed"},
		{Subject: "b", Sender: "Ann", Timestamp: 1000, Priority: true},
		{Subject: "", Priority: true},
		{Subject: "c", Sender: "Bob", Timestamp: 2000, Priority: true},
		{Subject: "d", Sender: "Cid"},
	}}
	got := notifyHeadlines(cfg, rep)
	if len(got) != 2 || got[0].Sender != "Ann" || got[1].Subject != "c" {
		t.Fatalf("headlines: %+v", got)
	}
	// no priority entries: the batch fills the cap, the count never ships alone
	rep = &filter.Report{Entries: []filter.Entry{{Subject: "x", Sender: "Dana"}, {Subject: "y", Sender: "Eli"}}}
	if got := notifyHeadlines(cfg, rep); len(got) != 2 || got[1].Sender != "Eli" {
		t.Fatalf("fallback fill: %+v", got)
	}
	cfg.Notify.Max = 0
	if got := notifyHeadlines(cfg, rep); len(got) != 0 {
		t.Fatalf("max=0: %v", got)
	}
}

// TestNotifyTitleAndRows: the title is the deduped sender list
// ellipsized (never a static app name), the rows the aligned
// sender/subject/time 3-part table.
func TestNotifyTitleAndRows(t *testing.T) {
	head := []core.NotifyHeadline{
		{Sender: "Ann", Subject: "hello"},
		{Sender: "Ann", Subject: "re: hello"}, // deduped in the title
		{Sender: "Bob", Subject: "plans"},
		{Sender: "Carol", Subject: "cfp"},
		{Sender: "Dana", Subject: "receipt"},
	}
	if got := notifyTitle(head); got != "Ann, Bob, Carol ..." {
		t.Fatalf("title = %q", got)
	}
	if got := notifyTitle([]core.NotifyHeadline{{Subject: "x"}}); got != "new mail" {
		t.Fatalf("empty-sender title = %q", got)
	}
	rows := notifyRows(head[:2])
	if !strings.Contains(rows, "Ann") || !strings.Contains(rows, "re: hello") {
		t.Fatalf("rows must carry sender and subject:\n%s", rows)
	}
	long := notifyRows([]core.NotifyHeadline{{Sender: strings.Repeat("a", 40), Subject: strings.Repeat("b", 60)}})
	if strings.Contains(long, strings.Repeat("a", 20)) || strings.Contains(long, strings.Repeat("b", 40)) {
		t.Fatalf("rows must truncate to the columns:\n%s", long)
	}
}
