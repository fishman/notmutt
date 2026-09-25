// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build linux

package localipc

import (
	"fmt"
	"golang.org/x/sys/unix"
	"net"
	"os"
)

func CheckPeer(conn net.Conn) error {
	unixConn, err := asUnix(conn)
	if err != nil {
		return err
	}
	raw, err := unixConn.SyscallConn()
	if err != nil {
		return err
	}
	var credential *unix.Ucred
	var socketErr error
	if err := raw.Control(func(fd uintptr) {
		credential, socketErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return err
	}
	if socketErr != nil {
		return socketErr
	}
	if credential.Uid != uint32(os.Getuid()) {
		return fmt.Errorf("local ipc: refused peer uid %d", credential.Uid)
	}
	return nil
}
