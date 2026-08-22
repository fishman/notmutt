// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package compose

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// TestAssemblePartWireFacts pins the part wire facts the compose rows
// display (InlineFacts/AttachmentFacts - rendered verbatim): the inline
// part is quoted-printable with the explicit charset, an attachment
// rides base64 and the detected Content-Type. Assemble writes the same
// facts, so display and wire share one definition - a drift fails here.
func TestAssemblePartWireFacts(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "note-*.md")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("# hello\n")
	f.Close()
	st := &State{From: "a@b.c", To: []string{"d@e.f"}, Subject: "s"}
	st.AddAttachment(f.Name())
	if got := st.Attachments[0].MimeType; got != "text/markdown" {
		t.Fatalf("MimeTypeOf(.md) = %q, want text/markdown", got)
	}
	if got := InlineFacts(st); got != (PartFacts{Type: "text/plain", Encoding: "quoted-printable", Charset: "utf-8"}) {
		t.Fatalf("inline facts = %+v", got)
	}
	if got := AttachmentFacts(st.Attachments[0]); got != (PartFacts{Type: "text/markdown", Encoding: "base64"}) {
		t.Fatalf("attachment facts = %+v", got)
	}
	var buf bytes.Buffer
	if err := st.Assemble(&buf); err != nil {
		t.Fatal(err)
	}
	raw := buf.String()
	for _, want := range []string{
		"Content-Transfer-Encoding: quoted-printable",
		"Content-Transfer-Encoding: base64",
		"Content-Type: text/markdown",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("the wire must carry %q:\n%s", want, raw)
		}
	}
}

func TestMimeTypeOf(t *testing.T) {
	for name, want := range map[string]string{
		"notes.md":     "text/markdown",
		"x.PDF":        "application/pdf",
		"no-extension": "application/octet-stream",
		"x.unknownx":   "application/octet-stream",
	} {
		if got := MimeTypeOf(name); got != want {
			t.Fatalf("MimeTypeOf(%q) = %q, want %q", name, got, want)
		}
	}
}
