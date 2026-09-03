// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import (
	"reflect"
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

// linesText renders filled lines for assertions.
func linesText(ls []LineBox) []string {
	out := make([]string, len(ls))
	for i, l := range ls {
		var b strings.Builder
		for _, a := range l.Atoms {
			b.WriteString(a.text)
		}
		out[i] = b.String()
	}
	return out
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

func TestFlattenInlineInOrder(t *testing.T) {
	bs := buildBody(`<p>a<b>b</b>c</p>`)
	if got := renderAtoms(flattenInline(bs[0].Children)); got != "abc" {
		t.Fatalf("flatten = %q, want %q", got, "abc")
	}
}

func TestFlattenKeepsBRAndImg(t *testing.T) {
	bs := buildBody(`<p>x<br><img src="p.png">y</p>`)
	as := flattenInline(bs[0].Children)
	if got := renderAtoms(as); got != "x\ny" {
		t.Fatalf("flatten = %q, want %q", got, "x\ny")
	}
	if len(as) != 4 || !as[1].br || as[2].img == nil {
		t.Fatalf("atoms must be x, br, img, y in order, got %d", len(as))
	}
}

func TestFlattenPreTextStaysVerbatim(t *testing.T) {
	// nested inline under a bare pre inherits WSPre; flatten must keep
	// the run verbatim across the box boundary.
	bs := buildBody(`<pre>a  <b>b</b> c</pre>`)
	if got := renderAtoms(flattenInline(bs[0].Children)); got != "a  b c" {
		t.Fatalf("pre flatten = %q, want %q", got, "a  b c")
	}
}

func TestFillWrapsBeforeOverflow(t *testing.T) {
	bs := buildBody(`<p>one two three</p>`)
	ls := LayoutInline(bs[0], 8, mono(1), false)
	if got := linesText(ls); !reflect.DeepEqual(got, []string{"one two", "three"}) {
		t.Fatalf("lines = %q, want %q", got, []string{"one two", "three"})
	}
}

func TestLineEdgeSpacesDrop(t *testing.T) {
	bs := buildBody(`<p>  a b  </p>`)
	if got := linesText(LayoutInline(bs[0], 50, mono(1), false)); !reflect.DeepEqual(got, []string{"a b"}) {
		t.Fatalf("edges = %q, want %q", got, []string{"a b"})
	}
}

func TestCrossBoundaryCollapseToSingleSpace(t *testing.T) {
	// trailing space of <b>a </b> and leading space of " b" = one space
	bs := buildBody(`<p><b>a </b> b</p>`)
	if got := linesText(LayoutInline(bs[0], 50, mono(1), false)); !reflect.DeepEqual(got, []string{"a b"}) {
		t.Fatalf("cross-boundary = %q, want %q", got, []string{"a b"})
	}
}

func TestInlineBoundaryNewlineCollapses(t *testing.T) {
	bs := buildBody("<p><b>a\n</b>b</p>") // LF at an inline boundary collapses
	if got := linesText(LayoutInline(bs[0], 50, mono(1), false)); !reflect.DeepEqual(got, []string{"a b"}) {
		t.Fatalf("boundary newline = %q, want %q", got, []string{"a b"})
	}
}

func TestBrBreaksLine(t *testing.T) {
	bs := buildBody(`<p>a<br>b</p>`)
	if got := linesText(LayoutInline(bs[0], 50, mono(1), false)); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("br lines = %q, want %q", got, []string{"a", "b"})
	}
}

func TestPreAuthorNoWrap(t *testing.T) {
	bs := buildBody(`<pre>aa bb</pre>`)
	if got := linesText(LayoutInline(bs[0], 4, mono(1), false)); !reflect.DeepEqual(got, []string{"aa bb"}) {
		t.Fatalf("pre overflow = %q, want %q", got, []string{"aa bb"})
	}
	bs = buildBody("<pre>a\nb</pre>")
	if got := linesText(LayoutInline(bs[0], 50, mono(1), false)); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("pre newline = %q, want %q", got, []string{"a", "b"})
	}
}

