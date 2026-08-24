// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

// Package netcheck reports whether the system has a network path that
// could reach the internet - the scheduled-mail pre-flight (a clearly
// offline machine skips the futile transport attempts; the transport
// error remains the authority, a wrong online answer only delays).
// Per-platform implementations are selected by build tags, never by
// runtime probing: linux consults NetworkManager over D-Bus (falling
// back to the default route), darwin asks SCNetworkReachability, and
// unknown platforms defer to the transport.
package netcheck

import "context"

// Online reports whether the system has a usable network path.
// False means a delivery attempt would certainly fail; true is
// advisory - the transport's own error decides in the end.
func Online(ctx context.Context) bool {
	return online(ctx)
}
