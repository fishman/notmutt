// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

// The load-remote-images pipeline: image lines stay collapsed
// placeholders (privacy gate - the bytes are never decoded until the
// toggle), the toggle expands them to Image.Rows rows, and the
// terminal paint emits the decoded+scaled pixels after the frame
// (protocol by config + environment, sixel by default). All paints
// flow through imageWriter - nil in the frame tests, a buffer here.

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"strings"

	"github.com/mattn/go-sixel"
	"testing"

	"notmutt/config"
	"notmutt/core"
	"notmutt/mail"
)

// TestSetCellSize pins the ioctl-derived cell size: window pixels over
// cell counts, out-of-range or missing pixels keep the 10x20 defaults.
func TestSetCellSize(t *testing.T) {
	saveW, saveH := imgCellW, imgCellH
	defer func() { imgCellW, imgCellH = saveW, saveH }()
	imgCellW, imgCellH = 10, 20

	setCellSize(159, 33, 1908, 990) // the measured foot/tmux geometry
	if imgCellW != 12 || imgCellH != 30 {
		t.Fatalf("cell size = %dx%d, want 12x30", imgCellW, imgCellH)
	}
	for name, args := range map[string][4]int{
		"no pixels":  {159, 33, 0, 0},
		"corrupt px": {159, 33, 20000, 990},
		"no cells":   {0, 0, 1908, 990},
		"tiny cell":  {159, 33, 5, 5},
	} {
		imgCellW, imgCellH = 10, 20
		setCellSize(args[0], args[1], args[2], args[3])
		if imgCellW != 10 || imgCellH != 20 {
			t.Fatalf("%s: must keep the defaults, got %dx%d", name, imgCellW, imgCellH)
		}
	}
}

// trimRows strips the per-row width padding before comparisons.
func trimRows(s string) []string {
	rows := strings.Split(s, "\n")
	for i, r := range rows {
		rows[i] = strings.TrimRight(r, " ")
	}
	return rows
}

// testPNG renders a deterministic w x h PNG (noise-ish pixels so the
// encoded size is meaningful for the chunking tests).
func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x * 31 % 256), uint8(y * 17 % 256), uint8(x + y), 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// testImg is an image with noise-ish pixels (the x*y term kills the
// per-row correlation, so the encoded size is meaningful for the
// chunking tests).
func testImg(w, h int) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x * 31 % 256), uint8(y * 17 % 256), uint8(x * y % 256), 255})
		}
	}
	return img
}

func TestPagerImageLayout(t *testing.T) {
	cfg := config.Default()
	st := ResolveStyles(cfg.Theme, cfg.Palette)
	lines := []core.Line{
		{Kind: core.LineBody, Text: "before"},
		{Kind: core.LineBody, Text: "[image]", Image: &core.Image{Alt: "[image]", Cols: 40, Rows: 3}},
		{Kind: core.LineBody, Text: "after"},
	}
	p := newPager("t1", lines)
	p.setSize(80, 22, st)

	// collapsed default: 1:1, the Alt row shows
	if got := len(p.vp.lines); got != 3 {
		t.Fatalf("collapsed doc must be 1:1, got %d rows", got)
	}
	if got := stripANSI(p.render()); !strings.Contains(got, "[image]") {
		t.Fatalf("collapsed render must show the alt text:\n%s", got)
	}

	// the toggle expands the image line to its rows
	p.setImages(true)
	if got := len(p.vp.lines); got != 5 {
		t.Fatalf("expanded doc must be 5 rows, got %d", got)
	}
	rows := trimRows(stripANSI(p.render()))
	if rows[0] != "before" || rows[4] != "after" {
		t.Fatalf("expansion must keep the text order:\n%s", strings.Join(rows, "\n"))
	}
	for _, r := range rows[1:4] {
		if r != "" {
			t.Fatalf("image rows must carry no text, got %q", r)
		}
	}

	// scrolling moves over the expanded rows (a 2-row window forces
	// scroll space; the image rows carry no text)
	p.setSize(80, 2, st)
	p.scrollDown(3)
	rows = trimRows(stripANSI(p.render()))
	if rows[0] != "" || rows[1] != "after" {
		t.Fatalf("scroll must move by expanded rows:\n%s", strings.Join(rows[:3], "\n"))
	}

	// toggle back: collapsed, the Alt row restores
	p.setImages(false)
	if got := len(p.vp.lines); got != 3 {
		t.Fatalf("collapsed doc must be 1:1 again, got %d rows", got)
	}
	if got := stripANSI(p.render()); !strings.Contains(got, "[image]") {
		t.Fatalf("toggle-off must restore the alt text:\n%s", got)
	}
}

