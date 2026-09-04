// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

// The load-remote-images pipeline: image lines stay collapsed
// placeholders (privacy gate - bytes never decode until the toggle),
// the toggle expands them to Image.Rows rows, and the terminal paint
// emits the decoded+scaled pixels after the frame (protocol by config
// + environment, sixel by default). All paints flow through
// imageWriter - nil in frame tests, a buffer here.

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
	"time"

	"github.com/gdamore/tcell/v3"
	"github.com/mattn/go-sixel"
	"testing"

	"notmutt/config"
	"notmutt/core"
	"notmutt/mail"
)

// TestSetCellSize pins the ioctl-derived cell size: window pixels over cell counts, out-of-range or missing pixels keep the 10x20 defaults.
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

// testPNG renders a deterministic w x h PNG (noise-ish pixels so the encoded size is meaningful for the chunking tests).
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

// testImg is an image with noise-ish pixels (the x*y term kills per-row correlation, so the encoded size is meaningful for the chunking tests).
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
	p := newPager("t1", "", lines)
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
	p := newPager("t1", "", lines)
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
	// 400x900 px at an 80x30 window: the row budget binds first (aspect kept: 2/3), the pixel dims snap to exact cell multiples
	img, cols, rows, err := decodeImage(testPNG(t, 400, 900), 80, 30, 0, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if cols != 26 || rows != 30 {
		t.Fatalf("scale must be 26x30 cells, got %dx%d", cols, rows)
	}
	if b := img.Bounds(); b.Dx() != cols*imgCellW || b.Dy() != rows*imgCellH {
		t.Fatalf("pixel dims must snap to cell multiples: %dx%d", b.Dx(), b.Dy())
	}

	// the same image in a 100-row window binds on the width instead: a tall chart renders at its natural aspect, not squashed
	if _, cols, rows, err = decodeImage(testPNG(t, 400, 900), 80, 100, 0, 0, false); err != nil {
		t.Fatal(err)
	}
	if cols != 40 || rows != 45 {
		t.Fatalf("tall image must keep the width-bound aspect, got %dx%d", cols, rows)
	}

	// a wide image binds on the width cap
	if _, cols, rows, err = decodeImage(testPNG(t, 2000, 10), 80, 100, 0, 0, false); err != nil {
		t.Fatal(err)
	}
	if cols != 80 || rows != 1 {
		t.Fatalf("wide image must fill the window width, got %dx%d", cols, rows)
	}

	// a tiny image still occupies one cell (no zero-size expansion)
	if _, cols, rows, err = decodeImage(testPNG(t, 3, 3), 80, 100, 0, 0, false); err != nil {
		t.Fatal(err)
	}
	if cols != 1 || rows != 1 {
		t.Fatalf("tiny image must floor to one cell, got %dx%d", cols, rows)
	}

	// garbage bytes never decode
	if _, _, _, err := decodeImage([]byte("not an image"), 80, 100, 0, 0, false); err == nil {
		t.Fatal("garbage must fail the decode")
	}

	// jpeg/gif/webp decode via the blank-imported registrations: mail charts are rarely png, and an undecoded chart never renders
	var jbuf bytes.Buffer
	src, _, _ := image.Decode(bytes.NewReader(testPNG(t, 400, 300)))
	if err := jpeg.Encode(&jbuf, src, nil); err != nil {
		t.Fatal(err)
	}
	if _, cols, rows, err := decodeImage(jbuf.Bytes(), 80, 100, 0, 0, false); err != nil || cols != 40 || rows != 15 {
		t.Fatalf("jpeg must decode at its aspect, got %dx%d err=%v", cols, rows, err)
	}
	var gbuf bytes.Buffer
	if err := gif.Encode(&gbuf, src, nil); err != nil {
		t.Fatal(err)
	}
	if _, cols, rows, err := decodeImage(gbuf.Bytes(), 80, 100, 0, 0, false); err != nil || cols != 40 || rows != 15 {
		t.Fatalf("gif must decode at its aspect, got %dx%d err=%v", cols, rows, err)
	}

	// a declared display size is the target: a 200x300 image declared 600x600 upscales to 600px wide (2x) - with no declaration the scale would cap at 1 and render 200px
	if _, cols, rows, err = decodeImage(testPNG(t, 200, 300), 80, 100, 600, 600, false); err != nil {
		t.Fatal(err)
	}
	if cols != 40 || rows != 30 {
		t.Fatalf("declared size must upscale the image, got %dx%d", cols, rows)
	}

	// the window cap still binds: a declared 10000px image cannot leave the view, and a one-axis declaration scales the other axis with it (aspect kept)
	if _, cols, rows, err = decodeImage(testPNG(t, 400, 300), 80, 100, 10000, 0, false); err != nil {
		t.Fatal(err)
	}
	if cols != 80 || rows != 30 {
		t.Fatalf("declared size must cap at the window, got %dx%d", cols, rows)
	}
}

