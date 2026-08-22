// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package compose

import (
	"os"
	"strings"
	"testing"
	"time"

	"notmutt/core"
	"notmutt/mail"
)

func fixture() (core.Message, *mail.Message) {
	orig := core.Message{
		ID: "<msg-1@example.com>", Timestamp: time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC).Unix(),
		Author: "Alice <alice@example.com>", Subject: "Re: Re: hello",
		References: []string{"<a@x>", "<b@x>"},
		Tags:       []string{"inbox", "gmail"},
	}
	parsed := &mail.Message{
		From: "alice@example.com", MessageID: "<msg-1@example.com>", Subject: "Re: Re: hello",
		To: []string{"bob@example.com"}, Cc: []string{"carol@example.org"},
		Parts: []mail.Part{
			{Body: "line one"},
			{Body: "> quoted line"},
			{Body: "-- ", Signature: true},
			{Body: "alice", Signature: true},
		},
	}
	return orig, parsed
}

func TestReplyPrefill(t *testing.T) {
	orig, parsed := fixture()
	s := Reply(orig, parsed, "gmail", "Bob <bob@example.com>", "gmail", "bob")
	if s.Mode != ModeReply || s.Account != "gmail" || s.From != "Bob <bob@example.com>" {
		t.Fatalf("mode/account/from: %+v", s)
	}
	if len(s.To) != 1 || s.To[0] != "alice@example.com" {
		t.Fatalf("To = %v", s.To)
	}
	if len(s.Cc) != 0 {
		t.Fatalf("Cc = %v", s.Cc)
	}
	if s.Subject != "Re: hello" {
		t.Fatalf("Subject = %q", s.Subject)
	}
	if s.OriginalID != "<msg-1@example.com>" || s.MessageID != "<msg-1@example.com>" {
		t.Fatalf("ids: %q %q", s.OriginalID, s.MessageID)
	}
	if len(s.References) != 3 || s.References[0] != "<a@x>" || s.References[2] != "<msg-1@example.com>" {
		t.Fatalf("References = %v", s.References)
	}
	if s.Signature != "gmail" || s.SignatureBody != "bob" {
		t.Fatalf("signature = %q %q", s.Signature, s.SignatureBody)
	}
	body := s.Body
	if !strings.HasPrefix(body, "On Fri, Aug 14 2026, Alice <alice@example.com> wrote:\n") {
		t.Fatalf("attribution missing: %q", body)
	}
	if !strings.Contains(body, "> line one\n") || !strings.Contains(body, ">> quoted line\n") {
		t.Fatalf("quoted lines wrong: %q", body)
	}
	if strings.Contains(body, "> alice") {
		t.Fatalf("signature must not be quoted: %q", body)
	}
}

func TestReplyAllPrefill(t *testing.T) {
	orig, parsed := fixture()
	s := ReplyAll(orig, parsed, "gmail", "Bob <bob@example.com>", "carol@example.org", "gmail", "bob")
	if len(s.To) != 1 || s.To[0] != "alice@example.com" {
		t.Fatalf("To = %v", s.To)
	}
	// own address drops from the Cc; the rest of To+Cc carries over
	if len(s.Cc) != 1 || s.Cc[0] != "bob@example.com" {
		t.Fatalf("Cc = %v", s.Cc)
	}
}

