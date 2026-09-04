// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

// Terminal image emission: the mail renderer's image lines are decoded
// + scaled + emitted ONLY on the load-remote-images key (privacy gate
// - the bytes stay inert until then) and only for the visible window.
// Protocol: kitty opt-in via [pager] image-protocol (env match), sixel
// via the engaged screen's DA negotiation (under tmux a tmux query -
// tmux answers DA itself); unsupported terminals keep the collapsed
// Alt row. The writer is /dev/tty (the tcell screen cannot emit raw
// image protocols) and paint runs AFTER the frame flush, so pixels
// never race the text.

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

	"github.com/gdamore/tcell/v3"
	"github.com/mattn/go-sixel"
	"golang.org/x/image/draw"

	"notmutt/config"
	"notmutt/core"
)

const (
	imgMaxCols = 100  // paint cap: an image line never exceeds 100 cells wide
	kittyChunk = 4096 // kitty's max payload per DCS frame
)

// imgCellW/H are the terminal cell size in pixels: probed from the
// TIOCGWINSZ ioctl at startup and on resize, 10x20 when the pty
// reports no pixels. A wrong cell size misaligns every image.
var (
	imgCellW = 10
	imgCellH = 20
)

// imageWriter is the paint sink: /dev/tty in Run, nil in tests (paint paths no-op, so frame tests never write to a terminal).
var imageWriter io.Writer

// detectImageProtocol picks the image protocol: kitty only when
// [pager] image-protocol opts in and the kitty env matches; sixel when
// the screen's DA negotiation reported it (tmux answers DA1 itself
// with a build-time reply, so under tmux a tmux query replaces the
// negotiation). "" = no image support.
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
	if s != nil && s.Capabilities()&tcell.CapabilitySixel != 0 {
		return "sixel"
	}
	return ""
}

// setCellSize adopts a measured cell size (window pixels over cell counts); bounds keep a corrupt ioctl from breaking the paint math.
func setCellSize(cols, rows, pxW, pxH int) {
	if cols <= 0 || rows <= 0 || pxW <= 0 || pxH <= 0 {
		return
	}
	cw, ch := pxW/cols, pxH/rows
	if cw >= 1 && cw <= 60 && ch >= 1 && ch <= 60 {
		imgCellW, imgCellH = cw, ch
	}
}

// sixelCapable is the negotiated-capability seam: the app reads the sixel bit
// from the screen's Capabilities bitfield (tests stub it).
type sixelCapable interface {
	Capabilities() tcell.Capabilities
}

// tmuxSixel asks tmux for its sixel support (the server format; tmux's own DA1 reply to panes is fixed at build time and omits it).
var tmuxSixel = func() bool {
	out, err := exec.Command("tmux", "display", "-p", "#{sixel_support}").Output()
	return err == nil && strings.TrimSpace(string(out)) == "1"
}

// imgFillScaleCap: a standalone fill may upscale only this far - a figure
// whose natural width is at least half the column tops up to full width on
// a wide terminal; a small asset is never blown up to fill.
const imgFillScaleCap = 2.0

