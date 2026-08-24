// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package netcheck

import (
	"context"
	"os"
	"strings"

	"github.com/godbus/dbus/v5"
)

// online consults NetworkManager's connectivity state over D-Bus
// (0 none, 1 portal, 2 limited, 3 full): none is the definite
// offline signal, any other state means a path exists and the
// transport decides. Without NetworkManager (systemd-networkd,
// netplan, a bare wpa_supplicant setup) the D-Bus call fails and the
// default route is the fallback.
func online(ctx context.Context) bool {
	conn, err := dbus.SystemBus()
	if err == nil {
		defer conn.Close()
		obj := conn.Object("org.freedesktop.NetworkManager", "/org/freedesktop/NetworkManager")
		var state uint32
		if err := obj.CallWithContext(ctx, "org.freedesktop.NetworkManager.GetConnectivity", 0).Store(&state); err == nil {
			return state != 0
		}
	}
	return hasDefaultRoute()
}

// hasDefaultRoute reports a default IPv4 route (Linux): a 00000000
// destination line in /proc/net/route.
func hasDefaultRoute() bool {
	data, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n")[1:] {
		if f := strings.Fields(line); len(f) > 1 && f[1] == "00000000" {
			return true
		}
	}
	return false
}
