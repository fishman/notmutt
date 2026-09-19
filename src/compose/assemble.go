// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package compose

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/emersion/go-message"
	"github.com/emersion/go-message/mail"

	"notmutt/lib/mimeutil"
	notmail "notmutt/mail"
)

// DropBcc removes the Bcc header from a delivered message. The FCC copy keeps
// it so the sender's record retains blind recipients.
func DropBcc(data []byte) []byte {
	end := len(data)
	for i := 0; i+1 < len(data); i++ {
		if data[i] == '\n' && (data[i+1] == '\n' || (i+2 < len(data) && data[i+1] == '\r' && data[i+2] == '\n')) {
			end = i + 1
			break
		}
	}
	head, rest := data[:end], data[end:]
	var b bytes.Buffer
	skip := false
	for _, l := range bytes.SplitAfter(head, []byte("\n")) {
		if len(l) == 0 {
			continue
		}
		line := bytes.TrimSuffix(l, []byte("\n"))
		if skip && len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
			continue
		}
		skip = len(line) >= 4 && strings.EqualFold(string(line[:4]), "bcc:")
		if !skip {
			b.Write(l)
		}
	}
	b.Write(rest)
	return b.Bytes()
}

// Assemble writes the composition. Markdown becomes text/plain plus text/html
// alternatives; attachments wrap the body entity in multipart/mixed.
func (s *State) Assemble(w io.Writer) error {
	hdr, err := composeHeader(s)
	if err != nil {
		return err
	}
	body := MessageBody(*s)
	if s.Markdown {
		plain, html, err := markdownAlternatives(body)
		if err != nil {
			return err
		}
		return assembleMarkdown(w, hdr, s.Attachments, plain, html)
	}
	return assemblePlain(w, hdr, s.Attachments, body, InlineFacts(s))
}

func composeHeader(s *State) (mail.Header, error) {
	hdr := mail.Header{}
	for name, addrs := range map[string][]string{"From": {s.From}, "To": s.To, "Cc": s.Cc, "Bcc": s.Bcc, "Reply-To": s.ReplyTo} {
		if len(addrs) == 0 {
			continue
		}
		parsed := make([]*mail.Address, 0, len(addrs))
		for _, addr := range addrs {
			p, err := mail.ParseAddress(addr)
			if err != nil {
				return hdr, fmt.Errorf("%s: %v", name, err)
			}
			parsed = append(parsed, p)
		}
		hdr.SetAddressList(name, parsed)
	}
	hdr.SetSubject(s.Subject)
	hdr.SetDate(time.Now())
	if err := hdr.GenerateMessageID(); err != nil {
		return hdr, err
	}
	if s.MessageID != "" {
		hdr.Set("In-Reply-To", s.MessageID)
		if len(s.References) > 0 {
			hdr.Set("References", strings.Join(s.References, " "))
		}
	}
	return hdr, nil
}

func assemblePlain(w io.Writer, hdr mail.Header, atts []Attachment, body string, facts PartFacts) error {
	if len(atts) == 0 {
		hdr.Set("Content-Type", facts.Type+"; charset="+facts.Charset)
		hdr.Set("Content-Transfer-Encoding", facts.Encoding)
		mw, err := message.CreateWriter(w, hdr.Header)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(mw, body); err != nil {
			return err
		}
		return mw.Close()
	}
	hdr.Set("Content-Type", mimeutil.MultipartMixed.String())
	mw, err := message.CreateWriter(w, hdr.Header)
	if err != nil {
		return err
	}
	if err := writeTextPart(mw, facts.Type, []byte(body)); err != nil {
		return err
	}
	return writeAttachments(mw, atts)
}

func assembleMarkdown(w io.Writer, hdr mail.Header, atts []Attachment, plain string, html []byte) error {
	if len(atts) == 0 {
		hdr.Set("Content-Type", mimeutil.MultipartAlternative.String())
		mw, err := message.CreateWriter(w, hdr.Header)
		if err != nil {
			return err
		}
		if err := writeAlternatives(mw, plain, html); err != nil {
			return err
		}
		return mw.Close()
	}
	hdr.Set("Content-Type", mimeutil.MultipartMixed.String())
	mw, err := message.CreateWriter(w, hdr.Header)
	if err != nil {
		return err
	}
	altHeader := message.Header{}
	altHeader.Set("Content-Type", mimeutil.MultipartAlternative.String())
	alt, err := mw.CreatePart(altHeader)
	if err != nil {
		return err
	}
	if err := writeAlternatives(alt, plain, html); err != nil {
		return err
	}
	if err := alt.Close(); err != nil {
		return err
	}
	return writeAttachments(mw, atts)
}

func writeAlternatives(w *message.Writer, plain string, html []byte) error {
	if err := writeTextPart(w, mimeutil.TextPlain.String(), []byte(plain)); err != nil {
		return err
	}
	return writeTextPart(w, mimeutil.TextHTML.String(), html)
}

func writeTextPart(w *message.Writer, typ string, body []byte) error {
	h := message.Header{}
	h.Set("Content-Type", typ+"; charset=utf-8")
	h.Set("Content-Transfer-Encoding", "quoted-printable")
	part, err := w.CreatePart(h)
	if err != nil {
		return err
	}
	if _, err := part.Write(body); err != nil {
		return err
	}
	return part.Close()
}

func writeAttachments(mw *message.Writer, atts []Attachment) error {
	for _, a := range atts {
		ah := mail.AttachmentHeader{}
		facts := AttachmentFacts(a)
		ah.Set("Content-Type", facts.Type)
		ah.Set("Content-Transfer-Encoding", facts.Encoding)
		ah.SetFilename(a.Name)
		part, err := mw.CreatePart(ah.Header)
		if err != nil {
			return err
		}
		var copyErr error
		if a.DraftPart > 0 {
			_, copyErr = notmail.WriteDraftAttachment(a.Path, a.DraftPart-1, part)
		} else {
			f, err := os.Open(a.Path)
			if err != nil {
				return err
			}
			_, copyErr = io.Copy(part, f)
			f.Close()
		}
		if copyErr != nil {
			return copyErr
		}
		if err := part.Close(); err != nil {
			return err
		}
	}
	return mw.Close()
}
