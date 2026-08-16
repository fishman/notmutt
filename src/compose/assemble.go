package compose

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/emersion/go-message/mail"
)

// DropBcc returns the message with the Bcc header removed - the mutt
// delivery shape: Bcc rides the envelope only, the wire message never
// carries it (write_bcc defaults off); the fcc copy keeps it (mutt's
// FCC mode always writes Bcc). Header-block only: the scan stops at
// the first blank line (LF or CRLF), so a body line reading "Bcc: ..."
// never matches. Folded continuations go with the header.
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
// Message-ID, In-Reply-To and References for replies), one text/plain
// body part (signature attached), one part per attachment. Pure bytes
// - the send job writes the same buffer to transport and fcc. The
// body is the user's own text; nothing here sanitizes (sanitize is
// render-only, F1). References is written verbatim - the prefill
// already carries the full chain including the original's own
// message-id (spec section 6).
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
	mw, err := mail.CreateWriter(w, hdr)
	if err != nil {
		return err
	}
	ih := mail.InlineHeader{}
	f := InlineFacts(s)
	ih.Set("Content-Type", f.Type+"; charset="+f.Charset)
	ih.Set("Content-Transfer-Encoding", f.Encoding)
	b, err := mw.CreateSingleInline(ih)
	if err != nil {
		return err
	}
	if _, err := io.WriteString(b, BodyWithSig(s.Body, s.SignatureBody)); err != nil {
		return err
	}
	if err := b.Close(); err != nil {
		return err
	}
	for _, a := range s.Attachments {
		f, err := os.Open(a.Path)
		if err != nil {
			return err
		}
		ah := mail.AttachmentHeader{}
		af := AttachmentFacts(a)
		ah.Set("Content-Type", af.Type)
		ah.Set("Content-Transfer-Encoding", af.Encoding)
		ah.SetFilename(a.Name)
		ab, err := mw.CreateAttachment(ah)
		if err != nil {
			f.Close()
			return err
		}
		if _, err := io.Copy(ab, f); err != nil {
			f.Close()
			return err
		}
		f.Close()
		if err := ab.Close(); err != nil {
			return err
		}
	}
	return mw.Close()
}