// TestDecodeImageFillUpscale pins the standalone fill's upscale window: a
// figure whose natural width sits below the column (600px in an 80-cell
// window) stretches to fill when the caller asks for a fill, stays at
// natural px otherwise; a small asset (20 cols natural) is never blown up
// even on a fill request (the imgFillScaleCap guard).
func TestDecodeImageFillUpscale(t *testing.T) {
	img, cols, rows, err := decodeImage(testPNG(t, 600, 300), 80, 100, 0, 0, false)
	if err != nil {
		t.Fatal(err)
	}
	if cols != 60 || rows != 15 {
		t.Fatalf("no fill must keep natural px, got %dx%d", cols, rows)
	}
	if _, cols, rows, err = decodeImage(testPNG(t, 600, 300), 80, 100, 0, 0, true); err != nil {
		t.Fatal(err)
	}
	if cols != 80 {
		t.Fatalf("a fill must stretch a near-column figure to the window, got %d cols", cols)
	}
	if img == nil {
		t.Fatal("decode must return an image")
	}
	if _, cols, rows, err = decodeImage(testPNG(t, 200, 100), 80, 100, 0, 0, true); err != nil {
		t.Fatal(err)
	}
	if cols != 20 || rows != 5 {
		t.Fatalf("a small asset must never blow up on a fill, got %dx%d", cols, rows)
	}
}

// stubCaps is the negotiated-capability stand-in: the sixel bit answers from a field, so a test pins the screen's DA reply.
type stubCaps struct{ sixel bool }

func (s stubCaps) Capabilities() tcell.Capabilities {
	if s.sixel {
		return tcell.CapabilitySixel
	}
	return 0
}

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
	// the non-tmux cases must not inherit the ambient session's TMUX (the tmux query path is pinned separately in TestDetectImageProtocolTmux)
	t.Setenv("TMUX", "")
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
// first; the negotiation stays the fallback (both share the same
// build flag, so they cannot genuinely disagree).
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

func TestKittyTransmit(t *testing.T) {
	var buf bytes.Buffer
	kittyTransmit(&buf, 0, testImg(600, 600))
	out := buf.String()
	if !strings.HasPrefix(out, "\x1b_Ga=t,i=0,f=100,t=d,m=1;") {
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
// (exact cell multiples), each at its own pixel offset, the gap cells
// between them transparent.
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

// TestModelRenderImagesToggle runs the full path: open an html-only message with an inline image, verify the placeholder gate, the alt+i toggle expansion, the terminal paint, and the toggle-off clear.
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
	SetOpenHandler(func(req OpenReq) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID: req.ThreadID,
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

	// the loop split: the stale rects clear before the frame, the blocks paint after it
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
	// the block sits at doc row 2 (before, blank, image) - screen row 4,
	// centered in the 80-cell window (10 decoded cols, offset 35)
	// the first sight transmits the full decode under id 0 (a=t), then
	// places the visible slice at the cursor (a=p with the decode's crop
	// rows) - no delete-all, no EL sweep
	if !strings.HasPrefix(buf.String(), "\x1b_Ga=t,i=0,f=100,t=d,m=0;") {
		t.Fatalf("the paint must transmit the decode first, got %q", show(buf.String()[:min(36, buf.Len())]))
	}
	if !strings.Contains(buf.String(), "\x1b[4;36H\x1b_Ga=p,i=0,p=1,y=0,h=") {
		t.Fatalf("the paint must place the visible slice at the centered rows, got %q", show(buf.String()))
	}
	if len(m.painted) != 1 {
		t.Fatalf("the paint must track one rect, got %d", len(m.painted))
	}

	// toggle off: the second press tears the layer down BEFORE the
	// collapsed frame renders - every placement AND its data goes, the
	// transmit ids reset
	m = press(t, m, "alt+i")
	if !strings.Contains(buf.String(), "\x1b_Ga=d,d=A\x1b\\") {
		t.Fatalf("toggle-off must free the whole kitty layer")
	}
	m, _ = m.Update(frameTick{})
	if out := m.View(); !strings.Contains(out, "[image]") {
		t.Fatalf("toggle-off must restore the placeholder:\n%s", out)
	}
	if len(m.painted) != 0 {
		t.Fatalf("toggle-off must drop the rect bookkeeping")
	}
}

// TestKittyClearImageRectsFreeAll pins the kitty clear path: a placement
// is pixels EL erases text from but never removes, so tearing the layer
// down must free every image AND its data (a=d,d=A) and reset the
// transmit ids - never the sixel-style per-row EL sweep that would leave
// the previous image frozen over the new frame. A scroll-away is NOT
// this path: it deletes one placement by id and keeps the data.
func TestKittyClearImageRectsFreeAll(t *testing.T) {
	cfg := config.Default()
	cfg.Pager.ImageProtocol = "kitty"
	st := config.NewStore(cfg)
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Tags: []string{"inbox"}},
	})})
	m := New(view, nil, testBindings(), testTagActions(), nil, st, cfg.UI)
	m.imgProto = "kitty"
	img := &core.Image{Cols: 10, Rows: 5}
	m.painted = map[*core.Image]cellRect{img: {x: 3, y: 4, w: 10, h: 5, bg: "#112233"}}
	m.kimg = map[*core.Image]int{img: 3}
	m.kimgNext = 4
	var buf bytes.Buffer
	old := imageWriter
	imageWriter = &buf
	defer func() { imageWriter = old }()
	m.clearImageRects()
	if len(m.painted) != 0 || len(m.kimg) != 0 || m.kimgNext != 0 {
		t.Fatalf("clear must reset the paint and transmit state (painted=%d kimg=%d next=%d)", len(m.painted), len(m.kimg), m.kimgNext)
	}
	if got := buf.String(); got != "\x1b_Ga=d,d=A\x1b\\" {
		t.Fatalf("kitty clear must free-all the images, got %q", show(got))
	}
}