func TestPagerVisibleImages(t *testing.T) {
	cfg := config.Default()
	st := ResolveStyles(cfg.Theme, cfg.Palette)
	lines := []core.Line{}
	for range 40 {
		lines = append(lines, core.Line{Kind: core.LineBody, Text: "a"})
	}
	lines = append(lines, core.Line{Kind: core.LineBody, Text: "[img]", Image: &core.Image{Alt: "[img]", Cols: 40, Rows: 3}})
	for range 40 {
		lines = append(lines, core.Line{Kind: core.LineBody, Text: "b"})
	}
	p := newPager("t1", lines)
	p.setSize(80, 22, st)
	p.setImages(true)

	// scrolled before the block: the window sees no image
	if blocks := p.visibleImages(); len(blocks) != 0 {
		t.Fatalf("window above the image must see no block, got %+v", blocks)
	}

	// scrolled into the block: the doc anchor survives
	p.scrollDown(40)
	blocks := p.visibleImages()
	if len(blocks) != 1 || blocks[0].line != 40 || blocks[0].doc != 40 || blocks[0].rows != 3 {
		t.Fatalf("window must see the image block, got %+v", blocks)
	}

	// scrolled past: the window sees no image
	p.scrollDown(21)
	if blocks = p.visibleImages(); len(blocks) != 0 {
		t.Fatalf("scrolled-past image must leave the window, got %+v", blocks)
	}

	// collapsed: the doc stops expanding, the block still lists (the
	// decoder's trigger)
	p.scrollUp(21)
	p.setImages(false)
	if blocks = p.visibleImages(); len(blocks) != 1 {
		t.Fatalf("collapsed images must still list, got %+v", blocks)
	}
	if got := len(p.vp.lines); got != 81 {
		t.Fatalf("collapse must return the doc to 1:1, got %d rows", got)
	}
}

