# HTML layout engine: block flow and vertical rhythm (plan 3)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make stage-1 inline layout linear on hostile input (a content-reachable DoS today), then stand up real CSS vertical flow in `lib/html`: px margins resolved from the cascade + UA floor, sibling/parent-child collapse with collapse-through empties, the hr rule box, and list hanging-marker gutters - all strictly additive with zero behavior change to the running mail walker.

**Architecture:** Weasyprint-shaped two-stage renderer per the spec (`docs/superpowers/specs/2026-09-03-html-layout-engine-design.md`). Plan 3 finishes the remaining stage-1 story started by the foundation and inline plans: `inline.go`'s O(n^2) token paths become O(n); `Style` gains resolved-px geometry (margins + list padding-left) parsed from the cascade and filled by a new `ua.go` UA-margin layer; a new `block.go` stacks blocks vertically with weasyprint's margin-collapse model into an ordered px row stream. Nothing in `mail/` is touched and the walker keeps rendering unchanged.

**Tech Stack:** Go, x/net/html, cascadia (existing deps). Test cmd: `cd src && go test -count=1 ./lib/html/`. Full gate: `go test -count=1 -tags "lua mcp" ./...`, `go vet ./lib/html/`, `gofmt -l lib/html/`.

**Spec refs:** Sections "UA floor" (margins list), "block flow and vertical rhythm", "inline layout and whitespace" (metrics). WeasyPrint refs: `layout/block.py` (`collapse_margin`, the adjoining-margin run, `bottom_space`), `formatting_structure/boxes.py` (`top_margin_collapses`/`bottom_margin_collapses`), `css/html5_ua.css` (margins, font ladder, hr).

**Threat model (locked):** a malicious sender can ship a megabyte HTML part. Every loop that scales with input must be O(n); any super-linear path is a content-reachable DoS on the read surface. BUGS.org "html inline layout is O(n^2) on one unbroken token" is that bug. The seam accumulator in Task 3 must therefore run on incremental extrema, never by rescanning a margin list (which would be O(n^2) across many siblings).

---

## File structure

- Modify: `src/lib/html/metrics.go` - add the optional `RuneStepper` incremental interface.
- Modify: `src/lib/html/inline.go` - `atomizeText` collapse mode builds words in a `strings.Builder` (one atom per word, byte-identical output); `cutText` steps rune widths when the meter supports it. `LayoutInline`/`breakAtSpace` untouched.
- Modify: `src/lib/html/html.go` - `Style` gains px margin fields (+ per-side author flags) and `PadLeft`; `StyleOf` resets them (non-inherited); `apply` parses `margin`/longhands. Zero walker drift (the walker never reads geometry).
- Modify: `src/lib/html/ua.go` - `uaMargins(tag, depth, s)` fills UA margin defaults and the `ul`/`ol` padding-left gutter; keeps the shared `uaDefaults` emphasis table untouched.
- Modify: `src/lib/html/box.go` - `buildElement` layers `uaMargins` (like it layers `effectiveWS`).
- Create: `src/lib/html/block.go` - `Row`, `LayoutBlock`, the seam-based vertical flow.
- Test: `src/lib/html/inline_test.go` (pins + DoS regressions), `src/lib/html/geometry_test.go` (cascade + UA margins), `src/lib/html/block_test.go` (flow).

## Decisions locked for this plan

- **Geometry fields are px on `Style` and non-inherited.** `StyleOf` copies the parent then zeroes margins + `PadLeft` (exactly like it zeroes `Display`), then author rules re-apply. UA defaults are layered by the box builder after `StyleOf` (`uaMargins`), filling only sides the author did not set - same layering as `effectiveWS`. The shared `uaDefaults` emphasis table is not touched.
- **Margins are not inherited; anonymous runs and whitespace-only runs carry none.** An anonymous `RoleBlock` (`Tag == ""`) shares its container's `*Style`, so block flow must read geometry only off element blocks (`geom` returns zero for `Tag == ""`). Otherwise a stray run inside a margined div would double its margin.
- **Only `margin` (px/em/auto) is parsed from author CSS.** `padding-left` on `ul`/`ol` is a UA-only 40px gutter; author `padding`, `width`, and other box properties stay out of the cascade (spec: the whitelist plus "the additions this spec names (margin, ...)"; padding is not among them). `auto` resolves to 0 (no block centering). Unsupported lengths (%, inherit, keyword) leave the side unset.
- **Heading margins are UA-resolved px** (base 16px, the only length scale - no font-size property). Weasyprint resolves `margin: .67em` against the heading's own font (`h1` 2em): h1 = .67*2*16 = 21px, h2 = .83*1.5*16 = 20px, h3 = 19px, h4 = 21px, h5 = 22px, h6 = 25px (rounded). The non-heading block floor: p/ul/ol/dl/pre/blockquote/figure 16px top+bottom; nested lists (a `ul`/`ol` under any list) 0; blockquote/figure 40px left+right; dd 40px left only; ul/ol padding-left 40px; hr .5em = 8px. No body margin (the full-bleed terminal renderer has none).
- **hr is an ordinary collapsible block that is never collapse-through.** Measured against real weasyprint (background-painted boxes, 96dpi row scan): `<p>a</p><p>b</p>` yields one 16px gap; `<p>a</p><hr><p>b</p>` yields 16px each side of a 2px rule; between 0-margin blocks the rule yields 8px each side. So hr's margins collapse with neighbors like any sibling's; the rule borders only stop the empty hr box collapsing *through* (so the 2px rule renders instead of vanishing). The block-flow model therefore needs no special seam logic for hr - an hr always emits one content row, and a box with no content rows collapses through. (This corrects the loose "borders stop margin collapse" reading in the spec prose; the measured weasyprint behavior governs.)
- **Stage-1 output is a flat, ordered px row stream** (`[]Row`), because blocks are pure vertical flow (no floats). Each `Row` carries its collapsed `Gap` px above, absolute content `X` (insets + text-align lead folded in) and `W` (content-box width), the owning `Box`, the `Line`, an `HR` flag, and a `Marker` string on a list item's first row. Stage 2 (plan 6) quantizes `Gap` to blank rows, maps `X`/`W` to cells, and draws marker glyphs; it will drop the first row's leading gap to match the walker's no-top-blank behavior (a pinned mail-level test depends on it). Marker glyph geometry is stage-2 (R11 config data); stage 1 conveys only the type and the inset content edge.
- **Seam collapse is incremental.** The vertical run of adjoining margins is kept as `(maxPos, minNeg)` running extrema (`collapse = maxPos + minNeg`, weasyprint block.py:920). Margins append in O(1); a content row consumes the run in O(1). No margin list is ever rescanned, so stacking N siblings stays O(N) even with a hostile megabyte of `<p>`s.
- **Additive only.** `mail/html.go` (the walker) is untouched; the pinned `html_*_test.go` suite must stay green unweakened at the Task 4 gate. Regression tests added here are new; none of the pinned ones are edited.

