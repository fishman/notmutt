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
)

// DropBcc removes the Bcc header (mutt delivery shape): Bcc rides the
// envelope only, never the wire (write_bcc off); the fcc copy keeps it
// (FCC mode always writes Bcc). Header-block only - the scan stops at
// the first blank line (LF/CRLF), so a body "Bcc:" never matches;
// folded continuations go with the header.
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
		line := l
		if line[len(line)-1] == '\n' {
			line = line[:len(line)-1]
		}
		if skip && (line[0] == ' ' || line[0] == '\t') {
			continue
		}
		skip = false
		if len(line) >= 4 && strings.EqualFold(string(line[:4]), "bcc:") {
			skip = true
			continue
		}
		b.Write(l)
	}
	b.Write(rest)
	return b.Bytes()
}

// Assemble writes the message bytes: headers (From/To/Cc/Subject/Date/
// Message-ID, In-Reply-To/References for replies), one text/plain body
// part (signature attached), one part per attachment. Pure bytes - the
// send job writes the same buffer to transport and fcc. Nothing here
// sanitizes (sanitize is render-only, F1); References is written
// verbatim - the prefill carries the full chain (spec section 6).
//
// Wire shape per neomutt (send/send.c, send/multipart.c, send/header.c):
// a bare body is ONE text/plain part, no multipart or Content-Disposition;
// attachments wrap in multipart/mixed, only they carry Content-Disposition.
// The mail package's Writer would force multipart/mixed + inline
// Content-Disposition on every message - clients (Betterbird) read that
// as an attached body file.
func (s *State) Assemble(w io.Writer) error {
	hdr := mail.Header{}
	setAddrs := func(name string, addrs []string) error {
		var parsed []*mail.Address
		for _, a := range addrs {
			p, err := mail.ParseAddress(a)
			if err != nil {
				return fmt.Errorf("%s: %v", name, err)
			}
			parsed = append(parsed, p)
		}
		hdr.SetAddressList(name, parsed)
		return nil
	}
	if err := setAddrs("From", []string{s.From}); err != nil {
		return err
	}
	if err := setAddrs("To", s.To); err != nil {
		return err
	}
	if err := setAddrs("Cc", s.Cc); err != nil {
		return err
	}
	if err := setAddrs("Bcc", s.Bcc); err != nil {
		return err
	}
	if len(s.ReplyTo) > 0 {
		if err := setAddrs("Reply-To", s.ReplyTo); err != nil {
			return err
		}
	}
	hdr.SetSubject(s.Subject)
	hdr.SetDate(time.Now())
	if err := hdr.GenerateMessageID(); err != nil {
		return err
	}
	if s.MessageID != "" {
		hdr.Set("In-Reply-To", s.MessageID)
		if len(s.References) > 0 {
			hdr.Set("References", strings.Join(s.References, " "))
		}
	}
	f := InlineFacts(s)
	body := BodyWithSig(s.Body, s.SignatureBody)
	if len(s.Attachments) == 0 {
		hdr.Set("Content-Type", f.Type+"; charset="+f.Charset)
		hdr.Set("Content-Transfer-Encoding", f.Encoding)
		mw, err := message.CreateWriter(w, hdr.Header)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(mw, body); err != nil {
			return err
		}
		return mw.Close()
	}
	hdr.Set("Content-Type", "multipart/mixed")
	mw, err := message.CreateWriter(w, hdr.Header)
	if err != nil {
		return err
	}
	bh := message.Header{}
	bh.Set("Content-Type", f.Type+"; charset="+f.Charset)
	bh.Set("Content-Transfer-Encoding", f.Encoding)
	bp, err := mw.CreatePart(bh)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(bp, body); err != nil {
		return err
	}
	if err := bp.Close(); err != nil {
		return err
	}
	for _, a := range s.Attachments {
		af, err := os.Open(a.Path)
		if err != nil {
			return err
		}
		ah := mail.AttachmentHeader{}
		afacts := AttachmentFacts(a)
		ah.Set("Content-Type", afacts.Type)
		ah.Set("Content-Transfer-Encoding", afacts.Encoding)
		ah.SetFilename(a.Name)
		ab, err := mw.CreatePart(ah.Header)
		if err != nil {
			af.Close()
			return err
		}
		if _, err := io.Copy(ab, af); err != nil {
			af.Close()
			return err
		}
		af.Close()
		if err := ab.Close(); err != nil {
			return err
		}
	}
	return mw.Close()
}
