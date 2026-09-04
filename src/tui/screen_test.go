// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

// Regression for the user report of a lingering black rectangle in the
// pager after fast scrolling with images: clearImageRects ELs the image
// block's background straight to the tty (out-of-band, never through
// tcell), so the rows tcell thinks it owns are untouched. When the
// image leaves, the vacated rows are often blank and unchanged - the
// next pushFrame skips them and the erase's color stays on screen as a
// black band. The fix: markRowsCleared forces the next push to re-emit
// those rows so tcell reconciles the terminal with the model.
//
// LOCKED: regression test pinned to that bug. Do not edit, weaken, or
// remove without explicit user confirmation.

import (
	"testing"

	"github.com/gdamore/tcell/v3"
)

// countingScreen observes how many SetContent calls a push makes per
// row (fakeScreen only exposes final buffer values, which hide whether
// an unchanged row was re-emitted).
type countingScreen struct {
	*fakeScreen
	writes []int
}

func (c *countingScreen) SetContent(x, y int, primary rune, combining []rune, style tcell.Style) {
	c.writes[y]++
	c.fakeScreen.SetContent(x, y, primary, combining, style)
}

func TestPushFrameReemitsClearedRows(t *testing.T) {
	s := &countingScreen{fakeScreen: newFakeScreen(), writes: make([]int, 8)}
	s.SetSize(80, 8)
	t.Cleanup(func() {
		delete(pushedFrames, s)
		delete(clearedRows, s)
	})
	frame := "row0\nrow1\nrow2\nrow3\nrow4\nrow5\nrow6\nrow7"
	pushFrame(s, frame, 0, 0, false)
	for y, n := range s.writes {
		if n == 0 {
			t.Fatalf("first push wrote nothing on row %d", y)
		}
	}
	// an identical push with nothing cleared stays a no-op
	pushFrame(s, frame, 0, 0, false)
	before := append([]int(nil), s.writes...)
	// ...but an out-of-band clear (an EL an image erase wrote to the
	// tty) marks its rows, and the next identical push must re-emit them
	markRowsCleared(s, 2, 3)
	pushFrame(s, frame, 0, 0, false)
	for y := 2; y <= 4; y++ {
		if s.writes[y] <= before[y] {
			t.Fatalf("cleared row %d not re-emitted (writes %d -> %d)", y, before[y], s.writes[y])
		}
	}
	for _, y := range []int{0, 1, 5, 6, 7} {
		if s.writes[y] != before[y] {
			t.Fatalf("untouched row %d re-emitted (%d -> %d)", y, before[y], s.writes[y])
		}
	}
	// the re-emission drains the mark: a further identical push is again a no-op
	final := append([]int(nil), s.writes...)
	pushFrame(s, frame, 0, 0, false)
	for y := range s.writes {
		if s.writes[y] != final[y] {
			t.Fatalf("row %d written after clearedRows drained", y)
		}
	}
}
