// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

// Terminal image emission: the mail renderer's image lines are decoded
// + scaled + emitted to the terminal ONLY on the load-remote-images key
// (privacy gate - the bytes stay inert until then) and only for the
// visible window. Protocol: kitty opt-in via [pager] image-protocol
// (env match), sixel via the engaged screen's DA negotiation (under
// tmux a tmux query - tmux answers DA itself); unsupported terminals
// keep the collapsed Alt row. The writer is
// /dev/tty (the tcell screen cannot emit raw image protocols) and paint
// runs AFTER the frame flush, so pixels never race the text.

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"

	"github.com/mattn/go-sixel"
	"golang.org/x/image/draw"

	"notmutt/config"
	"notmutt/core"
)

const (
	imgCellW   = 10 // ponytail: default cell px; the calibration knob if a terminal's cell grid differs
	imgCellH   = 20
	imgMaxCols = 100 // paint caps: an image line never exceeds 100x30 cells
	imgMaxRows = 30
	kittyChunk = 4096 // kitty's max payload per DCS frame
)

// imageWriter is the paint sink: /dev/tty in Run, nil in tests (all
// paint paths no-op, so the frame tests never write to a terminal).
var imageWriter io.Writer

// detectImageProtocol picks the image protocol: kitty only when
// [pager] image-protocol opts in and the kitty env matches; sixel when
// the engaged screen's DA negotiation reported it (tmux answers DA1
// itself with a build-time reply, so under tmux a tmux query replaces
// the negotiation). "" = no image support.
func detectImageProtocol(p config.Pager, s sixelCapable) string {
	if p.ImageProtocol == "kitty" {
		if os.Getenv("KITTY_WINDOW_ID") != "" {
			return "kitty"
		}
		switch os.Getenv("TERM_PROGRAM") {
		case "kitty", "wezterm", "alacritty", "ghostty":
			return "kitty"
		}
		return ""
	}
	if os.Getenv("TMUX") != "" && tmuxSixel() {
		return "sixel"
	}
	if s != nil && s.Sixel() {
		return "sixel"
	}
	return ""
}

// sixelCapable is the negotiated sixel flag the screen exposes (the
// tcell Screen.Sixel seam; tests stub it).
type sixelCapable interface {
	Sixel() bool
}

// tmuxSixel asks tmux for its sixel support (the server format; tmux's
// own DA1 reply to panes is fixed at build time and omits it).
var tmuxSixel = func() bool {
	out, err := exec.Command("tmux", "display", "-p", "#{sixel_support}").Output()
	return err == nil && strings.TrimSpace(string(out)) == "1"
}

// decodeImage decodes the raw image bytes and scales to the cell
// grid: at most widthCells (capped at imgMaxCols) x imgMaxRows cells,
// aspect preserved, then snapped UP to exact cell multiples so the
// pixel dims align with the terminal's cells. Returns the scaled
// image (always NRGBA) plus its cell dims.
func decodeImage(data []byte, widthCells int) (image.Image, int, int, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, err
	}
	if widthCells > imgMaxCols {
		widthCells = imgMaxCols
	}
	if widthCells < 1 {
		widthCells = 1
	}
	sw, sh := float64(src.Bounds().Dx()), float64(src.Bounds().Dy())
	scale := math.Min((float64(widthCells)*imgCellW)/sw, (float64(imgMaxRows)*imgCellH)/sh)
	if scale > 1 {
		scale = 1
	}
	cols := int(sw * scale / imgCellW)
	rows := int(sh * scale / imgCellH)
	if cols < 1 {
		cols = 1
	}
	if rows < 1 {
		rows = 1
	}
	dw, dh := cols*imgCellW, rows*imgCellH
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	return dst, cols, rows, nil
}

// kittyEncode transmits the image as PNG over the kitty graphics
// protocol: base64 chunks of kittyChunk bytes, the classic chunked
// frame (a=T transmit placement at the cursor).
func kittyEncode(w io.Writer, img image.Image) {
	var buf bytes.Buffer
	png.Encode(&buf, img)
	data := base64.StdEncoding.EncodeToString(buf.Bytes())
	chunks := (len(data) + kittyChunk - 1) / kittyChunk
	for i := range chunks {
		end := min((i+1)*kittyChunk, len(data))
		m := 1
		if i == chunks-1 {
			m = 0
		}
		if i == 0 {
			fmt.Fprintf(w, "\x1b_Gf=100,t=d,a=T,m=%d;%s\x1b\\", m, data[i*kittyChunk:end])
		} else {
			fmt.Fprintf(w, "\x1b_Gm=%d;%s\x1b\\", m, data[i*kittyChunk:end])
		}
	}
}

