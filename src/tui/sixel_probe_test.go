// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"os"
	"testing"

	"github.com/gdamore/tcell/v3"
)

// TestProbeSixel engages a real terminal and reports the negotiated
// sixel capability (the vendored fork's Screen.Sixel seam). Init's
// capability negotiation sends the primary DA query itself - no manual
// probe - so the value is exactly what notmutt would see. Gated:
// NOTMUTT_PROBE_SIXEL=1 only - the test grabs the controlling
// terminal, so the regular suite skips it.
func TestProbeSixel(t *testing.T) {
	if os.Getenv("NOTMUTT_PROBE_SIXEL") != "1" {
		t.Skip("set NOTMUTT_PROBE_SIXEL=1 to probe the terminal's negotiated sixel support")
	}
	s, err := tcell.NewScreen()
	if err != nil {
		t.Fatalf("NewScreen: %v", err)
	}
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Logf("Sixel() = %v", s.Sixel())
}
