package tui

import (
	"fmt"
	"strings"
	"testing"

	"notmutt/core"
)

// TestPreviewStaysInIndexAndShowsBox pins the preview surface: p opens
// the popup over the index (mode stays "index"), the box carries the
// row's subject as its title, the loaded content lands inside it, and
// the keyhint derives the pager keys (activeBindings flips with the
// popup).
func TestPreviewStaysInIndexAndShowsBox(t *testing.T) {
	SetOpenHandler(func(threadID string, preview bool) {})
	defer SetOpenHandler(func(threadID string, preview bool) {})
	m := model()
	m.width, m.height = 80, 24
	m = press(t, m, "P")
	if !m.preview || m.mode != "index" {
		t.Fatalf("p must open the preview popup over the index, preview=%v mode=%q", m.preview, m.mode)
	}
	if m.previewThread != "t1" || m.previewTitle == "" {
		t.Fatalf("preview target missing: thread=%q title=%q", m.previewThread, m.previewTitle)
	}
	m = pressEvent(t, m, core.ThreadLoaded{ThreadID: "t1", Preview: true,
		Lines: []core.Line{{Kind: core.LineBody, Text: "body line"}}})
	out := stripANSI(m.View())
	// the scroll hint lists every pager scroll key, arrows included
	// (the live config's arrow overlay is part of the fixture)
	for _, want := range []string{m.previewTitle, "body line", "down/j/k/up scroll  enter open  q close", "╭" + strings.Repeat("─", 2), "╰─", "tab-prev"} {
		if !strings.Contains(out, want) {
			t.Fatalf("preview box missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "cursor-down") {
		t.Fatalf("the keyhint must derive the pager keys during preview:\n%s", out)
	}
}

// TestPreviewScrollsAndCloses pins the popup keys: the pager scroll
// keys scroll the box (index cursor untouched), q closes it and drops
// the pager. A long fixture body makes the preview window movable
// (the box is only 14 rows at 80x24).
func TestPreviewScrollsAndCloses(t *testing.T) {
	SetOpenHandler(func(threadID string, preview bool) {})
	defer SetOpenHandler(func(threadID string, preview bool) {})
	m := model()
	m.width, m.height = 80, 24
	m = press(t, m, "P")
	lines := make([]core.Line, 40)
	for i := range lines {
		lines[i] = core.Line{Kind: core.LineBody, Text: "line"}
	}
	m = pressEvent(t, m, core.ThreadLoaded{ThreadID: "t1", Preview: true, Lines: lines})
	if m.pager == nil {
		t.Fatal("preview must hold a pager")
	}
	m = press(t, m, "j")
	if m.pager.vp.offset == 0 {
		t.Fatalf("j must scroll the preview pager, offset=%d", m.pager.vp.offset)
	}
	if m.mode != "index" || !m.preview {
		t.Fatalf("scrolling must not leave the preview, mode=%q preview=%v", m.mode, m.preview)
	}
	m = press(t, m, "q")
	if m.preview || m.previewThread != "" || m.pager != nil {
		t.Fatalf("q must close the preview, preview=%v thread=%q pager=%v", m.preview, m.previewThread, m.pager != nil)
	}
	if m.mode != "index" {
		t.Fatalf("closing the preview must stay in index, mode=%q", m.mode)
	}
}

// TestPreviewOpensFull pins the promotion: the open key re-opens the
// same thread for real (the seam sees preview=false - the app marks it
// read), the pager resizes to the full frame, and the frame keeps its
// height invariant.
func TestPreviewOpensFull(t *testing.T) {
	var calls []string
	SetOpenHandler(func(threadID string, preview bool) {
		calls = append(calls, fmt.Sprintf("%s:%v", threadID, preview))
	})
	defer SetOpenHandler(func(threadID string, preview bool) {})
	m := model()
	m.width, m.height = 80, 24
	lines := []core.Line{{Kind: core.LineBody, Text: "body line"}}
	m = press(t, m, "P")
	m = pressEvent(t, m, core.ThreadLoaded{ThreadID: "t1", Preview: true, Lines: lines})
	m = press(t, m, "enter")
	m = pressEvent(t, m, core.ThreadLoaded{ThreadID: "t1", Lines: lines})
	if m.preview || m.previewThread != "" {
		t.Fatalf("enter must promote the preview to a full open, preview=%v thread=%q", m.preview, m.previewThread)
	}
	if m.mode != "pager" {
		t.Fatalf("enter must open the pager, mode=%q", m.mode)
	}
	if len(calls) != 2 || calls[0] != "t1:true" || calls[1] != "t1:false" {
		t.Fatalf("P must preview then enter must open: %v", calls)
	}
	out := m.View()
	if got := strings.Count(out, "\n") + 1; got != m.height {
		t.Fatalf("the promoted pager frame must keep the height invariant, got %d want %d", got, m.height)
	}
}

// TestPreviewStaleReplyDrops pins the async guard: a preview reply
// landing after the popup closed must not force the thread open.
func TestPreviewStaleReplyDrops(t *testing.T) {
	SetOpenHandler(func(threadID string, preview bool) {})
	defer SetOpenHandler(func(threadID string, preview bool) {})
	m := model()
	m.width, m.height = 80, 24
	m = press(t, m, "P") // fetch in flight
	m = press(t, m, "q") // closed before the reply lands
	m = pressEvent(t, m, core.ThreadLoaded{ThreadID: "t1", Preview: true})
	if m.mode != "index" || m.preview || m.pager != nil {
		t.Fatalf("a stale preview reply must drop, mode=%q preview=%v pager=%v", m.mode, m.preview, m.pager != nil)
	}
}

// TestPreviewTargetWinsOverRacingOpen pins the FIFO ordering: a full
// open for another thread landing mid-preview must not stick - the
// preview target's reply re-asserts the popup over the index.
func TestPreviewTargetWinsOverRacingOpen(t *testing.T) {
	SetOpenHandler(func(threadID string, preview bool) {})
	defer SetOpenHandler(func(threadID string, preview bool) {})
	m := model()
	m.width, m.height = 80, 24
	m = press(t, m, "P") // preview fetch in flight
	lines := []core.Line{{Kind: core.LineBody, Text: "body line"}}
	m = pressEvent(t, m, core.ThreadLoaded{ThreadID: "t2", Lines: lines})
	if m.mode != "pager" {
		t.Fatalf("the racing open reply must open its pager first, mode=%q", m.mode)
	}
	m = pressEvent(t, m, core.ThreadLoaded{ThreadID: "t1", Preview: true, Lines: lines})
	if m.mode != "index" || !m.preview || m.previewThread != "t1" {
		t.Fatalf("the preview target must win, mode=%q preview=%v thread=%q", m.mode, m.preview, m.previewThread)
	}
	if pagerThreadID(m.pager) != "t1" {
		t.Fatalf("the preview box must hold the preview target's content, pager=%q", pagerThreadID(m.pager))
	}
}
