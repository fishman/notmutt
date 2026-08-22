// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build windows

package tui

// probeCellSize is a no-op on windows: the console has no TIOCGWINSZ pixel semantics, the 10x20 defaults stay.
func probeCellSize() {}