// sixelEncode emits the image as sixels; go-sixel writes the complete
// DCS sequence in one call.
func sixelEncode(w io.Writer, img image.Image) {
	sixel.NewEncoder(w).Encode(img)
}

// cropImage cuts a pixel sub-rect of the scaled image (decodeImage
// always produces NRGBA, but draw.Draw crops any source).
func cropImage(src image.Image, x, y, w, h int) image.Image {
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), src, image.Pt(x, y), draw.Src)
	return dst
}

// cellRect is a screen rect in cells (paint-diff bookkeeping).
type cellRect struct{ x, y, w, h int }

// clearRect fills a cell rect with the theme background (or the
// terminal default when the theme leaves it unset) - the stale pixels
// of a moved or vanished image never linger.
func clearRect(w io.Writer, x, y, cols, rows int, bg string) {
	if cols < 1 || rows < 1 {
		return
	}
	fmt.Fprintf(w, "\x1b[%d;%dH", y+1, x+1)
	if rgb := hexRGB(bg); rgb != "" {
		fmt.Fprintf(w, "\x1b[48;2;%sm", rgb)
	}
	fmt.Fprint(w, strings.Repeat(" ", cols*rows))
	fmt.Fprint(w, "\x1b[0m")
}

// paintImage emits one image block at the screen cell (x, y): cursor
// home, then the protocol encode of the visible pixel slice (srcY,
// srcH - the crop keeps a window-edge block from overflowing onto the
// keyhint/status rows).
func paintImage(w io.Writer, proto string, src image.Image, srcY, srcH, x, y int) {
	fmt.Fprintf(w, "\x1b[%d;%dH", y+1, x+1)
	crop := src
	if srcY != 0 || srcH != src.Bounds().Dy() {
		crop = cropImage(src, 0, srcY, src.Bounds().Dx(), srcH)
	}
	switch proto {
	case "kitty":
		kittyEncode(w, crop)
	case "sixel":
		sixelEncode(w, crop)
	}
}

// bgHexOf renders a lipgloss background color as #rrggbb; "" when the
// theme leaves the terminal default (NoColor) or the color is not a
// plain hex (ANSI/palette entries) - the clear then uses the
// terminal's default background.
func bgHexOf(c color.Color) string {
	if r, ok := c.(color.RGBA); ok && r.A == 0xff {
		return fmt.Sprintf("#%02x%02x%02x", r.R, r.G, r.B)
	}
	return ""
}

// themeBG is the theme's normal background hex (#rrggbb, "" when the
// theme leaves the terminal default) - the clear fill color.
func (m *Model) themeBG() string {
	return bgHexOf(m.styles.Normal.GetBackground())
}

// prepareImages decodes the pager window's image lines (the privacy
// gate: bytes decode ONLY here - the render-images toggle and scrolls
// into an image) and re-lays-out when a decode gives a block dims.
// Runs on the render side, before the frame builds, so a decode-
// gained expansion lands in the SAME frame.
func (m *Model) prepareImages() {
	if m.mode != "pager" || m.pager == nil || !m.pager.images || m.imgProto == "" {
		return
	}
	if m.pager.width < 1 {
		return
	}
	dirty := false
	for _, b := range m.pager.visibleImages() {
		img := m.pager.lines[b.line].Image
		if img == nil {
			continue
		}
		if _, ok := m.imgCache[img]; ok {
			continue
		}
		scaled, cols, rows, err := decodeImage(img.Data, m.pager.width)
		if err != nil {
			continue
		}
		m.imgCache[img] = scaled
		img.Cols, img.Rows = cols, rows
		dirty = true
	}
	if dirty {
		m.pager.relayout()
	}
}

// setImgMode applies the load-remote-images toggle (alt+i): off ->
// remote (embedded cid:/data: bytes render, http(s) srcs fetch on
// demand, gated by this key) -> off. Toggling off drops the fetched
// remote bytes - the network never feeds the decode outside the mode.
func (m *Model) setImgMode(mode int) {
	switch mode {
	case 0:
		m.clearImageRects() // before the frame: the collapsed Alt row renders
		m.dropRemoteData()
		m.pager.setImages(false)
	case 1:
		m.pager.setImages(true)
		m.fetchRemoteImages()
	}
	m.imgMode = mode
}

// fetchRemoteImages arms the message's remote-image fetches (the
// remote images mode): every URL-image line without bytes fetches
// once - the keypress is the gate, the decode stays per-window
// (prepareImages), so below-fold images fetch now and expand when
// scrolled into view. imgFetching single-flights the in-flight URLs,
// the seam owns the goroutine so the render path never blocks.
func (m *Model) fetchRemoteImages() {
	if m.pager == nil {
		return
	}
	for i := range m.pager.lines {
		img := m.pager.lines[i].Image
		if img == nil || img.URL == "" || len(img.Data) > 0 || m.imgFetching[img.URL] {
			continue
		}
		m.imgFetching[img.URL] = true
		onImageFetch(img.URL)
	}
}

