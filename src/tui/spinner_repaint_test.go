// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

// The spin-ticker contract ("the spinner animates while the client is
// busy, nothing else is happening"): a statusSpinTick while busy
// advances the frame and requests a full repaint; an idle straggler
// tick requests one full repaint too so the idle glyph swaps back in.
// The loop owns the cadence (loop.go's busy-gated ticker), so a tick is
// never batched with a real event - it can only drop the every-update
// paint default, never a real render. Fabricated threads, never mail.

import (
	"strings"
	"testing"

	"notmutt/core"
)

func TestSpinTickRepaintsWhileBusy(t *testing.T) {
	m := sized(model())
	m = pressEvent(t, m, core.TaskChanged{ID: "sync", Active: true, Label: "sync"})
	first := m.View() // the cache builds: busy, spin frame 0
	m.paint = false   // the loop consumed the render (loop.go)
	next, _ := m.Update(statusSpinTick{})
	if !next.paint {
		t.Fatalf("a busy spin tick must request a repaint")
	}
	after := next.View()
	fl, al := strings.Split(first, "\n"), strings.Split(after, "\n")
	if len(fl) != len(al) {
		t.Fatalf("frame height changed on a spin tick: %d -> %d", len(fl), len(al))
	}
	if fl[len(fl)-1] == al[len(al)-1] {
		t.Fatalf("the status row must advance on a spin tick:\n%q", al[len(al)-1])
	}
}

func TestSpinTickDisarmReturnsToIdle(t *testing.T) {
	m := sized(model())
	m = pressEvent(t, m, core.TaskChanged{ID: "sync", Active: true, Label: "sync"})
	m, _ = m.Update(statusSpinTick{}) // busy: advance one frame
	m.paint = false
	m = pressEvent(t, m, core.TaskChanged{ID: "sync", Active: false, Label: "sync"})
	next, _ := m.Update(statusSpinTick{}) // idle: the tick disarms
	if !next.paint {
		t.Fatalf("the idle tick must request one full repaint to swap the idle glyph in")
	}
	if !strings.Contains(stripANSI(next.View()), statusIdleGlyph) {
		t.Fatalf("the idle glyph must return after the client goes idle")
	}
}

// TestSpinnerSegmentThemed pins the themed working indicator: the busy
// frame rides the view pill's color (the onedark reference's green),
// the idle marker the count pill's (yellow) - resolved per theme, never
// hardcoded.
func TestSpinnerSegmentThemed(t *testing.T) {
	st := DefaultStyles()
	busy := spinnerSegment(true, 0, st)
	idle := spinnerSegment(false, 0, st)
	if busy.style.Render("x") != st.View.Render("x") {
		t.Fatalf("the busy spinner must carry the view pill's color")
	}
	if idle.style.Render("x") != st.Count.Render("x") {
		t.Fatalf("the idle marker must carry the count pill's color")
	}
	if idle.content != statusIdleGlyph {
		t.Fatalf("idle must render the fixed glyph: %q", idle.content)
	}
	if !strings.Contains(busy.content, "⠋") {
		t.Fatalf("busy must render a spin frame: %q", busy.content)
	}
}
