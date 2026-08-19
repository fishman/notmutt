// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

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

// maxImgBytes bounds one image attachment's buffered data (the
// render-on-key path); imgBudget bounds the total per message - a mail
// with dozens of images must not balloon in RAM when none render.
const (
	maxImgBytes = 4 << 20
	imgBudget   = 16 << 20
)

// RenderThread parses each message's file and produces the pager's
// render lines (the Line type lives in core - the open job ships them
// to the TUI in the ThreadLoaded event): per message a header block
// (subject, from/date - or the full header set under the h toggle),
// then the body with quoted levels and signature, then attachment
// lines. A per-message failure (no path, unreadable file, unparseable
// mail) becomes an error line so the rest of the thread stays readable
// - mutt-style partial content. RenderThread errors only when the
// input is empty: then there is nothing to show at all.
//
// mode selects the part view (the toggle-render and source keys): the
// html part, the plain part, or the raw source of the html part.
// Html-only messages render in the plain view too - the raw source as
// plain text. The returned mime labels what actually rendered (for the
// status bar), resolved against the message's real parts, never the
// requested view.
//
// The subject is the notmuch value (the worker's messages carry it):
// notmuch decodes RFC 2047 at index time, so the pager and the index
// show the same string - the go-message parse keeps the raw header
// only as an empty fallback. width is the caller's terminal width: the
// html wrap caps at htmlWrapWidth, narrower terminals reflow.
// RenderThread renders the thread; links is the html view's label
// targets (the F key), non-empty only when labelLinks asks for the
// label render, empty in every other view. labelLinks is the pager
// F key's mode flag - the labels are mode-scoped, never a permanent
// decoration of the html view.
func RenderThread(msgs []core.Message, mode core.RenderMode, headers bool, width int, labelLinks bool) ([]core.Line, string, []string, error) {
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
		ml, ls := renderMessage(parsed, subj, mode, headers, width, labelLinks)
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
	// Headers is the full raw header block in file order (the h key):
	// every field, delivery headers included (Received, Return-Path,
	// DKIM-Signature, SPF), values unfolded.
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

type Attachment struct {
	Name      string
	Size      int64
	Truncated bool // size count hit the cap; the listed size is the cap
	ContentID string
	Data      []byte // image attachments only, for the render-on-key path
}

// ParseMessage opens one mail file and reads its structure with
// go-message: the text/plain inline parts become body parts (quoted
// depth + signature split), text/html parts are kept raw (the pager
// renders them; the render selects the view - the toggle-render key
// switches between the plain and the html part of an alternative
// pair), and attachment parts are listed with their sizes (image
// attachments buffer their bytes for the render-on-key path).
//
// Unknown charsets/encodings are tolerated, not fatal: go-message
// returns the part with the body reader still raw (undecoded) when it
// cannot map the charset, so the part renders as-is instead of killing
// the whole thread. A structural part error (e.g. a missing closing
// boundary) ends the part scan instead of aborting the parse - the
// parts read so far survive, mutt-style.
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
	// fields in file order (the textproto iterator reverses its
	// internal list). Values arrive unfolded (continuation lines
	// joined with a space by the parser).
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
			break // a structural error (e.g. a missing closing boundary):
			// keep the parts read so far, mutt-style - the mail is not lost
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
				// kept alongside the plain parts: the render picks the
				// view (toggle-render), so both halves of an alternative
				// pair survive the parse
				data, err := io.ReadAll(io.LimitReader(p.Body, maxPartBytes+1))
				if err != nil {
					return nil, fmt.Errorf("%s: %w", path, err)
				}
				m.Parts = append(m.Parts, Part{Body: string(data), HTML: true, Truncated: len(data) > maxPartBytes})
			}
		case *mail.AttachmentHeader:
			name, _ := h.Filename()
			if name == "" {
				name = "attachment"
			}
			a := Attachment{Name: name, ContentID: h.Get("Content-Id")}
			if ct, _, _ := h.ContentType(); strings.HasPrefix(ct, "image/") && imgBuffered < imgBudget {
				// image attachments buffer their bytes for the
				// render-on-key path; other attachments are size-counted
				// only. Over-budget images list without data.
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

// ExtractAttachment re-opens one mail file and reads the ordinal-th
// attachment's bytes and content type (the attachment view/save demand
// path) - ParseMessage size-counts non-image parts and never keeps
// them, so the demand path reads one part, capped like the parse. A
// structural part error ends the scan with "not found".
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
		h, ok := p.Header.(*mail.AttachmentHeader)
		if !ok {
			continue
		}
		if n != ordinal {
			n++
			continue
		}
		name, _ = h.Filename()
		if name == "" {
			name = "attachment"
		}
		typ, _, _ = h.ContentType()
		data, err = io.ReadAll(io.LimitReader(p.Body, maxPartBytes+1))
		if err != nil {
			return "", "", nil, fmt.Errorf("%s: %w", path, err)
		}
		return name, typ, data, nil
	}
	return "", "", nil, fmt.Errorf("attachment %d not found", ordinal)
}

