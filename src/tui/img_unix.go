// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build !windows

package tui

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// probeCellSize reads the pty's pixel dimensions from the TIOCGWINSZ
// ioctl - the kernel reports the real window pixels (foot and tmux 3.7
// propagate them), so no terminal query is involved and tmux can never
// fabricate a reply. Pixels of 0 (ssh, old tmux) keep the 10x20
// defaults. Runs at startup and on every resize.
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

// Called by kittyProbeTty.Start while the terminal is raw and before the
// screen's input pump starts. A separate nonblocking fd permits a hard
// deadline without changing the screen's tty settings or leaving a reader.
func probeKittyGraphics() (bool, []byte) {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR|unix.O_NONBLOCK, 0)
	if err != nil {
		return false, nil
	}
	defer f.Close()
	return probeKittyFile(f, kittyProbeTimeout)
}

func probeKittyFile(f *os.File, timeout time.Duration) (bool, []byte) {
	fd := int(f.Fd())
	if err := unix.SetNonblock(fd, true); err != nil {
		return false, nil
	}
	query := kittyGraphicsQuery()
	if n, err := unix.Write(fd, query); err != nil || n != len(query) {
		return false, nil
	}
	deadline := time.Now().Add(timeout)
	var data []byte
	var chunk [256]byte
	for len(data) < 4096 {
		wait := time.Until(deadline)
		if wait <= 0 {
			break
		}
		poll := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(poll, int((wait+time.Millisecond-1)/time.Millisecond))
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil || n == 0 || poll[0].Revents&unix.POLLIN == 0 {
			break
		}
		n, err = unix.Read(fd, chunk[:])
		if errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil || n == 0 {
			break
		}
		data = append(data, chunk[:n]...)
		if supported, done, pending := kittyGraphicsReply(data); done {
			return supported, pending
		}
	}
	return false, data
}
