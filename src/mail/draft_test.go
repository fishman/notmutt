// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/emersion/go-message/mail"
)

func simpleDraft(t *testing.T, body string) string {
	t.Helper()
	raw := "From: Bob <bob@example.com>\n" +
		"To: alice@example.com\n" +
		"Cc: carol@example.com\n" +
		"Bcc: hidden@example.net\n" +
		"Reply-To: bob@example.com\n" +
		"Subject: the draft\n" +
		"Content-Type: text/plain; charset=utf-8\n" +
		"\n" + body
	p := filepath.Join(t.TempDir(), "draft.eml")
	if err := os.WriteFile(p, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

func multipartDraft(t *testing.T, attachData []byte) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "draft.eml")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	mw, err := mail.CreateWriter(f, mail.Header{})
	if err != nil {
		t.Fatal(err)
	}
	body, err := mw.CreateSingleInline(mail.InlineHeader{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := body.Write([]byte("the body text\n")); err != nil {
		t.Fatal(err)
	}
	body.Close()
	ah := mail.AttachmentHeader{}
	ah.Set("Content-Type", "text/plain")
	ah.SetFilename("notes.txt")
	att, err := mw.CreateAttachment(ah)
	if err != nil {
		t.Fatal(err)
	}
	att.Write(attachData)
	att.Close()
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return p
}

func TestParseDraftSimple(t *testing.T) {
	body := "line one\n\tindented with a tab\n\n-- \nsig"
	d, err := ParseDraft(simpleDraft(t, body))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.To) != 1 || d.To[0] != "alice@example.com" {
		t.Fatalf("To = %v", d.To)
	}
	if len(d.Bcc) != 1 || d.Bcc[0] != "hidden@example.net" {
		t.Fatalf("Bcc must restore (ParseMessage drops it): %v", d.Bcc)
	}
	if len(d.Cc) != 1 || d.Cc[0] != "carol@example.com" {
		t.Fatalf("Cc = %v", d.Cc)
	}
	if len(d.ReplyTo) != 1 || d.ReplyTo[0] != "bob@example.com" {
		t.Fatalf("ReplyTo = %v", d.ReplyTo)
	}
	if d.Subject != "the draft" {
		t.Fatalf("Subject = %q", d.Subject)
	}
	if d.Body != body {
		t.Fatalf("Body = %q, want %q (tab must survive)", d.Body, body)
	}
	if d.BodyTruncated {
		t.Fatal("body must not be truncated")
	}
	if len(d.Atts) != 0 {
		t.Fatalf("Atts = %v, want none", d.Atts)
	}
}

func TestParseDraftMultipart(t *testing.T) {
	d, err := ParseDraft(multipartDraft(t, []byte("attachment bytes\n")))
	if err != nil {
		t.Fatal(err)
	}
	if d.Body != "the body text\n" {
		t.Fatalf("Body = %q", d.Body)
	}
	if len(d.Atts) != 1 {
		t.Fatalf("Atts = %v", d.Atts)
	}
	a := d.Atts[0]
	if a.Name != "notes.txt" || a.Ordinal != 0 {
		t.Fatalf("Att = %+v", a)
	}
}

func TestWriteDraftAttachment(t *testing.T) {
	p := multipartDraft(t, []byte("attachment bytes\n"))
	var buf bytes.Buffer
	if _, err := WriteDraftAttachment(p, 0, &buf); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "attachment bytes\n" {
		t.Fatalf("streamed = %q", buf.String())
	}
	if _, err := WriteDraftAttachment(p, 1, &bytes.Buffer{}); err == nil {
		t.Fatal("an out-of-range ordinal must error")
	}
}

func TestParseDraftCRLF(t *testing.T) {
	raw := "To: alice@example.com\r\n" +
		"Subject: crlf\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"line one\r\n\r\n-- \r\nsig\r\n"
	p := filepath.Join(t.TempDir(), "draft.eml")
	if err := os.WriteFile(p, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	d, err := ParseDraft(p)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(d.Body, "\r") {
		t.Fatalf("body must normalize CRLF: %q", d.Body)
	}
	if !strings.HasSuffix(d.Body, "-- \nsig\n") {
		t.Fatalf("body must keep the signature marker after CRLF: %q", d.Body)
	}
}
