package compose

import (
	"mime"
	"path/filepath"
	"regexp"
	"strings"
)

// ContentTypeOf derives the body part's MIME type: text/plain by
// default, text/markdown when the body carries markdown syntax. ONE
// definition - the compose row and Assemble share it. The heuristic is
// conservative: at least TWO distinct constructs must match, so a plain
// reply (quote lines, one signature) never flips.
var (
	mdHeading = regexp.MustCompile(`(?m)^#{1,6}\s`)
	mdFence   = regexp.MustCompile("(?m)^\\s*```")
	mdLink    = regexp.MustCompile(`\[[^\]]+\]\([^)]+\)`)
	mdBullet  = regexp.MustCompile(`(?m)^\s*[-*+]\s`)
	mdBold    = regexp.MustCompile(`\*\*[^*]+\*\*|__[^_]+__`)
)

func ContentTypeOf(body string) string {
	n := 0
	for _, re := range []*regexp.Regexp{mdHeading, mdFence, mdLink, mdBullet, mdBold} {
		if re.MatchString(body) {
			n++
		}
	}
	if n >= 2 {
		return "text/markdown"
	}
	return "text/plain"
}

// MimeTypeOf guesses a file's MIME type from its extension
// (application/octet-stream when unknown). ONE definition: the same
// value rides the attachment's Content-Type on the wire (Assemble) and
// in the compose row - the dialogue shows what the mail will carry.
// The type part only; mime.TypeByExtension appends charset params for
// text types, which the wire does not set here.
func MimeTypeOf(name string) string {
	t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
	if i := strings.IndexByte(t, ';'); i >= 0 {
		t = t[:i]
	}
	if t == "" {
		return "application/octet-stream"
	}
	return t
}

// PartFacts are one part's wire facts as Assemble writes them: the
// MIME type, the transfer encoding, the charset (text parts only).
// ONE definition - Assemble applies the facts to the wire and the
// compose rows display them (the matcha sender model: the composer
// decides the encoding, the UI never hardcodes it). Charset stays
// empty for parts whose wire carries none.
type PartFacts struct {
	Type, Encoding, Charset string
}

// InlineFacts derives the body part's wire facts: the derived content
// type, quoted-printable (the composer's fixed text encoding, the
// matcha shape), the explicit charset.
func InlineFacts(s *State) PartFacts {
	return PartFacts{Type: ContentTypeOf(s.Body), Encoding: "quoted-printable", Charset: "utf-8"}
}

// AttachmentFacts derives an attachment's wire facts: the detected
// type (octet-stream when the extension is unknown - the reader
// default), base64 (the composer's fixed attachment encoding), no
// charset on the wire.
func AttachmentFacts(a Attachment) PartFacts {
	typ := a.MimeType
	if typ == "" {
		typ = "application/octet-stream"
	}
	return PartFacts{Type: typ, Encoding: "base64"}
}
