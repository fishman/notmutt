// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

// fakeScreen is the v3 stand-in for the removed SimulationScreen: a
// minimal Screen backed by a cell buffer, events injected by writing
// to the EventQ channel (v3's injection path). Only the surface the
// tests use is real; the rest are no-ops.

import (
	"github.com/gdamore/tcell/v3"
	"github.com/gdamore/tcell/v3/color"
)

type fakeCell struct {
	Runes []rune
	Style tcell.Style
}

type fakeScreen struct {
	w, h  int
	cells [][]fakeCell
	evQ   chan tcell.Event
}

func newFakeScreen() *fakeScreen {
	s := &fakeScreen{evQ: make(chan tcell.Event, 16)}
	s.resize(80, 25)
	return s
}

func (s *fakeScreen) resize(w, h int) {
	s.w, s.h = w, h
	s.cells = make([][]fakeCell, h)
	for y := range s.cells {
		s.cells[y] = make([]fakeCell, w)
	}
}

func (s *fakeScreen) Init() error { return nil }
func (s *fakeScreen) Fini()       {}

func (s *fakeScreen) SetSize(w, h int) { s.resize(w, h) }
func (s *fakeScreen) Size() (int, int) { return s.w, s.h }

func (s *fakeScreen) SetContent(x, y int, primary rune, combining []rune, style tcell.Style) {
	if x < 0 || y < 0 || x >= s.w || y >= s.h {
		return
	}
	s.cells[y][x] = fakeCell{Runes: append([]rune{primary}, combining...), Style: style}
}

func (s *fakeScreen) Get(x, y int) (string, tcell.Style, int) {
	if x < 0 || y < 0 || x >= s.w || y >= s.h {
		return "", tcell.StyleDefault, 0
	}
	c := s.cells[y][x]
	if len(c.Runes) == 0 {
		return "", c.Style, 0
	}
	return string(c.Runes), c.Style, 1
}

func (s *fakeScreen) Show()  {}
func (s *fakeScreen) Sync()  {}
func (s *fakeScreen) Clear() { s.resize(s.w, s.h) }

func (s *fakeScreen) Fill(r rune, st tcell.Style) {
	for y := range s.cells {
		for x := range s.cells[y] {
			s.cells[y][x] = fakeCell{Runes: []rune{r}, Style: st}
		}
	}
}

func (s *fakeScreen) EventQ() chan tcell.Event { return s.evQ }

// InjectKey posts a key press onto the event queue (the v2
// SimulationScreen helper the loop tests drove).
func (s *fakeScreen) InjectKey(k tcell.Key, r rune, mod tcell.ModMask) {
	s.evQ <- tcell.NewEventKey(k, string(r), mod)
}

func (s *fakeScreen) Put(x, y int, str string, style tcell.Style) (string, int) {
	for _, r := range str {
		s.SetContent(x, y, r, nil, style)
		return str, 1
	}
	return str, 0
}

func (s *fakeScreen) PutStr(x, y int, str string) {
	s.Put(x, y, str, tcell.StyleDefault)
}

func (s *fakeScreen) PutStrStyled(x, y int, str string, style tcell.Style) {
	s.Put(x, y, str, style)
}

func (s *fakeScreen) SetStyle(tcell.Style)                             {}
func (s *fakeScreen) ShowCursor(x, y int)                              {}
func (s *fakeScreen) HideCursor()                                      {}
func (s *fakeScreen) SetCursorStyle(tcell.CursorStyle, ...color.Color) {}
func (s *fakeScreen) EnableMouse(...tcell.MouseFlags)                  {}
func (s *fakeScreen) DisableMouse()                                    {}
func (s *fakeScreen) EnablePaste()                                     {}
func (s *fakeScreen) DisablePaste()                                    {}
func (s *fakeScreen) EnableFocus()                                     {}
func (s *fakeScreen) DisableFocus()                                    {}
func (s *fakeScreen) Colors() int                                      { return 1 << 24 }
func (s *fakeScreen) CharacterSet() string                             { return "UTF-8" }
func (s *fakeScreen) RegisterRuneFallback(rune, string)                {}
func (s *fakeScreen) UnregisterRuneFallback(rune)                      {}
func (s *fakeScreen) Resize(int, int, int, int)                        {}
func (s *fakeScreen) Suspend() error                                   { return nil }
func (s *fakeScreen) Resume() error                                    { return nil }
func (s *fakeScreen) Beep() error                                      { return nil }
func (s *fakeScreen) LockRegion(int, int, int, int, bool)              {}
func (s *fakeScreen) Tty() (tcell.Tty, bool)                           { return nil, false }
func (s *fakeScreen) SetTitle(string)                                  {}
func (s *fakeScreen) SetClipboard([]byte)                              {}
func (s *fakeScreen) GetClipboard()                                    {}
func (s *fakeScreen) HasClipboard() bool                               { return false }
func (s *fakeScreen) ShowNotification(string, string)                  {}
func (s *fakeScreen) KeyboardProtocol() tcell.KeyProtocol              { return tcell.LegacyKeyboard }
func (s *fakeScreen) Terminal() (string, string)                       { return "", "" }
