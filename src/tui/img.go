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
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"strings"
	"time"

	_ "golang.org/x/image/webp" // decoder registrations: mail charts arrive as jpeg/gif/webp

	"github.com/mattn/go-sixel"
	"golang.org/x/image/draw"

	"notmutt/config"
	"notmutt/core"
)

const (
	imgMaxCols = 100  // paint cap: an image line never exceeds 100 cells wide
	kittyChunk = 4096 // kitty's max payload per DCS frame
)

// imgCellW/H are the terminal's cell size in pixels, probed at startup
// (CSI 18 t); the 10x20 defaults when no terminal answers. An image's
// reserved rows must match the pixels its raster occupies: a cell
// taller than the default leaves a gap under every painted image.
var (
	imgCellW = 10
	imgCellH = 20
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

// probeCellSize asks the terminal for its cell size in pixels (XTerm's
// CSI 18 t; kitty/ghostty/wezterm answer, tmux relays) so images
// reserve exactly the rows their pixels occupy. No reply within the
// deadline keeps the defaults. Runs before tcell takes the tty over -
// the reply would otherwise race tcell's input pump.
func probeCellSize() {
	f, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return
	}
	defer f.Close()
	if _, err := f.WriteString("\x1b[18t"); err != nil {
		return
	}
	f.SetReadDeadline(time.Now().Add(250 * time.Millisecond))
	buf := make([]byte, 64)
	n, err := f.Read(buf)
	if err != nil {
		return
	}
	if h, w, ok := parseCellReply(string(buf[:n])); ok {
		imgCellH, imgCellW = h, w
	}
}

// parseCellReply extracts the cell size from a CSI 18 t reply
// (ESC [ 4 ; <cell h px> ; <cell w px> t); ok=false for any other
// shape or an out-of-range size.
func parseCellReply(s string) (h, w int, ok bool) {
	i := strings.Index(s, "\x1b[4;")
	if i < 0 {
		return 0, 0, false
	}
	var vh, vw int
	if _, err := fmt.Sscanf(s[i+4:], "%d;%dt", &vh, &vw); err != nil {
		return 0, 0, false
	}
	if vh < 1 || vh > 200 || vw < 1 || vw > 200 {
		return 0, 0, false
	}
	return vh, vw, true
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
// grid: at most widthCells (capped at imgMaxCols) wide and heightRows
// tall, aspect preserved, then snapped UP to exact cell multiples so
// the pixel dims align with the terminal's cells. dispW/dispH are the
// email's declared display size in pixels (0 = unspecified): the
// declared axis is the target scale - an email that sizes its section
// for a 600px chart gets a 600px chart, capped by the window so the
// paint never leaves the visible area. With no declared size the scale
// caps at 1 (natural size, never upscale). Returns the scaled image
// (always NRGBA) plus its cell dims.
func decodeImage(data []byte, widthCells, heightRows, dispW, dispH int) (image.Image, int, int, error) {
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
	if heightRows < 1 {
		heightRows = 1
	}
	sw, sh := float64(src.Bounds().Dx()), float64(src.Bounds().Dy())
	scale := math.Min((float64(widthCells)*float64(imgCellW))/sw, (float64(heightRows)*float64(imgCellH))/sh)
	if dispW > 0 {
		scale = math.Min(scale, float64(dispW)/sw)
	}
	if dispH > 0 {
		scale = math.Min(scale, float64(dispH)/sh)
	}
	if scale > 1 && dispW == 0 && dispH == 0 {
		scale = 1
	}
	cols := int(sw * scale / float64(imgCellW))
	rows := int(sh * scale / float64(imgCellH))
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
// DCS sequence in one call. Transparent (P2=1) leaves the cleared page
// background visible behind alpha pixels - P2=0 would paint them in the
// terminal's default background (dark boxes around icons on a white page).
func sixelEncode(w io.Writer, img image.Image) {
	e := sixel.NewEncoder(w)
	e.Transparent = true
	e.Encode(img)
}

// cropImage cuts a pixel sub-rect of the scaled image (decodeImage
// always produces NRGBA, but draw.Draw crops any source).
func cropImage(src image.Image, x, y, w, h int) image.Image {
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), src, image.Pt(x, y), draw.Src)
	return dst
}

// cellRect is a screen rect in cells (paint-diff bookkeeping), with
// the block's clear background (the line's declared bg or the theme).
type cellRect struct {
	x, y, w, h int
	bg         string
}

// clearRects erases a rect set to each rect's background - per-row EL,
// the erase that also removes sixel graphics (a space fill leaves the
// pixels). Runs BEFORE the text frame, so the frame's fresh content
// draws over the cleared rows.
func clearRects(w io.Writer, rects []cellRect) {
	if w == nil {
		return
	}
	for _, r := range rects {
		if r.w < 1 || r.h < 1 {
			continue
		}
		for row := 0; row < r.h; row++ {
			fmt.Fprintf(w, "\x1b[%d;%dH", r.y+1+row, r.x+1)
			if rgb := hexRGB(r.bg); rgb != "" {
				fmt.Fprintf(w, "\x1b[48;2;%sm", rgb)
			}
			fmt.Fprint(w, "\x1b[K")
			fmt.Fprint(w, "\x1b[0m")
		}
	}
}

