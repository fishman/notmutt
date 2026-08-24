// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build darwin && cgo

package netcheck

/*
#cgo LDFLAGS: -framework SystemConfiguration
#include <SystemConfiguration/SystemConfiguration.h>
#include <CoreFoundation/CoreFoundation.h>

static int notmutt_reachable(void) {
	SCNetworkReachabilityRef ref =
		SCNetworkReachabilityCreateWithName(NULL, "apple.com");
	if (!ref) return 0;
	SCNetworkReachabilityFlags flags;
	int ok = SCNetworkReachabilityGetFlags(ref, &flags);
	CFRelease(ref);
	if (!ok) return 0;
	// reachable with no connection required: a live path. Connection
	// required (a captive portal, an on-demand link) means mail would
	// not get through - treat it as offline.
	return (flags & kSCNetworkReachabilityFlagsReachable) != 0 &&
		(flags & kSCNetworkReachabilityFlagsConnectionRequired) == 0;
}
*/
import "C"

import "context"

// online asks SCNetworkReachability whether the default network path
// is reachable (the macOS SystemConfiguration framework).
func online(ctx context.Context) bool {
	return C.notmutt_reachable() != 0
}