// TestModelStandaloneImageFillsAndCenters pins the images-on sizing for
// an image that owns its line (the semianalysis chart regression): a
// chart inside a table cell renders inline on an otherwise empty row,
// so it fills the text column (natural px, capped at the window) and
// centers instead of holding the authored disp width - a 550px chart
// authored for a ~600px browser column must not stay half the width of
// a 120-cell terminal. An image that shares its row with text keeps its
// authored disp size and flow offset.
func TestModelStandaloneImageFillsAndCenters(t *testing.T) {
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
	chart := testPNG(t, 2000, 1000)
	narrow := testPNG(t, 200, 100)
	img := func(b []byte) string { return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b) }
	body := "<p>before</p>" +
		"<table><tr><td><img src=\"" + img(chart) + "\" width=\"300\"></td></tr></table>" +
		"<table><tr><td><img src=\"" + img(narrow) + "\"></td></tr></table>" +
		"<p>see <img src=\"" + img(chart) + "\" width=\"300\"> inline after</p>"
	SetOpenHandler(func(req OpenReq) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID:   req.ThreadID,
			RenderMode: core.RenderHTML,
			Mime:       "text/html",
			Lines:      mail.RenderHTML(body, nil, 0),
		}})
		m = next
	})
	press(t, m, "enter") // discard: the open handler rebinds m
	if m.mode != "pager" {
		t.Fatalf("open must switch to pager, mode=%q", m.mode)
	}

	// embedded bytes decode on the toggle - no fetch, no reopen reply needed
	m = press(t, m, "alt+i")
	m, _ = m.Update(frameTick{})
	if out := m.View(); strings.Contains(out, "[image]") {
		t.Fatalf("the toggle must expand every image:\n%s", out)
	}

	var buf bytes.Buffer
	old := imageWriter
	imageWriter = &buf
	defer func() { imageWriter = old }()
	next, stale := m.paintRects()
	clearRects(imageWriter, stale)
	m.paintImages(next)

	byCols := map[int]*core.Image{}
	for img := range next {
		byCols[img.Cols] = img
	}
	// the standalone chart fills the window's text column (natural 2000px
	// scaled to the 80-cell budget), not its authored 300px/30 cols
	if img := byCols[80]; img == nil {
		t.Fatalf("the standalone chart must fill the column to 80 cols, got cols %v", colsOf(next))
	}
	// the narrower standalone image centers: 20 cols in an 80-cell window
	if img := byCols[20]; img == nil {
		t.Fatalf("the small standalone image must decode at natural 20 cols, got cols %v", colsOf(next))
	}
	if rect := next[byCols[20]]; rect.rect.x != 30 {
		t.Fatalf("the standalone image must center, rect.x=%d want 30", rect.rect.x)
	}
	// the inline-with-text image keeps its authored disp width (300px)
	if img := byCols[30]; img == nil {
		t.Fatalf("the inline image must keep its authored width (30 cols), got cols %v", colsOf(next))
	}
	if len(m.painted) != 3 {
		t.Fatalf("three blocks must paint, got %d", len(m.painted))
	}
}

func colsOf(m map[*core.Image]imgPaint) []int {
	var out []int
	for img := range m {
		out = append(out, img.Cols)
	}
	return out
}

// labelImgLine builds the worker's F-mode line for a link-wrapped isolated
// image: the [N] marker shares the row with the image's alt (allImages is
// false when a label is present), so the row carries Imgs plus the label
// run - verified against the images-on renderStage2Full output shape.
func labelImgLine(label string, img *core.Image) core.Line {
	return core.Line{
		Text: label + img.Alt,
		Runs: []core.Run{
			{Text: label, Label: true},
			{Text: img.Alt, Image: img},
		},
		Imgs: []core.ImagePos{{Image: img, X: len(label)}},
		Kind: core.LineBody,
	}
}

