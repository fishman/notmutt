# HTML inline layout + whitespace (Plan 2) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the stage-1 inline/whitespace layout in `src/lib/html`: flatten a block box's uniformly-inline children into text atoms, apply the 5-value white-space collapse model, fill lines greedily at a px width through a `Metrics` interface, honor an engine normalize option (wrap all text + emergency char-break), and offset lines for block text-align.

**Architecture:** Consumes the Plan 1 box tree (`Box`, `WS`, per-leaf computed `St`/`WS`, all non-nil). One new module `inline.go` + a `Metrics` interface in `metrics.go`, pure px, no terminal knowledge. Vertical stacking of the produced lines is block.go's job (Plan 3), not here. The current cell walker (src/mail/html.go) keeps running untouched; wiring stage-1 in is Plan 6.

**Tech Stack:** Go, stdlib only (strings, unicode/utf8). Same package `html` as box.go/ua.go/html.go; test helpers (`buildBody`) already live in box_test.go.

## Normative sources (read before touching code)

- Spec `docs/superpowers/specs/2026-09-03-html-layout-engine-design.md`, "Stage 1: inline layout and whitespace (`inline.go`)" (lines ~155-194).
- WeasyPrint two booleans (text/line_break.py): `wrap` = WS in {normal, pre-wrap, pre-line}; `collapse` = WS in {normal, nowrap, pre-line}; LF is a forced break in the pre family, a collapsible space otherwise.
- Walker reference behavior (src/mail/html.go): greedy wrap, drop line-end space (line 393), center/right pad math `(width-cells)/2` and `width-cells` (lines 399-407), break-before-word not at the space (line 338).

## Locked semantics (decisions already made; do not re-litigate)

- **Normalize** forces `wrap = true` for every class, keeps per-class `collapse` and newline handling, and adds `overflow-wrap: anywhere` emergency char-break (cut a single over-wide word or unbreakable preserved run at the content width). Author mode honors the class table exactly.
- **Preserved text that wraps** (pre-wrap in author mode; pre/pre-wrap under normalize) breaks at preserved interior spaces: greedy, at the LAST space whose preceding prefix fits, consuming the space (neither line keeps it). A preserved run with no fitting space: author mode overflows it whole; normalize char-breaks it.
- **Collapsible separators** render as exactly one space when content precedes on the line; a separator at a line start or line end drops (the line-end drop happens in flush). A break happens BEFORE the next word (the space drops), never at the separator. Two consecutive separators from adjacent leaves (cross-boundary collapse, "a leading space after a prior trailing space") render as one.
- **nowrap** (author): collapse is on, but separators are NOT break points and words never break - the whole run stays on one line, overflowing.
- **No empty lines**: a break with nothing on the line emits nothing. This mirrors the walker (`flush` no-ops on empty words, mail/html.go:386). Blank-line preservation (interior double newlines, consecutive `<br>`) is a Plan 6 parity decision, not made here.
- **LineBox.X**: only set when the pad is positive (an over-width line stays flush left), matching the walker's `pad > 0` guard.
- **Deferred to Plan 6** (walked there against the pinned walker tests, not invented here): `bindsLeft` punctuation-hugging (mail/html.go:369) - terminal typography, not in the spec's inline section; tab expansion inside preserved text (walker expands; stage-1 keeps the rune verbatim - tabs are rare in mail); grapheme-aware char-break (proportional fonts; `Metrics` stays Width-only today).

## File structure

- Create `src/lib/html/inline.go`: wsB, wsBehavior, atom, atomizeText, flattenInline, LineBox, LayoutInline, breakAtSpace, cutText, applyAlign.
- Create `src/lib/html/metrics.go`: Metrics interface.
- Test `src/lib/html/inline_test.go`: per-task tests + the `mono` px double, `renderAtoms`, `linesText` helpers.
- No other file changes. box.go/ua.go/html.go and the mail walker are untouched.

## Data model

```go
// metrics.go
type Metrics interface {
	Width(s string) int // px advance of s
}
```

