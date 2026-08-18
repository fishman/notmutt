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
// startup. The payload is the count plus the priority subjects,
// never bodies or ids (F6). The caller spawns the goroutine; no
// entries is a no-op.
func notifyNewMail(cfg config.Config, backend string, entries int, subjects []string) {
	if entries <= 0 {
		return
	}
	switch backend {
	case "beeep":
		notifyBeeep(entries, subjects)
	default:
		argv := expandNotifyTokens(cfg.Notify.Command, entries, subjects)
		if len(argv) == 0 {
			return
		}
		if err := exec.Command(argv[0], argv[1:]...).Run(); err != nil {
			diag.Warn("notify", "err", err.Error())
		}
	}
}

// expandNotifyTokens replaces {count} (the entry count) and {subjects}
// (the priority subjects, one per line) in the argv; absent tokens are
// left alone - a command that does not want them keeps working.
func expandNotifyTokens(argv []string, entries int, subjects []string) []string {
	out := make([]string, len(argv))
	n := strconv.Itoa(entries)
	s := strings.Join(subjects, "\n")
	for i, a := range argv {
		out[i] = strings.ReplaceAll(strings.ReplaceAll(a, "{count}", n), "{subjects}", s)
	}
	return out
}
