// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

// Package mail is the content pipeline: it opens message files with
// go-message (R6) and produces F1-clean render lines. The TUI never
// touches mail files - parse and sanitize happen here.
package mail

import (
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset" // register the common charsets with the decoder

	"github.com/emersion/go-message/mail"

	"notmutt/core"
)

// maxPartBytes bounds one part's read - a ceiling at the trust
// boundary, never an unbounded buffered read (F4); beyond it the body
// drops/marks truncated. 8 MiB covers any realistic part.
const maxPartBytes = 8 << 20

// maxImgBytes bounds one image's buffered data (render-on-key);
// imgBudget bounds the per-message total so many images never balloon RAM.
const (
	maxImgBytes = 4 << 20
	imgBudget   = 16 << 20
)

// RenderThread renders the thread's pager lines: per message a header
// block (subject/from/date, or the full raw set under the h toggle),
// body, attachment lines. A per-message failure becomes an error line
// so the rest of the thread stays readable - mutt-style; errors only
// on an empty input. mode selects the part view; the returned mime
// labels what actually rendered. The subject is the notmuch value
// (RFC 2047 decoded at index time, so pager and index agree). width
// caps the html wrap at htmlWrapWidth; links holds the F key's label
// targets, non-empty only under labelLinks (labels are mode-scoped).
func RenderThread(msgs []core.Message, mode core.RenderMode, headers bool, width int, labelLinks bool, dark bool, themeBG string) ([]core.Line, string, []string, error) {
	if len(msgs) == 0 {
		return nil, "", nil, fmt.Errorf("no messages in thread")
	}
	var lines []core.Line
	var links []string
	mime := ""
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
		if mime == "" {
			mime = ViewMime(parsed, mode)
		}
		subj := m.Subject
		if subj == "" {
			subj = parsed.Subject
		}
		ml, ls := renderMessage(parsed, subj, mode, headers, width, labelLinks, dark, themeBG)
		lines = append(lines, ml...)
		links = append(links, ls...)
	}
	return lines, mime, links, nil
}

type Message struct {
	From      string
	Date      string
	Subject   string
	MessageID string
	To        []string // bare addresses, reply-all prefill
	Cc        []string
	ReplyTo   []string
	// Headers: the full raw header block in file order (the h key),
	// delivery headers included, values unfolded.
	Headers     []string
	Parts       []Part
	Attachments []Attachment
}

type Part struct {
	Body      string
	Quoted    int // 0..5, capped
	Signature bool
	HTML      bool // Body holds raw html (rendered, not split)
	Truncated bool // HTML only: the raw body was capped
}

// refineMimeType maps generic header types (octet-stream/zip - the
// header alone cannot tell docx/xlsx/pptx apart) onto the filename's
// extension; concrete types (image/png, application/pdf) stay as sent.
func refineMimeType(ct, name string) string {
	switch ct {
	case "application/octet-stream", "application/zip":
		t := mime.TypeByExtension(strings.ToLower(filepath.Ext(name)))
		if i := strings.IndexByte(t, ';'); i >= 0 {
			t = t[:i]
		}
		if t != "" {
			return t
		}
	}
	return ct
}

type Attachment struct {
	Name      string
	MimeType  string
	Size      int64
	Truncated bool // size count hit the cap; the listed size is the cap
	ContentID string
	Data      []byte // image attachments only, for the render-on-key path
}