---

### Task 1: Linear inline layout (the DoS fix) + LayoutInline contract pins

**Files:**
- Modify: `src/lib/html/metrics.go` - add `RuneStepper`.
- Modify: `src/lib/html/inline.go` - `atomizeText` (collapse path, ~90-115), `cutText` (~294-311).
- Test: `src/lib/html/inline_test.go`.

This task is the BUGS.org "#1 html inline layout is O(n^2)" fix plus the four contract pins from BUGS.org "#3" (they characterize the LayoutInline surface so the refactor below - and later block flow - cannot silently change it). The pins pass on the current code (they are characterization, not red-first); the two linearity regressions are the red-first tests.

- [ ] **Step 1: Write the LayoutInline contract pins (green on current code)**

Append to `src/lib/html/inline_test.go`:

```go
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
	for _, r := range s { n += dblW(r) }
	return n
}
// RuneWidth makes dbl step-capable once Metrics grows RuneStepper, so the
// px-not-rune pin drives the incremental cut path too.
func (dbl) RuneWidth(r rune) int { return dblW(r) }
func dblW(r rune) int {
	if r >= 0x2E80 { return 2 }
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
```

- [ ] **Step 2: Run them to confirm they pass on the current code**

Run: `cd src && go test -count=1 -run 'TestAuthorOverwide|TestOverwideCentered|TestNoEmptyLines|TestCharBreakMeasures' ./lib/html/`
Expected: PASS (all four characterize current behavior).

- [ ] **Step 3: Write the failing linearity test for `cutText`**

The current `cutText` re-measures the prefix from the string head on every rune. A counting meter makes that quadratic cost observable without wall-clock timing:

```go
// countMeter counts every rune a Width call scans, so re-measuring
// prefixes on a long string shows up as O(n^2) scanned runes.
type countMeter struct{ scanned int }

func (m *countMeter) Width(s string) int {
	for range s { m.scanned++ }
	return utf8.RuneCountInString(s)
}
func (m *countMeter) RuneWidth(r rune) int { return 1 }

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
```

- [ ] **Step 4: Run it to confirm it FAILS on current code**

Run: `cd src && go test -count=1 -run TestCutTextDoesNotRescanFromHead ./lib/html/`
Expected: FAIL - `cutText rescanned 8002000 runes...` (the prefix re-measure loop is O(n^2) in the run length).

- [ ] **Step 5: Add `RuneStepper` to `Metrics`**

`src/lib/html/metrics.go`, appended below the interface comment:

```go
// RuneStepper is implemented by Metrics that can also advance one rune at
// a time. char-break uses it to carry a running px width instead of
// re-measuring prefixes (quadratic on a giant unbroken token). Width and
// the per-rune widths must agree (a monospace meter satisfies this).
type RuneStepper interface {
	RuneWidth(r rune) int
}
```

- [ ] **Step 6: Rewrite `cutText` to step when the meter allows**

`src/lib/html/inline.go`, replace `cutText` (its doc comment stays):

```go
// cutText cuts the longest rune prefix of s that fits the px budget,
// always returning at least one rune so char-break makes progress. When
// the meter steps rune-by-rune the running width is carried, so cutting
// a giant token is O(n); otherwise prefixes are re-measured (the
// proportional-metrics fallback, correct but not linear).
func cutText(s string, budget int, m Metrics) (head, tail string) {
	if s == "" {
		return "", ""
	}
	if st, ok := m.(RuneStepper); ok {
		w := 0
		i := 0
		for i < len(s) {
			r, n := utf8.DecodeRuneInString(s[i:])
			if w > 0 && w+st.RuneWidth(r) > budget {
				break
			}
			w += st.RuneWidth(r)
			i += n
		}
		return s[:i], s[i:]
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
```