```go
// inline.go
// wsB is the layout behavior of one white-space class.
type wsB struct {
	wrap         bool // text may wrap at break opportunities
	collapse     bool // collapsible whitespace collapses
	newlineBreak bool // LF is a forced break (the pre family)
}

func wsBehavior(w WS) wsB

// atom is one laid-out fragment of inline content. Exactly one of
// text / img / br is live; sep marks a single collapsible space.
type atom struct {
	st   *Style
	ws   WS   // effective white-space of the text that made this atom
	text string
	img  *Box // atomic image (px width resolved by the image plan)
	br   bool // <br> or preserved newline: forces a break
	sep  bool // one collapsed space (a break point iff the text wraps)
}

func (a atom) width(m Metrics) int // m.Width(a.text); img is 0 until the image plan

// LineBox is one filled line: its laid-out atoms in order.
type LineBox struct {
	Atoms []atom
	Width int // content px width (trailing space already dropped)
	X     int // lead offset for block text-align (px), 0 = flush left
}

// LayoutInline lays out one block's uniformly-inline children at the
// given content width. m measures text; norm enables normalize mode.
func LayoutInline(block *Box, width int, m Metrics, norm bool) []LineBox
```

Collapse uses the CSS collapsible set (space, tab, LF, CR, FF), not
unicode.IsSpace. LF handling runs on newline-normalized text
(CRLF and lone CR become LF) so `\r\n` never double-fires.

## Task 1: white-space behavior table

**Files:**
- Create: `src/lib/html/inline.go`
- Test: `src/lib/html/inline_test.go`

- [ ] **Step 1: Write the failing test**

```go
package html

import "testing"

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
```

- [ ] **Step 2: Run to confirm it fails**

Run: `cd src && go test -count=1 ./lib/html/ -run TestWSBehavior`
Expected: FAIL (wsBehavior undefined)

- [ ] **Step 3: Implement**

```go
// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

// wsB is the layout behavior of one white-space class.
type wsB struct {
	wrap         bool // text may wrap at break opportunities
	collapse     bool // collapsible whitespace collapses
	newlineBreak bool // LF is a forced break (the pre family)
}

// wsBehavior maps a white-space class to its behavior (weasyprint
// text/line_break.py): normal/pre-wrap/pre-line wrap; normal/nowrap/
// pre-line collapse; the pre family breaks on LF.
func wsBehavior(w WS) wsB {
	switch w {
	case WSNowrap:
		return wsB{collapse: true}
	case WSPre:
		return wsB{newlineBreak: true}
	case WSPreWrap:
		return wsB{wrap: true, newlineBreak: true}
	case WSPreLine:
		return wsB{wrap: true, collapse: true, newlineBreak: true}
	default: // WSNormal
		return wsB{wrap: true, collapse: true}
	}
}
```

- [ ] **Step 4: Run to confirm it passes**

Run: `cd src && go test -count=1 ./lib/html/ -run TestWSBehavior`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/lib/html/inline.go src/lib/html/inline_test.go
git commit -m "feat(html): white-space behavior table for inline layout"
```

## Task 2: Metrics interface

**Files:**
- Create: `src/lib/html/metrics.go`

- [ ] **Step 1: Implement**

```go
// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

// Metrics measures text in CSS px so stage 1 needs no terminal
// knowledge. The terminal backend provides monospace metrics (one cell
// is one px advance of the chosen char width, wide runes double); a PDF
// backend later provides real font metrics. Break opportunities are
// rune boundaries today; proportional fonts that need grapheme-level
// cuts extend the interface when that backend lands.
type Metrics interface {
	Width(s string) int // px advance of s
}
```

- [ ] **Step 2: Compile check**

Run: `cd src && go build ./lib/html/`
Expected: PASS (no consumers yet)

- [ ] **Step 3: Commit**

```bash
git add src/lib/html/metrics.go
git commit -m "feat(html): metrics interface for px text measurement"
```

## Task 3: collapse a text leaf into atoms

**Files:**
- Modify: `src/lib/html/inline.go` (atom, collapsible, normalizeLF, atomizeText)
- Test: `src/lib/html/inline_test.go` (mono, renderAtoms + collapse tests)

- [ ] **Step 1: Write the failing tests**

Append to inline_test.go. Helpers introduced here (`mono`, `renderAtoms`) are used by every later task.

```go
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
```

- [ ] **Step 2: Run to confirm they fail**

Run: `cd src && go test -count=1 ./lib/html/ -run 'TestCollapse|TestNewlineBecomes|TestCRLF|TestPreLineKeeps|TestPreAndPreWrap'`
Expected: FAIL (atomizeText undefined)

- [ ] **Step 3: Implement**

Append to inline.go:

```go
// atom is one laid-out fragment of inline content. Exactly one of
// text / img / br is live; sep marks a single collapsible space.
type atom struct {
	st   *Style
	ws   WS   // effective white-space of the text that made this atom
	text string
	img  *Box // atomic image (px width resolved by the image plan)
	br   bool // <br> or preserved newline: forces a break
	sep  bool // one collapsed space (a break point iff the text wraps)
}