func TestDecodeImage(t *testing.T) {
	// 400x900 px at an 80x30 window: the row budget binds first
	// (aspect kept: 2/3), the pixel dims snap to exact cell multiples
	img, cols, rows, err := decodeImage(testPNG(t, 400, 900), 80, 30, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if cols != 26 || rows != 30 {
		t.Fatalf("scale must be 26x30 cells, got %dx%d", cols, rows)
	}
	if b := img.Bounds(); b.Dx() != cols*imgCellW || b.Dy() != rows*imgCellH {
		t.Fatalf("pixel dims must snap to cell multiples: %dx%d", b.Dx(), b.Dy())
	}

	// the same image in a 100-row window binds on the width instead:
	// a tall chart renders at its natural aspect, not squashed
	if _, cols, rows, err = decodeImage(testPNG(t, 400, 900), 80, 100, 0, 0); err != nil {
		t.Fatal(err)
	}
	if cols != 40 || rows != 45 {
		t.Fatalf("tall image must keep the width-bound aspect, got %dx%d", cols, rows)
	}

	// a wide image binds on the width cap
	if _, cols, rows, err = decodeImage(testPNG(t, 2000, 10), 80, 100, 0, 0); err != nil {
		t.Fatal(err)
	}
	if cols != 80 || rows != 1 {
		t.Fatalf("wide image must fill the window width, got %dx%d", cols, rows)
	}

	// a tiny image still occupies one cell (no zero-size expansion)
	if _, cols, rows, err = decodeImage(testPNG(t, 3, 3), 80, 100, 0, 0); err != nil {
		t.Fatal(err)
	}
	if cols != 1 || rows != 1 {
		t.Fatalf("tiny image must floor to one cell, got %dx%d", cols, rows)
	}

	// garbage bytes never decode
	if _, _, _, err := decodeImage([]byte("not an image"), 80, 100, 0, 0); err == nil {
		t.Fatal("garbage must fail the decode")
	}

	// jpeg/gif/webp decode via the blank-imported registrations: mail
	// charts are rarely png, and an undecoded chart never renders
	var jbuf bytes.Buffer
	src, _, _ := image.Decode(bytes.NewReader(testPNG(t, 400, 300)))
	if err := jpeg.Encode(&jbuf, src, nil); err != nil {
		t.Fatal(err)
	}
	if _, cols, rows, err := decodeImage(jbuf.Bytes(), 80, 100, 0, 0); err != nil || cols != 40 || rows != 15 {
		t.Fatalf("jpeg must decode at its aspect, got %dx%d err=%v", cols, rows, err)
	}
	var gbuf bytes.Buffer
	if err := gif.Encode(&gbuf, src, nil); err != nil {
		t.Fatal(err)
	}
	if _, cols, rows, err := decodeImage(gbuf.Bytes(), 80, 100, 0, 0); err != nil || cols != 40 || rows != 15 {
		t.Fatalf("gif must decode at its aspect, got %dx%d err=%v", cols, rows, err)
	}

	// a declared display size is the target: a 200x300 image declared
	// 600x600 upscales to 600px wide (2x) - with no declaration the
	// scale would cap at 1 and render 200px
	if _, cols, rows, err = decodeImage(testPNG(t, 200, 300), 80, 100, 600, 600); err != nil {
		t.Fatal(err)
	}
	if cols != 40 || rows != 30 {
		t.Fatalf("declared size must upscale the image, got %dx%d", cols, rows)
	}

	// the window cap still binds: a declared 10000px image cannot leave
	// the view, and a one-axis declaration scales the other axis with
	// it (aspect kept)
	if _, cols, rows, err = decodeImage(testPNG(t, 400, 300), 80, 100, 10000, 0); err != nil {
		t.Fatal(err)
	}
	if cols != 80 || rows != 30 {
		t.Fatalf("declared size must cap at the window, got %dx%d", cols, rows)
	}
}

// stubCaps is the negotiated-capability stand-in: Sixel answers from a
// field, so a test pins the screen's DA reply.
type stubCaps struct{ sixel bool }

func (s stubCaps) Sixel() bool { return s.sixel }

func TestDetectImageProtocol(t *testing.T) {
	base := config.Default()
	kitty := config.Default()
	kitty.Pager.ImageProtocol = "kitty"
	cases := []struct {
		cfg  config.Pager
		env  map[string]string
		caps stubCaps
		want string
	}{
		// kitty is opt-in: the kitty environment alone never selects it
		{base.Pager, map[string]string{"KITTY_WINDOW_ID": "1"}, stubCaps{}, ""},
		{kitty.Pager, map[string]string{"KITTY_WINDOW_ID": "1"}, stubCaps{}, "kitty"},
		{kitty.Pager, map[string]string{"TERM_PROGRAM": "wezterm"}, stubCaps{}, "kitty"},
		{kitty.Pager, map[string]string{"TERM_PROGRAM": "ghostty"}, stubCaps{}, "kitty"},
		{kitty.Pager, map[string]string{}, stubCaps{}, ""},
		// sixel when the screen's DA negotiation reported it
		{base.Pager, map[string]string{}, stubCaps{sixel: true}, "sixel"},
		// a negative reply selects nothing
		{base.Pager, map[string]string{}, stubCaps{}, ""},
	}
	for _, c := range cases {
		for _, k := range []string{"KITTY_WINDOW_ID", "TERM_PROGRAM"} {
			t.Setenv(k, c.env[k])
		}
		if got := detectImageProtocol(c.cfg, c.caps); got != c.want {
			t.Errorf("detectImageProtocol(%v, %+v) = %q, want %q", c.env, c.caps, got, c.want)
		}
	}
	// a never-engaged screen (nil) selects nothing either
	if got := detectImageProtocol(base.Pager, nil); got != "" {
		t.Errorf("detectImageProtocol(nil screen) = %q, want \"\"", got)
	}
}

// TestDetectImageProtocolTmux pins the tmux path: tmux answers DA1
// itself (build-time reply), so under tmux the tmux query is tried
// first; the negotiation stays the fallback (both share the same build
// flag, so they cannot genuinely disagree).
func TestDetectImageProtocolTmux(t *testing.T) {
	t.Setenv("TMUX", "x")
	orig := tmuxSixel
	defer func() { tmuxSixel = orig }()
	tmuxSixel = func() bool { return true }
	if got := detectImageProtocol(config.Default().Pager, stubCaps{}); got != "sixel" {
		t.Fatalf("tmux with sixel support: got %q, want sixel", got)
	}
	tmuxSixel = func() bool { return false }
	if got := detectImageProtocol(config.Default().Pager, stubCaps{sixel: true}); got != "sixel" {
		t.Fatalf("query false + negotiated sixel: got %q, want sixel (fallback)", got)
	}
	if got := detectImageProtocol(config.Default().Pager, stubCaps{}); got != "" {
		t.Fatalf("tmux without sixel: got %q, want empty", got)
	}
}

func TestKittyEncode(t *testing.T) {
	var buf bytes.Buffer
	kittyEncode(&buf, testImg(600, 600))
	out := buf.String()
	if !strings.HasPrefix(out, "\x1b_Gf=100,t=d,a=T,m=1;") {
		t.Fatalf("first chunk must open the transmit frame, got %q", show(out[:24]))
	}
	if !strings.HasSuffix(out, "\x1b\\") || !strings.Contains(out, "\x1b_Gm=0;") {
		t.Fatalf("last chunk must close with m=0:\n%s", show(out))
	}
	if !strings.Contains(out, "\x1b_Gm=1;") {
		t.Fatalf("a large image must continue with m=1 chunks")
	}
	// every payload chunk fits the frame limit
	for _, ch := range strings.Split(out, "\x1b_G")[1:] {
		semi := strings.IndexByte(ch, ';')
		payload := ch[semi+1 : len(ch)-2] // strip the terminator
		if len(payload) > kittyChunk {
			t.Fatalf("chunk exceeds the kitty limit: %d > %d", len(payload), kittyChunk)
		}
	}
}

func TestSixelEncode(t *testing.T) {
	var buf bytes.Buffer
	sixelEncode(&buf, testImg(100, 100))
	out := buf.String()
	if !strings.HasPrefix(out, "\x1bP") || !strings.HasSuffix(out, "\x1b\\") {
		t.Fatalf("sixel must be a complete DCS sequence, got %q", show(out[:16]))
	}
}

// TestComposeImages pins the offscreen batch compose: two images at
// different offsets land in one canvas whose dims are the rect union
// (exact cell multiples), each image at its own pixel offset, and the
// gap cells between them transparent (the page background shows
// through).
func TestComposeImages(t *testing.T) {
	one := testImg(20, 40) // 2x2 cells
	two := testImg(40, 40) // 4x2 cells
	paints := []imgPaint{
		{rect: cellRect{x: 2, y: 3, w: 2, h: 2}, img: one, top: 0, h: 2},
		{rect: cellRect{x: 6, y: 3, w: 4, h: 2}, img: two, top: 0, h: 2},
	}
	canvas, union := composeImages(paints)
	if union.x != 2 || union.y != 3 || union.w != 8 || union.h != 2 {
		t.Fatalf("union must span both rects, got %+v", union)
	}
	b := canvas.Bounds()
	if b.Dx() != 8*imgCellW || b.Dy() != 2*imgCellH {
		t.Fatalf("canvas must snap to cell multiples: %dx%d", b.Dx(), b.Dy())
	}
	if _, _, _, a := canvas.At(3*imgCellW, 0).RGBA(); a != 0 {
		t.Fatalf("the gap must stay transparent, alpha=%d", a)
	}
	// the second image lands at its own offset, not the canvas origin
	if got := canvas.At((6-2)*imgCellW, 0); got != two.At(0, 0) {
		t.Fatalf("the offset image must land at its rect, got %v", got)
	}
}

func TestClearRects(t *testing.T) {
	var buf bytes.Buffer
	clearRects(&buf, []cellRect{{x: 3, y: 5, w: 10, h: 2, bg: "#112233"}})
	got := buf.String()
	if !strings.HasPrefix(got, "\x1b[6;4H\x1b[48;2;17;34;51m\x1b[K") {
		t.Fatalf("clear must home and EL-fill with the block bg, got %q", show(got))
	}
	if !strings.Contains(got, "\x1b[7;4H") {
		t.Fatalf("clear must erase every row, got %q", show(got))
	}
	if !strings.HasSuffix(got, "\x1b[0m") {
		t.Fatalf("clear must reset the fill, got %q", show(got))
	}

	buf.Reset()
	clearRects(&buf, []cellRect{{x: 0, y: 0, w: 3, h: 1}}) // no bg: the terminal default
	if strings.Contains(buf.String(), "48;2") {
		t.Fatalf("an unset block bg must not emit an SGR fill")
	}
}

func TestBgHexOf(t *testing.T) {
	cfg := config.Default()
	if got := bgHexOf(ResolveStyles(cfg.Theme, cfg.Palette).Normal.GetBackground()); got == "" {
		t.Fatalf("the default theme must have a plain hex background, got %q", got)
	}
	if got := bgHexOf(nil); got != "" {
		t.Fatalf("nil color must be empty, got %q", got)
	}
}

// TestModelRenderImagesToggle runs the full path: open an html-only
// message with an inline image, verify the placeholder gate, the
// alt+i toggle expansion, the terminal paint, and the toggle-off
// clear.
func TestModelRenderImagesToggle(t *testing.T) {
	cfg := config.Default()
	cfg.Pager.ImageProtocol = "kitty"
	st := config.NewStore(cfg)
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Tags: []string{"inbox"}},
	})})
	m := New(view, nil, testBindings(), testTagActions(), nil, st, cfg.UI)
	m.imgProto = "kitty" // the engaged screen's negotiation (unit-stubbed)
	m.width, m.height = 80, 100
	png := testPNG(t, 100, 200)
	body := "<p>before</p><img src=\"data:image/png;base64," + base64.StdEncoding.EncodeToString(png) + "\"><p>after</p>"
	SetOpenHandler(func(threadID string, preview, headers bool, _ int) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID: threadID,
			Lines:    mail.RenderHTML(body, nil, 0),
		}})
		m = next
	})
	press(t, m, "enter") // discard: the open handler rebinds m
	if m.mode != "pager" {
		t.Fatalf("open must switch to pager, mode=%q", m.mode)
	}

	var buf bytes.Buffer
	old := imageWriter
	imageWriter = &buf
	defer func() { imageWriter = old }()

	// the loop split: the stale rects clear before the frame, the
	// blocks paint after it
	paint := func() {
		next, stale := m.paintRects()
		clearRects(imageWriter, stale)
		m.paintImages(next)
	}

	// the privacy gate: the placeholder renders, the paint writes nothing
	paint()
	if buf.Len() != 0 {
		t.Fatalf("collapsed images must not paint, got %d bytes", buf.Len())
	}
	if out := m.View(); !strings.Contains(out, "[image]") {
		t.Fatalf("the placeholder must render before the toggle:\n%s", out)
	}

	// the toggle expands; the paint emits a kitty frame at the block
	m = press(t, m, "alt+i")
	if !m.renderDue {
		t.Fatalf("the toggle must defer the paint")
	}
	m, _ = m.Update(frameTick{})
	out := m.View()
	if strings.Contains(out, "[image]") || !strings.Contains(out, "after") {
		t.Fatalf("the toggle must expand the image and keep the text:\n%s", out)
	}
	paint()
	// the block sits at doc row 2 (before, blank, image) - screen row 4
	if !strings.HasPrefix(buf.String(), "\x1b[4;1H\x1b_Gf=") {
		t.Fatalf("the paint must emit a kitty frame at the image rows, got %q", show(buf.String()[:24]))
	}
	if len(m.painted) != 1 {
		t.Fatalf("the paint must track one rect, got %d", len(m.painted))
	}

	// toggle off: the second press (the cycle: off -> remote -> off)
	// clears the rect BEFORE the collapsed frame renders
	m = press(t, m, "alt+i")
	if !strings.Contains(buf.String(), "\x1b[4;1H") {
		t.Fatalf("toggle-off must clear the painted rect")
	}
	m, _ = m.Update(frameTick{})
	if out := m.View(); !strings.Contains(out, "[image]") {
		t.Fatalf("toggle-off must restore the placeholder:\n%s", out)
	}
	if len(m.painted) != 0 {
		t.Fatalf("toggle-off must drop the rect bookkeeping")
	}
}