- [ ] **Step 7: Run the cutText test - it now passes**

Run: `cd src && go test -count=1 -run 'TestCutTextDoesNotRescanFromHead|TestCharBreakMeasures' ./lib/html/`
Expected: PASS.

- [ ] **Step 8: Rewrite `atomizeText`'s collapse path to build words in a buffer**

The old collapse path appends one rune at a time to an immutable string (`out[n-1].text += string(r)`), which is O(word^2) byte copies. Replace the `sep`-tracking collapse loop body (`src/lib/html/inline.go` lines ~90-115, from `var out []atom` in the collapse branch to the closing `return out`) with a buffer that flushes one atom per word. Output is byte-identical: words still come out as single atoms separated by single `sep` atoms; a br (pre-line newline) still flushes and separates.

```go
	var out []atom
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			out = append(out, atom{st: st, ws: ws, text: word.String()})
			word.Reset()
		}
	}
	sep := false
	for _, r := range txt {
		if collapsible(r) {
			if b.newlineBreak && r == '\n' { // pre-line: LF is a kept break
				flush()
				out = append(out, atom{st: st, ws: ws, br: true})
				sep = false
			} else {
				sep = true
			}
			continue
		}
		if sep {
			flush() // the prior word atomizes before its break space
			out = append(out, atom{st: st, ws: ws, text: " ", sep: true})
			sep = false
		}
		word.WriteRune(r)
	}
	flush()
	if sep {
		out = append(out, atom{st: st, ws: ws, text: " ", sep: true})
	}
	return out
```

The pre-family (non-collapse) branch above this is untouched. `strings` is already imported in the file.

- [ ] **Step 9: Run the whole inline suite - behavior is unchanged**

Run: `cd src && go test -count=1 ./lib/html/`
Expected: PASS, including the two locked no-space-boundary regressions, the Step-1 pins, and the Step-4 cutText test.

- [ ] **Step 10: Add the end-to-end DoS regression (giant unbroken token)**

The deterministic test above proves `cutText` no longer rescan; this proves the atomize buffer + stepping together keep a hostile single-token mail linear. It is the regression that would hang if either quadratic path returns.

```go
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
```

Add `"time"` to the test file imports. If this test fails in a reintroduction, its leaked goroutine keeps burning CPU until the `go test` process exits - that is the point (a slow, obvious red).

- [ ] **Step 11: Confirm the DoS was real, then confirm the fix**

Run a scratch timing on current HEAD before the fix is applied elsewhere in the file (or `git stash` the atomize change and re-run). Using the same probe at a smaller n keeps it quick:

Run: `cd src && go test -count=1 -run TestGiantUnbrokenTokenLaysOutLinearly -timeout 90s ./lib/html/`
Expected on the OLD atomize path: FAIL after 3s (and the run stalls on the leaked goroutine until timeout). Expected on the NEW path: PASS promptly. If the old path's stall is inconvenient in CI, this step is a one-time developer check, not a committed assertion.

- [ ] **Step 12: Full package gate**

Run: `cd src && go test -count=1 ./lib/html/ && go vet ./lib/html/ && gofmt -l lib/html/`
Expected: PASS, vet clean, gofmt lists nothing.

- [ ] **Step 13: Commit**

```bash
git add src/lib/html/metrics.go src/lib/html/inline.go src/lib/html/inline_test.go
git commit -m "feat(html): linear inline atomizing and char-break measurement"
```

(Code commit: no co-author line.)

---

### Task 2: Cascade + UA geometry (px margins, list gutter)

**Files:**
- Modify: `src/lib/html/html.go` - `Style` struct (~53-66), `apply` (~134-200), `StyleOf` reset (~267-292).
- Modify: `src/lib/html/ua.go` - add `uaMargins`.
- Modify: `src/lib/html/box.go` - `buildElement` layers `uaMargins` (~121-137).
- Test: `src/lib/html/geometry_test.go`.

This is the block-flow prerequisite: geometry on `Style`, resolved to px, filled author-then-UA, non-inherited. It changes nothing the walker reads, so no `mail/` output moves.

- [ ] **Step 1: Write the failing cascade tests**

`src/lib/html/geometry_test.go`:

```go
// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import "testing"

func TestParseMarginLengths(t *testing.T) {
	cases := map[string]int{
		"12px": 12, "0": 0, "0px": 0, "auto": 0,
		"1em": 16, "1.5em": 24, "2em": 32, "0.67em": 11,
	}
	for in, want := range cases {
		got, ok := parseLen(in)
		if !ok || got != want {
			t.Errorf("parseLen(%q) = %d,%v want %d,true", in, got, ok, want)
		}
	}
	for _, in := range []string{"10%", "inherit", "initial", "xx", "1.5px", "-2px"} {
		if _, ok := parseLen(in); ok {
			t.Errorf("parseLen(%q) accepted an unsupported value", in)
		}
	}
}

func TestApplyMarginShorthandSetsSides(t *testing.T) {
	var s Style
	s.apply(ParseDecls("margin: 1px 2px 3px 4px"))
	if s.MarginTop != 1 || s.MarginRight != 2 || s.MarginBottom != 3 || s.MarginLeft != 4 {
		t.Fatalf("4-value margin = %d/%d/%d/%d", s.MarginTop, s.MarginRight, s.MarginBottom, s.MarginLeft)
	}
	for _, set := range []bool{s.MarginTopSet, s.MarginRightSet, s.MarginBottomSet, s.MarginLeftSet} {
		if !set {
			t.Fatal("margin shorthand must mark all four sides set")
		}
	}
	s = Style{}
	s.apply(ParseDecls("margin: 1em 0"))
	if s.MarginTop != 16 || s.MarginBottom != 16 || s.MarginRight != 0 || s.MarginLeft != 0 {
		t.Fatalf("2-value margin = %d/%d/%d/%d", s.MarginTop, s.MarginRight, s.MarginBottom, s.MarginLeft)
	}
}

func TestApplyMarginLonghandOverridesShorthand(t *testing.T) {
	var s Style
	s.apply(ParseDecls("margin: 1px; margin-top: 2em"))
	if s.MarginTop != 32 || s.MarginTopSet != true {
		t.Fatalf("longhand override top = %d", s.MarginTop)
	}
	if s.MarginBottom != 1 {
		t.Fatalf("shorthand bottom leaked = %d", s.MarginBottom)
	}
}

func TestMarginsDoNotInherit(t *testing.T) {
	parent := &Style{MarginTop: 16, MarginTopSet: true, MarginBottom: 16, PadLeft: 40}
	child := StyleOf(el("p"), parent, nil)
	if child.MarginTop != 0 || child.MarginBottom != 0 || child.PadLeft != 0 {
		t.Fatalf("geometry inherited: t=%d b=%d pl=%d", child.MarginTop, child.MarginBottom, child.PadLeft)
	}
	if child.MarginTopSet || child.MarginBottomSet {
		t.Fatal("margin set flags must not inherit")
	}
}
```

`el("p")` is the existing test helper for a bare element node (defined in the box or html tests; if it is not exported across test files, confirm it exists - it is used by the white-space tests). If it is absent, build the node with `el`, else fall back to the `buildBody` doc's first element via a helper.

- [ ] **Step 2: Run to confirm the tests fail**

Run: `cd src && go test -count=1 -run 'TestParseMargin|TestApplyMargin|TestMarginsDoNotInherit' ./lib/html/`
Expected: FAIL - `parseLen`/`MarginTop` etc. do not exist yet.

- [ ] **Step 3: Add the geometry fields to `Style`**

`src/lib/html/html.go` `Style` struct (after the `WSSet` field):

```go
	// Resolved-px box geometry (stage 1). Non-inherited: StyleOf zeroes
	// these each node. *Set marks a side the author declared; the UA floor
	// fills only unset sides. The mail walker never reads these.
	MarginTop, MarginRight, MarginBottom, MarginLeft int
	MarginTopSet, MarginRightSet, MarginBottomSet, MarginLeftSet bool
	PadLeft int // padding-left px (ul/ol gutter; UA-only)
```

- [ ] **Step 4: Add `parseLen` and the margin parser**

`src/lib/html/html.go`, near `mustInt`:

```go
// parseLen folds one CSS length to px: em resolves against the 16px base
// (the only length scale), px passes through, auto and a bare 0 are 0.
// Percentages and other units are not values.
func parseLen(v string) (int, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	switch {
	case v == "0" || v == "auto":
		return 0, true
	case strings.HasSuffix(v, "px"):
		if n, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(v, "px"))); err == nil && n >= 0 {
			return n, true
		}
	case strings.HasSuffix(v, "em") || strings.HasSuffix(v, "rem"):
		f, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(v, "rem"), "em")), 64)
		if err == nil && f >= 0 {
			return int(math.Round(f * 16)), true
		}
	}
	return 0, false
}

// marginSides expands a margin value list to top/right/bottom/left.
func marginSides(v string) ([]string, bool) {
	f := strings.Fields(v)
	switch len(f) {
	case 1:
		return []string{f[0], f[0], f[0], f[0]}, true
	case 2:
		return []string{f[0], f[1], f[0], f[1]}, true
	case 3:
		return []string{f[0], f[1], f[2], f[1]}, true
	case 4:
		return f, true
	}
	return nil, false
}

func setMargin(s *Style, side string, v string) {
	if px, ok := parseLen(v); ok {
		switch side {
		case "top":
			s.MarginTop, s.MarginTopSet = px, true
		case "right":
			s.MarginRight, s.MarginRightSet = px, true
		case "bottom":
			s.MarginBottom, s.MarginBottomSet = px, true
		case "left":
			s.MarginLeft, s.MarginLeftSet = px, true
		}
	}
}
```

In `apply`, after the `white-space` branch, add the margin parse (shorthand first so a longhand in the same block wins, deterministic):

