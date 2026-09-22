// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build !linux

package localipc

import "net"

func CheckPeer(net.Conn) error { return nil }
