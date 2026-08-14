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
