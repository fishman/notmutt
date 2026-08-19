// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"

	"notmutt/config"
	"notmutt/core"
)

// resolveNotifyBackend picks the effective backend: explicit config
// wins; "" (the default) auto-detects - the platform backend when the
// session can show notifications, the argv command otherwise.
func resolveNotifyBackend(cfg config.Config, probe func() bool) string {
	if cfg.Notify.Backend != "" {
		return cfg.Notify.Backend
	}
	if probe() {
		return "beeep"
	}
	return "command"
}

// notifyDaemonReachable probes for a notification daemon: darwin
// always (beeep uses osascript, part of the OS), linux on the session
// bus (org.freedesktop.Notifications answers GetServerInformation),
// elsewhere never - the command backend takes over. beeep keeps its
// own dbus -> notify-send -> kdialog fallback per show either way.
func notifyDaemonReachable() bool {
	if runtime.GOOS == "darwin" {
		return true
	}
	if runtime.GOOS != "linux" {
		return false
	}
	conn, err := dbus.SessionBus()
	if err != nil {
		return false
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	call := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications").
		CallWithContext(ctx, "org.freedesktop.Notifications.GetServerInformation", 0)
	return call.Err == nil
}

// notifyNewMail runs the [notify] side effect for one processed batch
// (the filter job's completion event, R2): the argv command backend
// or the platform backend ("beeep"), the backend resolved once at
// startup. The payload is the count plus the priority headlines -
// sender, subject, timestamp - never bodies or ids (F6). The caller
// spawns the goroutine; no entries is a no-op.
func notifyNewMail(cfg config.Config, backend string, entries int, head []core.NotifyHeadline) {
	if entries <= 0 {
		return
	}
	switch backend {
	case "beeep":
		notifyBeeep(entries, head)
	default:
		argv := expandNotifyTokens(cfg.Notify.Command, entries, head)
		if len(argv) == 0 {
			return
		}
		if err := exec.Command(argv[0], argv[1:]...).Run(); err != nil {
			diag.Warn("notify", "err", err.Error())
		}
	}
}

// expandNotifyTokens replaces {count} (the entry count) and {subjects}
// (the priority headlines as aligned sender/subject/time rows) in the
// argv; absent tokens are left alone - a command that does not want
// them keeps working.
func expandNotifyTokens(argv []string, entries int, head []core.NotifyHeadline) []string {
	out := make([]string, len(argv))
	n := strconv.Itoa(entries)
	s := notifyRows(head)
	for i, a := range argv {
		out[i] = strings.ReplaceAll(strings.ReplaceAll(a, "{count}", n), "{subjects}", s)
	}
	return out
}

// notifyTitle is the notification title: the deduped sender list,
// ellipsized - the senders, never a static app name (the reference
// script's fixed title was the first thing to go).
func notifyTitle(head []core.NotifyHeadline) string {
	var names []string
	seen := map[string]bool{}
	for _, h := range head {
		s := strings.TrimSpace(h.Sender)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		names = append(names, s)
	}
	switch {
	case len(names) == 0:
		return "new mail"
	case len(names) > 3:
		return strings.Join(names[:3], ", ") + " ..."
	default:
		return strings.Join(names, ", ")
	}
}

// notifyRows renders the headline list as aligned 3-part rows: sender,
// subject, time - the reference script's bare subject list with the
// parts it dropped restored. The columns truncate by rune: the body
// renders in a system popup, not a terminal, so cell widths do not
// apply; a long line cannot break the alignment either way.
func notifyRows(head []core.NotifyHeadline) string {
	if len(head) == 0 {
		return ""
	}
	var b strings.Builder
	for _, h := range head {
		b.WriteString(truncRunes(h.Sender, 16) + "  ")
		b.WriteString(truncRunes(h.Subject, 30) + "  ")
		b.WriteString(notifyTime(h.Timestamp) + "\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// notifyTime formats the headline timestamp: clock time for today,
// date and clock for anything older.
func notifyTime(ts int64) string {
	t := time.Unix(ts, 0)
	if t.Year() == time.Now().Year() && t.YearDay() == time.Now().YearDay() {
		return t.Format("15:04")
	}
	return t.Format("Jan 2 15:04")
}

// truncRunes truncates s to n runes; truncation never mangles UTF-8.
func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