// TestReplyAllCcBuild pins the neomutt Cc rules: own-address exclusion
// by mailbox part (case-insensitive - a case variant is still the own
// address), dedupe, To-xref exclusion, and the empty-To Cc-to-To swap.
func TestReplyAllCcBuild(t *testing.T) {
	if got := replyAllCc([]string{"Carol@Example.org"}, []string{"bob@example.com"}, "carol@example.org", nil); len(got) != 1 || got[0] != "bob@example.com" {
		t.Fatalf("case variant of the own address must be excluded: %v", got)
	}
	if got := replyAllCc([]string{"dave@example.com"}, []string{"dave@example.com"}, "me@example.com", nil); len(got) != 1 {
		t.Fatalf("a duplicate across To and Cc appears once: %v", got)
	}
	if got := replyAllCc([]string{"dave@example.com"}, nil, "me@example.com", []string{"dave@example.com"}); len(got) != 0 {
		t.Fatalf("entries already in the To are not repeated in the Cc: %v", got)
	}
	if got := replyAllCc([]string{"me@example.com"}, nil, "me@example.com", nil); len(got) != 0 {
		t.Fatalf("the own address as the only recipient: no Cc: %v", got)
	}
	// the From parse failed (To = [""]): the Cc becomes the To
	orig, _ := fixture()
	parsed := &mail.Message{From: "", MessageID: "<m3@example.com>", To: []string{"dave@example.com"}}
	s := ReplyAll(orig, parsed, "gmail", "Bob <bob@example.com>", "carol@example.org", "gmail", "bob")
	if len(s.To) != 1 || s.To[0] != "dave@example.com" || len(s.Cc) != 0 {
		t.Fatalf("empty To must take the Cc: To=%v Cc=%v", s.To, s.Cc)
	}
}

func TestForwardPrefill(t *testing.T) {
	orig, parsed := fixture()
	s := Forward(orig, parsed, "gmail", "Bob <bob@example.com>", "gmail", "bob")
	if s.Mode != ModeForward || len(s.To) != 0 || len(s.Cc) != 0 {
		t.Fatalf("forward prefill: %+v", s)
	}
	if s.Subject != "Fwd: hello" {
		t.Fatalf("Subject = %q", s.Subject)
	}
	if !strings.Contains(s.Body, "> line one") {
		t.Fatalf("forward must quote the body: %q", s.Body)
	}
}

func TestNewCompose(t *testing.T) {
	s := NewCompose("nimbus", "Sender <sender@example.com>", "", "")
	if s.Mode != ModeCompose || s.Account != "nimbus" {
		t.Fatalf("new compose: %+v", s)
	}
	if len(s.To) != 0 || s.Subject != "" || s.Body != "" || s.Signature != "" {
		t.Fatalf("new compose must be blank: %+v", s)
	}
}

func TestAddAttachment(t *testing.T) {
	s := NewCompose("gmail", "Bob <bob@example.com>", "", "")
	path := t.TempDir() + "/note.txt"
	if err := writeFixture(path); err != nil {
		t.Fatal(err)
	}
	if err := s.AddAttachment(path); err != nil {
		t.Fatal(err)
	}
	if len(s.Attachments) != 1 || s.Attachments[0].Name != "note.txt" || s.Attachments[0].Path != path || s.Attachments[0].Size == 0 {
		t.Fatalf("attachment = %+v", s.Attachments)
	}
	if err := s.AddAttachment(t.TempDir()); err == nil {
		t.Fatal("a directory must error")
	}
	if err := s.AddAttachment(t.TempDir() + "/nope"); err == nil {
		t.Fatal("a missing path must error")
	}
}

func writeFixture(path string) error {
	return os.WriteFile(path, []byte("hello attachment"), 0600)
}

func TestQuoteDepthCap(t *testing.T) {
	orig := core.Message{Timestamp: 0, Author: "A <a@b>"}
	parsed := &mail.Message{
		Parts: []mail.Part{
			{Body: "plain"},
			{Body: "> one"},
			{Body: ">> two"},
			{Body: ">>>>> deep"},
			{Body: ">>>>>> six"},
		},
	}
	body := Quote(orig, parsed.Parts)
	lines := strings.Split(body, "\n")
	want := []string{"> plain", ">> one", ">>> two", ">>>>>> deep", ">>>>>> six"}
	for i, w := range want {
		if lines[i+1] != w {
			t.Fatalf("line %d = %q, want %q\n%s", i, lines[i+1], w, body)
		}
	}
}