// ParseMessage opens one mail file and reads its structure: text/plain
// inline parts become body parts (quoted depth + signature split),
// text/html parts stay raw (the render selects the view), attachment
// parts list with sizes (images buffer bytes for the render-on-key
// path). Unknown charsets/encodings are tolerated, not fatal: the part
// renders raw, undecoded. A structural part error ends the scan,
// keeping the parts read so far - mutt-style.
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
	// the full raw block for the h key: Fields() walks the parsed
	// fields in file order; values arrive unfolded
	iter := hdr.Fields()
	for iter.Next() {
		m.Headers = append(m.Headers, iter.Key()+": "+iter.Value())
	}
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
	if addrs, err := hdr.AddressList("Reply-To"); err == nil {
		for _, a := range addrs {
			m.ReplyTo = append(m.ReplyTo, a.Address)
		}
	}
	m.Date = hdr.Get("Date")
	m.Subject = core.DecodeSubject(hdr.Get("Subject"))
	var imgBuffered int64
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
			break // a structural error: keep the parts read so far, mutt-style
		}
		switch h := p.Header.(type) {
		case *mail.InlineHeader:
			ct, _, _ := h.ContentType()
			switch {
			case ct == "text/plain":
				data, err := io.ReadAll(io.LimitReader(p.Body, maxPartBytes+1))
				if err != nil {
					return nil, fmt.Errorf("%s: %w", path, err)
				}
				parts := splitBody(string(data))
				if len(data) > maxPartBytes {
					parts = append(parts, Part{Body: "[content truncated]"})
				}
				m.Parts = append(m.Parts, parts...)
			case ct == "text/html":
				// kept alongside the plain parts so both halves of an
				// alternative pair survive the parse
				data, err := io.ReadAll(io.LimitReader(p.Body, maxPartBytes+1))
				if err != nil {
					return nil, fmt.Errorf("%s: %w", path, err)
				}
				m.Parts = append(m.Parts, Part{Body: string(data), HTML: true, Truncated: len(data) > maxPartBytes})
				// and as a download entry (the v dialog / s save path)
				m.Attachments = append(m.Attachments, Attachment{Name: "html", MimeType: "text/html", Size: int64(len(data)), Truncated: len(data) > maxPartBytes})
			}
		case *mail.AttachmentHeader:
			name, _ := h.Filename()
			if name == "" {
				name = "attachment"
			}
			a := Attachment{Name: name, ContentID: h.Get("Content-Id")}
			ct, _, _ := h.ContentType()
			a.MimeType = refineMimeType(ct, name)
			if strings.HasPrefix(ct, "image/") && imgBuffered < imgBudget {
				// image attachments buffer bytes for the render-on-key
				// path; others are size-counted only; over-budget
				// images list without data
				data, err := io.ReadAll(io.LimitReader(p.Body, maxImgBytes+1))
				if err != nil {
					return nil, fmt.Errorf("%s: %w", path, err)
				}
				a.Size = int64(len(data))
				a.Truncated = len(data) > maxImgBytes
				if !a.Truncated {
					a.Data = data
					imgBuffered += int64(len(data))
				}
			} else {
				size, err := io.Copy(io.Discard, io.LimitReader(p.Body, maxPartBytes+1))
				if err != nil {
					return nil, fmt.Errorf("%s: %w", path, err)
				}
				a.Size = size
				a.Truncated = size > maxPartBytes
			}
			m.Attachments = append(m.Attachments, a)
		}
	}
	return m, nil
}

// ExtractAttachment re-opens the mail file and reads the ordinal-th
// attachment's bytes + type (the view/save demand path - the parse
// size-counts and keeps nothing, so the demand path reads one part,
// capped like the parse). The html part of an alternative pair counts
// as an entry, matching the parse walk. A structural error ends the
// scan with "not found".
func ExtractAttachment(path string, ordinal int) (name, typ string, data []byte, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", "", nil, err
	}
	defer f.Close()
	mr, err := mail.CreateReader(f)
	if err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
		return "", "", nil, fmt.Errorf("%s: %w", path, err)
	}
	defer mr.Close()
	n := 0
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
			break
		}
		// the entry walk matches the parse walk: the html part of an
		// alternative pair lists as an attachment too, so the v
		// dialog's ordinals index the same stream
		switch h := p.Header.(type) {
		case *mail.AttachmentHeader:
			name, _ = h.Filename()
			if name == "" {
				name = "attachment"
			}
			typ, _, _ = h.ContentType()
		case *mail.InlineHeader:
			typ, _, _ = h.ContentType()
			if typ != "text/html" {
				continue
			}
			name = "html"
		default:
			continue
		}
		if n != ordinal {
			n++
			continue
		}
		data, err = io.ReadAll(io.LimitReader(p.Body, maxPartBytes+1))
		if err != nil {
			return "", "", nil, fmt.Errorf("%s: %w", path, err)
		}
		return name, typ, data, nil
	}
	return "", "", nil, fmt.Errorf("attachment %d not found", ordinal)
}

