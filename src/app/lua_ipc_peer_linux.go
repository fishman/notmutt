// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package app

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// peerCheck rejects a connection whose uid is not this process's. The 0700
// runtime dir already bounds reach (only its owner traverses it); the
// SO_PEERCRED check is defense in depth against a loosened dir. Non-linux
// builds (lua_ipc_peer_other.go) rely on the dir - this vendored x/sys/unix
// has no SO_PEERCRED there.
func peerCheck(conn net.Conn) error {
	uc, ok := conn.(*net.UnixConn)
	if !ok {
		return fmt.Errorf("lua ipc: non-unix connection")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return err
	}
	var cred *unix.Ucred
	var sockErr error
	if err := raw.Control(func(fd uintptr) {
		cred, sockErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if sockErr != nil {
		return sockErr
	}
	if !allowPeer(cred.Uid) {
		return fmt.Errorf("lua ipc: refused (peer uid %d, want %d)", cred.Uid, os.Getuid())
	}
	return nil
}
