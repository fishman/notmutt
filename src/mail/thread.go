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

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset" // register the common charsets with the decoder

	"github.com/emersion/go-message/mail"

	"notmutt/core"
)

// maxPartBytes bounds one part's read (body content or attachment
// size count): the trust boundary gets a ceiling, never an unbounded
// buffered read (F4). The body content beyond the ceiling is dropped
// and marked; an attachment beyond it reports the capped size and a
// truncated marker. 8 MiB covers any realistic single part.
const maxPartBytes = 8 << 20

// RenderThread parses each message's file and produces the pager's
// render lines (the Line type lives in core - the open job ships them
// to the TUI in the ThreadLoaded event): per message a header block
// (subject, from/date), then the body with quoted levels and signature,
// then attachment lines. A per-message failure (no path, unreadable
// file, unparseable mail) becomes an error line so the rest of the
// thread stays readable - mutt-style partial content. RenderThread
// errors only when the input is empty: then there is nothing to show
// at all.
func RenderThread(msgs []core.Message) ([]core.Line, error) {
	if len(msgs) == 0 {
		return nil, fmt.Errorf("no messages in thread")
	}
	var lines []core.Line
	for i, m := range msgs {
		if i > 0 {
			lines = append(lines, core.Line{})
		}
		if len(m.Paths) == 0 {
			lines = append(lines, core.Line{Text: core.SanitizeControls(fmt.Sprintf("message %s: no path", m.ID)), Kind: core.LineError})
			continue
		}
		parsed, err := ParseMessage(m.Paths[0])
		if err != nil {
			lines = append(lines, core.Line{Text: core.SanitizeControls(fmt.Sprintf("failed to parse message: %v", err)), Kind: core.LineError})
			continue
		}
		lines = append(lines, renderMessage(parsed)...)
	}
	return lines, nil
}

type Message struct {
	From        string
	Date        string
	Subject     string
	MessageID   string
	To          []string // bare addresses, reply-all prefill
	Cc          []string
	Parts       []Part
	Attachments []Attachment
}

type Part struct {
	Body      string
	Quoted    int // 0..5, capped
	Signature bool
}

type Attachment struct {
	Name      string
	Size      int64
	Truncated bool // size count hit maxPartBytes; the listed size is the cap
}

// ParseMessage opens one mail file and reads its structure with
// go-message: the text/plain inline parts become body parts (quoted
// depth + signature split), other inline parts are skipped, and
// attachment parts are listed with their sizes.
//
// Unknown charsets/encodings are tolerated, not fatal: go-message
// returns the part with the body reader still raw (undecoded) when it
// cannot map the charset, so the part renders as-is instead of killing
// the whole thread. Only structural errors abort the parse.
func ParseMessage(path string) (*Message, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	mr, err := mail.CreateReader(f)
	if err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	defer mr.Close()
	hdr := mr.Header
	m := &Message{}
	if addrs, err := hdr.AddressList("From"); err == nil && len(addrs) > 0 {
		m.From = addrs[0].Address
	}
	m.MessageID = hdr.Get("Message-Id")
	if addrs, err := hdr.AddressList("To"); err == nil {
		for _, a := range addrs {
			m.To = append(m.To, a.Address)
		}
	}
	if addrs, err := hdr.AddressList("Cc"); err == nil {
		for _, a := range addrs {
			m.Cc = append(m.Cc, a.Address)
		}
	}
	m.Date = hdr.Get("Date")
	m.Subject = hdr.Get("Subject")
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		switch h := p.Header.(type) {
		case *mail.InlineHeader:
			if ct, _, _ := h.ContentType(); ct == "text/plain" {
				data, err := io.ReadAll(io.LimitReader(p.Body, maxPartBytes+1))
				if err != nil {
					return nil, fmt.Errorf("%s: %w", path, err)
				}
				parts := splitBody(string(data))
				if len(data) > maxPartBytes {
					parts = append(parts, Part{Body: "[content truncated]"})
				}
				m.Parts = append(m.Parts, parts...)
			}
		case *mail.AttachmentHeader:
			name, _ := h.Filename()
			if name == "" {
				name = "attachment"
			}
			size, err := io.Copy(io.Discard, io.LimitReader(p.Body, maxPartBytes+1))
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			m.Attachments = append(m.Attachments, Attachment{Name: name, Size: size, Truncated: size > maxPartBytes})
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

func renderMessage(m *Message) []core.Line {
	var lines []core.Line
	add := func(text string, kind core.LineKind, quoted int) {
		lines = append(lines, core.Line{Text: core.SanitizeControls(text), Kind: kind, Quoted: quoted})
	}
	add(m.Subject, core.LineSubject, 0)
	add(m.From+"  "+m.Date, core.LineHeader, 0)
	for _, p := range m.Parts {
		kind := core.LineBody
		if p.Signature {
			kind = core.LineSignature
		}
		add(p.Body, kind, p.Quoted)
	}
	for _, a := range m.Attachments {
		line := fmt.Sprintf("attachment: %s (%d bytes)", a.Name, a.Size)
		if a.Truncated {
			line = fmt.Sprintf("attachment: %s (truncated, >%d bytes)", a.Name, maxPartBytes)
		}
		add(line, core.LineAttachment, 0)
	}
	return lines
}