// RenderAttachment renders an attachment's bytes as pager body lines
// (the v dialog's view): sanitized text (F1). Binary attachments render
// garbled but harmless - the s key's save path is their real consumer.
func RenderAttachment(data []byte) []core.Line {
	var lines []core.Line
	for _, l := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		lines = append(lines, core.Line{Text: core.SanitizeControls(l), Kind: core.LineBody})
	}
	return lines
}

// QuoteDepth counts a line's leading ">" markers (mutt-style, one
// optional space per layer), capped at 5 visible quote layers.
func QuoteDepth(line string) int {
	depth := 0
	rest := line
	for depth < 5 {
		next := strings.TrimPrefix(rest, ">")
		if next == rest {
			break
		}
		depth++
		rest = strings.TrimPrefix(next, " ")
	}
	return depth
}

// splitBody splits the raw text into parts: quoted depth by leading
// ">" count (capped at 5), signature after the first standalone "-- ".
// The marker line stays a part so the delimiter renders once, never
// re-prefixed. Tabs expand to the 8-column stop first - the sanitize
// pass would drop an unexpanded tab entirely.
func splitBody(text string) []Part {
	var parts []Part
	sig := false
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSuffix(line, "\r")
		line = expandTabs(line)
		if !sig && line == "-- " {
			sig = true
		}
		parts = append(parts, Part{Body: line, Quoted: QuoteDepth(line), Signature: sig})
	}
	// the trailing newline is line termination, not an empty line
	if len(parts) > 0 && strings.HasSuffix(text, "\n") {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// expandTabs pads each tab to the next multiple of 8 columns (one
// column per rune) - the shape coreutils expand produces.
func expandTabs(line string) string {
	if !strings.ContainsRune(line, '\t') {
		return line
	}
	var b strings.Builder
	b.Grow(len(line))
	col := 0
	for _, r := range line {
		if r == '\t' {
			for n := 8 - col%8; n > 0; n-- {
				b.WriteByte(' ')
			}
			col += 8 - col%8
			continue
		}
		b.WriteRune(r)
		col++
	}
	return b.String()
}

// renderMessage renders one parsed message; links returns the html
// view's link labels (the F key), empty in every other view.
func renderMessage(m *Message, subject string, mode core.RenderMode, headers bool, width int, labelLinks bool, dark bool, themeBG string) (lines []core.Line, links []string) {
	add := func(text string, kind core.LineKind, quoted int) {
		lines = append(lines, core.Line{Text: core.SanitizeControls(text), Kind: kind, Quoted: quoted})
	}
	// the curated header block (Date/From/To/Subject) tops every view;
	// the h key replaces it with the full raw block in every view
	if headers {
		lines = append(lines, headerLines(m)...)
	} else {
		add(fmt.Sprintf("%-8s %s", "Date:", m.Date), core.LineHeader, 0)
		add(fmt.Sprintf("%-8s %s", "From:", m.From), core.LineHeader, 0)
		add(fmt.Sprintf("%-8s %s", "To:", strings.Join(m.To, ", ")), core.LineHeader, 0)
		if len(m.Cc) > 0 {
			add(fmt.Sprintf("%-8s %s", "Cc:", strings.Join(m.Cc, ", ")), core.LineHeader, 0)
		}
		add(fmt.Sprintf("%-8s %s", "Subject:", subject), core.LineSubject, 0)
	}
	hasPlain, hasHTML := partFlags(m)
	// The view selection: the html view renders the html part, the
	// plain view the plain parts, the source view the html part's raw
	// text. An html-only message renders in all three - plain as
	// unstyled text (runs stripped, images kept), html styled, source raw.
	for _, p := range m.Parts {
		switch {
		case p.HTML && mode == core.RenderHTML:
			// the F key's label render carries the "[N]" labels + target list
			var htmlLines []core.Line
			var ls []string
			htmlLines, ls = renderHTML(p.Body, m.Attachments, width, labelLinks, dark, themeBG)
			links = append(links, ls...)
			if len(htmlLines) == 0 {
				htmlLines = renderPlain(p.Body)
			}
			lines = append(lines, htmlLines...)
			if p.Truncated {
				add("[content truncated]", core.LineBody, 0)
			}
		case p.HTML && mode == core.RenderSource:
			lines = append(lines, renderPlain(p.Body)...)
			if p.Truncated {
				add("[content truncated]", core.LineBody, 0)
			}
		case p.HTML && !hasPlain:
			htmlLines, _ := renderHTML(p.Body, m.Attachments, width, false, dark, themeBG)
			for i := range htmlLines {
				htmlLines[i].Runs = nil // unstyled: plain has no colors
			}
			if len(htmlLines) == 0 {
				htmlLines = renderPlain(p.Body)
			}
			lines = append(lines, htmlLines...)
			if p.Truncated {
				add("[content truncated]", core.LineBody, 0)
			}
		case !p.HTML && (mode == core.RenderPlain || !hasHTML):
			lines = append(lines, partLines(p)...)
		}
	}
	for _, a := range m.Attachments {
		line := fmt.Sprintf("attachment: %s (%s, %d bytes)", a.Name, a.MimeType, a.Size)
		if a.Truncated {
			line = fmt.Sprintf("attachment: %s (%s, truncated, >%d bytes)", a.Name, a.MimeType, maxPartBytes)
		}
		add(line, core.LineAttachment, 0)
	}
	return lines, links
}

// headerLines renders the full raw header block (the h key): every
// field in file order, delivery headers included, not a curated
// summary - an encoded-word subject renders encoded here.
func headerLines(m *Message) []core.Line {
	var lines []core.Line
	for _, h := range m.Headers {
		lines = append(lines, core.Line{Text: core.SanitizeControls(h), Kind: core.LineHeader})
	}
	return lines
}

// partFlags reports which part kinds the message carries.
func partFlags(m *Message) (hasPlain, hasHTML bool) {
	for _, p := range m.Parts {
		if p.HTML {
			hasHTML = true
		} else {
			hasPlain = true
		}
	}
	return
}

// ViewMime labels what the mode actually renders: the html part (or
// its raw source) when the view selects it, the plain parts otherwise.
func ViewMime(m *Message, mode core.RenderMode) string {
	hasPlain, hasHTML := partFlags(m)
	if hasHTML && (mode == core.RenderHTML || mode == core.RenderSource || (mode == core.RenderPlain && !hasPlain)) {
		return "text/html"
	}
	return "text/plain"
}

// partLines converts one split body part to its render line.
func partLines(p Part) []core.Line {
	kind := core.LineBody
	if p.Signature {
		kind = core.LineSignature
	}
	return []core.Line{{Text: core.SanitizeControls(p.Body), Kind: kind, Quoted: p.Quoted}}
}

// renderPlain converts a plain-text body to render lines (the html
// fallback path: split like a plain part).
func renderPlain(body string) []core.Line {
	var lines []core.Line
	for _, p := range splitBody(body) {
		lines = append(lines, partLines(p)...)
	}
	return lines
}

// QuoteParts returns the parts to quote: the plain parts (html
// filtered out - the quote never carries raw markup), or the rendered
// text of the first html part when the original is html-only.
func QuoteParts(parts []Part, width int) []Part {
	plain := false
	for _, p := range parts {
		if !p.HTML {
			plain = true
			break
		}
	}
	if plain {
		out := make([]Part, 0, len(parts))
		for _, p := range parts {
			if !p.HTML {
				out = append(out, p)
			}
		}
		return out
	}
	for _, p := range parts {
		if p.HTML {
			return HTMLQuoteBody(p.Body, width)
		}
	}
	return nil
}

// HTMLQuoteBody renders an html-only original to quote parts: the
// rendered lines joined as text, then the standard body split.
func HTMLQuoteBody(body string, width int) []Part {
	lines := RenderHTML(body, nil, width)
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Text)
		b.WriteByte('\n')
	}
	return splitBody(b.String())
}