func (a atom) width(m Metrics) int {
	if a.img != nil {
		return 0
	}
	return m.Width(a.text)
}

// collapsible reports a CSS-collapsible rune (space, tab, LF, CR, FF).
func collapsible(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\f':
		return true
	}
	return false
}

// normalizeLF folds CRLF and lone CR to LF so a break never double-fires.
func normalizeLF(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "\n")
}

// atomizeText collapses one text leaf under its effective white-space
// class into atoms. Collapse modes split into words separated by single
// space atoms (sep); the pre family keeps text verbatim, splitting only
// on newlines (a trailing LF drops; blank source lines leave only their
// break, mirroring the walker's empty-line drop).
func atomizeText(txt string, st *Style, ws WS) []atom {
	b := wsBehavior(ws)
	txt = normalizeLF(txt)
	if !b.collapse {
		var out []atom
		for i, ln := range strings.Split(strings.TrimSuffix(txt, "\n"), "\n") {
			if i > 0 {
				out = append(out, atom{st: st, ws: ws, br: true})
			}
			if ln == "" {
				continue
			}
			out = append(out, atom{st: st, ws: ws, text: ln})
		}
		return out
	}
	var out []atom
	sep := false
	for _, r := range txt {
		if collapsible(r) {
			if b.newlineBreak { // pre-line: LF is a kept break
				out = append(out, atom{st: st, ws: ws, br: true})
				sep = false
			} else {
				sep = true
			}
			continue
		}
		if sep {
			out = append(out, atom{st: st, ws: ws, text: " ", sep: true})
			sep = false
		}
		if n := len(out); n > 0 && !out[n-1].sep && !out[n-1].br {
			out[n-1].text += string(r)
		} else {
			out = append(out, atom{st: st, ws: ws, text: string(r)})
		}
	}
	if sep {
		out = append(out, atom{st: st, ws: ws, text: " ", sep: true})
	}
	return out
}
```

- [ ] **Step 4: Run to confirm they pass**

Run: `cd src && go test -count=1 ./lib/html/ -run 'TestCollapse|TestNewlineBecomes|TestCRLF|TestPreLineKeeps|TestPreAndPreWrap'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/lib/html/inline.go src/lib/html/inline_test.go
git commit -m "feat(html): collapse text leaves into inline atoms"
```

## Task 4: flatten inline children to an atom stream

**Files:**
- Modify: `src/lib/html/inline.go` (flattenInline)
- Test: `src/lib/html/inline_test.go`

- [ ] **Step 1: Write the failing tests**

```go
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
```

- [ ] **Step 2: Run to confirm they fail**

Run: `cd src && go test -count=1 ./lib/html/ -run 'TestFlatten'`
Expected: FAIL (flattenInline undefined)

- [ ] **Step 3: Implement**

Append to inline.go:

```go
// flattenInline turns a block's inline-level children into one ordered
// atom stream. RoleInline boxes flatten transparently: their text leaves
// already carry the effective white-space class (promoted by the box
// builder), so the tag is irrelevant at layout time. Consecutive sep
// atoms from adjacent leaves collapse at fill (one rendered space), not
// here.
func flattenInline(cs []*Box) []atom {
	var out []atom
	for _, c := range cs {
		switch c.Role {
		case RoleInline:
			out = append(out, flattenInline(c.Children)...)
		case RoleText:
			out = append(out, atomizeText(c.Text, c.St, c.WS)...)
		case RoleImg:
			out = append(out, atom{st: c.St, ws: c.WS, img: c})
		case RoleBR:
			out = append(out, atom{st: c.St, ws: c.WS, br: true})
		}
	}
	return out
}
```

- [ ] **Step 4: Run to confirm they pass**

Run: `cd src && go test -count=1 ./lib/html/ -run 'TestFlatten'`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add src/lib/html/inline.go src/lib/html/inline_test.go
git commit -m "feat(html): flatten inline box children into an atom stream"
```

