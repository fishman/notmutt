// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package core

// LineKind identifies the render line's style class (the TUI maps kinds
// to the theme styles).
type LineKind int

const (
	LineSubject LineKind = iota
	LineHeader
	LineBody
	LineSignature
	LineAttachment
	LineError
)

// LineAttrs is a bitmask of run emphasis - the renderer-visible subset
// of the theme's unified attrs (R11), transported as data so the pager
// can join runs into one SGR sequence.
type LineAttrs uint8

const (
	AttrBold LineAttrs = 1 << iota
	AttrItalic
	AttrUnderline
	AttrReverse
)

// Run is one styled text span of an HTML-sourced body line. Fg/Bg are
// hex colors (#rrggbb) or "" for inherit - the mail's own colors, theme
// independent; the pager resolves them to SGR at paint.
type Run struct {
	Text  string
	Fg    string // #rrggbb, "" = none
	Bg    string
	Attrs LineAttrs
	// Label marks the F key's link-marker run: the "[N]" the html
	// renderer inserts before every link. It never merges with mail
	// text, so the TUI finds the marker under entry by exact run match.
	Label bool
	// Image marks an inline image's placeholder run: the pager blanks
	// the run once the image decodes (Rows > 0), the pixels paint at
	// the run's cell offset (the line's ImagePos).
	Image *Image
}

// Image is a referenced mail image rendered on its own line(s): Data
// is the raw encoded bytes (png/jpeg/gif/webp), Alt the fallback text.
// Images are NEVER rendered by default - the pager shows the Alt text
// and the TUI decodes + paints only on the render-images key (privacy
// gate: local images decode on demand, remote ones fetch only in the
// remote mode - URL names the http(s) src of a remote image that has
// no bytes yet; the TUI fetches it on the key and sets Data).
// Cols/Rows are the cell dimensions the TUI sets when it decodes the
// image for the render-images path; 0 means not decoded - the line
// collapses to one Alt row.
type Image struct {
	Data []byte
	URL  string // the http(s) src, when the image is remote (Data empty)
	Alt  string
	Cols int
	Rows int
}

// Line is one pager render line: the text plus the style kind. All text
// has been stripped of C0/DEL/C1 control chars before it leaves the mail
// package (F1) - the render surface never sees raw mail content. Lines
// are produced on the async open job (mail.RenderThread + registered
// render transforms) and travel to the TUI in the ThreadLoaded event.
type Line struct {
	Text   string
	Kind   LineKind
	Quoted int // LineBody only, 0..5 (capped)
	Runs   []Run
	// Bg is the line's default background (#rrggbb, "" = theme): the
	// html view's mail-declared background (the <body> color), so the
	// whole rendered region - pad and blank lines included - respects
	// it. Run backgrounds paint over it; trailing blocks extend over
	// the pad.
	Bg    string
	Image *Image // LineBody only; the line occupies Image.Rows rows
	// Imgs holds a text line's inline images (the icon rows): each
	// image's block sits at cell offset X on the line, the line keeps
	// its words (the placeholder runs blank once the image decodes).
	Imgs []ImagePos
}

// ImagePos is one inline image block on a text line: the image and
// its cell offset.
type ImagePos struct {
	Image *Image
	X     int
}