// decodeImage decodes and scales the raw bytes to the cell grid: at
// most widthCells (capped at imgMaxCols) wide and heightRows tall,
// aspect preserved, snapped UP to exact cell multiples so the pixel
// dims align with the terminal's cells. dispW/dispH are the email's
// declared display size in pixels (0 = unspecified): the declared axis
// is the target scale - an email that sizes its section for a 600px
// chart gets a 600px chart, capped by the window. With no declared
// size the scale caps at 1 (natural size, never upscale); a fill
// request relaxes that cap up to imgFillScaleCap so a standalone
// figure whose natural width sits below the column still stretches
// toward it. Returns the scaled image (always NRGBA) plus its cell
// dims.
func decodeImage(data []byte, widthCells, heightRows, dispW, dispH int, fill bool) (image.Image, int, int, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, err
	}
	widthCells = max(1, min(widthCells, imgMaxCols))
	heightRows = max(1, heightRows)
	sw, sh := float64(src.Bounds().Dx()), float64(src.Bounds().Dy())
	scale := math.Min((float64(widthCells)*float64(imgCellW))/sw, (float64(heightRows)*float64(imgCellH))/sh)
	if dispW > 0 {
		scale = math.Min(scale, float64(dispW)/sw)
	}
	if dispH > 0 {
		scale = math.Min(scale, float64(dispH)/sh)
	}
	if scale > 1 && dispW == 0 && dispH == 0 && (!fill || scale > imgFillScaleCap) {
		scale = 1
	}
	cols := max(1, int(sw*scale/float64(imgCellW)))
	rows := max(1, int(sh*scale/float64(imgCellH)))
	dw, dh := cols*imgCellW, rows*imgCellH
	dst := image.NewNRGBA(image.Rect(0, 0, dw, dh))
	// ApproxBiLinear over CatmullRom: ~3x cheaper on downscale (the
	// scroll settle + first-view decode), visually fine on terminal images.
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
	return dst, cols, rows, nil
}

// kittySend writes the PNG of img as chunked kitty data under the given
// action/qualifier head (first chunk carries it, later chunks only m).
func kittySend(w io.Writer, head string, img image.Image) {
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
			fmt.Fprintf(w, "\x1b_G%s,m=%d;%s\x1b\\", head, m, data[i*kittyChunk:end])
		} else {
			fmt.Fprintf(w, "\x1b_Gm=%d;%s\x1b\\", m, data[i*kittyChunk:end])
		}
	}
}

// kittyTransmit first-sights an image: upload its full decode under a
// stable image id (transmit-only - data, no placement yet). The visible
// slice is placed right after, so a later scroll can re-crop ANY window
// of the stored decode with a bare a=p, never a resend.
func kittyTransmit(w io.Writer, id int, img image.Image) {
	kittySend(w, fmt.Sprintf("a=t,i=%d,f=100,t=d", id), img)
}

// kittyPlace moves an already-transmitted image: re-issuing the (id, 1)
// placement at the cursor replaces the old one in place - the vacated
// cells clear themselves, nothing else needs deleting. yPx/hPx crop the
// placement to the visible rows of the stored decode.
func kittyPlace(w io.Writer, id, yPx, hPx int) {
	fmt.Fprintf(w, "\x1b_Ga=p,i=%d,p=1,y=%d,h=%d\x1b\\", id, yPx, hPx)
}

// kittyDeletePlacement removes one placement but keeps its image data
// (lowercase d=i): an image scrolled away re-places on the way back with
// no resend.
func kittyDeletePlacement(w io.Writer, id int) {
	fmt.Fprintf(w, "\x1b_Ga=d,d=i,i=%d,p=1\x1b\\", id)
}

// kittyFreeAll wipes every kitty image AND frees its data (uppercase
// d=A): the toggle-off/resize/exit paths tear the whole layer down, so
// ids reset and the next transmit may reuse a number without colliding
// with retained data.
func kittyFreeAll(w io.Writer) {
	fmt.Fprint(w, "\x1b_Ga=d,d=A\x1b\\")
}

// sixelEncode emits the image as sixels; go-sixel writes the complete
// DCS sequence in one call. Transparent (P2=1) leaves the cleared page
// background visible behind alpha pixels - P2=0 paints them in the
// terminal's default background (dark boxes around icons on a white page).
func sixelEncode(w io.Writer, img image.Image) {
	e := sixel.NewEncoder(w)
	e.Transparent = true
	e.Encode(img)
}

// cropImage cuts a pixel sub-rect of the scaled image (decodeImage always produces NRGBA, but draw.Draw crops any source).
func cropImage(src image.Image, x, y, w, h int) image.Image {
	dst := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), src, image.Pt(x, y), draw.Src)
	return dst
}

// cellRect is a screen rect in cells (paint-diff bookkeeping) with the block's clear background (the line's declared bg or the theme).
type cellRect struct {
	x, y, w, h int
	bg         string
}

