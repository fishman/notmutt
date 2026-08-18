// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build cli

package notmuch

// New is the runtime backend factory under the cli build tag.
func New() Backend { return NewCLI() }
