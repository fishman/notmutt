// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"os"
	"testing"

	"github.com/gdamore/tcell/v3"
)

// TestProbeSixel engages a real terminal and reports the negotiated
// sixel capability (the vendored fork's Capabilities bitfield). Init's
// negotiation sends the primary DA query itself - no manual probe - so
// the value is exactly what notmutt would see. Gated on
// NOTMUTT_PROBE_SIXEL=1 (the test grabs the controlling terminal).
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
	t.Logf("Sixel() = %v", s.Capabilities()&tcell.CapabilitySixel != 0)
}
