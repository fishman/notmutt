// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// mono is a fixed-advance test double: one px per rune. The terminal
// backend's real Metrics is a mail-side type over the same interface.
type mono int

func (m mono) Width(s string) int { return int(m) * utf8.RuneCountInString(s) }

// renderAtoms joins atom text, br as newline, for asserting a raw stream.
func renderAtoms(as []atom) string {
	var b strings.Builder
	for _, a := range as {
		if a.br {
			b.WriteByte('\n')
		} else {
			b.WriteString(a.text)
		}
	}
	return b.String()
}

func TestWSBehavior(t *testing.T) {
	tests := []struct {
		ws           WS
		wrap         bool
		collapse     bool
		newlineBreak bool
	}{
		{WSNormal, true, true, false},
		{WSNowrap, false, true, false},
		{WSPre, false, false, true},
		{WSPreWrap, true, false, true},
		{WSPreLine, true, true, true},
	}
	for _, tc := range tests {
		b := wsBehavior(tc.ws)
		if b.wrap != tc.wrap || b.collapse != tc.collapse || b.newlineBreak != tc.newlineBreak {
			t.Errorf("wsBehavior(%d) = %+v, want wrap=%v collapse=%v newlineBreak=%v",
				tc.ws, b, tc.wrap, tc.collapse, tc.newlineBreak)
		}
	}
}

func TestCollapseRunsToOneSpace(t *testing.T) {
	if got := renderAtoms(atomizeText("a  b\tc", &Style{}, WSNormal)); got != "a b c" {
		t.Fatalf("collapse = %q, want %q", got, "a b c")
	}
}

func TestNewlineBecomesSpaceNormalAndNowrap(t *testing.T) {
	for _, ws := range []WS{WSNormal, WSNowrap} {
		if got := renderAtoms(atomizeText("a \n b", &Style{}, ws)); got != "a b" {
			t.Fatalf("ws=%d newline = %q, want %q", ws, got, "a b")
		}
	}
}

func TestCRLFIsOneNewline(t *testing.T) {
	// \r\n must never fire twice: one collapsed space in normal,
	// one forced break in the pre family.
	if got := renderAtoms(atomizeText("a\r\nb", &Style{}, WSNormal)); got != "a b" {
		t.Fatalf("normal CRLF = %q, want %q", got, "a b")
	}
	for _, ws := range []WS{WSPre, WSPreWrap, WSPreLine} {
		if got := renderAtoms(atomizeText("a\r\nb", &Style{}, ws)); got != "a\nb" {
			t.Fatalf("ws=%d CRLF = %q, want %q", ws, got, "a\nb")
		}
	}
}

func TestPreLineKeepsBreakCollapsesIndent(t *testing.T) {
	// pre-line: the newline is a kept break; the indent collapses to one
	// space (the fill drops it at the line start).
	if got := renderAtoms(atomizeText("a\n b", &Style{}, WSPreLine)); got != "a\n b" {
		t.Fatalf("pre-line = %q, want %q", got, "a\n b")
	}
}

func TestPreAndPreWrapKeepWhitespace(t *testing.T) {
	for _, ws := range []WS{WSPre, WSPreWrap} {
		if got := renderAtoms(atomizeText("a  b\n c", &Style{}, ws)); got != "a  b\n c" {
			t.Fatalf("ws=%d = %q, want %q", ws, got, "a  b\n c")
		}
	}
}