```go
	if v, ok := decls["margin"]; ok {
		if sides, ok := marginSides(v); ok {
			for i, side := range []string{"top", "right", "bottom", "left"} {
				setMargin(s, side, sides[i])
			}
		}
	}
	for _, side := range []string{"top", "right", "bottom", "left"} {
		if v, ok := decls["margin-"+side]; ok {
			setMargin(s, side, v)
		}
	}
```

- [ ] **Step 5: Reset geometry in `StyleOf`**

In `StyleOf`, with the existing resets:

```go
	s.MarginTop, s.MarginRight, s.MarginBottom, s.MarginLeft = 0, 0, 0, 0
	s.MarginTopSet, s.MarginRightSet, s.MarginBottomSet, s.MarginLeftSet = false, false, false, false
	s.PadLeft = 0 // geometry is not inherited
```

- [ ] **Step 6: Run the cascade tests - they pass**

Run: `cd src && go test -count=1 -run 'TestParseMargin|TestApplyMargin|TestMarginsDoNotInherit' ./lib/html/`
Expected: PASS.

- [ ] **Step 7: Write the failing UA-default tests**

Same file:

```go
func TestUAMarginsFillUnsetSides(t *testing.T) {
	cases := []struct {
		tag     string
		depth   int
		t, r, b, l int
		pl      int
	}{
		{"p", 0, 16, 0, 16, 0, 0},
		{"ul", 0, 16, 0, 16, 0, 40},
		{"ul", 1, 0, 0, 0, 0, 40}, // nested list drops its vertical margins
		{"ol", 0, 16, 0, 16, 0, 40},
		{"li", 0, 0, 0, 0, 0, 0},
		{"blockquote", 0, 16, 40, 16, 40, 0},
		{"dd", 0, 16, 0, 16, 40, 0},
		{"hr", 0, 8, 0, 8, 0, 0},
		{"h1", 0, 21, 0, 21, 0, 0},
		{"h4", 0, 21, 0, 21, 0, 0},
		{"h6", 0, 25, 0, 25, 0, 0},
		{"span", 0, 0, 0, 0, 0, 0},
	}
	for _, tc := range cases {
		var s Style
		uaMargins(tc.tag, tc.depth, &s)
		if s.MarginTop != tc.t || s.MarginRight != tc.r || s.MarginBottom != tc.b || s.MarginLeft != tc.l || s.PadLeft != tc.pl {
			t.Errorf("uaMargins(%s,%d) = %d/%d/%d/%d pl%d, want %d/%d/%d/%d pl%d",
				tc.tag, tc.depth, s.MarginTop, s.MarginRight, s.MarginBottom, s.MarginLeft, s.PadLeft,
				tc.t, tc.r, tc.b, tc.l, tc.pl)
		}
	}
}

func TestUAMarginsDoNotOverrideAuthor(t *testing.T) {
	var s Style
	s.apply(ParseDecls("margin-bottom: 3px"))
	uaMargins("p", 0, &s)
	if s.MarginBottom != 3 || s.MarginTop != 16 {
		t.Fatalf("UA clobbered author: b=%d t=%d", s.MarginBottom, s.MarginTop)
	}
}
```

- [ ] **Step 8: Run to confirm they fail**

Run: `cd src && go test -count=1 -run 'TestUAMargins' ./lib/html/`
Expected: FAIL - `uaMargins` undefined.

- [ ] **Step 9: Implement `uaMargins` in `ua.go`**

```go
// uaMargins fills the UA margin defaults for a tag where the author did
// not set the side, and the ul/ol padding-left gutter. depth is the list
// nesting the box sits under (0 = no list ancestor): nested lists drop
// their vertical margins. Layered by the box builder after StyleOf, so
// the running mail walker never sees these. Heading margins are the UA
// em values folded to px at the base-16 ladder (html5_ua.css fonts:
// h1 2em .. h6 .67em), since stage 1 has no font-size property.
func uaMargins(tag string, depth int, s *Style) {
	t, b := 0, 0
	switch tag {
	case "h1":
		t, b = 21, 21
	case "h2":
		t, b = 20, 20
	case "h3":
		t, b = 19, 19
	case "h4":
		t, b = 21, 21
	case "h5":
		t, b = 22, 22
	case "h6":
		t, b = 25, 25
	case "p", "dl", "pre", "blockquote", "figure", "dd":
		t, b = 16, 16
	case "ul", "ol":
		if depth == 0 {
			t, b = 16, 16
		}
		if tag == "ul" || tag == "ol" {
			s.PadLeft = 40 // the hanging-marker gutter
		}
	case "hr":
		t, b = 8, 8
	}
	if !s.MarginTopSet {
		s.MarginTop = t
	}
	if !s.MarginBottomSet {
		s.MarginBottom = b
	}
	l, r := 0, 0
	switch tag {
	case "blockquote", "figure":
		l, r = 40, 40
	case "dd":
		l = 40
	}
	if !s.MarginLeftSet {
		s.MarginLeft = l
	}
	if !s.MarginRightSet {
		s.MarginRight = r
	}
}
```

- [ ] **Step 10: Layer `uaMargins` in the box builder**

`src/lib/html/box.go` `buildElement`, next to the white-space promotion:

```go
	st.WS = effectiveWS(tag, st)
	uaMargins(tag, listDepth, st)
```

