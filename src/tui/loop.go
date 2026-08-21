// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"os"

	"github.com/gdamore/tcell/v3"
)

// Run starts the loop: the real screen, then runLoop (the test entry
// point drives runLoop with a simulation screen). The image paint
// sink is /dev/tty - the tcell screen cannot emit raw image
// protocols, and the direct fd writes after a frame flush are ordered
// and safe (the draw clobbers the cursor position on the next frame).
func Run(model Model, quitCh <-chan struct{}) error {
	probeCellSize()
	s, err := tcell.NewScreen(tcell.OptAdvancedKeys(true))
	if err != nil {
		return err
	}
	if err := s.Init(); err != nil {
		return err
	}
	defer s.Fini()
	model.imgProto = detectImageProtocol(model.st.Config().Pager, s)
	if tty, err := os.OpenFile("/dev/tty", os.O_WRONLY, 0); err == nil {
		imageWriter = tty
		defer func() {
			imageWriter = nil
			tty.Close()
		}()
	}
	return runLoop(model, s, quitCh)
}

// runLoop is the event loop (decision record 23 - the tea runtime is
// gone): events come from the screen, the model's cmds run on
// goroutines and their messages come back on cmdCh, quitCh is the
// app's cancellation, quitMsg is the model's own quit (the bound q
// action). After every batch the paint gate decides whether the frame
// rebuilds - the model's frameTick cadence IS the render cadence now
// (no renderer tick to align, the WithFPS machinery dies with tea).
func runLoop(m Model, s tcell.Screen, quitCh <-chan struct{}) error {
	loopScreen = s
	defer func() { loopScreen = nil }()
	// the quit path persists the chooser's last directory: the defer
	// sees the loop's final model
	defer saveChooserDir(m)
	cmdCh := make(chan []any, 64)
	run := func(c Cmd) {
		if c != nil {
			go func() { cmdCh <- c() }()
		}
	}
	run(m.Init())
	w, h := s.Size()
	m, cmd := m.Update(WindowSizeMsg{Width: w, Height: h})
	run(cmd)
	x, y, show := m.textCursor()
	pushFrame(s, m.View(), x, y, show)
	// tcell v3's event pump: the Screen owns the EventQ channel (the
	// ChannelEvents forwarder is gone in v3); it stays open until Fini,
	// so quitting still comes from quitCh or the model's quitMsg.
	evCh := s.EventQ()
	for {
		var msgs []any
		select {
		case ev := <-evCh:
			if ev == nil {
				return nil
			}
			var msg any
			switch e := ev.(type) {
			case *tcell.EventKey:
				// v3 delivers key releases (kitty protocol); the
				// release path is not wired, so drop them.
				if e.Pressed() {
					if press, _, ok := keyPressOf(e); ok {
						msg = press
					}
				}
			case *tcell.EventResize:
				probeCellSize()
				w, h := e.Size()
				msg = WindowSizeMsg{Width: w, Height: h}
			}
			if msg == nil {
				continue
			}
			m, cmd = m.Update(msg)
			run(cmd)
		case msgs = <-cmdCh:
			for _, msg := range msgs {
				if _, isQuit := msg.(quitMsg); isQuit {
					return nil
				}
				m, cmd = m.Update(msg)
				run(cmd)
			}
		case <-quitCh:
			return nil
		}
		if m.ShouldRender() {
			next, stale := m.paintRects()
			clearRects(imageWriter, stale) // before the text frame: EL drops the stale pixels
			x, y, show := m.textCursor()
			pushFrame(s, m.View(), x, y, show)
			m.paintImages(next) // pixels after the text frame, never before
		}
	}
}
