package tui

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"notmutt/config"
	"notmutt/mail"
)

// harness drives the vendored uv.TerminalRenderer exactly like
// bubbletea's cursed_renderer.flush: a full-size screen buffer,
// cleared, drawn with the frame, diffed against the previous frame.
type repaintHarness struct {
	tr   *uv.TerminalRenderer
	out  *bytes.Buffer
	w, h int
}

func newRepaintHarness(w, h int) *repaintHarness {
	out := &bytes.Buffer{}
	tr := uv.NewTerminalRenderer(out, []string{"TERM=xterm-256color"})
	tr.SetFullscreen(true)
	tr.SetScrollOptim(true)
	return &repaintHarness{tr: tr, out: out, w: w, h: h}
}

// render draws one frame and returns the bytes emitted for it.
func (r *repaintHarness) render(frame string) string {
	r.out.Reset()
	buf := uv.NewScreenBuffer(r.w, r.h)
	buf.Clear()
	uv.NewStyledString(frame).Draw(buf, buf.Bounds())
	r.tr.Render(buf.RenderBuffer)
	r.tr.Flush()
	return r.out.String()
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

// maxRow extracts the highest target row in the output's CUP
// positioning sequences; the diff only positions rows it writes.
var cupRe = regexp.MustCompile(`\x1b\[(\d+);\d+H`)

func maxRow(s string) int {
	max := 0
	for _, m := range cupRe.FindAllStringSubmatch(s, -1) {
		if n, err := strconv.Atoi(m[1]); err == nil && n > max {
			max = n
		}
	}
	return max
}

func pagerFrames(t *testing.T) (mail.Line, []mail.Line) {
	t.Helper()
	lines := []mail.Line{
		{Kind: mail.LineSubject, Text: "Subject: hello world this is a long subject line for the pager"},
		{Kind: mail.LineHeader, Text: "From: Alice <alice@example.com>"},
		{Kind: mail.LineHeader, Text: "Date: 2026-08-15 10:00"},
		{Kind: mail.LineHeader, Text: "To: Bob <bob@example.com>"},
	}
	for i := 0; i < 30; i++ {
		lines = append(lines, mail.Line{Kind: mail.LineBody, Text: fmt.Sprintf("body line %d with some words to fill the width", i)})
	}
	lines = append(lines, mail.Line{Kind: mail.LineSignature, Text: "-- "})
	lines = append(lines, mail.Line{Kind: mail.LineSignature, Text: "Alice"})
	return mail.Line{}, lines
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

// TestRepaintPagerScroll pins the double-press regression: the pre-glow
// pager moved a read-position indicator whose style-only change diffed
// to ZERO emitted bytes (the indicator wrap was overridden by the
// line's own fg+bg style, so the parsed cells were identical), and the
// first press rendered nothing. Line scrolling changes every visible
// line's content, so the first press must repaint the window.
func TestRepaintPagerScroll(t *testing.T) {
	cfg := config.Default()
	st := ResolveStyles(cfg.Theme, cfg.Palette)
	ui := cfg.UI
	_, lines := pagerFrames(t)
	km := config.Default().Bindings["pager"]
	d := statusData{view: "inbox", visible: 100}

	p := newPager("t1", lines)
	p.setSize(80, 22, st)

	r := newRepaintHarness(80, 24)
	if open := r.render(pagerFrame(p, km, st, ui, d)); len(open) == 0 {
		t.Fatal("open frame emitted nothing")
	}

	p.scrollDown(1)
	first := r.render(pagerFrame(p, km, st, ui, d))
	p.scrollDown(1)
	second := r.render(pagerFrame(p, km, st, ui, d))

	if len(first) == 0 {
		t.Fatalf("first scroll press emitted nothing (the double-press bug):\n%s", show(first))
	}
	if len(second) == 0 {
		t.Fatalf("second scroll press emitted nothing:\n%s", show(second))
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

	// an index-like full frame first, so the diff has a previous frame
	var idx strings.Builder
	for i := 0; i < 22; i++ {
		idx.WriteString(st.Normal.Render(fmt.Sprintf("row %d", i)))
		idx.WriteByte('\n')
	}
	idx.WriteString(keyhintRow(config.Default().Bindings["index"], 80))
	idx.WriteByte('\n')
	idx.WriteString(statusLineWidth(st, ui, d, 80))

	r := newRepaintHarness(80, 24)
	r.render(idx.String())

	// empty thread: pager renders 22 blank rows, keyhint and status
	// anchored at the bottom - a 24-line frame every time
	p := newPager("t1", nil)
	p.setSize(80, 22, st)
	frame := pagerFrame(p, km, st, ui, d)
	if got := strings.Count(frame, "\n") + 1; got != 24 {
		t.Fatalf("empty pager frame must be exactly 24 lines, got %d:\n%s", got, show(frame))
	}
	last := stripANSI(strings.Split(frame, "\n")[23])
	if !strings.Contains(last, "inbox") {
		t.Fatalf("the status row must be the frame's last line: %q", last)
	}

	emitted := r.render(frame)
	// the diff rewrites the blanked content rows line by line and leaves
	// the keyhint/status rows alone; the pre-glow bug wrote a 3-line
	// frame at the top and cleared to end of display (ED), orphaning
	// the stale rows on screen
	if strings.Contains(emitted, "\x1b[J") {
		t.Fatalf("the diff must not clear to end of display (the status-at-top bug):\n%s", show(emitted))
	}
}