(`listDepth` is the `buildElement` parameter; the nested-list rule keys off the list nesting the box is under.)

- [ ] **Step 11: Run the UA tests and a no-drift check**

Run: `cd src && go test -count=1 ./lib/html/`
Expected: PASS (box tests still pass - the builder change only writes fields nothing reads yet).

Run: `cd src && go test -count=1 ./...` (default build, no tags)
Expected: PASS - the mail/html walker tests stay green (no drift from Style changes; the walker reads no geometry).

- [ ] **Step 12: Commit**

```bash
git add src/lib/html/html.go src/lib/html/ua.go src/lib/html/box.go src/lib/html/geometry_test.go
git commit -m "feat(html): cascade px margins and UA margin defaults"
```

---

### Task 3: Block flow - vertical stacking, collapse, hr, list gutter

**Files:**
- Create: `src/lib/html/block.go`
- Test: `src/lib/html/block_test.go`

The vertical engine. Input is `Build`'s box stream (uniformly block-level children after `splitRuns`; a top level with no block child is one implicit text run). Output is the ordered px `Row` stream. The seam threads through the whole tree so siblings AND parent/child margins collapse in one run - mirroring weasyprint's `adjoining_margins` recursion. Because no modeled border or padding-top interrupts a block's content edge (only hr renders content), a content row is the only thing that consumes a seam; a box that emits no content row collapses through. Every operation is O(1) per margin and O(1) per emitted line.

- [ ] **Step 1: Write the failing block-flow tests**

`src/lib/html/block_test.go`:

```go
// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import (
	"reflect"
	"testing"
)

// rowsText renders the non-hr rows' text for assertions.
func rowsText(rs []Row) []string {
	var out []string
	for _, r := range rs {
		if r.HR {
			continue
		}
		var b strings.Builder
		for _, a := range r.Line.Atoms {
			b.WriteString(a.text)
		}
		out = append(out, b.String())
	}
	return out
}

// gap asserts the px gap above each row (a faithful stage-1 value; stage 2
// quantizes and drops the leading one).
func gaps(rs []Row) []int {
	out := make([]int, len(rs))
	for i, r := range rs {
		out[i] = r.Gap
	}
	return out
}

func TestBlockSiblingParagraphsCollapse(t *testing.T) {
	bs := buildBody(`<p>one</p><p>two</p>`)
	rs := LayoutBlock(bs, 200, mono(1), false)
	if got := rowsText(rs); !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Fatalf("rows = %q", got)
	}
	// each p is 16px top+bottom; the shared boundary collapses to one 16px
	if got := gaps(rs); !reflect.DeepEqual(got, []int{16, 16}) {
		t.Fatalf("gaps = %v, want [16 16]", got)
	}
}

func TestBlockLargerNeighborMarginWins(t *testing.T) {
	bs := buildBody(`<p style="margin:20px 0 30px">one</p><p style="margin:10px 0">two</p>`)
	rs := LayoutBlock(bs, 200, mono(1), false)
	// boundary: p1 mb 30 vs p2 mt 10 -> max 30; row 1 is p1's own mt 20
	if got := gaps(rs); !reflect.DeepEqual(got, []int{20, 30}) {
		t.Fatalf("gaps = %v, want [20 30]", got)
	}
}

func TestBlockEmptyBoxCollapsesThrough(t *testing.T) {
	bs := buildBody(`<p>one</p><p></p><p>two</p>`)
	rs := LayoutBlock(bs, 200, mono(1), false)
	if got := rowsText(rs); !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Fatalf("rows = %q (empty p must leave no row)", got)
	}
	if got := gaps(rs); !reflect.DeepEqual(got, []int{16, 16}) {
		t.Fatalf("gaps = %v, want [16 16] (empty p collapses through)", got)
	}
}

func TestBlockHRuleKeepsRuleRow(t *testing.T) {
	bs := buildBody(`<p>one</p><hr><p>two</p>`)
	rs := LayoutBlock(bs, 200, mono(1), false)
	if len(rs) != 3 || !rs[1].HR {
		t.Fatalf("want one, HR, two; got %d rows", len(rs))
	}
	// hr's 8px margins collapse with the 16px paragraph margins: 16 either
	// side of the 2px rule (measured against weasyprint)
	if got := gaps(rs); !reflect.DeepEqual(got, []int{16, 16, 16}) {
		t.Fatalf("gaps = %v, want [16 16 16]", got)
	}
}

func TestBlockHRuleTightNeighborsKeepHalfEm(t *testing.T) {
	// divs carry no UA margins: hr keeps its own 8px each side
	bs := buildBody(`<div>one</div><hr><div>two</div>`)
	rs := LayoutBlock(bs, 200, mono(1), false)
	if got := gaps(rs); !reflect.DeepEqual(got, []int{0, 8, 8}) {
		t.Fatalf("gaps = %v, want [0 8 8]", got)
	}
}

func TestBlockListContentInGutterWithMarker(t *testing.T) {
	bs := buildBody(`<ul><li>one</li><li>two</li></ul>`)
	rs := LayoutBlock(bs, 200, mono(1), false)
	if got := rowsText(rs); !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Fatalf("rows = %q", got)
	}
	// list mt 16 above the first item; items are contiguous; content sits at
	// the ul's 40px padding-left; each item's first row carries its marker
	if got := gaps(rs); !reflect.DeepEqual(got, []int{16, 0}) {
		t.Fatalf("gaps = %v, want [16 0]", got)
	}
	for i, x := range []int{40, 40} {
		if rs[i].X != x {
			t.Fatalf("row %d X = %d, want %d", i, rs[i].X, x)
		}
	}
	for i, want := range []string{"disc", "disc"} {
		if rs[i].Marker != want {
			t.Fatalf("row %d marker = %q, want %q", i, rs[i].Marker, want)
		}
	}
}

func TestBlockNestedListIndentsAndMarks(t *testing.T) {
	bs := buildBody(`<ul><li>outer<ul><li>inner</li></ul></li></ul>`)
	rs := LayoutBlock(bs, 200, mono(1), false)
	if got := rowsText(rs); !reflect.DeepEqual(got, []string{"outer", "inner"}) {
		t.Fatalf("rows = %q", got)
	}
	// nested list has no vertical margins: inner is contiguous under outer
	if got := gaps(rs); !reflect.DeepEqual(got, []int{16, 0}) {
		t.Fatalf("gaps = %v, want [16 0]", got)
	}
	if rs[0].X != 40 || rs[1].X != 80 {
		t.Fatalf("content X = %d/%d, want 40/80", rs[0].X, rs[1].X)
	}
	if rs[0].Marker != "disc" || rs[1].Marker != "circle" {
		t.Fatalf("markers = %q/%q, want disc/circle", rs[0].Marker, rs[1].Marker)
	}
}

func TestBlockBlockquoteInsetsContent(t *testing.T) {
	bs := buildBody(`<p>one</p><blockquote>two</blockquote><p>three</p>`)
	rs := LayoutBlock(bs, 200, mono(1), false)
	if got := rowsText(rs); !reflect.DeepEqual(got, []string{"one", "two", "three"}) {
		t.Fatalf("rows = %q", got)
	}
	// blockquote: 16px margins + 40px each side; its line wraps at 200-80
	if rs[1].X != 40 || rs[1].W != 120 {
		t.Fatalf("blockquote X/W = %d/%d, want 40/120", rs[1].X, rs[1].W)
	}
}
```