## Task 5: line fill, normalize, and alignment

**Files:**
- Modify: `src/lib/html/inline.go` (LineBox, LayoutInline, breakAtSpace, cutText, applyAlign)
- Test: `src/lib/html/inline_test.go`

- [ ] **Step 1: Write the failing tests**

Append to inline_test.go (helpers `mono`, `linesText`):

```go
import "reflect"

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
```

- [ ] **Step 2: Run to confirm they fail**

Run: `cd src && go test -count=1 ./lib/html/ -run 'TestFill|TestLineEdge|TestCrossBoundary|TestInlineBoundary|TestBr|TestPreAuthor|TestNowrapAuthor|TestPreWrapAuthor|TestNormalize|TestAlign'`
Expected: FAIL (LayoutInline undefined)

- [ ] **Step 3: Implement**

Append to inline.go:

```go
// LineBox is one filled line: its laid-out atoms in order.
type LineBox struct {
	Atoms []atom
	Width int // content px width (trailing space already dropped)
	X     int // lead offset for block text-align (px), 0 = flush left
}

// LayoutInline lays out one block's uniformly-inline children at the
// given content width. m measures text; norm enables normalize mode
// (wrap every class, char-break words that still overflow). Vertical
// flow is block.go's job. The block's text-align offsets each line.
func LayoutInline(block *Box, width int, m Metrics, norm bool) []LineBox {
	atoms := flattenInline(block.Children)
	var lines []LineBox
	var cur []atom
	cw := 0
	curBreak := false // the trailing atom is a legal break point
	emit := func(a atom) {
		cur = append(cur, a)
		cw += a.width(m)
		curBreak = false
	}
	flush := func() {
		if n := len(cur); n > 0 && cur[n-1].sep {
			cw -= cur[n-1].width(m)
			cur = cur[:n-1] // a line never renders a trailing space
		}
		if len(cur) == 0 {
			return // breaks with nothing on the line emit nothing
		}
		lines = append(lines, LineBox{Atoms: cur, Width: cw})
		cur = nil
		cw = 0
		curBreak = false
	}
	// charBreak emits a text atom in width-wide pieces, one per line
	// (normalize's overflow-wrap:anywhere).
	charBreak := func(a atom) {
		for a.text != "" {
			head, tail := cutText(a.text, width, m)
			a.text = tail
			emit(atom{st: a.st, ws: a.ws, text: head})
			if a.text != "" {
				flush()
			}
		}
	}
	for _, a := range atoms {
		if a.br {
			flush()
			continue
		}
		if a.img != nil {
			emit(a)
			continue
		}
		if a.sep {
			// one space between words on the same line; a separator
			// that opens a line (or follows one) drops here or at flush
			if len(cur) > 0 && !cur[len(cur)-1].sep {
				emit(a)
			}
			curBreak = wsBehavior(a.ws).wrap || norm
			continue
		}
		b := wsBehavior(a.ws)
		if norm {
			b.wrap = true // normalize wraps every class
		}
		if !b.collapse && b.wrap {
			// preserved text that wraps at interior spaces (pre-wrap in
			// author mode; pre and pre-wrap under normalize)
			rest := a.text
			for rest != "" {
				if m.Width(rest) <= width-cw {
					emit(atom{st: a.st, ws: a.ws, text: rest})
					break
				}
				if head, tail, ok := breakAtSpace(rest, width-cw, m); ok {
					emit(atom{st: a.st, ws: a.ws, text: head})
					flush()
					rest = tail
					continue
				}
				if len(cur) > 0 {
					flush()
					continue
				}
				if norm {
					charBreak(atom{st: a.st, ws: a.ws, text: rest})
				} else {
					emit(atom{st: a.st, ws: a.ws, text: rest}) // overflow whole
				}
				break
			}
			continue
		}
		aw := a.width(m)
		if aw == 0 {
			continue
		}
		if len(cur) > 0 && cw+aw > width && curBreak {
			flush() // break before the word; the trailing space drops
		}
		if len(cur) == 0 && aw > width {
			if norm {
				charBreak(a)
			} else {
				emit(a) // author mode: an over-wide word overflows
			}
			continue
		}
		emit(a)
	}
	flush()
	applyAlign(lines, block, width)
	return lines
}

// breakAtSpace splits preserved text at the last space whose preceding
// run fits the budget, dropping the break space itself (neither line
// keeps it). ok is false when no such space exists.
func breakAtSpace(s string, budget int, m Metrics) (head, tail string, ok bool) {
	best := -1
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' || i == 0 {
			continue
		}
		if m.Width(s[:i]) <= budget {
			best = i
		}
	}
	if best < 0 {
		return "", s, false
	}
	return s[:best], s[best+1:], true
}

// cutText cuts the longest rune prefix of s that fits the px budget,
// always returning at least one rune so char-break makes progress.
func cutText(s string, budget int, m Metrics) (head, tail string) {
	if s == "" {
		return "", ""
	}
	_, size := utf8.DecodeRuneInString(s)
	if m.Width(s[:size]) > budget {
		return s[:size], s[size:]
	}
	i := size
	for i < len(s) {
		_, n := utf8.DecodeRuneInString(s[i:])
		if m.Width(s[:i+n]) > budget {
			break
		}
		i += n
	}
	return s[:i], s[i:]
}

// applyAlign offsets each line for the block's text-align (center and
// right pad in px; a line wider than the content box stays flush left).
func applyAlign(lines []LineBox, block *Box, width int) {
	align := ""
	if block.St != nil {
		align = block.St.Align
	}
	for i := range lines {
		switch align {
		case "center":
			if x := (width - lines[i].Width) / 2; x > 0 {
				lines[i].X = x
			}
		case "right":
			if x := width - lines[i].Width; x > 0 {
				lines[i].X = x
			}
		}
	}
}
```