// attachFetched attaches a fetch reply to its image lines (the remote
// images mode): the bytes land on every line sharing the URL, the
// pager re-expands, and the decode runs on the next prepareImages. A
// failed fetch keeps the Alt row and drops the URL so the next cycle
// never refetches.
func (m *Model) attachFetched(e core.ImageFetched) {
	delete(m.imgFetching, e.URL)
	if m.pager == nil {
		return
	}
	dirty := false
	for i := range m.pager.lines {
		l := &m.pager.lines[i]
		if l.Image == nil || l.Image.URL != e.URL {
			continue
		}
		dirty = true
		if e.Err != nil {
			l.Image.URL = ""
			continue
		}
		l.Image.Data = e.Data
	}
	if !dirty {
		return
	}
	if e.Err != nil {
		m.logEntry("image fetch failed", true)
	}
	m.pager.relayout()
}

// dropRemoteData collapses the remote images (local mode): the fetched
// bytes leave the model (network data never decodes outside the remote
// mode), the decode cache dies with them, and the blocks shrink back
// to their Alt rows.
func (m *Model) dropRemoteData() {
	if m.pager == nil {
		return
	}
	dirty := false
	for i := range m.pager.lines {
		l := &m.pager.lines[i]
		if l.Image == nil || l.Image.URL == "" {
			continue
		}
		delete(m.imgCache, l.Image)
		l.Image.Data = nil
		l.Image.Cols, l.Image.Rows = 0, 0
		dirty = true
	}
	if dirty {
		m.pager.relayout()
	}
}

// paintImages writes the pager's image blocks to the terminal after
// the frame flush (pixels never race the text). Rects track the
// previous frame: moved or vanished blocks clear to the theme bg
// first. The non-pager and toggled-off paths clear every painted
// rect - the safety net for a mode change that skips the dispatch
// clears.
func (m *Model) paintImages() {
	if m.imgProto == "" {
		return
	}
	if m.mode != "pager" || m.pager == nil || !m.pager.images {
		m.clearImageRects()
		return
	}
	if imageWriter == nil {
		return
	}
	off, height := m.pager.vp.offset, m.pager.vp.height
	bg := m.themeBG()
	next := map[*core.Image]cellRect{}
	for _, b := range m.pager.visibleImages() {
		img := m.pager.lines[b.line].Image
		if img.Rows == 0 {
			continue // not decoded: the Alt row shows
		}
		scaled, ok := m.imgCache[img]
		if !ok {
			continue
		}
		// the block's window rect: rows [windowTop, windowTop+Rows);
		// the visible top sits at window row max(windowTop, 0) (the
		// paint emits only the visible pixel slice below it)
		windowTop := b.doc - off
		visTop := max(0, -windowTop)
		visBot := min(img.Rows, height-windowTop)
		if visBot <= visTop {
			continue
		}
		rect := cellRect{x: 0, y: 1 + max(windowTop, 0), w: b.cols, h: visBot - visTop}
		if prev, ok := m.painted[img]; ok && prev == rect {
			continue // unchanged: the terminal still holds the pixels
		}
		if prev, ok := m.painted[img]; ok {
			clearRect(imageWriter, prev.x, prev.y, prev.w, prev.h, bg)
		}
		paintImage(imageWriter, m.imgProto, scaled, visTop*imgCellH, (visBot-visTop)*imgCellH, rect.x, rect.y)
		next[img] = rect
	}
	for img, prev := range m.painted {
		if _, ok := next[img]; !ok {
			clearRect(imageWriter, prev.x, prev.y, prev.w, prev.h, bg)
		}
	}
	m.painted = next
}

// clearImageRects fills every painted rect with the theme bg - the
// toggle-off, mode-exit and resize paths run it BEFORE the next frame
// so the collapsed text never renders under stale pixels.
func (m *Model) clearImageRects() {
	if len(m.painted) == 0 {
		return
	}
	if imageWriter != nil {
		bg := m.themeBG()
		for _, r := range m.painted {
			clearRect(imageWriter, r.x, r.y, r.w, r.h, bg)
		}
	}
	clear(m.painted)
}

// resetImages drops the decode cache and painted rects on a resize
// (the cell math changed): the dims zero, the layout collapses, the
// next prepareImages re-decodes at the new width.
func (m *Model) resetImages() {
	m.imgCache = map[*core.Image]image.Image{}
	m.clearImageRects()
	if m.pager == nil {
		return
	}
	for i := range m.pager.lines {
		if img := m.pager.lines[i].Image; img != nil {
			img.Cols, img.Rows = 0, 0
		}
	}
	m.pager.relayout()
}