(These expected values are derived from the measured weasyprint probes and the UA floor table in Task 2. If a re-probe against real weasyprint disagrees on any of them, the probe wins - rerun the measurement and fix the expectation.)

- [ ] **Step 2: Run to confirm they fail**

Run: `cd src && go test -count=1 -run 'TestBlock' ./lib/html/`
Expected: FAIL - `block.go` has no `Row`/`LayoutBlock` yet.

- [ ] **Step 3: Implement `block.go`**

```go
// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

// Row is one emitted content row of block flow: a filled text line or the
// 2px hr rule, positioned in px. Rows are pure vertical flow (no floats),
// so a flat ordered stream is lossless for stage 2.
type Row struct {
	Gap    int      // collapsed px of margin above this row's content edge
	X      int      // absolute px left edge of the content box
	W      int      // content-box px width (wrap/align budget)
	Box    *Box     // the block that owns the row (style/theme)
	Line   LineBox  // filled content line (unused when HR)
	HR     bool     // this row is the 2px hr rule
	Marker string   // list marker type; hangs in the gutter before X (a list item's first row)
}

// seam is the run of mutually-adjoining vertical margins since the last
// content edge, kept as running extrema: collapse(list) = max(pos) +
// min(neg) (weasyprint block.py collapse_margin). Appending a margin and
// consuming the seam are both O(1) - a margin list is never rescanned, so
// stacking N siblings stays O(N) even on hostile input.
type seam struct {
	maxPos int
	minNeg int
}

func (s *seam) add(m int) {
	if m > s.maxPos {
		s.maxPos = m
	}
	if m < s.minNeg {
		s.minNeg = m
	}
}

func (s *seam) take() int {
	g := s.maxPos + s.minNeg
	*s = seam{}
	return g
}

// geom is a block box's resolved geometry in px. Anonymous runs (Tag "")
// carry their container's shared style pointer and must read as zero: an
// anonymous box has no margins of its own.
func geom(b *Box) (mt, mr, mb, ml, pl int) {
	if b.Tag == "" || b.St == nil {
		return
	}
	return b.St.MarginTop, b.St.MarginRight, b.St.MarginBottom, b.St.MarginLeft, b.St.PadLeft
}

// LayoutBlock lays out the document's top-level flow boxes into an ordered
// px row stream at the given content width. Top-level content with no block
// child (a pure-inline body) lays out as one implicit run.
func LayoutBlock(bs []*Box, width int, m Metrics, norm bool) []Row {
	if !hasBlockChild(bs) {
		bs = []*Box{{Role: RoleBlock, Children: bs}}
	}
	var s seam
	return flow(bs, 0, width, &s, m, norm)
}

// flow stacks cs in their container's content box at (x0, w), threading one
// seam across the whole tree: a sibling's margin, its parent's margin, and a
// collapse-through descendant all land in the same run because no modeled
// border or padding interrupts a block's content edge. A box that emits no
// content row collapses through (its margins stay in the run).
func flow(cs []*Box, x0, w int, s *seam, m Metrics, norm bool) []Row {
	var rows []Row
	for _, c := range cs {
		mt, mr, mb, ml, pl := geom(c)
		s.add(mt)
		cx := x0 + ml + pl
		if cw := w - ml - mr - pl; cw < 0 {
			cw = 0
		}
		first := len(rows)
		switch {
		case c.Tag == "hr":
			rows = append(rows, Row{Gap: s.take(), X: cx, W: cw, Box: c, HR: true})
		case hasBlockChild(c.Children):
			rows = append(rows, flow(c.Children, cx, cw, s, m, norm)...)
		default:
			for i, line := range LayoutInline(c, cw, m, norm) {
				gap := 0
				if i == 0 {
					gap = s.take() // only the first line consumes the seam
				}
				rows = append(rows, Row{Gap: gap, X: cx + line.X, W: cw, Box: c, Line: line})
			}
		}
		if c.Marker != "" && len(rows) > first {
			rows[first].Marker = c.Marker // hangs on the item's first content row
		}
		s.add(mb)
	}
	return rows
}
```