// TestModelRenderImagesRemote pins the remote mode: the alt+i press
// arms the fetch seam for the visible urls, the ImageFetched reply
// attaches to the image lines, and a stale reply after the mode
// cycled away drops - network data never decodes outside the remote
// mode.
func TestModelRenderImagesRemote(t *testing.T) {
	cfg := config.Default()
	cfg.Pager.ImageProtocol = "kitty"
	st := config.NewStore(cfg)
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Tags: []string{"inbox"}},
	})})
	m := New(view, nil, testBindings(), testTagActions(), nil, st, cfg.UI)
	m.imgProto = "kitty" // the engaged screen's negotiation (unit-stubbed)
	m.width, m.height = 80, 100
	body := "<p>before</p><img src=\"http://example.com/x.png\"><p>after</p>"
	SetOpenHandler(func(threadID string, preview, headers bool, _ int) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID:   threadID,
			RenderMode: core.RenderHTML,
			Mime:       "text/html",
			Lines:      mail.RenderHTML(body, nil, 0),
		}})
		m = next
	})
	var fetched []string
	SetImageFetchHandler(func(url string) { fetched = append(fetched, url) })
	press(t, m, "enter") // discard: the open handler rebinds m
	if m.mode != "pager" {
		t.Fatalf("open must switch to pager, mode=%q", m.mode)
	}

	// the first alt+i press arms the fetch (there is no local mode
	// anymore - embedded cid:/data: bytes render, http(s) fetch on
	// demand)
	m = press(t, m, "alt+i")
	if len(fetched) != 1 || fetched[0] != "http://example.com/x.png" {
		t.Fatalf("the toggle must fetch the visible url, got %v", fetched)
	}

	// the reply attaches: the bytes land on the image line
	imgLine := func() int {
		for i := range m.pager.lines {
			if m.pager.lines[i].Image != nil && m.pager.lines[i].Image.URL == "http://example.com/x.png" {
				return i
			}
		}
		return -1
	}
	if i := imgLine(); i < 0 {
		t.Fatalf("the render must carry an image line for the url")
	}

	next, _ := m.Update(EventMsg{Event: core.ImageFetched{
		URL:  "http://example.com/x.png",
		Data: testPNG(t, 40, 20),
	}})
	m = next
	if i := imgLine(); i < 0 || len(m.pager.lines[i].Image.Data) == 0 {
		t.Fatalf("the reply must attach its bytes to the image line")
	}
	m, _ = m.Update(frameTick{})
	if out := m.View(); strings.Contains(out, "[image]") {
		t.Fatalf("the fetched image must expand:\n%s", out)
	}

	// the mode cycled away: a stale reply drops without touching the
	// lines (off mode, second press)
	m = press(t, m, "alt+i")
	if m.imgMode != 0 {
		t.Fatalf("the second press must cycle to off, mode=%d", m.imgMode)
	}
	next, _ = m.Update(EventMsg{Event: core.ImageFetched{
		URL:  "http://example.com/x.png",
		Data: []byte("stale bytes"),
	}})
	m = next
	if i := imgLine(); i >= 0 && len(m.pager.lines[i].Image.Data) > 0 && m.pager.lines[i].Image.Data[0] == 's' {
		t.Fatalf("a stale reply must never overwrite the remote data")
	}
}

