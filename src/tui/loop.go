// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"os"
	"time"

	"github.com/gdamore/tcell/v3"
)

// Run starts the loop: the real screen, then runLoop (tests drive
// runLoop with a simulation screen). The image paint sink is /dev/tty
// - the tcell screen cannot emit raw image protocols; the direct fd
// writes after a frame flush are ordered and safe.
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
// goroutines with their messages back on cmdCh, quitCh is the app's
// cancellation, quitMsg the model's own quit (the bound q action).
// After every batch the paint gate decides whether the frame rebuilds
// - the frameTick cadence IS the render cadence now.
func runLoop(m Model, s tcell.Screen, quitCh <-chan struct{}) error {
	loopScreen = s
	defer func() { loopScreen = nil }()
	// the quit path persists the chooser's last directory: the defer sees the loop's final model
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
	// tcell v3's event pump: the Screen owns the EventQ channel (the ChannelEvents forwarder is gone in v3); it stays open until Fini, so quitting still comes from quitCh or the model's quitMsg.
	evCh := s.EventQ()
	// the status spinner's cadence: a loop-owned ticker gated by busy().
	// A cmd-batched tick shared its goroutine with the bus reader, so an
	// idle sync (quiet bus) froze the spinner and a tick delivered in
	// the same batch as a real event ate that event's full-repaint
	// request. Delivered on its own select arm, the tick is never
	// batched - it can only drop the every-update paint default, never a
	// real event's render request.
	var spinC <-chan time.Time
	var spinTicker *time.Ticker
	stopSpin := func() {
		if spinTicker != nil {
			spinTicker.Stop()
			spinTicker = nil
			spinC = nil
		}
	}
	defer stopSpin()
	for {
		if m.busy() && spinTicker == nil {
			spinTicker = time.NewTicker(statusSpinTickInterval)
			spinC = spinTicker.C
		} else if !m.busy() && spinTicker != nil {
			stopSpin()
		}
		var msgs []any
		select {
		case ev := <-evCh:
			if ev == nil {
				return nil
			}
			var msg any
			switch e := ev.(type) {
			case *tcell.EventKey:
				// press-only: tcell v3 delivers key RELEASES on
				// release-reporting terminals (the kitty protocol - tmux
				// strips it, which is why the double-fire only shows
				// outside tmux). A release is not a press: dispatching it
				// fires every key twice. 1c1beb4 dropped this guard with
				// the dead legend path - it is the drop.
				if e.Pressed() {
					if press, ok := keyPressOf(e); ok {
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
		case <-spinC: // nil blocks; armed, fires the tick on its own cadence
			m, cmd = m.Update(statusSpinTick{})
			run(cmd)
		case <-quitCh:
			return nil
		}
		if m.ShouldRender() {
			next, stale := m.paintRects()
			clearRects(imageWriter, stale) // before the text frame: EL drops the stale pixels
			x, y, show := m.textCursor()
			pushFrame(s, m.View(), x, y, show)
			m.paintImages(next) // pixels after the text frame, never before
			// the render consumes the paint gate: paint is a render
			// request, not a latch - clearing here keeps an idle model
			// quiet, so the spinner ticker's repaints never accumulate.
			m.paint = false
		}
	}
}
