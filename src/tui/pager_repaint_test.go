// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

// Pager repaint contract on the tcell screen: a scroll changes the
// visible buffer (the double-press regression - the pre-glow pager's
// read-position indicator diffed to zero emitted bytes, so the first
// press rendered nothing), and the frame is always exactly height
// lines with the status row last, so a short pager never leaves the
// previous frame's rows on screen (the diff renderer's stale-row
// failure mode; the loop writes the full frame and tcell diffs
// internally).

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"notmutt/config"
	"notmutt/core"
)

// pushFrameCapture paints the frame and returns the buffer.
func pushFrameCapture(s *fakeScreen, frame string) []fakeCell {
	pushFrame(s, frame, 0, 0, false)
	return copyCells(cellsOf(s))
}

func pagerFrames(t *testing.T) (core.Line, []core.Line) {
	t.Helper()
	lines := []core.Line{
		{Kind: core.LineSubject, Text: "Subject: hello world this is a long subject line for the pager"},
		{Kind: core.LineHeader, Text: "From: Alice <alice@example.com>"},
		{Kind: core.LineHeader, Text: "Date: 2026-08-15 10:00"},
		{Kind: core.LineHeader, Text: "To: Bob <bob@example.com>"},
	}
	for i := 0; i < 30; i++ {
		lines = append(lines, core.Line{Kind: core.LineBody, Text: fmt.Sprintf("body line %d with some words to fill the width", i)})
	}
	lines = append(lines, core.Line{Kind: core.LineSignature, Text: "-- "})
	lines = append(lines, core.Line{Kind: core.LineSignature, Text: "Alice"})
	return core.Line{}, lines
}

func pagerFrame(p *pager, km map[string]string, st Styles, ui config.UI, d statusData) string {
	var b strings.Builder
	b.WriteString(p.render())
	b.WriteString("\n")
	b.WriteString(keyhintRow(km, 80))
	b.WriteString("\n")
	b.WriteString(statusLineWidth(st, ui, d, 80))
	return b.String()
}