// TestModelLabelLinkImageFillsAndCenters pins the easyjump render parity:
// a link-wrapped isolated image under F carries its [N] label on the same
// row as the image (the mail render's labelLinks shape), and that chrome
// must not flip the standalone verdict - the image centers and fills like
// its unlabeled counterpart instead of holding the authored disp width at
// the label's flow offset.
func TestModelLabelLinkImageFillsAndCenters(t *testing.T) {
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
	chart := &core.Image{Data: testPNG(t, 2000, 1000), Alt: "[image]", DispW: 300}
	narrow := &core.Image{Data: testPNG(t, 200, 100), Alt: "[image]"}
	content := []core.Line{
		{Text: "before", Kind: core.LineBody},
		{Text: "", Kind: core.LineBody},
		labelImgLine("[1]", chart),
		{Text: "", Kind: core.LineBody},
		labelImgLine("[2]", narrow),
		{Text: "", Kind: core.LineBody},
		{Text: "after", Kind: core.LineBody},
	}
	SetOpenHandler(func(req OpenReq) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID:   req.ThreadID,
			RenderMode: core.RenderHTML,
			Mime:       "text/html",
			LinkLabels: true,
			Images:     true,
			Lines:      content,
		}})
		m = next
	})
	press(t, m, "enter") // discard: the open handler rebinds m
	if m.mode != "pager" {
		t.Fatalf("open must switch to pager, mode=%q", m.mode)
	}

	_ = m.View() // the decode trigger: prepareImages sizes the visible images

	var buf bytes.Buffer
	old := imageWriter
	imageWriter = &buf
	defer func() { imageWriter = old }()
	next, stale := m.paintRects()
	clearRects(imageWriter, stale)
	m.paintImages(next)

	// the link-wrapped chart fills the window's text column like the
	// unlabeled standalone render, not its authored 300px/30 cols
	has := func(cols int) *core.Image {
		for img := range next {
			if img.Cols == cols {
				return img
			}
		}
		return nil
	}
	if img := has(80); img == nil {
		t.Fatalf("the labeled standalone chart must fill the column to 80 cols, got %v", colsOf(next))
	}
	// the narrower labeled image centers: 20 cols in an 80-cell window,
	// not the label's flow offset
	nimg := has(20)
	if nimg == nil {
		t.Fatalf("the labeled small image must decode at natural 20 cols, got %v", colsOf(next))
	}
	if rect := next[nimg]; rect.rect.x != 30 {
		t.Fatalf("the labeled standalone image must center, rect.x=%d want 30", rect.rect.x)
	}
	if len(m.painted) != 2 {
		t.Fatalf("two blocks must paint, got %d", len(m.painted))
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
	SetOpenHandler(func(req OpenReq) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID:   req.ThreadID,
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

	// the first alt+i press arms the fetch (there is no local mode anymore - embedded cid:/data: bytes render, http(s) fetch on demand)
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

	// the mode cycled away: a stale reply drops without touching the lines (off mode, second press)
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
// the bytes arrive, and collapse on the toggle-off press.
func TestModelRenderSemianalysisImages(t *testing.T) {
	html, err := os.ReadFile("../testdata/html/semianalysis.html")
	if err != nil {
		t.Fatal(err)
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
	SetOpenHandler(func(req OpenReq) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID:   req.ThreadID,
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

	// the buttons row carries its 5 icons (4 buttons + the READ IN APP text-icon) as inline images at increasing offsets, and the paint emits one rect per icon at that offset (real images, not placeholder text)
	var iconLine *core.Line
	for i := range m.pager.lines {
		if len(m.pager.lines[i].Imgs) == 5 {
			iconLine = &m.pager.lines[i]
			break
		}
	}
	if iconLine == nil {
		t.Fatalf("the buttons row must carry the 5 inline images")
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
	if len(xs) < 5 {
		t.Fatalf("the 5 icons must paint at their offsets, got %d rects with x>0", len(xs))
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

// TestModelRenderImagesScrollCycle pins the kitty scroll: a below-fold
// block image decodes when scrolled into view and transmits once, its
// placement deletes by id (data kept) when scrolled past, and the way
// back re-places with a bare a=p - never a retransmit, never a
// full-window clear.
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
	SetOpenHandler(func(req OpenReq) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID: req.ThreadID,
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

	// scroll into it: the decode expands the doc, the first sight transmits
	// the full decode under id 0 (a=t) and places its visible slice
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
	if !strings.HasPrefix(buf.String(), "\x1b_Ga=t,i=0,f=100,t=d,m=0;") {
		t.Fatalf("the first sight must transmit the decode, got %q", show(buf.String()[:min(30, buf.Len())]))
	}

	// scroll past: nothing paints and nothing stales - paintKitty deletes
	// the departed placement by id (data kept for a cheap return)
	m.pager.vp.offset = 60
	m.View()
	np, stale := m.paintRects()
	if len(np) != 0 || len(stale) != 0 {
		t.Fatalf("a scrolled-past kitty image must not paint or stale, got %d/%d", len(np), len(stale))
	}
	m.paintImages(np)
	if len(m.painted) != 0 {
		t.Fatalf("the scrolled-past image must drop its rect, got %d", len(m.painted))
	}
	if !strings.Contains(buf.String(), "\x1b_Ga=d,d=i,i=0,p=1\x1b\\") {
		t.Fatalf("scroll-past must delete the placement by id (keeping the data), got %q", show(buf.String()))
	}

	// scroll back: the retained data re-places with a bare a=p - the decode
	// is never re-transmitted, so the transmit count stays one
	m.pager.vp.offset = 25
	m.View()
	paint()
	if len(m.painted) != 1 {
		t.Fatalf("scrolling back must re-place the image, got %d rects", len(m.painted))
	}
	if got := strings.Count(buf.String(), "\x1b_Ga=t,i=0,f=100,t=d,"); got != 1 {
		t.Fatalf("returning must not retransmit the decode, transmit count=%d", got)
	}
	if !strings.Contains(buf.String(), "\x1b_Ga=p,i=0,p=1,y=") {
		t.Fatalf("returning must re-place the image, got %q", show(buf.String()))
	}
	if m.imgSuppress {
		t.Fatalf("kitty re-places in place and must never enter the scroll hold")
	}
}

// TestModelScrollSettleFSM pins the scroll-burst settle: a paint whose
// rects all translated as one (same size, new spot - the sixel scroll
// case, where every frame re-encodes the visible set) clears the pixels
// and enters the hold; motion while held keeps it and decode pauses; a
// still pager past the debounce lifts it and repaints the settled
// window once; and a paint that is NOT a pure translation - the first
// decode, a rect leaving the window - never holds.
func TestModelScrollSettleFSM(t *testing.T) {
	cfg := config.Default()
	cfg.Pager.ImageProtocol = "kitty"
	st := config.NewStore(cfg)
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Tags: []string{"inbox"}},
	})})
	m := New(view, nil, testBindings(), testTagActions(), nil, st, cfg.UI)
	m.imgProto = "sixel" // the hold FSM is sixel-only: kitty re-places in place and never holds
	m.width, m.height = 60, 100
	var body strings.Builder
	for range 8 {
		body.WriteString("<p>head</p>")
	}
	body.WriteString("<img src=\"data:image/png;base64," + base64.StdEncoding.EncodeToString(testPNG(t, 120, 200)) + "\">")
	for range 60 {
		body.WriteString("<p>tail</p>")
	}
	SetOpenHandler(func(req OpenReq) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID:   req.ThreadID,
			RenderMode: core.RenderHTML,
			Mime:       "text/html",
			Lines:      mail.RenderHTML(body.String(), nil, 0),
		}})
		m = next
	})
	press(t, m, "enter") // discard: the open handler rebinds m
	if m.mode != "pager" {
		t.Fatalf("open must switch to pager, mode=%q", m.mode)
	}

	m = press(t, m, "alt+i")
	m, _ = m.Update(frameTick{})

	var buf bytes.Buffer
	old := imageWriter
	imageWriter = &buf
	defer func() { imageWriter = old }()
	paint := func() (map[*core.Image]imgPaint, []cellRect) {
		next, stale := m.paintRects()
		clearRects(imageWriter, stale)
		m.paintImages(next)
		return next, stale
	}

	// the top-of-window decode: one block, painted once, no hold
	m.View()
	blocks := m.pager.visibleImages()
	if len(blocks) != 1 || blocks[0].img.Rows == 0 {
		t.Fatalf("the top-of-window image must decode, got %d blocks", len(blocks))
	}
	img := blocks[0].img
	doc := blocks[0].doc
	np, _ := paint()
	if len(np) != 1 || len(m.painted) != 1 {
		t.Fatalf("the first paint must emit one rect, got %d/%d", len(np), len(m.painted))
	}
	if m.imgSuppress {
		t.Fatalf("a first paint must not enter the hold")
	}

	// a pure translation (image fully visible, doc still has head above
	// it): the rects clear and the hold enters, nothing emits
	m.pager.vp.offset = doc - 2
	m.View()
	if np, stale := paint(); np != nil || stale != nil {
		t.Fatalf("a suppressed frame must emit nothing, got %d/%d", len(np), len(stale))
	}
	if !m.imgSuppress {
		t.Fatalf("a translation scroll must enter the hold")
	}
	if len(m.painted) != 0 {
		t.Fatalf("entering the hold must clear the painted rects")
	}
	if buf.Len() == 0 {
		t.Fatalf("entering the hold must clear the stale pixels to the terminal")
	}

	// decode pauses while held: a would-be decode (fresh cache) does not run
	img.Rows, img.Cols = 0, 0
	delete(m.imgCache, img)
	m.View()
	if img.Rows != 0 {
		t.Fatalf("decode must pause during the hold, rows=%d", img.Rows)
	}

	// motion while held refreshes the settle clock and keeps the hold
	m.pager.vp.offset = doc
	m, _ = m.Update(imgSettleTick{})
	if !m.imgSuppress {
		t.Fatalf("motion must keep the hold")
	}

	// a still pager under the debounce also stays held
	m, _ = m.Update(imgSettleTick{})
	if !m.imgSuppress {
		t.Fatalf("a sub-debounce settle tick must keep the hold")
	}

	// a still pager past the debounce lifts the hold: decode resumes and
	// the next render repaints the settled window once
	m.imgSettleAt = time.Now().Add(-imgSettleDebounce - time.Millisecond)
	m, _ = m.Update(imgSettleTick{})
	if m.imgSuppress {
		t.Fatalf("a settled pager must lift the hold")
	}
	if !m.paint {
		t.Fatalf("lifting the hold must arm a repaint")
	}
	m.View()
	np, stale := paint()
	if len(np) != 1 || len(m.painted) != 1 {
		t.Fatalf("the settle must repaint the translated rect, got %d/%d", len(np), len(m.painted))
	}
	if m.imgSuppress {
		t.Fatalf("a settle repaint must not re-enter the hold")
	}

	// a rect leaving the window is not a translation: it stales, never holds
	m.pager.vp.offset = doc + 40
	m.View()
	np, stale = paint()
	if m.imgSuppress {
		t.Fatalf("an image leaving the window must not hold")
	}
	if len(np) != 0 || len(stale) != 1 {
		t.Fatalf("a leaving image must stale one rect, got %d/%d", len(np), len(stale))
	}
}

