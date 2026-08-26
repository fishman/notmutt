// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package core

// LineKind identifies the render line's style class (the TUI maps
// kinds to theme styles).
type LineKind int

const (
	LineSubject LineKind = iota
	LineHeader
	LineBody
	LineSignature
	LineAttachment
	LineError
	LineSecurity
)

// LineAttrs is a bitmask of run emphasis - the renderer-visible subset
// of the theme's unified attrs (R11), transported as data so the pager
// joins runs into one SGR sequence.
type LineAttrs uint8

const (
	AttrBold LineAttrs = 1 << iota
	AttrItalic
	AttrUnderline
	AttrReverse
)

// Run is one styled text span of an HTML-sourced body line; Fg/Bg are
// hex colors (#rrggbb) or "" for inherit, resolved to SGR at paint.
type Run struct {
	Text  string
	Fg    string // #rrggbb, "" = none
	Bg    string
	Attrs LineAttrs
	// Label marks the F key's link-marker run: the "[N]" the html
	// renderer inserts before every link; it never merges with mail
	// text, so the TUI finds it by exact run match.
	Label bool
	// Image marks an inline image's placeholder run: the pager blanks
	// it once the image decodes (Rows > 0) and paints at the cell offset.
	Image *Image
}

// Image is a referenced mail image rendered on its own line(s): Data
// is the raw encoded bytes (png/jpeg/gif/webp), Alt the fallback text.
// Images are NEVER rendered by default - the pager shows the Alt text
// and the TUI decodes + paints only on the render-images key (privacy
// gate: local decode on demand, remote fetch only in remote mode - URL
// names the http(s) src with no bytes yet). Cols/Rows are the decoded
// cell dimensions, 0 = not decoded (line collapses to one Alt row).
type Image struct {
	Data []byte
	URL  string // the http(s) src, when the image is remote (Data empty)
	Alt  string
	Cols int
	Rows int
	// DispW/DispH are the email's declared display size in pixels (img
	// width/height attrs or style; 0 = unspecified); the decode targets it.
	DispW int
	DispH int
}

// Line is one pager render line: the text plus the style kind. All text
// is stripped of C0/DEL/C1 control chars before it leaves the mail
// package (F1) - the render surface never sees raw mail content. Lines
// are produced on the async open job and travel in ThreadLoaded.
type Line struct {
	Text   string
	Kind   LineKind
	Quoted int // LineBody only, 0..5 (capped)
	// OK is LineSecurity only: the S/MIME signature verified (valid + chain
	// to pinned roots); the pager renders it green, a failed verify red.
	OK   bool
	Runs []Run
	// Bg is the line's default background (#rrggbb, "" = theme): the
	// html view's mail-declared <body> color, respected by the whole
	// rendered region - pad and blank lines included. Run backgrounds
	// paint over it; trailing blocks extend over the pad.
	Bg    string
	Image *Image // LineBody only; the line occupies Image.Rows rows
	// Imgs holds a text line's inline images (the icon rows): each
	// block sits at cell offset X while the line keeps its words (the
	// placeholder runs blank once the image decodes).
	Imgs []ImagePos
}

// ImagePos is one inline image block on a text line: the image and
// its cell offset.
type ImagePos struct {
	Image *Image
	X     int
}
