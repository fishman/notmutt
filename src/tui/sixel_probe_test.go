// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"os"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"
)

// TestProbeSixel engages a real terminal and reports the negotiated
// sixel capability (the vendored fork's Screen.Sixel seam). Gated:
// NOTMUTT_PROBE_SIXEL=1 only - the test grabs the controlling
// terminal, so the regular suite skips it.
func TestProbeSixel(t *testing.T) {
	if os.Getenv("NOTMUTT_PROBE_SIXEL") != "1" {
		t.Skip("set NOTMUTT_PROBE_SIXEL=1 to probe the terminal's negotiated sixel support")
	}
	// the raw DA reply via /dev/tty, read before tcell engages (its tty
	// opens only at Init, and its Tty() claims one either way): a
	// negative Sixel with a ;4-carrying reply means the fork's parse
	// dropped it; a reply without ;4 means the terminal does not claim
	// sixel at all.
	if f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0); err == nil {
		if _, err := f.Write([]byte("\x1b[c")); err == nil {
			reply := make(chan string, 1)
			go func() {
				buf := make([]byte, 128)
				n, _ := f.Read(buf)
				reply <- string(buf[:n])
			}()
			select {
			case r := <-reply:
				t.Logf("raw DA reply: %q", r)
			case <-time.After(time.Second):
				t.Logf("raw DA reply: (none within 1s)")
			}
		}
		f.Close()
	}
	s, err := tcell.NewScreen()
	if err != nil {
		t.Fatalf("NewScreen: %v", err)
	}
	if err := s.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	defer s.Fini()
	t.Logf("Sixel() = %v", s.Sixel())
}
