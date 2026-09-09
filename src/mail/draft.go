// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

// Draft parse/serialize: the resume-draft seam reads a stored draft back
// to compose fields. A draft is whatever Assemble wrote (body part +
// attachment parts), so ParseDraft understands the shapes compose emits.
// Bodies stay byte-faithful (a tab is a tab) - SplitSignature re-detaches
// the signature tail at resume, never re-expanded here.
package mail

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"

	"notmutt/core"
)

// Draft is a stored draft's compose-fields: address lists, subject, and
// the body as sent (signature block included - SplitSignature detaches
// it at resume). Atts enumerates the attachment parts in file order with
// their part ordinal (DraftPart = ordinal + 1 at resume) and size.
type Draft struct {
	To, Cc, Bcc, ReplyTo []string
	Subject              string
	Body                 string
	BodyTruncated        bool
	Atts                 []DraftAtt
}

type DraftAtt struct {
	Ordinal        int
	Name, MimeType string
	Size           int64
}

// ParseDraft reads one stored draft back to compose fields. Bcc survives
// because drafts are not redelivered - nothing downstream strips it.
// Text/plain inline parts assemble the body; attachment parts list with
// their ordinal. Unreadable headers (bad addresses) drop, never fail the
// resume - the user edits the field back.
func ParseDraft(path string) (*Draft, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	mr, err := openMail(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	defer mr.Close()
	hdr := mr.Header
	d := &Draft{}
	for name, dst := range map[string]*[]string{
		"To": &d.To, "Cc": &d.Cc, "Bcc": &d.Bcc, "Reply-To": &d.ReplyTo,
	} {
		if addrs, err := hdr.AddressList(name); err == nil {
			for _, a := range addrs {
				*dst = append(*dst, a.Address)
			}
		}
	}
	d.Subject = core.DecodeSubject(hdr.Get("Subject"))
	ord := 0
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
			break
		}
		if p == nil {
			break // an unknown-encoding part returns no part: keep the scan so far
		}
		switch h := p.Header.(type) {
		case *mail.InlineHeader:
			ct, _, _ := h.ContentType()
			if ct != "text/plain" {
				continue
			}
			data, err := io.ReadAll(io.LimitReader(p.Body, maxPartBytes+1))
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			if len(data) > maxPartBytes {
				d.BodyTruncated = true
				data = data[:maxPartBytes]
			}
			d.Body += strings.ReplaceAll(string(data), "\r\n", "\n")
		case *mail.AttachmentHeader:
			name, _ := h.Filename()
			if name == "" {
				name = "attachment"
			}
			ct, _, _ := h.ContentType()
			size, _ := io.Copy(io.Discard, io.LimitReader(p.Body, maxPartBytes+1))
			if size > maxPartBytes {
				size = maxPartBytes
			}
			d.Atts = append(d.Atts, DraftAtt{Ordinal: ord, Name: name, MimeType: refineMimeType(ct, name), Size: size})
			ord++
		}
	}
	return d, nil
}

// WriteDraftAttachment streams the ordinal-th attachment part of the
// stored draft to w (the resume path: assemble reads part bytes out of
// the still-present draft file). Same attachment-only enumeration as
// ParseDraft, so its ordinals index this stream directly. Unbounded on
// purpose - send must not truncate a large attachment the way the
// view/save demand path does.
func WriteDraftAttachment(path string, ordinal int, w io.Writer) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	mr, err := openMail(f)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", path, err)
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
		if p == nil {
			break // an unknown-encoding part returns no part: keep the scan so far
		}
		if _, ok := p.Header.(*mail.AttachmentHeader); !ok {
			continue
		}
		if n != ordinal {
			n++
			continue
		}
		return io.Copy(w, p.Body)
	}
	return 0, fmt.Errorf("attachment %d not found", ordinal)
}