// TestModelCropScrollBurstHolds pins the re-crop settle: a scroll that
// keeps the same images visible but re-crops one (an image at a window
// edge) re-encodes the whole set per frame - but a lone step must stay
// live (a single page-down over a tall image would otherwise blank it),
// while a second re-crop within the debounce window is a burst and enters
// the hold. A settle repaint arms the lone path again.
func TestModelCropScrollBurstHolds(t *testing.T) {
	cfg := config.Default()
	cfg.Pager.ImageProtocol = "kitty"
	st := config.NewStore(cfg)
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Tags: []string{"inbox"}},
	})})
	m := New(view, nil, testBindings(), testTagActions(), nil, st, cfg.UI)
	m.imgProto = "sixel" // the re-crop burst hold is sixel-only
	m.width, m.height = 60, 100
	var body strings.Builder
	for range 8 {
		body.WriteString("<p>head</p>")
	}
	body.WriteString("<img src=\"data:image/png;base64," + base64.StdEncoding.EncodeToString(testPNG(t, 120, 200)) + "\">")
	for range 60 {
		body.WriteString("<p>tail</p>")
	}
	SetOpenHandler(func(req OpenReq) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID:   req.ThreadID,
			RenderMode: core.RenderHTML,
			Mime:       "text/html",
			Lines:      mail.RenderHTML(body.String(), nil, 0),
		}})
		m = next
	})
	press(t, m, "enter") // discard: the open handler rebinds m
	m = press(t, m, "alt+i")
	m, _ = m.Update(frameTick{})

	var buf bytes.Buffer
	old := imageWriter
	imageWriter = &buf
	defer func() { imageWriter = old }()
	paint := func() (map[*core.Image]imgPaint, []cellRect) {
		next, stale := m.paintRects()
		clearRects(imageWriter, stale)
		m.paintImages(next)
		return next, stale
	}

	m.View()
	blk := m.pager.visibleImages()[0]
	if blk.img.Rows == 0 {
		t.Fatalf("the image must decode, rows=%d", blk.img.Rows)
	}
	doc := blk.doc
	// first paint: empty set, live, no hold
	m.pager.vp.offset = 0
	m.View()
	np, _ := paint()
	if len(np) != 1 || m.imgSuppress {
		t.Fatalf("the first paint must emit one rect live, got %d/%v", len(np), m.imgSuppress)
	}

	// a lone re-crop (the image crops at the top edge): live, no hold
	m.pager.vp.offset = doc + 1
	m.View()
	np, _ = paint()
	if len(np) != 1 {
		t.Fatalf("a lone re-crop must paint one rect, got %d", len(np))
	}
	if m.imgSuppress {
		t.Fatalf("a lone re-crop must not enter the hold")
	}

	// a second re-crop within the debounce window is a burst: hold, clear
	m.pager.vp.offset = doc + 2
	m.View()
	if np, stale := paint(); np != nil || stale != nil {
		t.Fatalf("a re-crop burst must emit nothing, got %d/%d", len(np), len(stale))
	}
	if !m.imgSuppress {
		t.Fatalf("a re-crop burst must enter the hold")
	}
	if len(m.painted) != 0 {
		t.Fatalf("a re-crop burst must clear the painted rects")
	}

	// a settled repaint arms the lone path again: the next re-crop is live
	m.imgSettleAt = time.Now().Add(-imgSettleDebounce - time.Millisecond)
	m, _ = m.Update(imgSettleTick{})
	m.pager.vp.offset = doc + 3
	m.View()
	np, _ = paint()
	if len(np) != 1 {
		t.Fatalf("a re-crop after settle must paint one rect, got %d", len(np))
	}
	if m.imgSuppress {
		t.Fatalf("a re-crop after settle must not hold")
	}
}
func TestModelImagesReopenCarriesFlag(t *testing.T) {
	cfg := config.Default()
	st := config.NewStore(cfg)
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Tags: []string{"inbox"}},
	})})
	m := New(view, nil, testBindings(), testTagActions(), nil, st, cfg.UI)
	m.imgProto = "kitty" // the engaged screen's negotiation (unit-stubbed)
	m.width, m.height = 80, 100
	SetOpenHandler(func(req OpenReq) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID:   req.ThreadID,
			MsgID:      req.MsgID,
			RenderMode: core.RenderHTML,
			Mime:       "text/html",
			Images:     req.Images,
			Lines:      []core.Line{{Text: "alpha"}},
		}})
		m = next
	})
	press(t, m, "enter") // discard: the open handler rebinds m
	if m.mode != "pager" || m.images {
		t.Fatalf("a fresh open must be images-off, mode=%q images=%v", m.mode, m.images)
	}

	loaded := func(images, refine bool, text string) {
		t.Helper()
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID:   "t1",
			MsgID:      "a",
			RenderMode: core.RenderHTML,
			Mime:       "text/html",
			Images:     images,
			Refine:     refine,
			Lines:      []core.Line{{Text: text}},
		}})
		m = next
	}

	// an images-on reopen reply replaces the content and flips m.images
	loaded(true, false, "beta")
	if !m.images {
		t.Fatalf("an images-on reply must flip m.images")
	}
	if len(m.pager.lines) != 1 || m.pager.lines[0].Text != "beta" {
		t.Fatalf("an images-on reply must replace the content")
	}
	if m.imgMode != 1 {
		t.Fatalf("an images-on reply must keep the toggle on, imgMode=%d", m.imgMode)
	}

	// a refine reply forces the replacement even with the flag unchanged
	loaded(true, true, "gamma")
	if len(m.pager.lines) != 1 || m.pager.lines[0].Text != "gamma" {
		t.Fatalf("a refine reply must replace the content")
	}

	// a no-change reply stays idempotent: same flags, no refine
	loaded(true, false, "delta")
	if len(m.pager.lines) != 1 || m.pager.lines[0].Text != "gamma" {
		t.Fatalf("a no-change reply must not replace the content")
	}

	// an images-off reply replaces and flips m.images back
	loaded(false, false, "eps")
	if m.images || len(m.pager.lines) != 1 || m.pager.lines[0].Text != "eps" {
		t.Fatalf("an images-off reply must replace and flip m.images back")
	}
	if m.imgMode != 0 {
		t.Fatalf("an images-off reply must collapse the toggle, imgMode=%d", m.imgMode)
	}
}

