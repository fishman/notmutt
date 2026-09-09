// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package compose

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"notmutt/core"
	"notmutt/mail"
)

// draftFile writes a single-part draft and returns its path (the shape
// saveDraft produces for a draft without attachments).
func draftFile(t *testing.T, body string) string {
	t.Helper()
	raw := "From: Bob <bob@example.com>\n" +
		"To: alice@example.com\n" +
		"Bcc: hidden@example.net\n" +
		"Subject: the draft\n" +
		"Content-Type: text/plain; charset=utf-8\n" +
		"\n" + body
	p := filepath.Join(t.TempDir(), "draft.eml")
	if err := os.WriteFile(p, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestResumeBuilder: a saved draft resumes into a standalone compose -
// envelope (Bcc included) and body restored, signature tail detached,
// ResumePath set, and no thread identity.
func TestResumeBuilder(t *testing.T) {
	p := draftFile(t, "line one\n\n-- \nsig text")
	d, err := mail.ParseDraft(p)
	if err != nil {
		t.Fatal(err)
	}
	orig := core.Message{ID: "id:abc", Paths: []string{p}, Tags: []string{"draft"}}
	st := Resume(orig, d, "gmail", "bob@example.com")

	if st.Mode != ModeCompose {
		t.Fatalf("Mode = %v, want compose", st.Mode)
	}
	if len(st.To) != 1 || st.To[0] != "alice@example.com" {
		t.Fatalf("To = %v", st.To)
	}
	if len(st.Bcc) != 1 || st.Bcc[0] != "hidden@example.net" {
		t.Fatalf("Bcc = %v", st.Bcc)
	}
	if st.Subject != "the draft" {
		t.Fatalf("Subject = %q", st.Subject)
	}
	if st.Body != "line one" {
		t.Fatalf("Body = %q (signature must detach)", st.Body)
	}
	if st.SignatureBody != "sig text" {
		t.Fatalf("SignatureBody = %q", st.SignatureBody)
	}
	if st.Signature != "" {
		t.Fatalf("Signature name must be empty (the text is authoritative): %q", st.Signature)
	}
	if st.ResumePath != p {
		t.Fatalf("ResumePath = %q, want %q", st.ResumePath, p)
	}
	if st.OriginalID != "" || st.MessageID != "" || len(st.References) != 0 {
		t.Fatalf("a resume must not thread or tag: %+v", st)
	}
	if len(st.Attachments) != 0 {
		t.Fatalf("Attachments = %v, want none", st.Attachments)
	}
}

// TestResumeNoSignature: a draft saved without a signature restores the
// whole body with no signature body.
func TestResumeNoSignature(t *testing.T) {
	p := draftFile(t, "no signature here")
	d, err := mail.ParseDraft(p)
	if err != nil {
		t.Fatal(err)
	}
	st := Resume(core.Message{Paths: []string{p}}, d, "gmail", "bob@example.com")
	if st.Body != "no signature here" || st.SignatureBody != "" {
		t.Fatalf("body=%q sig=%q", st.Body, st.SignatureBody)
	}
}

// TestResumeRoundTrip: resume then re-assemble reproduces the saved
// body byte-for-byte after a full parse - assemble re-encodes
// (quoted-printable), so fidelity is asserted decoded, not raw.
func TestResumeRoundTrip(t *testing.T) {
	saved := "line one\n\ttabbed\nquoted\n\n-- \nsig line"
	p := draftFile(t, saved)
	d, err := mail.ParseDraft(p)
	if err != nil {
		t.Fatal(err)
	}
	st := Resume(core.Message{Paths: []string{p}}, d, "gmail", "bob@example.com")

	var buf strings.Builder
	if err := st.Assemble(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Bcc: <hidden@example.net>") {
		t.Fatalf("the fcc/assemble shape keeps Bcc:\n%s", buf.String())
	}
	outPath := filepath.Join(t.TempDir(), "out.eml")
	if err := os.WriteFile(outPath, []byte(buf.String()), 0600); err != nil {
		t.Fatal(err)
	}
	rd, err := mail.ParseDraft(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if rd.Body != saved {
		t.Fatalf("round trip changed the body: %q != %q", rd.Body, saved)
	}
}

// TestResumeStreamsAttachment: a draft saved with an attachment resumes
// to an Attachment whose bytes stream from the draft (DraftPart = the
// stored part), and re-assembly reproduces the attachment bytes - the
// original file is long gone, only the draft remains.
func TestResumeStreamsAttachment(t *testing.T) {
	dir := t.TempDir()
	orig := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(orig, []byte("attachment data\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// save the state like saveDraft does: Assemble to the draft path
	src := NewCompose("gmail", "bob@example.com", "", "")
	src.To = []string{"alice@example.com"}
	src.Bcc = []string{"hidden@example.net"}
	src.Subject = "the draft"
	src.Body = "line one\n\n-- \nsig"
	if err := src.AddAttachment(orig); err != nil {
		t.Fatal(err)
	}
	draft := filepath.Join(dir, "draft.eml")
	var buf strings.Builder
	if err := src.Assemble(&buf); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(draft, []byte(buf.String()), 0600); err != nil {
		t.Fatal(err)
	}

	d, err := mail.ParseDraft(draft)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Atts) != 1 {
		t.Fatalf("Atts = %v", d.Atts)
	}
	st := Resume(core.Message{Paths: []string{draft}}, d, "gmail", "bob@example.com")
	if len(st.Attachments) != 1 {
		t.Fatalf("Attachments = %v", st.Attachments)
	}
	a := st.Attachments[0]
	if a.Name != "notes.txt" || a.Path != draft || a.DraftPart != 1 {
		t.Fatalf("Attachment = %+v, want DraftPart 1 on the draft path", a)
	}
	if a.Size == 0 || a.MimeType == "" {
		t.Fatalf("Attachment carries no size/type: %+v", a)
	}

	var out strings.Builder
	if err := st.Assemble(&out); err != nil {
		t.Fatal(err)
	}
	outPath := filepath.Join(dir, "resumed.eml")
	if err := os.WriteFile(outPath, []byte(out.String()), 0600); err != nil {
		t.Fatal(err)
	}
	rd, err := mail.ParseDraft(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if rd.Body != "line one\n\n-- \nsig" || rd.Atts[0].Name != "notes.txt" {
		t.Fatalf("resumed re-assembly wrong: body=%q atts=%v", rd.Body, rd.Atts)
	}
	var got strings.Builder
	if _, err := mail.WriteDraftAttachment(outPath, 0, &got); err != nil {
		t.Fatal(err)
	}
	if got.String() != "attachment data\n" {
		t.Fatalf("streamed attachment = %q, want %q", got.String(), "attachment data\n")
	}
}