// clearRects erases a rect set to each rect's background - per-row EL,
// the erase that also removes sixel graphics (a space fill leaves the
// pixels). Runs BEFORE the text frame, so the fresh content draws over
// the cleared rows.
func clearRects(w io.Writer, rects []cellRect) {
	if w == nil {
		return
	}
	for _, r := range rects {
		if r.w < 1 || r.h < 1 {
			continue
		}
		markRowsCleared(loopScreen, r.y, r.h)
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

// imgBG is an image block's clear color: the line's declared background (the mail's body/html bg, a table cell's bgcolor) or the theme default when unset.
func (m *Model) imgBG(line int) string {
	if b := m.pager.lines[line].Bg; b != "" {
		return b
	}
	return m.themeBG()
}

// composeImages builds one offscreen canvas covering the union of the
// paint rects: each image's visible slice draws at its cell-aligned
// offset, the gap cells stay transparent - P2=1 shows the page
// background (and any unchanged image) through them. One DCS burst
// paints the batch atomically; the per-image bursts were the flicker.
func composeImages(paints []imgPaint) (image.Image, cellRect) {
	ux, uy := paints[0].rect.x, paints[0].rect.y
	bx, by := ux+paints[0].rect.w, uy+paints[0].rect.h
	for _, p := range paints[1:] {
		ux = min(ux, p.rect.x)
		uy = min(uy, p.rect.y)
		bx = max(bx, p.rect.x+p.rect.w)
		by = max(by, p.rect.y+p.rect.h)
	}
	union := cellRect{x: ux, y: uy, w: bx - ux, h: by - uy}
	canvas := image.NewNRGBA(image.Rect(0, 0, union.w*imgCellW, union.h*imgCellH))
	for _, p := range paints {
		slice := cropImage(p.img, 0, p.top*imgCellH, p.img.Bounds().Dx(), p.h*imgCellH)
		ox, oy := (p.rect.x-ux)*imgCellW, (p.rect.y-uy)*imgCellH
		draw.Draw(canvas, image.Rect(ox, oy, ox+slice.Bounds().Dx(), oy+slice.Bounds().Dy()), slice, image.Point{}, draw.Over)
	}
	return canvas, union
}

// bgHexOf renders a lipgloss background color as #rrggbb; "" when the
// theme leaves the terminal default (NoColor) or the color is not a
// plain hex - the clear then uses the terminal's default background.
func bgHexOf(c color.Color) string {
	if r, ok := c.(color.RGBA); ok && r.A == 0xff {
		return fmt.Sprintf("#%02x%02x%02x", r.R, r.G, r.B)
	}
	return ""
}

// themeBG is the theme's normal background hex (#rrggbb, "" when the theme leaves the terminal default) - the clear fill color.
func (m *Model) themeBG() string {
	return bgHexOf(m.styles.Normal.GetBackground())
}

// prepareImages decodes the pager window's image lines (the privacy
// gate: bytes decode ONLY here - the render-images toggle and scrolls
// into an image) and re-lays-out when a decode gives a block dims.
// Runs before the frame builds, so a decode-gained expansion lands in
// the SAME frame.
func (m *Model) prepareImages() {
	if m.mode != "pager" || m.pager == nil || !m.pager.images || m.imgProto == "" {
		return
	}
	if m.imgSuppress {
		return // a scroll burst: decode resumes when the pager settles
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
		// a standalone image fills the text column (decodeImage's window
		// budget - a near-column figure tops up a wide terminal, a chart
		// sized for a 600px browser column must not stay half the width of a
		// 120-cell terminal) instead of the email's authored disp width.
		// Inline-with-text images keep their authored disp (the mail's
		// intent).
		dw, dh := img.DispW, img.DispH
		fill := m.pager.standaloneLine(b.line, img)
		if fill {
			dw, dh = 0, 0
		}
		scaled, cols, rows, err := decodeImage(img.Data, m.pager.width, m.pager.vp.height, dw, dh, fill)
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

// setImgMode applies the load-remote-images toggle (alt+i): on lays the
// page out images-on at real geometry (the reopen re-renders with the
// images flag - embedded images size from their bytes, remote seat once
// their measured px ride the refine), off restores the placeholder
// markers. The local switch (setImages + fetch) is immediate so an
// already-decoded image expands before the reopen reply lands; the reply
// only replaces when its images flag differs from the installed content.
// Toggling off drops the fetched bytes and the size caches - the network
// never feeds the decode outside the mode.
func (m *Model) setImgMode(mode int) {
	m.imgSuppress = false // a mode toggle ends any scroll-burst hold
	switch mode {
	case 0:
		m.clearImageRects() // before the frame: the collapsed markers render
		m.dropRemoteData()
		m.pager.setImages(false)
		m.imgRemote = map[string][]byte{}
		m.imgRemoteSize = map[string]core.ImgSize{}
		m.refinePending = false
	case 1:
		m.pager.setImages(true)
		m.fetchRemoteImages()
	}
	m.imgMode = mode
	m.reopenDispatch(pagerThreadID(m.pager), pagerMsgID(m.pager), m.renderMode, m.showHeaders, m.linkMode, mode == 1, m.imgRemoteSize, false)
}

// fetchRemoteImages arms the message's remote-image fetches (the
// remote images mode): every URL-image line without bytes fetches
// once - the keypress is the gate, the decode stays per-window
// (prepareImages), so below-fold images fetch now and expand when
// scrolled into view. imgFetching single-flights in-flight URLs.
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

// lineImages lists a line's image blocks: the block image line or the inline row's images.
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

// attachFetched caches a fetch reply and attaches it to its image lines
// (the remote images mode): the bytes land on every line sharing the
// URL and the measured px join the refine payload. A size that was
// newly measured arms one coalesced refine reopen once the in-flight set
// empties - the images-on content re-lays-out so the remote image seats
// at real geometry. A failed fetch keeps the Alt row AND the URL, so
// the next toggle refetches (a transient failure must not kill the image
// forever). The bytes never cross into the worker - only the px ride
// the refine.
func (m *Model) attachFetched(e core.ImageFetched) {
	delete(m.imgFetching, e.URL)
	if m.pager == nil {
		return
	}
	if e.Err != nil {
		return
	}
	if m.imgRemote == nil {
		m.imgRemote = map[string][]byte{}
		m.imgRemoteSize = map[string]core.ImgSize{}
	}
	m.imgRemote[e.URL] = e.Data
	newSize := false
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(e.Data)); err == nil && cfg.Width > 0 && cfg.Height > 0 {
		if prev, ok := m.imgRemoteSize[e.URL]; !ok || prev.W != cfg.Width || prev.H != cfg.Height {
			newSize = true
		}
		m.imgRemoteSize[e.URL] = core.ImgSize{W: cfg.Width, H: cfg.Height}
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
	if dirty {
		m.pager.relayout()
	}
	// a new size seats only once the images-on content is installed (the
	// alt+i reopen reply): arm the refine when the in-flight set empties
	if newSize && m.images && len(m.imgFetching) == 0 && !m.refinePending {
		m.refinePending = true
		m.reopenDispatch(pagerThreadID(m.pager), pagerMsgID(m.pager), m.renderMode, m.showHeaders, m.linkMode, true, m.imgRemoteSize, true)
	}
}

// attachRemoteBytes copies the cached remote bytes onto a reopened
// pager's URL lines (the images-on/refine replies carry fresh image
// objects whose Data the worker never filled - the decode must find its
// bytes without a refetch).
func (m *Model) attachRemoteBytes() {
	if m.pager == nil {
		return
	}
	for i := range m.pager.lines {
		for _, img := range lineImages(&m.pager.lines[i]) {
			if img.URL == "" || len(img.Data) > 0 {
				continue
			}
			if data, ok := m.imgRemote[img.URL]; ok {
				img.Data = data
			}
		}
	}
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

// imgPaint is one block's paint: its rect, the decoded image, and the visible pixel slice (rows into the decoded image).
type imgPaint struct {
	rect cellRect
	img  image.Image
	top  int
	h    int
}

// paintRects computes the frame's image state. Sixel: the blocks to
// paint (unchanged rects excluded) plus the stale rects the frame
// displaces or drops - the caller clears them BEFORE the text frame (EL
// removes sixel, and an after-frame clear would erase the freshly drawn
// text), then paints after it. Kitty: kittyRects returns every visible
// image with no stale rects - its placements move and crop in place.
// The non-pager and toggled-off paths stale every painted rect - the
// safety net for a mode change that skips the dispatch clears.
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
	if m.imgProto == "kitty" {
		// kitty placements move and crop in place - nothing stale clears
		// (EL erases text only) and paintKitty skips the unchanged ones,
		// so every visible image returns.
		return m.kittyRects()
	}
	if m.imgSuppress {
		return nil, nil // a scroll burst: rects cleared at entry, text only until the settle tick
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
		// the block's window rect: rows [windowTop, windowTop+Rows); the visible top sits at max(windowTop, 0), and the paint emits only the visible pixel slice below it
		windowTop := b.doc - off
		visTop := max(0, -windowTop)
		visBot := min(img.Rows, height-windowTop)
		if visBot <= visTop {
			continue
		}
		rect := cellRect{x: b.x, y: 1 + max(windowTop, 0), w: b.cols, h: visBot - visTop, bg: m.imgBG(b.line)}
		p := imgPaint{rect: rect, img: scaled, top: visTop, h: visBot - visTop}
		if prev, ok := m.painted[img]; ok && prev == rect {
			continue // unchanged: the terminal still holds the pixels
		}
		if prev, ok := m.painted[img]; ok {
			stale = append(stale, prev)
		}
		next[img] = p
	}
	// a scroll over still-visible images: the same set is painted again at a
	// shifted spot. A pure translation (every rect the same size, just moved)
	// enters the hold on the first frame - the pinned settle case. A re-crop
	// (an image cropping at a window edge as the page moves under it) also
	// re-encodes the whole set per frame, but a lone step must stay live (a
	// single page-down over a tall image would otherwise blank it) - only a
	// re-crop burst within the debounce window of the last live re-crop holds.
	// A set change - an image entering or leaving - stays live: only the
	// changed rect re-encodes, the untouched ones keep their pixels.
	same := len(next) == len(m.painted) && len(next) > 0
	if same {
		for img := range m.painted {
			if _, ok := next[img]; !ok {
				same = false
				break
			}
		}
	}
	translated := same
	if same {
		for img, prev := range m.painted {
			if n := next[img]; n.rect.w != prev.w || n.rect.h != prev.h {
				translated = false
				break
			}
		}
	}
	if translated || (same && time.Since(m.cropLiveAt) < imgSettleDebounce) {
		m.enterImgSuppress()
		return nil, nil
	}
	if same {
		m.cropLiveAt = time.Now() // a lone re-crop: paint it, arm the burst gate
	}
	for img, prev := range m.painted {
		if _, ok := next[img]; !ok {
			stale = append(stale, prev)
		}
	}
	return next, stale
}

// kittyRects lists the frame's visible images for the kitty re-place
// model. Nothing is stale and nothing is suppressed: paintKitty decides
// per image between re-place, delete and skip, so the whole visible set
// returns every frame.
func (m *Model) kittyRects() (map[*core.Image]imgPaint, []cellRect) {
	off, height := m.pager.vp.offset, m.pager.vp.height
	next := map[*core.Image]imgPaint{}
	for _, b := range m.pager.visibleImages() {
		img := b.img
		if img.Rows == 0 {
			continue // not decoded: the Alt row shows
		}
		scaled, ok := m.imgCache[img]
		if !ok {
			continue
		}
		windowTop := b.doc - off
		visTop := max(0, -windowTop)
		visBot := min(img.Rows, height-windowTop)
		if visBot <= visTop {
			continue
		}
		rect := cellRect{x: b.x, y: 1 + max(windowTop, 0), w: b.cols, h: visBot - visTop, bg: m.imgBG(b.line)}
		next[img] = imgPaint{rect: rect, img: scaled, top: visTop, h: visBot - visTop}
	}
	return next, nil
}

// enterImgSuppress starts the scroll-burst hold: the painted rects clear
// once (the scrolled text would otherwise under-run stale pixels), and
// decode+encode pause until imgSettleTick lifts the hold on a still pager.
func (m *Model) enterImgSuppress() {
	m.clearImageRects()
	m.imgSuppress = true
	m.cropLiveAt = time.Time{} // a hold ends any re-crop burst arm
	m.imgSettleAt = time.Now()
	if m.pager != nil {
		m.imgSettleOff, m.imgSettleX = m.pager.vp.offset, m.pager.x
	}
}

// paintImages writes the frame's image blocks to the terminal after
// the text frame (pixels never race the text; the stale rects were
// cleared before the frame). The sixel batch composes into ONE
// offscreen canvas and transmits as one DCS: the terminal paints the
// frame's images atomically instead of sweeping burst by burst. Kitty
// routes to paintKitty (per-image placements, no compose).
func (m *Model) paintImages(next map[*core.Image]imgPaint) {
	if m.imgProto == "kitty" {
		m.paintKitty(next)
		return
	}
	if imageWriter == nil || len(next) == 0 {
		return
	}
	paints := make([]imgPaint, 0, len(next))
	for _, p := range next {
		paints = append(paints, p)
	}
	canvas, union := composeImages(paints)
	fmt.Fprintf(imageWriter, "\x1b[%d;%dH", union.y+1, union.x+1)
	sixelEncode(imageWriter, canvas)
	m.painted = make(map[*core.Image]cellRect, len(next))
	for img, p := range next {
		m.painted[img] = p.rect
	}
}

// paintKitty places the frame's images under the per-image re-place
// model: a decode is transmitted once under a stable id, then every
// frame a moved/cropped image re-places with a tiny a=p (the vacated
// cells clear themselves), a departed one is deleted by id - data kept,
// so it re-places cheaply on the way back - and a static one is left
// untouched. No full-window wipe, no hold.
func (m *Model) paintKitty(next map[*core.Image]imgPaint) {
	w := imageWriter
	for img := range m.painted {
		if _, ok := next[img]; ok {
			continue
		}
		if id, ok := m.kimg[img]; ok && w != nil {
			kittyDeletePlacement(w, id) // left the window: drop the placement, keep the data
		}
		delete(m.painted, img)
	}
	for img, p := range next {
		prev, was := m.painted[img]
		id, have := m.kimg[img]
		if !have {
			id = m.kimgNext
			m.kimgNext++
			m.kimg[img] = id
			if w != nil {
				kittyTransmit(w, id, p.img)
			}
		}
		if was && prev == p.rect {
			continue // unchanged: the terminal still holds this exact placement
		}
		if w != nil {
			fmt.Fprintf(w, "\x1b[%d;%dH", p.rect.y+1, p.rect.x+1)
			kittyPlace(w, id, p.top*imgCellH, p.h*imgCellH)
		}
		m.painted[img] = p.rect
	}
}

// clearImageRects erases every painted rect to its block background - the toggle-off, mode-exit and resize paths run it BEFORE the next frame so the collapsed text never renders under stale pixels.
func (m *Model) clearImageRects() {
	if m.imgProto == "kitty" {
		// a placement is pixels only a delete removes: free the whole layer
		// (uppercase d=A drops the data too) and reset the transmit state so
		// the next frame transmits under fresh ids.
		if imageWriter != nil && (len(m.painted) > 0 || m.kimgNext > 0) {
			kittyFreeAll(imageWriter)
		}
		m.kimg = map[*core.Image]int{}
		m.kimgNext = 0
		clear(m.painted)
		return
	}
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

// resetImages drops the decode cache and painted rects on a resize (the cell math changed): the dims zero, the layout collapses, the next prepareImages re-decodes at the new width.
func (m *Model) resetImages() {
	m.imgSuppress = false // a resize ends any scroll-burst hold
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
