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

// TestSplitSignature: the tail detaches at the first line that is
// exactly "-- " (the SigBlock marker). The split is structural - no
// saved signature name to match - and re-assembly through BodyWithSig
// reproduces the original bytes whether or not the split is right.
func TestSplitSignature(t *testing.T) {
	orig := "line one\nline two\n\n-- \nsigned tail"
	body, sig := SplitSignature(orig)
	if body != "line one\nline two" {
		t.Fatalf("body = %q", body)
	}
	if sig != "signed tail" {
		t.Fatalf("sig = %q", sig)
	}
	if got := BodyWithSig(body, sig); got != orig {
		t.Fatalf("round trip = %q, want %q", got, orig)
	}
}

// TestSplitSignatureNone: no "-- " marker - the whole text is body.
func TestSplitSignatureNone(t *testing.T) {
	body, sig := SplitSignature("just a body\nno signature\n")
	if body != "just a body\nno signature\n" || sig != "" {
		t.Fatalf("body=%q sig=%q", body, sig)
	}
}

// TestSplitSignatureBodyContainsMarker: a body that quotes a "-- " line
// mis-splits but is byte-faithful on re-assembly - nothing is lost.
func TestSplitSignatureBodyContainsMarker(t *testing.T) {
	orig := "the reply said\n\n-- \nme too\n\n-- \nreal sig"
	body, sig := SplitSignature(orig)
	if got := BodyWithSig(body, sig); got != orig {
		t.Fatalf("round trip = %q, want %q", got, orig)
	}
}

// TestSplitSignatureSignatureOnly: a body that was only a signature
// (the SigBlock prefix, no text above) still splits cleanly.
func TestSplitSignatureSignatureOnly(t *testing.T) {
	body, sig := SplitSignature("\n\n-- \nsig")
	if body != "" || sig != "sig" {
		t.Fatalf("body=%q sig=%q", body, sig)
	}
}
