// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"strings"
	"testing"

	"notmutt/core"
)

// openPreview presses P and delivers the preview content for id.
func openPreview(t *testing.T, m Model, id string, lines []core.Line) Model {
	m = press(t, m, "P")
	return pressEvent(t, m, core.ThreadLoaded{ThreadID: id, Preview: true, Lines: lines})
}

// TestAiStreamAppendRenders pins the summary stream: the first delta
// replaces the placeholder, token-per-event deltas merge into a
// flowing line, and newline deltas start fresh lines - all visible as
// they arrive.
func TestAiStreamAppendRenders(t *testing.T) {
	m := model()
	next, _ := m.Update(EventMsg{Event: core.AiStarted{JobID: "j1", ThreadID: "t1"}})
	m = next
	for _, d := range []string{"alpha ", "beta ", "gamma", "\n", "next line"} {
		next, _ = m.Update(EventMsg{Event: core.AiChunk{JobID: "j1", Text: d}})
		m = next
	}
	got := stripANSI(m.render())
	wantAll(t, got, "alpha beta gamma", "next line")
	if strings.Contains(got, "summarizing...") {
		t.Fatalf("the placeholder must be replaced by the first delta:\n%s", got)
	}
}

// TestPreviewShrinkNoPanic pins the stale-layout bug: a render with a
// long body builds the preview's expanded layout (doc/imgFrom), and
// replacing the body with a shorter one must drop the layout - the
// old row map pointed past the new, shorter line list.
func TestPreviewShrinkNoPanic(t *testing.T) {
	m := openDialogue(t, model(), "t1")
	m.composeTab().Body = strings.Repeat("a line of reply text\n", 20)
	if frame := stripANSI(m.render()); !strings.Contains(frame, "a line of reply text") {
		t.Fatalf("the long body must render in the preview:\n%s", frame)
	}
	// the neovim edit: the reply is cut down to 11 lines
	m.composeTab().Body = "short reply\n"
	if frame := stripANSI(m.render()); !strings.Contains(frame, "short reply") {
		t.Fatalf("the shortened body must render in the preview:\n%s", frame)
	}
}

// TestPreviewStaysInIndexAndShowsBox pins the preview surface: p opens
// the popup over the index (mode stays "index"), the box carries the
// row's subject as its title, the loaded content lands inside it, and
// the keyhint derives the pager keys (activeBindings flips).
func TestPreviewStaysInIndexAndShowsBox(t *testing.T) {
	stubOpenHandler(t)
	m := model()
	m = openPreview(t, m, "t1", []core.Line{{Kind: core.LineBody, Text: "body line"}})
	if !m.preview || m.mode != "index" {
		t.Fatalf("p must open the preview popup over the index, preview=%v mode=%q", m.preview, m.mode)
	}
	if m.previewThread != "t1" || m.previewTitle == "" {
		t.Fatalf("preview target missing: thread=%q title=%q", m.previewThread, m.previewTitle)
	}
	out := stripANSI(m.View())
	// the scroll hint lists every pager scroll key, arrows included
	// (the live config's arrow overlay is part of the fixture)
	wantAll(t, out, m.previewTitle, "body line", "down/j/k/up scroll  enter open  q close", "╭"+strings.Repeat("─", 2), "╰─", "tab-prev")
	if strings.Contains(out, "cursor-down") {
		t.Fatalf("the keyhint must derive the pager keys during preview:\n%s", out)
	}
}

// TestPreviewScrollsAndCloses pins the popup keys: the pager scroll
// keys scroll the box (index cursor untouched), q closes it and drops
// the pager. A long fixture body makes the preview window movable.
func TestPreviewScrollsAndCloses(t *testing.T) {
	stubOpenHandler(t)
	m := model()
	lines := make([]core.Line, 40)
	for i := range lines {
		lines[i] = core.Line{Kind: core.LineBody, Text: "line"}
	}
	m = openPreview(t, m, "t1", lines)
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
	SetOpenHandler(func(threadID, msgID string, preview, headers bool, _ int) {
		calls = append(calls, fmt.Sprintf("%s:%v", threadID, preview))
	})
	defer SetOpenHandler(func(threadID, msgID string, preview, headers bool, _ int) {})
	m := model()
	lines := []core.Line{{Kind: core.LineBody, Text: "body line"}}
	m = openPreview(t, m, "t1", lines)
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

// TestPreviewStaleReplyDrops pins the async guard: a preview reply landing after the popup closed must not force the thread open.
func TestPreviewStaleReplyDrops(t *testing.T) {
	stubOpenHandler(t)
	m := model()
	m = press(t, m, "P") // fetch in flight
	m = press(t, m, "q") // closed before the reply lands
	m = pressEvent(t, m, core.ThreadLoaded{ThreadID: "t1", Preview: true})
	if m.mode != "index" || m.preview || m.pager != nil {
		t.Fatalf("a stale preview reply must drop, mode=%q preview=%v pager=%v", m.mode, m.preview, m.pager != nil)
	}
}

// TestPreviewTargetWinsOverRacingOpen pins the FIFO ordering: a full open for another thread landing mid-preview must not stick - the preview target's reply re-asserts the popup.
func TestPreviewTargetWinsOverRacingOpen(t *testing.T) {
	stubOpenHandler(t)
	m := model()
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