// show escapes control sequences for readable output.
func show(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '\x1b':
			b.WriteString("ESC")
		case '\n':
			b.WriteString("\\n\n")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// TestQuoteColorDepthMapping pins the quote color rules: depth 0
// (plain body text) renders the normal text color - a plain mail never
// shares a custom color with the first reply layer quote - and quote
// depths 1-5 keep their own colors, five distinct layers.
func TestQuoteColorDepthMapping(t *testing.T) {
	cfg := config.Default()
	st := ResolveStyles(cfg.Theme, cfg.Palette)
	sg := sgrSetOf(st)
	if got := quoteColor(sg, 0); got != sg.normal {
		t.Fatal("depth 0 must render the normal text color")
	}
	if got := quoteColor(sg, 1); got == sg.normal {
		t.Fatal("the first quote layer must be distinct from the normal text")
	}
	seen := map[sgr]bool{}
	for d := 1; d <= 5; d++ {
		seen[quoteColor(sg, d)] = true
	}
	if len(seen) != 5 {
		t.Fatalf("quote depths 1-5 must keep 5 distinct colors, got %d", len(seen))
	}
}

// TestPagerMsgMark pins the thread-position tint: the whole message
// block (subject, headers, body, attachments) carries the mark's open
// - the recent-5 messages tint one color, the last other-side message
// a more prominent one; an error line keeps the error style (it must
// stay alarming), and an unmarked message dispatches normally. The
// tint opens are part of the pagerKey fingerprint, so a theme switch
// repaints marked messages.
func TestPagerMsgMark(t *testing.T) {
	cfg := config.Default()
	st := ResolveStyles(cfg.Theme, cfg.Palette)
	lines := []core.Line{
		{Kind: core.LineSubject, Text: "Subject: tinted"},
		{Kind: core.LineHeader, Text: "From: sender@example.com"},
		{Kind: core.LineBody, Text: "body"},
		{Kind: core.LineAttachment, Text: "[1] notes.txt"},
		{Kind: core.LineError, Text: "open failed"},
	}
	for _, tc := range []struct {
		mark  core.MsgMark
		opens []sgr // per-line expected open: nil = the default dispatch
	}{
		{core.MarkRecent, []sgr{st.sgr.pagerRecent, st.sgr.pagerRecent, st.sgr.pagerRecent, st.sgr.pagerRecent, st.sgr.pagerErr}},
		{core.MarkOther, []sgr{st.sgr.pagerOther, st.sgr.pagerOther, st.sgr.pagerOther, st.sgr.pagerOther, st.sgr.pagerErr}},
		{core.MarkNone, []sgr{st.sgr.pagerHdr, st.sgr.pagerHdrColor(0), st.sgr.normal, st.sgr.pagerAtt, st.sgr.pagerErr}},
	} {
		p := newPager("t", "m", lines)
		p.mark = tc.mark
		p.setSize(0, 0, st)
		for i, l := range lines {
			got := p.styleLine(i)
			want := tc.opens[i].open + l.Text + tc.opens[i].close
			if tc.opens[i].open == "" {
				want = l.Text
			}
			if got != want {
				t.Fatalf("mark %v line %d: styleLine = %q, want %q", tc.mark, i, got, want)
			}
		}
	}
	for _, g := range []sgr{st.sgr.pagerRecent, st.sgr.pagerOther} {
		if !strings.Contains(st.sgr.pagerKey, g.open) {
			t.Fatal("the tint opens must fingerprint the pagerKey (theme-switch repaint)")
		}
	}
}

// TestRepaintPagerScroll pins the double-press regression: the pre-glow
// pager moved a read-position indicator whose style-only change diffed
// to ZERO emitted bytes (the indicator wrap was overridden by the
// line's own fg+bg style, so the parsed cells were identical), and the
// first press rendered nothing. Line scrolling changes every visible
// line's content, so the first press must change the buffer.
func TestRepaintPagerScroll(t *testing.T) {
	cfg := config.Default()
	st := ResolveStyles(cfg.Theme, cfg.Palette)
	ui := cfg.UI
	_, lines := pagerFrames(t)
	km := config.Default().Bindings["pager"]
	d := statusData{view: "inbox", visible: 100}

	p := newPager("t1", "", lines)
	p.setSize(80, 22, st)
	s := newSim(t, 80, 24)

	open := pushFrameCapture(s, pagerFrame(p, km, st, ui, d))
	p.scrollDown(1)
	first := pushFrameCapture(s, pagerFrame(p, km, st, ui, d))
	p.scrollDown(1)
	second := pushFrameCapture(s, pagerFrame(p, km, st, ui, d))

	if sameCells(open, first) {
		t.Fatal("the first scroll press must change the frame (the double-press bug)")
	}
	if sameCells(first, second) {
		t.Fatal("the second scroll press must change the frame")
	}
}

// TestRepaintAfterSuspend pins the editor-return corruption: the
// suspend cycle wipes the cell buffer while pushFrame's row cache
// survives, so a post-resume push must re-emit every row. A row-skip
// against the fresh buffer leaves the cleared terminal blank everywhere
// except the changed rows, and later repaints skip the same rows
// forever - the compose dialog recovers only when a wholly different
// frame (leaving compose) re-pushes everything.
func TestRepaintAfterSuspend(t *testing.T) {
	s := newSim(t, 20, 4)
	pushFrameCapture(s, "aaaa\nbbbb\ncccc\ndddd")
	s.Clear() // the disengage/engage cycle wipes the cell buffer
	resetPushedFrames(s)
	frameB := "aaaa\nBBBB\ncccc\ndddd"
	pushFrameCapture(s, frameB)
	cs := cellsOf(s)
	for y, want := range strings.Split(frameB, "\n") {
		if got := rowText(cs, 20, y); got != want {
			t.Fatalf("row %d = %q, want %q - unchanged rows must re-emit after a suspend", y, got, want)
		}
	}
}

// TestRepaintEmptyPagerFrame pins the status-at-top regression: the
// pre-glow pager rendered fewer lines than the window for short or
// empty content, and the diff placed the keyhint and status rows at the
// top while stale rows stayed on screen. The frame must always be
// exactly height lines (blank rows padded), the status row last.
func TestRepaintEmptyPagerFrame(t *testing.T) {
	cfg := config.Default()
	st := ResolveStyles(cfg.Theme, cfg.Palette)
	ui := cfg.UI
	km := config.Default().Bindings["pager"]
	d := statusData{view: "inbox", visible: 100}

	// an index-like full frame first, so the screen has a previous paint
	var idx strings.Builder
	for i := 0; i < 22; i++ {
		idx.WriteString(st.Normal.Render(fmt.Sprintf("row %d", i)))
		idx.WriteByte('\n')
	}
	idx.WriteString(keyhintRow(config.Default().Bindings["index"], 80))
	idx.WriteByte('\n')
	idx.WriteString(statusLineWidth(st, ui, d, 80))
	s := newSim(t, 80, 24)
	pushFrameCapture(s, idx.String())

	// empty thread: pager renders 22 blank rows, keyhint and status
	// anchored at the bottom - a 24-line frame every time
	p := newPager("t1", "", nil)
	p.setSize(80, 22, st)
	frame := pagerFrame(p, km, st, ui, d)
	if got := strings.Count(frame, "\n") + 1; got != 24 {
		t.Fatalf("empty pager frame must be exactly 24 lines, got %d:\n%s", got, show(frame))
	}
	last := stripANSI(strings.Split(frame, "\n")[23])
	if !strings.Contains(last, "inbox") {
		t.Fatalf("the status row must be the frame's last line: %q", last)
	}

	pushFrameCapture(s, frame)
	cs := cellsOf(s)
	for r := 0; r < 22; r++ {
		if got := rowText(cs, 80, r); got != "" {
			t.Fatalf("row %d must be blank after the empty pager (the stale-row bug): %q", r, got)
		}
	}
	if got := rowText(cs, 80, 22); !strings.Contains(got, "scroll-bottom") {
		t.Fatalf("keyhint clobbered: %q", got)
	}
	if got := rowText(cs, 80, 23); !strings.Contains(got, "inbox") {
		t.Fatalf("status clobbered: %q", got)
	}
}

// TestPagerLazyLargeDoc pins the lazy-styling contract on a document
// much larger than the window (500 lines vs a 22-line window; the
// pre-fix style() pass re-styled the whole document on every resize -
// the 385ms stall): setSize styles only the visible band plus a
// margin, a same-width resize extends the band without re-styling old
// lines, a width or theme change restyles only the band, and scrolls
// into unstyled regions render the right lines.
func TestPagerLazyLargeDoc(t *testing.T) {
	cfg := config.Default()
	st := ResolveStyles(cfg.Theme, cfg.Palette)

	lines := make([]core.Line, 500)
	for i := range lines {
		lines[i] = core.Line{Kind: core.LineBody, Text: fmt.Sprintf("line %d of the large thread body", i)}
	}
	p := newPager("t1", "", lines)

	styled := func() int {
		n := 0
		for _, s := range p.styled {
			if s != "" {
				n++
			}
		}
		return n
	}
	want := func(offset, height, width int) string {
		var b strings.Builder
		for i := offset; i < offset+height; i++ {
			if i > offset {
				b.WriteByte('\n')
			}
			if i < len(lines) {
				b.WriteString(padCellsRight(fmt.Sprintf("line %d of the large thread body", i), width))
			} else {
				b.WriteString(strings.Repeat(" ", width))
			}
		}
		return b.String()
	}
	assert := func(offset, height, width, wantStyled int) {
		t.Helper()
		// styling is lazy at render time: the render styles the band,
		// then the cache count must reflect only that band
		got := stripANSI(p.render())
		if n := styled(); n != wantStyled {
			t.Fatalf("styled lines = %d, want %d", n, wantStyled)
		}
		if got != want(offset, height, width) {
			t.Fatalf("window content mismatch at offset %d x %d:\n%s\nvs:\n%s",
				offset, height, show(got), show(want(offset, height, width)))
		}
	}

	p.setSize(80, 22, st)
	// the band is window + 2*margin (42), never the 500-line document
	assert(0, 22, 80, 42)

	// a same-width resize (height only) extends the band by the 8 new
	// lines; the 42 old lines are not re-styled
	p.setSize(80, 30, st)
	assert(0, 30, 80, 50)

	// a width change invalidates the cache and restyles only the band
	p.setSize(100, 30, st)
	assert(0, 30, 100, 50)

	// a scroll into an unstyled region styles only the band it enters
	p.scrollDown(250)
	assert(250, 30, 100, 120)
	p.scrollBottom()
	assert(470, 30, 100, 170)
	p.scrollUp(10)
	assert(460, 30, 100, 180)
	p.scrollUp(10)
	assert(450, 30, 100, 190)

	// a height shrink keeps the band inside the styled range: nothing
	// restyles
	p.setSize(100, 25, st)
	assert(450, 25, 100, 190)

	// a theme switch (a different style set) invalidates like a width
	// change: the band restyles at the new colors
	st2 := DefaultStyles()
	st2.Pager.Quoted[0] = st2.Pager.Quoted[0].Foreground(lipgloss.Color("#ff0000"))
	st2.sgr = sgrSetOf(st2)
	p.setSize(100, 25, st2)
	assert(450, 25, 100, 65)
	// a jump to the tail styles the tail band on demand and renders the
	// document's last line
	p.scrollBottom()
	assert(475, 25, 100, 70)
	last := strings.Split(stripANSI(p.render()), "\n")[24]
	if last != padCellsRight("line 499 of the large thread body", 100) {
		t.Fatalf("bottom window must end at the document tail: %q", last)
	}
}

// TestPagerHeaderRotation pins the header block colors: the block
// cycles the theme's header-colors list (wrapping), a non-header line
// resets the run, and an empty list falls back to hdrdefault. The
// default theme carries the four onedark header colors.
func TestPagerHeaderRotation(t *testing.T) {
	st := DefaultStyles()
	st.Pager.HdrDefault = st.Pager.HdrDefault.Foreground(lipgloss.Color("#101010"))
	st.Pager.HeaderColors = []config.Style{
		{Fg: "#202020"}, {Fg: "#303030"}, {Fg: "#404040"}, {Fg: "#505050"},
	}
	st.sgr = sgrSetOf(st)
	col := func(i int) sgr { return st.sgr.pagerHdrColors[i] }
	if got := st.sgr.pagerHdrColor(0); got != col(0) {
		t.Fatalf("line 0 must take color 0")
	}
	if got := st.sgr.pagerHdrColor(3); got != col(3) {
		t.Fatalf("line 3 must take color 3")
	}
	if got := st.sgr.pagerHdrColor(4); got != col(0) {
		t.Fatalf("line 4 must wrap to color 0")
	}
	st2 := DefaultStyles()
	st2.sgr = sgrSetOf(st2)
	if got := st2.sgr.pagerHdrColor(0); got != st2.sgr.pagerDef {
		t.Fatalf("an empty list must fall back to hdrdefault")
	}
	// the run resets after a non-header line: message 2's block
	// restarts the cycle
	p := newPager("", "", []core.Line{
		{Kind: core.LineHeader, Text: "h0"},
		{Kind: core.LineHeader, Text: "h1"},
		{Kind: core.LineBody, Text: "body"},
		{Kind: core.LineHeader, Text: "h2"},
	})
	p.st = st
	opens := func(i int) string {
		s := p.styleLine(i)
		return s[:strings.Index(s, "m")+1]
	}
	if got := opens(0); got != col(0).open {
		t.Fatalf("run line 0: %q, want %q", got, col(0).open)
	}
	if got := opens(1); got != col(1).open {
		t.Fatalf("run line 1: %q, want %q", got, col(1).open)
	}
	if got := opens(3); got != col(0).open {
		t.Fatalf("a run after a body line must restart: %q, want %q", got, col(0).open)
	}
	cfg := config.Default()
	if len(cfg.Theme.Variants["dark"].Pager.HeaderColors) != 6 {
		t.Fatalf("the default theme must carry the six onedark quoted colors, got %d", len(cfg.Theme.Variants["dark"].Pager.HeaderColors))
	}
}