// RenderAttachment renders an attachment's bytes as pager body lines
// (the v dialog's view): sanitized text lines (F1). Binary
// attachments render garbled but harmless - the save path (the s key)
// is their real consumer.
func RenderAttachment(data []byte) []core.Line {
	var lines []core.Line
	for _, l := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		lines = append(lines, core.Line{Text: core.SanitizeControls(l), Kind: core.LineBody})
	}
	return lines
}

// QuoteDepth counts a line's leading ">" markers (mutt-style quote
// nesting, one optional space per layer), capped at the 5 visible
// quote layers.
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
// The marker line itself stays a part - renderMessage emits it as-is,
// so the delimiter renders exactly once, never re-prefixed. Tabs
// expand to the default 8-column stop first - a report aligned with
// tabs must render column-aligned (and the sanitize pass drops C0
// controls, tab included, so an unexpanded tab would vanish entirely).
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

// expandTabs pads each tab to the next multiple of 8 columns (the
// terminal default stop), counting one column per rune - the shape
// coreutils expand produces for tab-aligned reports.
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
func renderMessage(m *Message, subject string, mode core.RenderMode, headers bool, width int, labelLinks bool) (lines []core.Line, links []string) {
	add := func(text string, kind core.LineKind, quoted int) {
		lines = append(lines, core.Line{Text: core.SanitizeControls(text), Kind: kind, Quoted: quoted})
	}
	if headers && mode == core.RenderPlain {
		lines = append(lines, headerLines(m)...)
	} else {
		add(subject, core.LineSubject, 0)
		add(m.From+"  "+m.Date, core.LineHeader, 0)
	}
	hasPlain, hasHTML := partFlags(m)
	// The view selection: the html view renders the html part, the plain
	// view the plain parts, the source view the html part's raw text.
	// An html-only message renders in all three: the plain view shows
	// the html as unstyled text (runs stripped, images kept), the html
	// view styled, the source view raw - the raw markup belongs to the
	// source view alone.
	for _, p := range m.Parts {
		switch {
		case p.HTML && mode == core.RenderHTML:
			// the F key's label render carries the inline "[N]" labels
			// and the target list; the plain render is unlabeled (the
			// label renderer is link-mode's, never the default)
			var htmlLines []core.Line
			var ls []string
			if labelLinks {
				htmlLines, ls = RenderHTMLWithLinks(p.Body, m.Attachments, width)
			} else {
				htmlLines = RenderHTML(p.Body, m.Attachments, width)
			}
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
			htmlLines := RenderHTML(p.Body, m.Attachments, width)
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
		line := fmt.Sprintf("attachment: %s (%d bytes)", a.Name, a.Size)
		if a.Truncated {
			line = fmt.Sprintf("attachment: %s (truncated, >%d bytes)", a.Name, maxPartBytes)
		}
		add(line, core.LineAttachment, 0)
	}
	return lines, links
}

// headerLines renders the full raw header block (the h key): every
// field of the message in file order - the delivery headers
// (Received, Return-Path, DKIM-Signature, SPF) included, not a
// curated summary. The raw block is the true header: an encoded-word
// subject renders encoded here.
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

// QuoteParts returns the body parts to quote: the plain parts (html
// parts filtered out - the quote never carries raw markup) when the
// original carries plain content, or the rendered text of the first
// html part when it is html-only (the reply quotes the rendered text,
// never the raw markup).
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
// rendered lines joined as text, then the standard body split (quoted
// depth + signature on the rendered content).
func HTMLQuoteBody(body string, width int) []Part {
	lines := RenderHTML(body, nil, width)
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(l.Text)
		b.WriteByte('\n')
	}
	return splitBody(b.String())
}
