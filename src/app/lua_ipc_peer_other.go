// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package app

import "net"

// peerCheck trusts the filesystem boundary on non-linux platforms (this
// vendored x/sys/unix has no SO_PEERCRED there): the socket lives in a
// 0700 runtime dir only its owner can traverse.
func peerCheck(conn net.Conn) error {
	return nil
}
