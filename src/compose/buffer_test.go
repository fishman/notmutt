// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package compose

import (
	"strings"
	"testing"
)

func TestParseBufferRoundTrip(t *testing.T) {
	s := NewCompose("gmail", "Bob <bob@example.com>", "gmail", "bob")
	s.Body = "> quoted\nsecond line"
	buf := BodyWithSig(s.Body, s.SignatureBody)
	body, sigName, sigBody := ParseBuffer(buf, s.Signature, s.SignatureBody)
	if body != "> quoted\nsecond line" {
		t.Fatalf("body = %q", body)
	}
	if sigName != "gmail" || sigBody != "bob" {
		t.Fatalf("sig = %q %q", sigName, sigBody)
	}
}

func TestParseBufferEditedSignatureDetaches(t *testing.T) {
	buf := "body\n\n-- \nbob\nEDITED"
	body, sigName, sigBody := ParseBuffer(buf, "gmail", "bob")
	// the edited tail stays as the user's text; the signature detaches
	if body != "body\n\n-- \nbob\nEDITED" {
		t.Fatalf("body = %q", body)
	}
	if sigName != "" || sigBody != "" {
		t.Fatalf("edited tail must detach the signature: %q %q", sigName, sigBody)
	}
}

func TestParseBufferPlain(t *testing.T) {
	body, sigName, sigBody := ParseBuffer("plain body\n", "", "")
	if body != "plain body" || sigName != "" || sigBody != "" {
		t.Fatalf("plain parse = %q %q %q", body, sigName, sigBody)
	}
}

func TestParseBufferCRLF(t *testing.T) {
	s := NewCompose("gmail", "bob@example.com", "sig", "sig body")
	s.Body = "line1\nline2"
	buf := strings.ReplaceAll(BodyWithSig(s.Body, s.SignatureBody), "\n", "\r\n")
	body, sigName, sigBody := ParseBuffer(buf, s.Signature, s.SignatureBody)
	if body != "line1\nline2" || sigName != "sig" || sigBody != "sig body" {
		t.Fatalf("CRLF round trip: %q %q %q", body, sigName, sigBody)
	}
}
