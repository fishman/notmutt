// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package compose

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emersion/go-message/mail"
)

func TestAssemble(t *testing.T) {
	att := filepath.Join(t.TempDir(), "doc.txt")
	if err := os.WriteFile(att, []byte("attachment bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewCompose("gmail", "Bob <bob@example.com>", "gmail", "sig line")
	s.To = []string{"Alice <alice@example.com>"}
	s.Cc = []string{"cc@example.net"}
	s.Bcc = []string{"bcc@example.net"}
	s.ReplyTo = []string{"reply@example.net"}
	s.Subject = "hello"
	s.Body = "body line"
	s.MessageID = "<orig@example.com>"
	s.References = []string{"<a@x>", "<orig@example.com>"}
	s.OriginalID = "<orig@example.com>"
	if err := s.AddAttachment(att); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := s.Assemble(&buf); err != nil {
		t.Fatal(err)
	}

	mr, err := mail.CreateReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()
	hdr := mr.Header
	if from, _ := hdr.AddressList("From"); len(from) != 1 || from[0].Address != "bob@example.com" {
		t.Fatalf("From = %v", from)
	}
	if to, _ := hdr.AddressList("To"); len(to) != 1 || to[0].Address != "alice@example.com" {
		t.Fatalf("To = %v", to)
	}
	if cc, _ := hdr.AddressList("Cc"); len(cc) != 1 || cc[0].Address != "cc@example.net" {
		t.Fatalf("Cc = %v", cc)
	}
	if bcc, _ := hdr.AddressList("Bcc"); len(bcc) != 1 || bcc[0].Address != "bcc@example.net" {
		t.Fatalf("Bcc = %v", bcc)
	}
	if rt, _ := hdr.AddressList("Reply-To"); len(rt) != 1 || rt[0].Address != "reply@example.net" {
		t.Fatalf("Reply-To = %v", rt)
	}
	if hdr.Get("Subject") != "hello" {
		t.Fatalf("Subject = %q", hdr.Get("Subject"))
	}
	if hdr.Get("Message-Id") == "" {
		t.Fatal("Message-Id must be generated")
	}
	if hdr.Get("In-Reply-To") != "<orig@example.com>" {
		t.Fatalf("In-Reply-To = %q", hdr.Get("In-Reply-To"))
	}
	if refs, _ := hdr.MsgIDList("References"); len(refs) != 2 || refs[0] != "a@x" || refs[1] != "orig@example.com" {
		t.Fatalf("References = %v", refs)
	}

	var inline, attached []byte
	var inlineCT string
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(p.Body)
		if err != nil {
			t.Fatal(err)
		}
		switch p.Header.(type) {
		case *mail.InlineHeader:
			inline = data
			inlineCT = p.Header.Get("Content-Type")
		case *mail.AttachmentHeader:
			attached = data
		}
	}
	if !strings.Contains(strings.ReplaceAll(string(inline), "\r\n", "\n"), "body line\n\n-- \nsig line") {
		t.Fatalf("inline part = %q", inline)
	}
	if inlineCT != "text/plain; charset=utf-8" {
		t.Fatalf("inline part Content-Type = %q", inlineCT)
	}
	if string(attached) != "attachment bytes" {
		t.Fatalf("attachment part = %q", attached)
	}
}

// TestAssembleWireShapes pins the neomutt wire shapes on raw bytes
// (send/send.c:2712, send/header.c:830): a bare body is ONE text/plain
// part, no multipart or Content-Disposition anywhere; attachments wrap
// in multipart/mixed, only they carry Content-Disposition. An inline
// body part makes Betterbird render a phantom attachment.
func TestAssembleWireShapes(t *testing.T) {
	single := NewCompose("gmail", "bob@example.com", "", "")
	single.To = []string{"alice@example.com"}
	single.Subject = "x"
	single.Body = "hello"
	var buf bytes.Buffer
	if err := single.Assemble(&buf); err != nil {
		t.Fatal(err)
	}
	raw := strings.ToLower(buf.String())
	if strings.Contains(raw, "multipart") || strings.Contains(raw, "content-disposition") {
		t.Fatalf("a bare body must be one plain part:\n%s", buf.String())
	}
	if !strings.Contains(raw, "content-type: text/plain") || !strings.Contains(raw, "content-transfer-encoding: quoted-printable") {
		t.Fatalf("the single part must carry its type and encoding:\n%s", buf.String())
	}

	att := filepath.Join(t.TempDir(), "doc.txt")
	if err := os.WriteFile(att, []byte("attachment bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	with := NewCompose("gmail", "bob@example.com", "", "")
	with.To = []string{"alice@example.com"}
	with.Subject = "x"
	with.Body = "hello"
	if err := with.AddAttachment(att); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := with.Assemble(&buf); err != nil {
		t.Fatal(err)
	}
	raw = strings.ToLower(buf.String())
	if !strings.Contains(raw, "multipart/mixed") {
		t.Fatalf("attachments must wrap in multipart/mixed:\n%s", buf.String())
	}
	if strings.Contains(raw, "content-disposition: inline") {
		t.Fatalf("the body part must carry no disposition:\n%s", buf.String())
	}
	if n := strings.Count(raw, "content-disposition: attachment"); n != 1 {
		t.Fatalf("exactly the attachment must carry disposition, found %d:\n%s", n, buf.String())
	}
}

func TestAssembleMarkdownBody(t *testing.T) {
	s := NewCompose("gmail", "bob@example.com", "", "")
	s.To = []string{"a@b.c"}
	s.Body = "# title\n\n- one\n- two"
	var buf bytes.Buffer
	if err := s.Assemble(&buf); err != nil {
		t.Fatal(err)
	}
	mr, err := mail.CreateReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()
	p, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if ct := p.Header.Get("Content-Type"); ct != "text/markdown; charset=utf-8" {
		t.Fatalf("markdown body Content-Type = %q", ct)
	}
}

func TestAssembleBadAddressFails(t *testing.T) {
	s := NewCompose("gmail", "bob@example.com", "", "")
	s.To = []string{"not an address"}
	var buf bytes.Buffer
	if err := s.Assemble(&buf); err == nil {
		t.Fatal("a bad recipient address must fail assembly")
	}
}

func TestAssembleNoSignature(t *testing.T) {
	s := NewCompose("gmail", "bob@example.com", "", "")
	s.To = []string{"a@b.c"}
	s.Subject = "x"
	s.Body = "only body"
	var buf bytes.Buffer
	if err := s.Assemble(&buf); err != nil {
		t.Fatal(err)
	}
	mr, err := mail.CreateReader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	defer mr.Close()
	p, err := mr.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(p.Body)
	if string(data) != "only body" {
		t.Fatalf("body = %q", data)
	}
}

func TestDropBcc(t *testing.T) {
	for name, tc := range map[string]struct {
		in, want string
	}{
		"simple": {
			"Bcc: hidden@example.com\nTo: x@y.z\n\nbody\n",
			"To: x@y.z\n\nbody\n",
		},
		"folded": {
			"To: x@y.z\nBcc: a@b.c,\n c@d.e\nSubject: s\n\nbody",
			"To: x@y.z\nSubject: s\n\nbody",
		},
		"first line": {
			"Bcc: h@x.c\n\nbody",
			"\nbody",
		},
		"case insensitive": {
			"to: x@y.z\nbcc: hidden@x.c\n\nbody",
			"to: x@y.z\n\nbody",
		},
		"body line kept": {
			"To: x@y.z\n\nbody\nBcc: not-a-header@x.c\n",
			"To: x@y.z\n\nbody\nBcc: not-a-header@x.c\n",
		},
		"no bcc": {
			"To: x@y.z\nSubject: s\n\nbody\n",
			"To: x@y.z\nSubject: s\n\nbody\n",
		},
		"crlf": {
			"To: x@y.z\r\nBcc: a@b.c\r\n\r\nbody\r\n",
			"To: x@y.z\r\n\r\nbody\r\n",
		},
	} {
		if got := string(DropBcc([]byte(tc.in))); got != tc.want {
			t.Fatalf("%s: DropBcc =\n%q\nwant\n%q", name, got, tc.want)
		}
	}
}

// TestDropBccAssembled pins the mutt delivery shape on assembled bytes:
// the wire copy (DropBcc) carries no Bcc, the fcc copy keeps it.
func TestDropBccAssembled(t *testing.T) {
	s := NewCompose("gmail", "bob@example.com", "", "")
	s.To = []string{"alice@example.com"}
	s.Bcc = []string{"bcc@example.net"}
	s.Subject = "x"
	s.Body = "y"
	var buf bytes.Buffer
	if err := s.Assemble(&buf); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	wire := string(DropBcc(data))
	if strings.Contains(wire, "Bcc:") {
		t.Fatalf("the wire copy must not carry Bcc:\n%s", wire)
	}
	if !strings.Contains(string(data), "Bcc:") || !strings.Contains(string(data), "bcc@example.net") {
		t.Fatalf("the fcc copy must keep Bcc:\n%s", data)
	}
	if !strings.Contains(wire, "Subject: x") || !strings.Contains(wire, "alice@example.com") {
		t.Fatalf("the wire copy must keep everything else:\n%s", wire)
	}
}