// TestModelRefineRemoteSeats pins the remote-size flow: a fetch caches
// the bytes and measured px, arms ONE refine reopen that re-lays the
// page at real geometry, the refine reply re-attaches the cached bytes
// and drops the arm, the off toggle clears the caches, and a stale
// refine reply (images toggled off since) drops.
func TestModelRefineRemoteSeats(t *testing.T) {
	const u = "http://example.com/x.png"
	cfg := config.Default()
	st := config.NewStore(cfg)
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Tags: []string{"inbox"}},
	})})
	m := New(view, nil, testBindings(), testTagActions(), nil, st, cfg.UI)
	m.imgProto = "kitty" // the engaged screen's negotiation (unit-stubbed)
	m.width, m.height = 80, 100
	content := []core.Line{
		{Text: "alpha"},
		{Text: "pic", Image: &core.Image{URL: u, Alt: "[image]"}},
	}
	var reqs []OpenReq
	auto := true
	SetOpenHandler(func(req OpenReq) {
		reqs = append(reqs, req)
		if !auto {
			return // record only: the armed refine is driven by the test
		}
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID:   req.ThreadID,
			MsgID:      req.MsgID,
			RenderMode: core.RenderHTML,
			Mime:       "text/html",
			Images:     req.Images,
			Refine:     req.Refine,
			Lines:      content,
		}})
		m = next
	})
	var fetched []string
	SetImageFetchHandler(func(url string) { fetched = append(fetched, url) })

	press(t, m, "enter") // discard: the open handler rebinds m
	if m.mode != "pager" || m.images {
		t.Fatalf("a fresh open must be images-off, mode=%q images=%v", m.mode, m.images)
	}

	// on: the toggle arms the fetch AND lays the content out images-on
	press(t, m, "alt+i")
	if len(fetched) != 1 || fetched[0] != u {
		t.Fatalf("the toggle must fetch the remote src, got %v", fetched)
	}
	if !m.images || m.imgMode != 1 {
		t.Fatalf("the images-on reply must install images-on content, images=%v imgMode=%d", m.images, m.imgMode)
	}

	// the fetch lands: bytes cached, px measured, one refine armed
	auto = false
	next, _ := m.Update(EventMsg{Event: core.ImageFetched{URL: u, Data: testPNG(t, 40, 20)}})
	m = next
	if sz, ok := m.imgRemoteSize[u]; !ok || sz.W != 40 || sz.H != 20 {
		t.Fatalf("the fetch must measure the px, got %+v", m.imgRemoteSize[u])
	}
	if len(m.imgRemote[u]) == 0 {
		t.Fatalf("the fetch must cache the bytes")
	}
	if !m.refinePending {
		t.Fatalf("the fetch must arm the refine reopen")
	}
	var refines int
	for _, r := range reqs {
		if r.Refine {
			refines++
		}
	}
	if refines != 1 {
		t.Fatalf("exactly one refine reopen must dispatch, got %d", refines)
	}
	last := reqs[len(reqs)-1]
	if !last.Refine || !last.Images || last.ImgSizes[u].W != 40 {
		t.Fatalf("the refine must carry Images + the px map, got %+v", last)
	}

	// the refine reply re-lays the content and drops the arm; the cached
	// bytes re-attach to the fresh lines (no refetch)
	next, _ = m.Update(EventMsg{Event: core.ThreadLoaded{
		ThreadID:   "t1",
		MsgID:      "a",
		RenderMode: core.RenderHTML,
		Mime:       "text/html",
		Images:     true,
		Refine:     true,
		Lines:      content,
	}})
	m = next
	if m.refinePending {
		t.Fatalf("the refine reply must drop the arm")
	}
	found := false
	for i := range m.pager.lines {
		for _, img := range lineImages(&m.pager.lines[i]) {
			if img.URL == u {
				found = len(img.Data) > 0
			}
		}
	}
	if !found {
		t.Fatalf("the refine content must carry the fetched bytes")
	}
	if len(fetched) != 1 {
		t.Fatalf("the re-attach must not refetch, got %d fetches", len(fetched))
	}

	// off: the toggle clears the caches and restores the markers
	auto = true
	press(t, m, "alt+i")
	if m.images || m.imgMode != 0 {
		t.Fatalf("the off reply must collapse, images=%v imgMode=%d", m.images, m.imgMode)
	}
	if len(m.imgRemote) != 0 || len(m.imgRemoteSize) != 0 {
		t.Fatalf("the off toggle must clear the fetch caches")
	}

	// a stale refine reply (images now off) drops without re-laying
	next, _ = m.Update(EventMsg{Event: core.ThreadLoaded{
		ThreadID:   "t1",
		MsgID:      "a",
		RenderMode: core.RenderHTML,
		Mime:       "text/html",
		Images:     true,
		Refine:     true,
		Lines:      content,
	}})
	m = next
	if m.images || m.refinePending {
		t.Fatalf("a stale refine must drop, images=%v refinePending=%v", m.images, m.refinePending)
	}
}