- [ ] **Step 4: Run to confirm they pass**

Run: `cd src && go test -count=1 ./lib/html/`
Expected: PASS (whole package: the new inline tests and the existing box/ua/html suites)

- [ ] **Step 5: Full build + vet + integration check**

```bash
cd src && go build ./lib/html/
cd src && go vet ./lib/html/
gofmt -l src/lib/html/inline.go src/lib/html/metrics.go src/lib/html/inline_test.go
cd src && go test -count=1 -tags "lua mcp" ./mail/ ./lib/...
```
Expected: all green; `gofmt -l` prints nothing; mail is untouched and still passes (the walker still runs).

- [ ] **Step 6: Commit**

```bash
git add src/lib/html/inline.go src/lib/html/inline_test.go
git commit -m "feat(html): greedy inline line fill with normalize and alignment"
```

## Task 6: full-suite gate

**Files:** none changed unless a check fails.

- [ ] **Step 1: Whole tagged suite**

Run: `cd src && go test -count=1 -tags "lua mcp" ./...`
Expected: PASS (the locked mail html_*_test.go, box/ua/html lib suites, and all tagged packages)

- [ ] **Step 2: vet + gofmt**

Run: `cd src && go vet ./lib/html/ && gofmt -l lib/html/`
Expected: no output

- [ ] **Step 3: Confirm zero behavior change to the running walker**

The mail suite passing unchanged IS this check: the walker in mail/html.go and the stage-2 render path were not touched, so user-visible rendering is byte-identical. State it in the task report.

## Self-review notes for the implementer

- Do NOT touch box.go, ua.go, html.go, or anything in src/mail in this plan; the box tree and the walker are locked. If a fixture exposes a box-tree bug, surface it to the coordinator instead of editing box.go.
- Keep regression discipline: box_test.go, ua_test.go, html_test.go and every mail html_*_test.go stay green and unedited.
- `linesText` must use one import block; if the test file already imports reflect, do not import it twice. Keep the helpers at the top of inline_test.go with the package clause once.
- `LayoutInline` is package-internal; stage-2 consumption and export shape is Plan 6. Do not add exported surface beyond LineBox.
- Commit messages must match the plan exactly (Conventional Commits, no AI marker/co-author on code).
