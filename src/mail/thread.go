// Package mail is the content pipeline: it opens message files with
// go-message (R6) and produces F1-clean render lines. The TUI never
// touches mail files - parse and sanitize happen here, at the mail
// boundary.
package mail

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/emersion/go-message/mail"

	"notmutt/core"
)

// LineKind identifies the render line's style class (the pager maps
// kinds to the theme styles).
type LineKind int

const (
	LineSubject LineKind = iota
	LineHeader
	LineBody
	LineSignature
	LineAttachment
)

// Line is one pager render line: the text plus the style kind. All
// text has been stripped of C0/DEL/C1 control chars before it leaves
// this package (F1) - the TUI never sees raw mail content.
type Line struct {
	Text   string
	Kind   LineKind
	Quoted int // LineBody only, 0..5 (capped)
}

// RenderThread parses each message's file and produces the pager's
// render lines: per message a header block (subject, from/date), then
// the body with quoted levels and signature, then attachment lines.
func RenderThread(msgs []core.Message) ([]Line, error) {
	var lines []Line
	for i, m := range msgs {
		if i > 0 {
			lines = append(lines, Line{})
		}
		if len(m.Paths) == 0 {
			return nil, fmt.Errorf("message %s: no path", m.ID)
		}
		parsed, err := ParseMessage(m.Paths[0])
		if err != nil {
			return nil, err
		}
		lines = append(lines, renderMessage(parsed)...)
	}
	return lines, nil
}

type Message struct {
	From        string
	Date        string
	Subject     string
	Parts       []Part
	Attachments []Attachment
}

type Part struct {
	Body      string
	Quoted    int // 0..5, capped
	Signature bool
}

type Attachment struct {
	Name string
	Size int64
}

// ParseMessage opens one mail file and reads its structure with
// go-message: the text/plain inline parts become body parts (quoted
// depth + signature split), other inline parts are skipped, and
// attachment parts are listed with their sizes.
func ParseMessage(path string) (*Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	mr, err := mail.CreateReader(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	hdr := mr.Header
	m := &Message{}
	if addrs, err := hdr.AddressList("From"); err == nil && len(addrs) > 0 {
		m.From = addrs[0].Address
	}
	m.Date = hdr.Get("Date")
	m.Subject = hdr.Get("Subject")
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		switch h := p.Header.(type) {
		case *mail.InlineHeader:
			if ct, _, _ := h.ContentType(); ct == "text/plain" {
				data, err := io.ReadAll(p.Body)
				if err != nil {
					return nil, err
				}
				m.Parts = append(m.Parts, splitBody(string(data))...)
			}
		case *mail.AttachmentHeader:
			name, _ := h.Filename()
			if name == "" {
				name = "attachment"
			}
			size, err := io.Copy(io.Discard, p.Body)
			if err != nil {
				return nil, err
			}
			m.Attachments = append(m.Attachments, Attachment{Name: name, Size: size})
		}
	}
	return m, nil
}

// splitBody splits the raw text into parts: quoted depth by leading
// ">" count (capped at 5), signature after the first standalone "-- ".
// The marker line itself stays a part - renderMessage emits it as-is,
// so the delimiter renders exactly once, never re-prefixed.
func splitBody(text string) []Part {
	var parts []Part
	sig := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if !sig && line == "-- " {
			sig = true
		}
		depth := 0
		for depth < 5 {
			rest := strings.TrimPrefix(line, ">")
			if rest == line {
				break
			}
			depth++
			line = strings.TrimPrefix(rest, " ")
		}
		line = strings.TrimPrefix(line, " ")
		parts = append(parts, Part{Body: line, Quoted: depth, Signature: sig})
	}
	// the trailing newline is line termination, not an empty line
	if len(parts) > 0 && strings.HasSuffix(text, "\n") {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// stripControls drops C0/DEL/C1 runes (F1; the same policy as the
// TUI's index renderer, enforced here at the mail boundary).
func stripControls(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || (r >= 0x7F && r <= 0x9F) }) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 0x20 || (r >= 0x7F && r <= 0x9F) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func renderMessage(m *Message) []Line {
	var lines []Line
	add := func(text string, kind LineKind, quoted int) {
		lines = append(lines, Line{Text: stripControls(text), Kind: kind, Quoted: quoted})
	}
	add(m.Subject, LineSubject, 0)
	add(m.From+"  "+m.Date, LineHeader, 0)
	for _, p := range m.Parts {
		kind := LineBody
		if p.Signature {
			kind = LineSignature
		}
		add(p.Body, kind, p.Quoted)
	}
	for _, a := range m.Attachments {
		add(fmt.Sprintf("attachment: %s (%d bytes)", a.Name, a.Size), LineAttachment, 0)
	}
	return lines
}
