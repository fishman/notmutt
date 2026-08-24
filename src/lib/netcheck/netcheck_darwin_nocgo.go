// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build darwin && !cgo

package netcheck

import "context"

// online without cgo: no framework access, so the transport is the
// authority (a wrong online answer only delays a try).
func online(ctx context.Context) bool { return true }
