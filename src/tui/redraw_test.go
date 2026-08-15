package tui

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"notmutt/config"
	"notmutt/core"
)

// recorder frames each renderer flush as one entry: flush() issues a single
// out.Write per rendered frame (vendored standard_renderer.go).
type recorder struct{ frames [][]byte }

func (r *recorder) Write(p []byte) (int, error) {
	r.frames = append(r.frames, append([]byte(nil), p...))
	return len(p), nil
}

// TestCursorMovePartialRepaint drives the REAL bubbletea program (event
// loop + vendored standard renderer) and asserts a cursor move repaints
// only the affected rows, never the full page. Regression for the user
// report: "when i press j 5 is highlighted and 4 is unhighlighted. every
// other line should stay the same, but instead the entire frame is
// redrawn."
func TestCursorMovePartialRepaint(t *testing.T) {
	view := core.NewView("inbox", "tag:inbox")
	view.SetGroups([]core.TagGroup{{Tags: []string{"inbox", "archive", "deleted", "sent", "draft", "pending", "spam"}}})
	var threads []*core.Thread
	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("t%d", i)
		threads = append(threads, core.NewThread(id, []*core.Message{
			{ID: fmt.Sprintf("m%d", i), Timestamp: int64(i), Author: "Ann", Subject: "s", Tags: []string{"inbox"}},
		}))
	}
	view.MergeThreads(threads)
	bus := core.NewBus()
	st := config.NewStore(config.Default())
	out := &recorder{}
	prog := tea.NewProgram(
		New(view, bus.Subscribe(), testBindings(), testTagActions(), bus, st, config.Default().UI),
		tea.WithOutput(out),
		tea.WithInput(strings.NewReader("")),
		// without a TTY, Run() sizes itself 0x0 and its own resizeMsg
		// clobbers the test's WindowSizeMsg - the renderer never
		// leaves the zero-size frame
		tea.WithWindowSize(80, 24),
	)
	done := make(chan struct{})
	go func() { prog.Run(); close(done) }()
	defer func() { prog.Quit(); <-done }()

	j := tea.KeyPressMsg{Text: "j", Code: 'j'}
	prog.Send(tea.WindowSizeMsg{Width: 80, Height: 24})
	// 100ms between sends: one flush per message (the renderer ticks at
	// 60fps, so back-to-back sends would collapse into a single flush).
	time.Sleep(100 * time.Millisecond)
	prog.Send(j) // first paint at the window size (legitimately full)
	time.Sleep(100 * time.Millisecond)
	prog.Send(j)
	time.Sleep(100 * time.Millisecond)
	prog.Send(j)
	time.Sleep(100 * time.Millisecond)

	// A cursor move must repaint at most 3 rows: the two cursor rows plus
	// the status-line segment - never the full page (the trailing-newline
	// bug made every line differ and forced full 24-row repaints). The v2
	// renderer repaints a row by positioning the cursor and erasing to
	// EOL, so each repainted row carries one erase-to-EOL sequence.
	// Every frame after the first paint must be partial.
	partial := 0
	painted := false
	for i, f := range out.frames {
		n := bytes.Count(f, []byte("\x1b[K"))
		if !painted {
			// startup frames carry no rows; the first content frame is
			// the legitimate full paint at the window size
			if n > 3 {
				painted = true
			}
			continue
		}
		if n > 3 {
			t.Fatalf("frame %d repainted %d rows (full page?): %d bytes\n%q", i, n, len(f), f)
		}
		if n == 2 || n == 3 {
			partial++
		}
	}
	if partial < 2 {
		t.Fatalf("expected 2 partial cursor-move frames, got %d: %v", partial, frameErases(out.frames))
	}
}

func waitFrames(t *testing.T, out *recorder, n int) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if len(out.frames) >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for frame %d (have %d)", n, len(out.frames))
}

func frameErases(frames [][]byte) []int {
	var s []int
	for _, f := range frames {
		s = append(s, bytes.Count(f, []byte("\x1b[K")))
	}
	return s
}