// imgBG is an image block's clear color: the line's declared
// background (the mail's body/html bg, a table cell's bgcolor) or the
// theme default when the line leaves it unset.
func (m *Model) imgBG(line int) string {
	if b := m.pager.lines[line].Bg; b != "" {
		return b
	}
	return m.themeBG()
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
		img := b.img
		if img == nil {
			continue
		}
		if _, ok := m.imgCache[img]; ok {
			continue
		}
		scaled, cols, rows, err := decodeImage(img.Data, m.pager.width, m.pager.vp.height, img.DispW, img.DispH)
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
		for _, img := range lineImages(&m.pager.lines[i]) {
			if img.URL == "" || len(img.Data) > 0 || m.imgFetching[img.URL] {
				continue
			}
			m.imgFetching[img.URL] = true
			onImageFetch(img.URL)
		}
	}
}

// lineImages lists a line's image blocks: the block image line or the
// inline row's images.
func lineImages(l *core.Line) []*core.Image {
	if l.Image != nil {
		return []*core.Image{l.Image}
	}
	out := make([]*core.Image, 0, len(l.Imgs))
	for _, im := range l.Imgs {
		out = append(out, im.Image)
	}
	return out
}

// attachFetched attaches a fetch reply to its image lines (the remote
// images mode): the bytes land on every line sharing the URL, the
// pager re-expands, and the decode runs on the next prepareImages. A
// failed fetch keeps the Alt row AND the URL: the next toggle
// refetches (a transient network failure must not kill the image
// forever).
func (m *Model) attachFetched(e core.ImageFetched) {
	delete(m.imgFetching, e.URL)
	if m.pager == nil {
		return
	}
	if e.Err != nil {
		return
	}
	dirty := false
	for i := range m.pager.lines {
		l := &m.pager.lines[i]
		for _, img := range lineImages(l) {
			if img.URL != e.URL {
				continue
			}
			img.Data = e.Data
			dirty = true
		}
	}
	if !dirty {
		return
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
		for _, img := range lineImages(l) {
			if img.URL == "" {
				continue
			}
			delete(m.imgCache, img)
			img.Data = nil
			img.Cols, img.Rows = 0, 0
			dirty = true
		}
	}
	if dirty {
		m.pager.relayout()
	}
}

// imgPaint is one block's paint: its rect, the decoded image and the
// visible pixel slice (rows into the decoded image).
type imgPaint struct {
	rect cellRect
	img  image.Image
	top  int
	h    int
}

// paintRects computes the frame's image state: the blocks to paint
// (unchanged rects excluded) and the stale rects the frame displaces
// or drops. The non-pager and toggled-off paths stale every painted
// rect - the safety net for a mode change that skips the dispatch
// clears. The caller clears the stale rects BEFORE the text frame
// (EL removes sixel - an after-frame clear would erase the freshly
// drawn text) and paints after it.
func (m *Model) paintRects() (next map[*core.Image]imgPaint, stale []cellRect) {
	if m.imgProto == "" {
		return nil, nil
	}
	if m.mode != "pager" || m.pager == nil || !m.pager.images {
		for _, r := range m.painted {
			stale = append(stale, r)
		}
		return nil, stale
	}
	off, height := m.pager.vp.offset, m.pager.vp.height
	next = map[*core.Image]imgPaint{}
	for _, b := range m.pager.visibleImages() {
		img := b.img
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
		rect := cellRect{x: b.x, y: 1 + max(windowTop, 0), w: b.cols, h: visBot - visTop, bg: m.imgBG(b.line)}
		if prev, ok := m.painted[img]; ok && prev == rect {
			continue // unchanged: the terminal still holds the pixels
		}
		if prev, ok := m.painted[img]; ok {
			stale = append(stale, prev)
		}
		next[img] = imgPaint{rect: rect, img: scaled, top: visTop, h: visBot - visTop}
	}
	for img, prev := range m.painted {
		if _, ok := next[img]; !ok {
			stale = append(stale, prev)
		}
	}
	return next, stale
}

// paintImages writes the frame's image blocks to the terminal after
// the text frame (pixels never race the text; the stale rects were
// cleared before the frame).
func (m *Model) paintImages(next map[*core.Image]imgPaint) {
	if imageWriter == nil {
		return
	}
	for _, p := range next {
		paintImage(imageWriter, m.imgProto, p.img, p.top*imgCellH, p.h*imgCellH, p.rect.x, p.rect.y)
	}
	m.painted = make(map[*core.Image]cellRect, len(next))
	for img, p := range next {
		m.painted[img] = p.rect
	}
}

// clearImageRects erases every painted rect to its block background -
// the toggle-off, mode-exit and resize paths run it BEFORE the next
// frame so the collapsed text never renders under stale pixels.
func (m *Model) clearImageRects() {
	if len(m.painted) == 0 {
		return
	}
	if imageWriter != nil {
		rects := make([]cellRect, 0, len(m.painted))
		for _, r := range m.painted {
			rects = append(rects, r)
		}
		clearRects(imageWriter, rects)
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
		for _, img := range lineImages(&m.pager.lines[i]) {
			img.Cols, img.Rows = 0, 0
		}
	}
	m.pager.relayout()
}
