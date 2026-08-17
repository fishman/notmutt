package tui

// Frame-chrome regression: drive the real program, decode the recorder
// frames into a screen buffer, and assert the chrome (tab bar, keyhint,
// status line) survives cursor moves and a refresh that shrinks the list.
// The diff renderer never erases cells past the last frame line, so a
// frame shorter than the screen leaves the previous paint's rows on
// screen (the short-list padding fix); and cursor-move diffs must not
// shift rows onto the chrome.

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

func bytesCount(b []byte, sub string) int {
	return bytes.Count(b, []byte(sub))
}

// screen decodes the renderer's ANSI output. LF is CR-LF (the terminal
// runs with OPOST); the supported sequences are CUP/CHA/CUU/CUD/CUF/CUB,
// EL, ED, ECH, DCH and plain chars.
type screen struct {
	rows   [][]rune
	w, h   int
	cx, cy int
}

func newScreen(w, h int) *screen {
	s := &screen{w: w, h: h}
	for i := 0; i < h; i++ {
		s.rows = append(s.rows, []rune(strings.Repeat(" ", w)))
	}
	return s
}

func (s *screen) apply(f []byte) {
	x, y := s.cx, s.cy
	for i := 0; i < len(f); i++ {
		b := f[i]
		if b == '\x1b' && i+1 < len(f) && f[i+1] == '[' {
			j := i + 2
			var params []int
			var cur int
			have := false
			for j < len(f) && (f[j] < 'A' || f[j] > '~') {
				if f[j] >= '0' && f[j] <= '9' {
					cur = cur*10 + int(f[j]-'0')
					have = true
				} else if f[j] == ';' {
					params = append(params, cur)
					cur = 0
					have = false
				}
				j++
			}
			if j >= len(f) {
				break
			}
			if have {
				params = append(params, cur)
			}
			final := f[j]
			i = j
			n := 1
			if len(params) > 0 && params[0] > 0 {
				n = params[0]
			}
			switch final {
			case 'H', 'f':
				cy, cx := 1, 1
				if len(params) > 0 {
					cy = params[0]
				}
				if len(params) > 1 {
					cx = params[1]
				}
				y, x = cy-1, cx-1
			case 'A':
				y -= n
			case 'B':
				y += n
			case 'C':
				x += n
			case 'D':
				x -= n
			case 'G', '`':
				x = n - 1
			case 'K':
				if len(params) == 0 || params[0] == 0 {
					for k := x; k < s.w; k++ {
						s.set(y, k, ' ')
					}
				}
			case 'X': // erase n chars right of the cursor
				for k := 0; k < n; k++ {
					s.set(y, x+k, ' ')
				}
			case 'P': // delete n chars, shift the rest left
				if n == 0 {
					n = 1
				}
				copy(s.rows[y][x:], s.rows[y][x+n:])
				for k := s.w - n; k < s.w; k++ {
					s.rows[y][k] = ' '
				}
			case 'J':
				for k := y; k < s.h; k++ {
					for m := 0; m < s.w; m++ {
						s.set(k, m, ' ')
					}
				}
			}
			continue
		}
		switch b {
		case '\n': // the terminal runs with OPOST: LF is CR-LF
			y++
			x = 0
			if y >= s.h {
				y = s.h - 1
			}
		case '\r':
			x = 0
		default:
			s.set(y, x, rune(b))
			x++
			if x >= s.w {
				x = s.w - 1
			}
		}
	}
	s.cx, s.cy = x, y
}

func (s *screen) set(y, x int, r rune) {
	if y >= 0 && y < s.h && x >= 0 && x < s.w {
		s.rows[y][x] = r
	}
}

func (s *screen) line(y int) string {
	return strings.TrimRight(string(s.rows[y]), " ")
}

// TestFrameChromeSurvivesRefresh runs a full list (60 threads), six
// cursor moves, a refresh that shrinks the list to 2 threads, and a
// refresh that grows it back. After each stage the chrome rows (tab bar
// 0, keyhint h-2, status h-1) must hold their content and the list must
// end exactly at the keyhint row: no stale rows, nothing on the chrome.
func TestFrameChromeSurvivesRefresh(t *testing.T) {
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
	out := &recorder{}
	prog := tea.NewProgram(
		New(view, bus.Subscribe(), testBindings(), testTagActions(), bus, st, config.Default().UI),
		tea.WithOutput(out),
		tea.WithInput(strings.NewReader("")),
		tea.WithWindowSize(w, h),
	)
	done := make(chan struct{})
	go func() { prog.Run(); close(done) }()
	defer func() { prog.Quit(); <-done }()

	j := tea.KeyPressMsg{Text: "j", Code: 'j'}
	prog.Send(tea.WindowSizeMsg{Width: w, Height: h})
	time.Sleep(100 * time.Millisecond)
	for i := 0; i < 6; i++ {
		prog.Send(j)
		time.Sleep(100 * time.Millisecond)
	}
	view.MergeThreads(nil)
	view.MergeThreads(threads[:2])
	bus.Publish(core.ViewDiff{View: "inbox"})
	time.Sleep(150 * time.Millisecond)
	shrinkFrames := len(out.frames)
	view.MergeThreads(threads)
	bus.Publish(core.ViewDiff{View: "inbox"})
	time.Sleep(150 * time.Millisecond)

	scr := newScreen(w, h)
	for i, f := range out.frames {
		scr.apply(f)
		if i == shrinkFrames-1 {
			// stable shrink: rows 3-21 blank, chrome intact, status "2"
			for r := 3; r <= 21; r++ {
				if scr.line(r) != "" {
					t.Fatalf("shrink frame %d: row %d not blank: %q", i, r, scr.line(r))
				}
			}
			if got := scr.line(0); !strings.HasPrefix(got, " inbox") {
				t.Fatalf("shrink frame %d: tab bar clobbered: %q", i, got)
			}
			if got := scr.line(22); !strings.Contains(got, "$ apply") {
				t.Fatalf("shrink frame %d: keyhint clobbered: %q", i, got)
			}
			if got := scr.line(23); !strings.Contains(got, "inbox") || !strings.Contains(got, "2") {
				t.Fatalf("shrink frame %d: status clobbered: %q", i, got)
			}
		}
	}
	// final state: full list, chrome intact
	for r := 1; r <= 21; r++ {
		if scr.line(r) == "" {
			t.Fatalf("final frame: list row %d empty", r)
		}
	}
	if got := scr.line(0); !strings.HasPrefix(got, " inbox") {
		t.Fatalf("final: tab bar clobbered: %q", got)
	}
	if got := scr.line(22); !strings.Contains(got, "$ apply") {
		t.Fatalf("final: keyhint clobbered: %q", got)
	}
	if got := scr.line(23); !strings.Contains(got, "inbox") {
		t.Fatalf("final: status clobbered: %q", got)
	}
}
