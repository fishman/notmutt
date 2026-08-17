package tui

import (
	"github.com/gdamore/tcell/v3"
)

// Run starts the loop: the real screen, then runLoop (the test entry
// point drives runLoop with a simulation screen).
func Run(model Model, quitCh <-chan struct{}) error {
	s, err := tcell.NewScreen()
	if err != nil {
		return err
	}
	if err := s.Init(); err != nil {
		return err
	}
	defer s.Fini()
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
			x, y, show := m.textCursor()
			pushFrame(s, m.View(), x, y, show)
		}
	}
}
