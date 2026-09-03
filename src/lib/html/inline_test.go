// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import (
	"reflect"
	"strings"
	"testing"
	"time"
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

func TestAuthorOverwideCollapsibleOverflowsWhole(t *testing.T) {
	// author mode: an over-wide collapsible word overflows whole (only the
	// pre/nowrap author overflow was pinned before).
	bs := buildBody(`<p>aa abcdef</p>`)
	if got := linesText(LayoutInline(bs[0], 4, mono(1), false)); !reflect.DeepEqual(got, []string{"aa", "abcdef"}) {
		t.Fatalf("author overflow = %q, want %q", got, []string{"aa", "abcdef"})
	}
}

func TestOverwideCenteredLineStaysFlushLeft(t *testing.T) {
	// an over-width or exactly-full line under center/right keeps X = 0.
	for _, tc := range []struct {
		align string
		width int
	}{
		{"center", 4}, // over-wide: pad would be negative
		{"right", 6},  // exactly full: pad is 0
	} {
		bs := buildBody(`<p style="text-align:` + tc.align + `">abcdef</p>`)
		ls := LayoutInline(bs[0], tc.width, mono(1), false)
		x := 0
		if len(ls) != 1 {
			x = -1
		} else {
			x = ls[0].X
		}
		if len(ls) != 1 || x != 0 {
			t.Fatalf("%s over-wide X = %d, want 1 line at X 0", tc.align, x)
		}
	}
}

func TestNoEmptyLinesAtFillSurface(t *testing.T) {
	// breaks with nothing between them emit no empty lines.
	bs := buildBody(`<p>a<br><br>b</p>`)
	if got := linesText(LayoutInline(bs[0], 50, mono(1), false)); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("double br = %q, want %q", got, []string{"a", "b"})
	}
}

// dbl is an uneven Metrics: runes at or above 0x2E80 measure 2px, others 1px.
// mono(n) scales uniformly, so a rune-count-for-px regression would pass on it.
type dbl struct{}

func (dbl) Width(s string) int {
	n := 0
	for _, r := range s {
		n += dblW(r)
	}
	return n
}

// RuneWidth makes dbl step-capable once Metrics grows RuneStepper, so the
// px-not-rune pin drives the incremental cut path too.
func (dbl) RuneWidth(r rune) int {
	return dblW(r)
}

func dblW(r rune) int {
	if r >= 0x2E80 {
		return 2
	}
	return 1
}

func TestCharBreakMeasuresPxNotRunes(t *testing.T) {
	// "ab界c" is 4 runes but 5px: a rune-count cut would keep it whole and
	// overflow; px cut breaks after 4px (the 界 costs 2).
	bs := buildBody(`<p>ab界c</p>`)
	if got := linesText(LayoutInline(bs[0], 4, dbl{}, true)); !reflect.DeepEqual(got, []string{"ab界", "c"}) {
		t.Fatalf("px char-break = %q, want %q", got, []string{"ab界", "c"})
	}
}

// countMeter counts every rune a Width call scans, so re-measuring
// prefixes on a long string shows up as O(n^2) scanned runes.
type countMeter struct {
	scanned int
}

func (m *countMeter) Width(s string) int {
	for range s {
		m.scanned++
	}
	return utf8.RuneCountInString(s)
}

func (m *countMeter) RuneWidth(r rune) int {
	return 1
}

func TestCutTextDoesNotRescanFromHead(t *testing.T) {
	s := strings.Repeat("x", 4000) // budget fits the whole run
	m := &countMeter{}
	if head, _ := cutText(s, len(s), m); head != s {
		t.Fatal("cutText returned a partial head for a fitting budget")
	}
	if m.scanned > 2*len(s) {
		t.Fatalf("cutText rescanned %d runes to fit one budget, want <= %d (quadratic re-measure)", m.scanned, 2*len(s))
	}
}

func TestGiantUnbrokenTokenLaysOutLinearly(t *testing.T) {
	// A ~1MB single word is a content-reachable DoS if atomizing or
	// char-breaking is super-linear (BUGS.org #1). On the fixed code this
	// finishes in well under a second; a reintroduced quadratic path hangs.
	n := 1_000_000
	bs := buildBody("<p>" + strings.Repeat("a", n) + "</p>")
	done := make(chan []LineBox, 1)
	go func() { done <- LayoutInline(bs[0], 80, mono(1), true) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("LayoutInline of a 1MB unbroken token did not finish in 3s (quadratic atomize/char-break)")
	}
}

func TestBreakAtSpaceStopsMeasuringPastBudget(t *testing.T) {
	// pre-fix breakAtSpace keeps measuring every later space against its
	// full prefix (~n^2 scanned); post-fix it stops once a prefix exceeds
	// the budget (widths are monotonic), so scanned stays ~budget-sized.
	s := strings.Repeat("ab ", 5000) // 15000 chars, a space every third byte
	m := &countMeter{}
	if head, tail, ok := breakAtSpace(s, 40, m); !ok || head == "" || tail == "" {
		t.Fatal("expected a break within the budget")
	}
	if m.scanned > 10*len(s) {
		t.Fatalf("breakAtSpace measured %d runes to break a 15000-rune string, want <= %d", m.scanned, 10*len(s))
	}
}

func TestPreservedSpaceDenseTokenLaysOutLinearly(t *testing.T) {
	// A space-dense preserved run that wraps (pre-wrap) used to cost ~n^3:
	// breakAtSpace rescanned past the width budget on every emitted line.
	// The early-break keeps each wrap O(budget), so this finishes fast.
	body := "<p style=\"white-space:pre-wrap\">" + strings.Repeat("ab ", 32000) + "</p>"
	bs := buildBody(body)
	done := make(chan []LineBox, 1)
	go func() { done <- LayoutInline(bs[0], 80, mono(1), false) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("LayoutInline of a 96k space-dense preserved run did not finish in 3s (cubic breakAtSpace)")
	}
}

func TestPreservedWrapFillDoesNotRescanRemainder(t *testing.T) {
	// The preserved-wrap fill used to re-measure the whole remaining tail
	// once per emitted line (O(n^2/budget)). Keeping a running tail width
	// makes the per-line check O(1); a countMeter counts every Width scan.
	s := strings.Repeat("ab ", 20000) // 60000 chars, ~770 wrapped lines at width 80
	m := &countMeter{}
	bs := buildBody(`<p style="white-space:pre-wrap">` + s + `</p>`)
	LayoutInline(bs[0], 80, m, false)
	if m.scanned > 30*len(s) {
		t.Fatalf("preserved fill measured %d runes for %d chars, want <= %d (whole-tail re-measure)", m.scanned, len(s), 30*len(s))
	}
}

func TestPreservedWrapTailFitsWhole(t *testing.T) {
	// The running remainder width must account for the break space
	// breakAtSpace drops (head + " " are consumed). Without it the width
	// drifts +1px per wrap, and a long run's final tail that fits gets
	// split onto an extra line: 186 lines here, not 187.
	s := strings.Repeat("ab ", 5000) + "x" // 15001 chars
	bs := buildBody(`<p style="white-space:pre-wrap">` + s + `</p>`)
	ls := LayoutInline(bs[0], 80, mono(1), false)
	if len(ls) != 186 {
		t.Fatalf("pre-wrap layout = %d lines, want 186 (tail must fit whole)", len(ls))
	}
	if got := linesText(ls)[185]; got != "ab ab ab ab ab x" {
		t.Fatalf("final line = %q, want the whole fitting tail %q", got, "ab ab ab ab ab x")
	}
}