// TestModelOpenLinksReopenKeepsSizes pins the F-key reopen on images-on
// content: the easyjump re-render carries the seated remote sizes like
// the images-on toggle does, so a fetched-and-seated remote image does
// not lose its geometry when the link labels come up.
func TestModelOpenLinksReopenKeepsSizes(t *testing.T) {
	const u = "http://example.com/x.png"
	cfg := config.Default()
	st := config.NewStore(cfg)
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Tags: []string{"inbox"}},
	})})
	m := New(view, nil, testBindings(), testTagActions(), nil, st, cfg.UI)
	m.imgProto = "kitty" // the engaged screen's negotiation (unit-stubbed)
	m.width, m.height = 80, 100
	content := []core.Line{
		{Text: "alpha"},
		{Text: "pic", Image: &core.Image{URL: u, Alt: "[image]"}},
	}
	var reqs []OpenReq
	SetOpenHandler(func(req OpenReq) {
		reqs = append(reqs, req)
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID:   req.ThreadID,
			MsgID:      req.MsgID,
			RenderMode: core.RenderHTML,
			Mime:       "text/html",
			Images:     req.Images,
			Refine:     req.Refine,
			LinkLabels: req.LabelLinks,
			Lines:      content,
		}})
		m = next
	})
	SetImageFetchHandler(func(url string) {})
	press(t, m, "enter") // discard: the open handler rebinds m

	// the toggle + a fetch seat the remote image: the refine reply installs
	// the images-on content with the measured px cached
	press(t, m, "alt+i")
	m, _ = m.Update(EventMsg{Event: core.ImageFetched{URL: u, Data: testPNG(t, 40, 20)}})
	if sz, ok := m.imgRemoteSize[u]; !ok || sz.W != 40 {
		t.Fatalf("the fetch must seat the remote px, got %+v", m.imgRemoteSize[u])
	}
	if !m.images {
		t.Fatalf("the refine must install images-on content")
	}

	// the F reopen follows the images-on pattern: the seated sizes ride it
	press(t, m, "F")
	last := reqs[len(reqs)-1]
	if !last.Images {
		t.Fatalf("the F reopen must be images-on, got %+v", last)
	}
	if !last.LabelLinks {
		t.Fatalf("the F reopen must request the labels")
	}
	if sz, ok := last.ImgSizes[u]; !ok || sz.W != 40 {
		t.Fatalf("the F reopen must carry the seated remote sizes, got %+v", last.ImgSizes)
	}
}

