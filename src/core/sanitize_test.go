// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package core

import (
	"strings"
	"testing"
)

func TestSanitizeControls(t *testing.T) {
	clean := SanitizeControls("ok \x1b[31mred\x07\x1b]0;title")
	if strings.ContainsAny(clean, "\x1b\x07") || !strings.Contains(clean, "ok") {
		t.Fatalf("C0/DEL/C1 runes must be stripped: %q", clean)
	}
	if SanitizeControls("plain") != "plain" {
		t.Fatal("clean text must pass through unchanged")
	}
}

func TestSanitizeTextKeepsNewlines(t *testing.T) {
	in := "para one\n\npara \x1b[31mtwo\x07\n\ttabbed"
	got := SanitizeText(in)
	if got != "para one\n\npara [31mtwo\n\ttabbed" {
		t.Fatalf("SanitizeText = %q, want newlines/tabs kept and control bytes stripped", got)
	}
	if SanitizeText("plain") != "plain" {
		t.Fatal("clean text must pass through unchanged")
	}
	if strings.Contains(SanitizeText("\x1b]0;title\x07x"), "\x1b\x07") {
		t.Fatal("escape bytes must still be stripped")
	}
}