// TestModelRenderSemianalysisImages runs the real fixture through the
// remote-images pipeline: the semianalysis newsletter's http(s) srcs
// (icons + article images) must fetch on the alt+i press, expand when
// the bytes arrive, and collapse on the toggle-off press. Pins the
// fixture's image flow, not its layout.
func TestModelRenderSemianalysisImages(t *testing.T) {
	html, err := os.ReadFile("../../testing/semianalysis.html")
	if err != nil {
		t.Skip(err)
	}
	cfg := config.Default()
	cfg.Pager.ImageProtocol = "kitty"
	st := config.NewStore(cfg)
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Tags: []string{"inbox"}},
	})})
	m := New(view, nil, testBindings(), testTagActions(), nil, st, cfg.UI)
	m.imgProto = "kitty" // the engaged screen's negotiation (unit-stubbed)
	m.width, m.height = 80, 100
	SetOpenHandler(func(threadID string, preview, headers bool, _ int) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID:   threadID,
			RenderMode: core.RenderHTML,
			Mime:       "text/html",
			Lines:      mail.RenderHTML(string(html), nil, 0),
		}})
		m = next
	})
	var fetched []string
	SetImageFetchHandler(func(url string) { fetched = append(fetched, url) })
	press(t, m, "enter") // discard: the open handler rebinds m
	if m.mode != "pager" {
		t.Fatalf("open must switch to pager, mode=%q", m.mode)
	}

	// the collapsed gate: placeholders render, nothing fetched
	if out := m.View(); !strings.Contains(out, "[image]") {
		t.Fatalf("the fixture must render image placeholders:\n%s", out)
	}

	// the press arms every remote src (all https)
	m = press(t, m, "alt+i")
	if len(fetched) == 0 {
		t.Fatalf("the fixture's visible http(s) srcs must fetch, got none")
	}
	for _, u := range fetched {
		if !strings.HasPrefix(u, "https://") {
			t.Fatalf("the fixture must fetch https srcs only, got %q", u)
		}
	}
	before := strings.Count(m.View(), "[image]")

	// the replies attach and expand the placeholders
	for _, u := range fetched {
		next, _ := m.Update(EventMsg{Event: core.ImageFetched{URL: u, Data: testPNG(t, 40, 20)}})
		m = next
	}
	m, _ = m.Update(frameTick{})
	after := strings.Count(m.View(), "[image]")
	if after >= before {
		t.Fatalf("the fetched images must expand: [image] count %d -> %d", before, after)
	}

	// the buttons row carries its 4 icons as inline images at
	// increasing offsets, and the paint emits one rect per icon at
	// that offset (the icons are real images, not placeholder text)
	var iconLine *core.Line
	for i := range m.pager.lines {
		if len(m.pager.lines[i].Imgs) == 4 {
			iconLine = &m.pager.lines[i]
			break
		}
	}
	if iconLine == nil {
		t.Fatalf("the buttons row must carry the 4 inline images")
	}
	for i := 1; i < len(iconLine.Imgs); i++ {
		if iconLine.Imgs[i].X <= iconLine.Imgs[i-1].X {
			t.Fatalf("inline images must sit at increasing offsets, got %d then %d",
				iconLine.Imgs[i-1].X, iconLine.Imgs[i].X)
		}
	}
	next, stale := m.paintRects()
	var xs []int
	for _, p := range next {
		if p.rect.w < 1 || p.rect.h < 1 {
			t.Fatalf("an icon rect must cover cells, got %+v", p.rect)
		}
		if p.rect.x > 0 {
			xs = append(xs, p.rect.x)
		}
	}
	if len(xs) < 4 {
		t.Fatalf("the 4 icons must paint at their offsets, got %d rects with x>0", len(xs))
	}
	if len(stale) != 0 {
		t.Fatalf("the first paint must not stale rects, got %d", len(stale))
	}

	// the toggle-off press restores the placeholders
	m = press(t, m, "alt+i")
	m, _ = m.Update(frameTick{})
	if out := m.View(); !strings.Contains(out, "[image]") {
		t.Fatalf("toggle-off must restore the placeholders:\n%s", out)
	}
}