`strings` is not needed here; `box.go`'s `hasBlockChild`/`RoleBlock` are in-package.

- [ ] **Step 4: Run the block tests**

Run: `cd src && go test -count=1 -run 'TestBlock' ./lib/html/`
Expected: PASS. If `TestBlockHRule*` or the list X expectations fail, re-probe weasyprint (appendix) before changing expectations - do not tune the test to the code.

- [ ] **Step 5: Verify a wide flat body stays linear (many siblings)**

Run: `cd src && go test -count=1 -run TestBlockSiblingParagraphsCollapse ./lib/html/` then a scratch run over a fabricated 50k-paragraph body to confirm it completes promptly (the seam never rescans). Expected: completes in well under a second.

- [ ] **Step 6: Full package gate**

Run: `cd src && go test -count=1 ./lib/html/ && go vet ./lib/html/ && gofmt -l lib/html/`
Expected: PASS, vet clean, gofmt lists nothing.

- [ ] **Step 7: Commit**

```bash
git add src/lib/html/block.go src/lib/html/block_test.go
git commit -m "feat(html): block flow with margin collapse and list gutters"
```

---

### Task 4: Full suite gate (no drift)

**Files:** none.

- [ ] **Step 1: Full tagged suite**

Run: `cd src && go test -count=1 -tags "lua mcp" ./...`
Expected: PASS - including the pinned mail `html_*_test.go` tests (punctuation hugging, dark adaptation, display non-inheritance, alignment clears, and the rest). They run the walker, which never reads geometry, and the shared cascade only gained inert fields.

- [ ] **Step 2: vet + gofmt**

Run: `cd src && go vet ./lib/html/ && gofmt -l lib/html/`
Expected: vet clean, gofmt lists nothing.

- [ ] **Step 3: Update BUGS.org**

Close the two entries this plan resolves (repo-root `BUGS.org`):
- "html inline layout is O(n^2) on one unbroken token (content-reachable hang)" - CLOSED, referencing Task 1's linearity tests.
- "html inline: pin Plan-2 contract gaps at the LayoutInline surface" - CLOSED, referencing the four Task 1 pins.
Match the existing CLOSED entries' prose style (what was wrong, the fix, the regression that pins it).

- [ ] **Step 4: Commit**

```bash
git add BUGS.org
git commit -m "docs: close html inline O(n^2) and contract-pin bugs"
```

(Doc commit: `Co-Authored-By: Deepseek` trailer.)

---

## Self-review notes

- **Spec coverage:** UA floor margins -> Task 2; collapse/block flow -> Task 3; hr rule -> Task 3; list hanging marker + 40px gutter -> Task 3; whitespace/metrics already shipped in plans 1-2; tables/images/normalize-to-stage-2 remain for plans 4-6. The DoS framing makes the perf work a first-class task, not a footnote.
- **Placeholders:** none; every task carries verbatim code and expected commands.
- **Consistency:** `Row`, `seam`, `geom`, `LayoutBlock`, `uaMargins`, `parseLen`, `RuneStepper`, `countMeter`, `dbl` are defined exactly where each task references them. `hasBlockChild`, `buildBody`, `el`, `mono`, `linesText` are existing in-package helpers.

## Appendix: weasyprint parity probes (developer cross-check, not CI)

The Task 3 expectations were measured on real weasyprint with background-painted content boxes, because glyph banding conflates font leading with margins. To re-probe any gap value:

```html
<!doctype html><html><head><style>@page{size:200px 300px;margin:0}
p{background:#222}hr{background:#eee}</style></head><body style="margin:0">
<p>one</p><hr><p>two</p></body></html>
```

Render at 96dpi (`weasyprint in.html out.pdf && pdftoppm -png -r 96 out.pdf out`) so 1 CSS px = 1 image px, then scan for the dark bands' y ranges; the all-white gap between content boxes is the collapsed margin (and the hr band is its 2px rule). Measured: p/p = 16; p/hr/p = 16 + rule + 16; div/hr/div = 8 + rule + 8. `list-style:none` moves `li` content to exactly the list's padding-left (40), confirming the gutter model.
