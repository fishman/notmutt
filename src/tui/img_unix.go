// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package tui

import (
	"os"

	"golang.org/x/sys/unix"
)

// probeCellSize reads the pty's pixel dimensions from the TIOCGWINSZ
// ioctl - the kernel reports the real window pixels (foot and tmux
// 3.7 propagate them into the pty), so no terminal query is involved
// and tmux can never fabricate a reply. Pixels of 0 (ssh, old tmux)
// keep the 10x20 defaults. Runs at startup and on every resize.
func probeCellSize() {
	f, err := os.OpenFile("/dev/tty", os.O_RDONLY, 0)
	if err != nil {
		return
	}
	defer f.Close()
	ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return
	}
	setCellSize(int(ws.Col), int(ws.Row), int(ws.Xpixel), int(ws.Ypixel))
}