// TestModelRenderImagesScrollCycle pins the scroll behavior: a below-
// fold block image decodes when scrolled into view, its rect clears
// when scrolled past, and the same rect paints again on the way back
// (the decode cache keeps the bytes, the pixel state is re-emitted).
func TestModelRenderImagesScrollCycle(t *testing.T) {
	cfg := config.Default()
	cfg.Pager.ImageProtocol = "kitty"
	st := config.NewStore(cfg)
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Tags: []string{"inbox"}},
	})})
	m := New(view, nil, testBindings(), testTagActions(), nil, st, cfg.UI)
	m.imgProto = "kitty"
	m.width, m.height = 60, 30
	var body strings.Builder
	body.WriteString("<p>head</p>")
	for range 20 {
		body.WriteString("<p>text</p>")
	}
	body.WriteString("<img src=\"http://example.com/chart.png\" alt=\"[image]\">")
	for range 20 {
		body.WriteString("<p>tail</p>")
	}
	SetOpenHandler(func(threadID string, preview, headers bool, _ int) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID: threadID,
			Lines:    mail.RenderHTML(body.String(), nil, 0),
		}})
		m = next
	})
	press(t, m, "enter")
	var fetched []string
	SetImageFetchHandler(func(url string) { fetched = append(fetched, url) })
	m = press(t, m, "alt+i")
	next, _ := m.Update(EventMsg{Event: core.ImageFetched{
		URL:  "http://example.com/chart.png",
		Data: testPNG(t, 100, 120),
	}})
	m = next
	m, _ = m.Update(frameTick{})

	var buf bytes.Buffer
	old := imageWriter
	imageWriter = &buf
	defer func() { imageWriter = old }()
	paint := func() {
		next, stale := m.paintRects()
		clearRects(imageWriter, stale)
		m.paintImages(next)
	}

	chart := func() *core.Image {
		for i := range m.pager.lines {
			if img := m.pager.lines[i].Image; img != nil && img.URL != "" {
				return img
			}
		}
		return nil
	}

	// below fold: not decoded, nothing painted
	m.pager.vp.offset = 0
	m.View()
	paint()
	if img := chart(); img != nil && img.Rows != 0 {
		t.Fatalf("a below-fold image must stay undecoded, rows=%d", img.Rows)
	}
	if len(m.painted) != 0 {
		t.Fatalf("nothing may paint below the fold, got %d rects", len(m.painted))
	}

	// scroll into it: the decode expands the doc, the paint emits the rect
	m.pager.vp.offset = 25
	m.View()
	paint()
	img := chart()
	if img == nil || img.Rows == 0 {
		t.Fatalf("scrolling into the image must decode it")
	}
	if len(m.painted) != 1 {
		t.Fatalf("the visible image must paint one rect, got %d", len(m.painted))
	}

	// scroll past: the rect stales (cleared before the frame) and the
	// bookkeeping drops it
	m.pager.vp.offset = 60
	m.View()
	np, stale := m.paintRects()
	if len(np) != 0 {
		t.Fatalf("scrolled-past images must not paint, got %d", len(np))
	}
	if len(stale) != 1 {
		t.Fatalf("the scrolled-past rect must stale, got %d", len(stale))
	}
	clearRects(imageWriter, stale)
	m.paintImages(np)

	// scroll back: the same rect paints again (the pixels were cleared
	// when it left the window - the cache keeps the bytes, the dims
	// stay decoded)
	m.pager.vp.offset = 25
	m.View()
	paint()
	if len(m.painted) != 1 {
		t.Fatalf("scrolling back must re-paint the image, got %d rects", len(m.painted))
	}
}

// TestSixelEncodeTransparent pins the P2=1 flag: alpha pixels must leave
// the cleared page background visible (P2=0 paints them in the terminal's
// default background), and the stream must round-trip to the same dims.
func TestSixelEncodeTransparent(t *testing.T) {
	img, _, _, err := decodeImage(testPNG(t, 40, 20), 40, 20, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	n := img.(*image.NRGBA)
	for y := range 2 {
		for x := range n.Bounds().Dx() {
			n.SetNRGBA(x, y, color.NRGBA{0, 0, 0, 0})
		}
	}
	var buf bytes.Buffer
	sixelEncode(&buf, img)
	head := buf.String()
	if !strings.HasPrefix(head, "\x1bP0;1;8q") {
		t.Fatalf("P2 flag: got %q, want P2=1", head[:min(8, len(head))])
	}
	var back image.Image
	if err := sixel.NewDecoder(bytes.NewReader(buf.Bytes())).Decode(&back); err != nil {
		t.Fatalf("round-trip decode: %v", err)
	}
	if back.Bounds().Dx() != 40 || back.Bounds().Dy() != 20 {
		t.Fatalf("round-trip dims %dx%d, want 40x20", back.Bounds().Dx(), back.Bounds().Dy())
	}
}
