package tui

// The loop's repaint contract on the simulation screen: a cursor move
// repaints only the affected rows. Regression for the user report:
// "when i press j 5 is highlighted and 4 is unhighlighted. every other
// line should stay the same, but instead the entire frame is
// redrawn." The frame is written whole every paint (tcell diffs
// internally), so the observable contract is buffer equality: the
// chrome and the untouched list rows must be identical after the move
// - only the two cursor rows (and the status segment, which resolves
// the cursor's legend) may change.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v3"

	"notmutt/config"
	"notmutt/core"
)

// newSim builds the simulation screen the loop tests drive.
func newSim(t *testing.T, w, h int) *fakeScreen {
	t.Helper()
	s := newFakeScreen()
	s.SetSize(w, h)
	return s
}

// rowText decodes one screen row to text, right-trimmed like the
// terminal renders it.
func rowText(cs []fakeCell, w, y int) string {
	var b strings.Builder
	for x := 0; x < w; x++ {
		r := ' '
		if len(cs[y*w+x].Runes) > 0 {
			r = cs[y*w+x].Runes[0]
		}
		b.WriteRune(r)
	}
	return strings.TrimRight(b.String(), " ")
}

func rowCells(cs []fakeCell, w, y int) []fakeCell {
	return cs[y*w : (y+1)*w]
}

// copyCells deep-copies the buffer (GetContents may alias the screen's
// internal rune slices).
func copyCells(cs []fakeCell) []fakeCell {
	out := make([]fakeCell, len(cs))
	for i := range cs {
		out[i] = cs[i]
		out[i].Runes = append([]rune(nil), cs[i].Runes...)
	}
	return out
}

// sameCells compares two cell rows: runes and style.
func sameCells(a, b []fakeCell) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i].Runes) != len(b[i].Runes) {
			return false
		}
		for j := range a[i].Runes {
			if a[i].Runes[j] != b[i].Runes[j] {
				return false
			}
		}
		if a[i].Style != b[i].Style {
			return false
		}
	}
	return true
}

// waitScreen polls the screen buffer until the predicate holds.
func waitScreen(t *testing.T, s *fakeScreen, w, h int, want func([]fakeCell) bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if want(cellsOf(s)) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for the frame state")
}

// TestLoopCursorMoveRepaints drives the REAL loop (runLoop on the
// simulation screen): a keypress flows through tcell -> the model ->
// the frame.
func TestLoopCursorMoveRepaints(t *testing.T) {
	const w, h = 80, 24
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
	m := New(view, bus.Subscribe(), testBindings(), testTagActions(), bus, st, config.Default().UI)
	s := newSim(t, w, h)
	quitCh := make(chan struct{})
	done := make(chan error, 1)
	go func() { done <- runLoop(m, s, quitCh) }()
	defer func() { close(quitCh); <-done }()

	waitScreen(t, s, w, h, func(cs []fakeCell) bool { return strings.Contains(rowText(cs, w, 22), "$ apply") })
	before := copyCells(cellsOf(s))
	s.InjectKey(tcell.KeyRune, 'j', tcell.ModNone)
	waitScreen(t, s, w, h, func(cs []fakeCell) bool {
		return !sameCells(rowCells(before, w, 1), rowCells(cs, w, 1)) ||
			!sameCells(rowCells(before, w, 2), rowCells(cs, w, 2))
	})
	after := copyCells(cellsOf(s))
	for y := 0; y < h; y++ {
		if y == 1 || y == 2 || y == h-1 {
			continue // the two cursor rows and the status segment may change
		}
		if !sameCells(rowCells(before, w, y), rowCells(after, w, y)) {
			t.Fatalf("row %d must not change on a cursor move", y)
		}
	}
}

// cellsOf rebuilds the buffer through the Screen Get API (v3 has no
// bulk GetContents).
func cellsOf(s *fakeScreen) []fakeCell {
	w, h := s.Size()
	cs := make([]fakeCell, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			str, st, _ := s.Get(x, y)
			cs[y*w+x] = fakeCell{Runes: []rune(str), Style: st}
		}
	}
	return cs
}
