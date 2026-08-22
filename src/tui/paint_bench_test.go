// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v3"
	"github.com/mattn/go-runewidth"
)

// paint benchmarks: the steady-state paint path at 120x40 with 5000
// rows (warm caches, one cursor move per paint). The loops isolate the
// marginal costs: View (model-side build), pushFrame (SGR parse into
// cells), Show (tcell diff + buffer write). m is always reassigned so
// the cursor anchors after the first move (the runLoop shape).
func benchPaint(b *testing.B, rows int) {
	m := rowsModel(rows)
	m, _ = m.Update(WindowSizeMsg{Width: 120, Height: 40})
	s := newFakeScreen()
	s.SetSize(120, 40)
	move := KeyPressMsg{Code: KeyDown}
	// warm: one full paint so the row caches and the sim buffer settle
	frame := m.View()
	x, y, show := m.textCursor()
	pushFrame(s, frame, x, y, show)
	b.ResetTimer()

	b.Run("ViewStatic", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = m.View()
		}
	})
	b.Run("ViewMove", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			m, _ = m.Update(move)
			_ = m.View()
		}
	})
	b.Run("ViewForced", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			m.paint = true
			_ = m.View()
		}
	})
	b.Run("PaintMove", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			m, _ = m.Update(move)
			m.paint = true
			frame := m.View()
			x, y, show := m.textCursor()
			pushFrame(s, frame, x, y, show)
		}
	})
	b.Run("PaintForced", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			m.paint = true
			frame := m.View()
			x, y, show := m.textCursor()
			pushFrame(s, frame, x, y, show)
		}
	})
	b.Run("PaintNoop", func(b *testing.B) {
		m.paint = true
		frame := m.View()
		x, y, show := m.textCursor()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			pushFrame(s, frame, x, y, show)
		}
	})
	b.Run("ShowOnly", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			s.Show()
		}
	})
	b.Run("PushNoShow", func(b *testing.B) {
		m.paint = true
		frame := m.View()
		x, y, show := m.textCursor()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			pushFrameNoShow(s, frame, x, y, show)
		}
	})
}

func BenchmarkPaint5000(b *testing.B) { benchPaint(b, 5000) }

// pushFrameNoShow is pushFrame minus the Show: the SetContent sweep alone (the Show moves into the ShowOnly measurement).
func pushFrameNoShow(s tcell.Screen, frame string, cursorX, cursorY int, showCursor bool) {
	style := tcell.StyleDefault
	w, h := s.Size()
	rows := strings.Split(frame, "\n")
	for y := 0; y < h; y++ {
		row := ""
		if y < len(rows) {
			row = rows[y]
		}
		cs, end := parseSGR(row, style)
		x := 0
		for _, c := range cs {
			if x >= w {
				break
			}
			if c.r >= ' ' && c.r != 0x7f {
				s.SetContent(x, y, c.r, nil, c.st)
				x += runewidth.RuneWidth(c.r)
			} else {
				x++
			}
		}
		for ; x < w; x++ {
			s.SetContent(x, y, ' ', nil, end)
		}
	}
	if showCursor {
		s.ShowCursor(cursorX, cursorY)
	} else {
		s.HideCursor()
	}
}
