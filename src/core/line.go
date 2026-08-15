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

// Line is one pager render line: the text plus the style kind. All text
// has been stripped of C0/DEL/C1 control chars before it leaves the mail
// package (F1) - the render surface never sees raw mail content. Lines
// are produced on the async open job (mail.RenderThread + registered
// render transforms) and travel to the TUI in the ThreadLoaded event.
type Line struct {
	Text   string
	Kind   LineKind
	Quoted int // LineBody only, 0..5 (capped)
}