// TestModelImagesKeepsScroll pins the images/refine scroll preservation:
// a same-message geometry re-render (the alt+i toggle reply, a refine)
// keeps the reader's place and pan; a fresh open of another message
// starts at the top.
func TestModelImagesKeepsScroll(t *testing.T) {
	cfg := config.Default()
	st := config.NewStore(cfg)
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "a", Timestamp: 100, Tags: []string{"inbox"}},
	})})
	m := New(view, nil, testBindings(), testTagActions(), nil, st, cfg.UI)
	m.imgProto = "kitty" // the engaged screen's negotiation (unit-stubbed)
	m.width, m.height = 80, 100
	content := make([]core.Line, 120)
	for i := range content {
		content[i] = core.Line{Text: "alpha"}
	}
	content[2] = core.Line{Text: strings.Repeat("x", 200)} // wide enough to pan
	SetOpenHandler(func(req OpenReq) {
		next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
			ThreadID:   req.ThreadID,
			MsgID:      req.MsgID,
			RenderMode: core.RenderHTML,
			Mime:       "text/html",
			Images:     req.Images,
			Lines:      content,
		}})
		m = next
	})
	press(t, m, "enter") // discard: the open handler rebinds m
	if m.mode != "pager" {
		t.Fatalf("expected the pager, mode=%q", m.mode)
	}
	m.pager.scrollDown(25)
	m.pager.x = 10
	at := m.pager.vp.offset
	if at == 0 {
		t.Fatalf("fixture must sit scrolled past the top, offset=%d", at)
	}

	// same-message images-on reply: offset and pan survive the re-layout
	next, _ := m.Update(EventMsg{Event: core.ThreadLoaded{
		ThreadID:   "t1",
		MsgID:      "a",
		RenderMode: core.RenderHTML,
		Mime:       "text/html",
		Images:     true,
		Lines:      content,
	}})
	m = next
	if m.pager.vp.offset != at || m.pager.x != 10 {
		t.Fatalf("an images-on re-render must keep the scroll, offset=%d x=%d, want %d/10", m.pager.vp.offset, m.pager.x, at)
	}

	// a refine reply on the same message keeps the scroll too
	m.pager.scrollDown(3)
	want := m.pager.vp.offset
	next, _ = m.Update(EventMsg{Event: core.ThreadLoaded{
		ThreadID:   "t1",
		MsgID:      "a",
		RenderMode: core.RenderHTML,
		Mime:       "text/html",
		Images:     true,
		Refine:     true,
		Lines:      content,
	}})
	m = next
	if m.pager.vp.offset != want || m.pager.x != 10 {
		t.Fatalf("a refine re-render must keep the scroll, offset=%d x=%d, want %d/10", m.pager.vp.offset, m.pager.x, want)
	}

	// a fresh open of another message starts at the top
	next, _ = m.Update(EventMsg{Event: core.ThreadLoaded{
		ThreadID:   "t2",
		MsgID:      "b",
		RenderMode: core.RenderHTML,
		Mime:       "text/html",
		Images:     true,
		Lines:      content,
	}})
	m = next
	if m.pager.vp.offset != 0 || m.pager.x != 0 {
		t.Fatalf("a fresh open must reset the scroll, offset=%d x=%d", m.pager.vp.offset, m.pager.x)
	}
}

// TestSixelEncodeTransparent pins the P2=1 flag: alpha pixels must leave the cleared page background visible (P2=0 paints them in the terminal's default background), and the stream must round-trip to the same dims.
func TestSixelEncodeTransparent(t *testing.T) {
	img, _, _, err := decodeImage(testPNG(t, 40, 20), 40, 20, 0, 0, false)
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