func TestNowrapAuthorWhole(t *testing.T) {
	bs := buildBody(`<p style="white-space:nowrap">one two three</p>`)
	if got := linesText(LayoutInline(bs[0], 5, mono(1), false)); !reflect.DeepEqual(got, []string{"one two three"}) {
		t.Fatalf("nowrap = %q, want %q", got, []string{"one two three"})
	}
}

func TestPreWrapAuthorWrapsAtSpaces(t *testing.T) {
	bs := buildBody(`<p style="white-space:pre-wrap">aa bb cc</p>`)
	if got := linesText(LayoutInline(bs[0], 5, mono(1), false)); !reflect.DeepEqual(got, []string{"aa bb", "cc"}) {
		t.Fatalf("pre-wrap = %q, want %q", got, []string{"aa bb", "cc"})
	}
}

func TestNormalizeWrapsNowrap(t *testing.T) {
	bs := buildBody(`<p style="white-space:nowrap">one two three</p>`)
	if got := linesText(LayoutInline(bs[0], 8, mono(1), true)); !reflect.DeepEqual(got, []string{"one two", "three"}) {
		t.Fatalf("normalize nowrap = %q, want %q", got, []string{"one two", "three"})
	}
}

func TestNormalizeCharBreaksWord(t *testing.T) {
	bs := buildBody(`<p>abcdef</p>`)
	if got := linesText(LayoutInline(bs[0], 4, mono(1), true)); !reflect.DeepEqual(got, []string{"abcd", "ef"}) {
		t.Fatalf("normalize char-break = %q, want %q", got, []string{"abcd", "ef"})
	}
}

func TestNormalizeWrapsPreAtSpaces(t *testing.T) {
	bs := buildBody(`<pre>aa bb</pre>`)
	if got := linesText(LayoutInline(bs[0], 4, mono(1), true)); !reflect.DeepEqual(got, []string{"aa", "bb"}) {
		t.Fatalf("normalize pre wrap = %q, want %q", got, []string{"aa", "bb"})
	}
}

func TestNormalizeCharBreaksPreWord(t *testing.T) {
	bs := buildBody(`<pre>abcdefgh</pre>`)
	if got := linesText(LayoutInline(bs[0], 4, mono(1), true)); !reflect.DeepEqual(got, []string{"abcd", "efgh"}) {
		t.Fatalf("normalize pre char-break = %q, want %q", got, []string{"abcd", "efgh"})
	}
}

func TestAlignCenter(t *testing.T) {
	bs := buildBody(`<p style="text-align:center">ab</p>`)
	ls := LayoutInline(bs[0], 20, mono(1), false)
	if len(ls) != 1 || ls[0].X != 9 || linesText(ls)[0] != "ab" {
		t.Fatalf("centered line X = %d (%q), want 9 (ab)", ls[0].X, linesText(ls)[0])
	}
}

func TestAlignRight(t *testing.T) {
	bs := buildBody(`<p style="text-align:right">ab</p>`)
	ls := LayoutInline(bs[0], 20, mono(1), false)
	if len(ls) != 1 || ls[0].X != 18 {
		t.Fatalf("right line X = %d, want 18", ls[0].X)
	}
}

func TestNormalizeCharBreaksNoSpaceBoundary(t *testing.T) {
	// adjacent spans with no whitespace are one unbreakable run; under
	// normalize the emergency char-break must cut it mid-line, matching
	// what a single "helloworld" text atom produces at width 6.
	bs := buildBody(`<p><span>hello</span><span>world</span></p>`)
	if got := linesText(LayoutInline(bs[0], 6, mono(1), true)); !reflect.DeepEqual(got, []string{"hellow", "orld"}) {
		t.Fatalf("normalize no-space boundary = %q, want %q", got, []string{"hellow", "orld"})
	}
}

func TestNormalizeNoSpaceBoundaryExactFill(t *testing.T) {
	// line exactly full + following no-space token: break at the token's
	// leading edge, never cram one rune onto the full line.
	bs := buildBody(`<p><span>hello</span><span>world</span></p>`)
	if got := linesText(LayoutInline(bs[0], 5, mono(1), true)); !reflect.DeepEqual(got, []string{"hello", "world"}) {
		t.Fatalf("normalize exact-fill boundary = %q, want %q", got, []string{"hello", "world"})
	}
}
