# HTML layout engine: images and replaced elements (plan 5)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `lib/html` real image geometry: the cascade accepts `width`/`height`/`max-width` (px and %), a caller-driven pre-pass decodes each image's intrinsic size, a pure sizing function resolves used px width/height against the available width exactly as weasyprint does (probe-measured), inline layout emits images at that width, and table cells seat an image's column. Purely additive: `<img>` stays an inline-level atomic leaf (the pinned box/inline tests keep their shape), images without a loader or bytes lay out at zero width exactly as today, and the running mail walker is untouched.

**Architecture:** Weasyprint-shaped two-stage renderer per the spec (`docs/superpowers/specs/2026-09-03-html-layout-engine-design.md`). Plan 5 is the "Stage 1: replaced elements" section. Images remain `RoleImg` leaves (x/net/html parses `img` as a void element and keeps `src`/`width`/`height`/`alt` on the Node; the box builder keeps the leaf and allocates a small geometry struct). A new `img.go` holds the sizing core: `ResolveImages` (one O(n) walk that asks a caller-supplied loader for each `img`'s pixel dimensions - image bytes never enter this package), `specImg` (effective specified width/height after the cascade and the `width`/`height` attribute hints), `usedImg` (the weasyprint-faithful replaced-element resolution at a concrete available width, memoized on the box), and `imgExtentW` (the width-independent content contribution used by run extents so table columns and inline runs seat an image before the layout width is known). `LayoutInline` resolves each image to its used px at the line width and emits the atom at that width; a lone image already emits its own row (an anonymous run around a sole inline image), which is the common mail shape. Nothing in `mail/` is touched.

**Tech Stack:** Go, x/net/html, cascadia (existing deps). Intrinsic decode is the CALLER's (`image.DecodeConfig` with the caller's registered codecs); this package imports no image codec. Test cmd: `cd src && go test -count=1 ./lib/html/`. Full gate: `go test -count=1 -tags "lua mcp" ./...`, `go vet ./lib/html/`, `gofmt -l lib/html/`.

**Spec refs:** CSS 2.1 section 10.4 "Replaced elements with intrinsic dimensions" (the constraint table this plan implements, probe-checked), CSS 2.1 10.4 `max-width` (the clamp). WeasyPrint refs: `layout/replaced.py` (`replacedbox_width`, `replacedbox_height`), `css/validation` width/height handling (a percentage height against an auto containing-block height is auto). Measured ground truth in the Probe appendix below (every expectation in Tasks 2 and 4 traces to a lettered probe).

**Threat model (locked):** a malicious sender can ship a megabyte HTML part. Every loop that scales with input must be O(n); any super-linear path is a content-reachable DoS on the read surface. Images add no super-linear path: `ResolveImages` walks each box exactly once (`#imgs <= #nodes`); `usedImg`/`imgExtentW`/`specImg` are O(1) integer arithmetic per image; image count is bounded by the node count (an attacker cannot mint images without nodes); and extent measurement stays one pass per cell exactly as in plan 4 (an image's content width is width-independent - px or intrinsic or, for a %, its intrinsic natural width - so the plan-4 table-extent memo remains valid, and no cell is re-measured per column). Image DECODE is not this package's cost: the loader is the caller's (mail), bounded by the message part size on the existing read path - the trust boundary stays in `mail/`, which already decodes for its own image rendering. An image with no loadable bytes contributes zero width and still lays out (a zero-width row today; alt text is a stage-2 concern).

**Conformance rule (locked 2026-09-03, applies verbatim):** things x/net/html already handles are never reimplemented in `lib/html`. x/net/html parses `img` (a void element: no children, attributes kept on the Node) and the cascade/cascadia match selectors - neither reads computed styles or image bytes. Role derivation (the `RoleImg` leaf), cascade `width`/`height`/`max-width`, replaced-element sizing, and column seating are `lib/html`'s legitimate derived work. This plan adds no parser, no selector engine, and no image decoder: the loader returns pixel dimensions, never bytes into this package. Tests pin consume-side shapes from real `buildBody` parses, and pure-function sizing tests drive `usedImg`/`specImg` on boxes a real parse produces (`buildElement` output), never hand-assembled DOM a parse could not yield.

**Privacy/trust boundary (locked):** this package never holds image bytes, mail or otherwise. `ResolveImages` takes a `load func(src) (w, h int, ok bool)`; the caller resolves `src` (`cid:`, `data:`, http(s), the mail store) and decodes. `lib/html` tests supply fabricated PNG dimensions through a fake loader, never real mail content.

---

## File structure

- Modify: `src/lib/html/html.go` - `Style.Width/Height/MaxWidth` (new `CSSLen` type); `apply` parses the three properties; `StyleOf` zeroes them per node (non-inherited geometry, same as margins); `parseSizeLen` next to `parseLen`.
- Modify: `src/lib/html/box.go` - `Box.res *imgRes` field; `buildElement` allocates it for `RoleImg` boxes (an `img` keeps its leaf shape and Node).
- Create: `src/lib/html/img.go` - `imgRes`, `specImg`, `imgExtentW`, `usedImg`, `ResolveImages`.
- Modify: `src/lib/html/inline.go` - `atom.width` returns the image's used px when resolved (else its extent width); `LayoutInline` resolves each image at the block's content width before emitting; `runExtents` uses `imgExtentW` for images (never a stale used width).
- Test: `src/lib/html/img_test.go` (new; cascade, sizing table A-N, ResolveImages, row emission, table seating).

## Decisions locked for this plan

- **Images stay inline-level atomic leaves.** `TestBuildRolesAndSkips` (box_test.go) pins an `<img>` as the third child of a 3-child anonymous run under a 2-box top level, and `TestFlattenKeepsBRAndImg` (inline_test.go) pins an img *atom* sequenced between `x` and `y`. RoleImg is not reclassified to block and the box model is unchanged. What plan 4's summary called "block-atomic rows" is an EMISSION property, not a role: a lone or block-isolated image already lands on its own Row (splitRuns wraps a sole inline image in an anonymous run; one atom = one line = one Row). An image sharing a line with text stays on that line in stage 1 (weasyprint-faithful); forcing every image onto its own terminal block is a stage-2 (plan 6) rendering decision, recorded as a divergence.
- **The width/height attributes are a NOTMUTT extension below the cascade.** Weasyprint IGNORES `width`/`height` attributes (probes B, C: an intrinsic 60x30 stays 60x30 under `width="120" height="40"`). notmutt reads them because the pinned mail contract sizes declared-size images from attributes (`TestImageDeclaredSizes`: `width=600 height=400` must produce a 600x400 display), and the two are the same px scale. So the effective specified width is: CSS `width` when declared (px or %; `auto`/`0` is no value), else the `width` attribute; the same for `height`. Probe D proves the precedence direction: CSS `width:90px` with `width="120"` present yields 90x45 (CSS wins, ratio scales). This extension is a documented divergence from weasyprint, not a faithful feature.
- **Percentage height is auto.** Weasyprint resolves a percentage height against the containing block's height, which is auto here (probes M, N: `height:50%` alone leaves an intrinsic 200x100; `width:120px;height:50%` gives 120x60 - the width honored, the height from the ratio). `specImg` never emits a pct height; a `height:50%` image behaves as `height:auto`.
- **Sizing is the CSS 2.1 10.4 constraint table, probe-checked.** Both axes specified: both honored, ratio dropped (H: 120x40). One axis specified: the other scales by the intrinsic ratio (D: 90x45, I: h120 -> w240); with no intrinsic ratio the auto axis takes the intrinsic value when it has one, else 0. Neither specified: intrinsic (A, M). `max-width` clamps the resolved width and rescales the height by the ratio (E: mw50 on 60x30 -> 50x25; F: `max-width:100%` on intrinsic 200x100 in a 100px box -> 100x50). A percentage width resolves against the available width (G: `width:50%` in a 200px box -> 100x50). Rounding is `math.Round` (half away from zero), which matches the measured fractional probes (J: w250 on a 1.5 ratio -> h167; K: w100 -> h67).
- **Extent width is width-independent; a % width seats the intrinsic natural width.** run extents measure at infinite available width, so a px or intrinsic image contributes its px/intrinsic width as both min and max (it is atomic - the widest unbreakable piece is the whole). A % width cannot resolve at infinite width and never forces a column wider than the image really is, so it contributes the intrinsic width when one exists and 0 otherwise (stage-1 clamp; a lone `width:100%` image still seats its intrinsic, then lays out at 100% of that - the common banner case). The value is stored on the box, never recomputed per column.
- **The used size is memoized on the box at layout.** `usedImg` writes `uW/uH/uSet` onto the image's `imgRes`; `LayoutInline` resolves before emitting, so the row's atoms carry the exact px plan 6 needs. The tree is laid out once per pass (the plan-4 memo contract), so a % image resolved at one width is never re-laid at another within a pass. `runExtents` deliberately bypasses the memo (uses `imgExtentW`) so an extent measurement can never read a stale layout width even if a future pass reorders measure/layout.
- **Image px height does not advance the vertical stream.** Rows are terminal-height strips with no line-box height (the stage-1 model since plan 1), so an image's `uH` is carried on the box for stage 2 and does not push subsequent rows down (a weasyprint divergence - an inline image there grows its line box). Recorded in BUGS.org. Alt text, broken-image placeholders, and DispW/DispH cell mapping are stage-2 (plan 6) concerns.
- **Additive only.** `mail/html.go` (the walker) is untouched; the pinned `html_*_test.go` suite must stay green unweakened at the Task 5 gate. Existing `<img>`-shaped tests keep passing because an image with no loader has no intrinsic and no CSS width, so it lays out at zero width exactly as today; `buildElement` allocating `res` changes no observable behavior without `ResolveImages`.

---

## Probe appendix (measured 2026-09-04, weasyprint + pdftoppm at 96dpi, integer-perfect magenta band scans)

Intrinsics: `m60x30.png` 60x30 (ratio 2), `m40x20.png` 40x20 (ratio 2), `m200x100.png` 200x100 (ratio 2), `m300x200.png` 300x200 (ratio 1.5).

| probe | CSS | attrs | intrinsic | avail | measured |
|---|---|---|---|---|---|
| A | none | none | 60x30 | - | 60x30 (intrinsic) |
| B | none | width=120 | 60x30 | - | 60x30 (**attr ignored**) |
| C | none | width=120 height=40 | 60x30 | - | 60x30 (**attrs ignored**) |
| D | width:90px | width=120 | 60x30 | - | 90x45 (CSS beats attr, ratio) |
| E | max-width:50px | none | 60x30 | - | 50x25 |
| F | max-width:100% | none | 200x100 | 100 | 100x50 |
| G | width:50% | none | 200x100 | 200 | 100x50 |
| H | width:120px height:40px | none | 60x30 | - | 120x40 (both, ratio dropped) |
| I | height:120px | none | 60x30 | - | 240x120 (width from ratio) |
| J | width:250px | none | 300x200 | - | 250x167 (166.67 rounds up) |
| K | width:100px | none | 300x200 | - | 100x67 (66.67 rounds up) |
| M | height:50% | none | 200x100 | - | 200x100 (% height = auto) |
| N | width:120px height:50% | none | 200x100 | - | 120x60 (height from ratio) |

Weasyprint-faithful (this plan reproduces exactly): A, D, E, F, G, H, I, J, K, M, N. Divergence (NOTMUTT extension, the pinned declared-size contract): B, C - the attribute sizes the image.

---

### Task 1: Cascade width/height/max-width (px and %)

**Files:**
- Modify: `src/lib/html/html.go` - `CSSLen` type, `Style` fields, `parseSizeLen`, `apply` branches, `StyleOf` zeroing.
- Test: `src/lib/html/img_test.go` (new file with the helpers the later tasks share).

The cascade gains three non-inherited geometry properties. They are parsed to a `CSSLen` (px or %); width/max-width percentages resolve against the containing width at layout (Task 2); a percentage height is stored but treated as auto at use. No behavior changes until Task 2 reads them - this task only makes the fields parse and not leak by inheritance.

- [ ] **Step 1: Write the failing cascade test**

Create `src/lib/html/img_test.go`:

```go
// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import "testing"

func TestCascadeImageSizingLengths(t *testing.T) {
	// px, %, and max-width all land on the style
	bs := buildBody(`<img style="width:120px;height:50%;max-width:300px" src="x">`)
	st := bs[0].St
	if st.Width != (CSSLen{Px: 120}) {
		t.Fatalf("width = %+v, want 120px", st.Width)
	}
	if st.Height != (CSSLen{Px: 50, Pct: true}) {
		t.Fatalf("height = %+v, want 50%%", st.Height)
	}
	if st.MaxWidth != (CSSLen{Px: 300}) {
		t.Fatalf("max-width = %+v, want 300px", st.MaxWidth)
	}
}

func TestImageSizingLengthsDoNotInherit(t *testing.T) {
	bs := buildBody(`<p style="width:200px">x<b style="max-width:100px">y</b></p>`)
	if bs[0].St.Width != (CSSLen{Px: 200}) {
		t.Fatalf("p width = %+v, want 200px", bs[0].St.Width)
	}
	// the b box is inline: find it under the p and check geometry zeroed
	var b *Box
	for _, c := range bs[0].Children {
		if c.Role == RoleInline {
			b = c
		}
	}
	if b == nil {
		t.Fatal("no inline b box under p")
	}
	if b.St.Width != (CSSLen{}) || b.St.Height != (CSSLen{}) {
		t.Fatalf("b width/height = %+v/%+v, want zeroed (not inherited)",
			b.St.Width, b.St.Height)
	}
	if b.St.MaxWidth != (CSSLen{Px: 100}) {
		t.Fatalf("b max-width = %+v, want 100px (its own declaration)", b.St.MaxWidth)
	}
}
```

(`buildBody` is the box_test.go helper, same package; `CSSLen` holds two ints and a bool, so struct `==` works for the assertions.)

- [ ] **Step 2: Run it to confirm it fails**

Run: `cd src && go test -count=1 -run TestCascadeImageSizingLengths ./lib/html/`
Expected: FAIL - undefined: CSSLen / `st.Width` does not exist.

- [ ] **Step 3: Add `CSSLen` and the `Style` fields**

`src/lib/html/html.go`, next to the `Style` struct's geometry comment (after `PadLeft` at line 73):

```go
// CSSLen is one length value for image sizing. Px is the number; when Pct
// is false it is CSS px, when true it is a percentage of the containing
// block's width. The zero value (Px 0, Pct false) means auto/none.
type CSSLen struct {
	Px  int
	Pct bool
}
```

In the `Style` struct, extend the geometry block:

```go
	PadLeft                                                      int // padding-left px (ul/ol gutter; UA-only)

	// Image sizing (replaced elements): width/height/max-width, px or %.
	// Non-inherited, zeroed per node like the margins above. Height is
	// px-only in effect - a percentage height is auto (see specImg).
	Width, Height, MaxWidth CSSLen
```

- [ ] **Step 4: Add `parseSizeLen`**

`src/lib/html/html.go`, after `parseLen` (line 141):

```go
// parseSizeLen folds one image-sizing length: px passes through, a
// percentage keeps Pct true (it resolves against the containing width at
// layout). auto, 0, em, and other units are not values (rejected, so the
// property stays unset, which means auto).
func parseSizeLen(v string) (CSSLen, bool) {
	v = strings.ToLower(strings.TrimSpace(v))
	if strings.HasSuffix(v, "px") {
		if n, err := strconv.Atoi(strings.TrimSuffix(v, "px")); err == nil && n > 0 {
			return CSSLen{Px: n}, true
		}
	}
	if strings.HasSuffix(v, "%") {
		if n, err := strconv.Atoi(strings.TrimSuffix(v, "%")); err == nil && n >= 0 {
			return CSSLen{Px: n, Pct: true}, true
		}
	}
	return CSSLen{}, false
}
```

- [ ] **Step 5: Wire `apply` and `StyleOf`**

`src/lib/html/html.go`, in `apply` after the margin longhand loop (after line 272):

```go
	if v, ok := decls["width"]; ok {
		if l, ok := parseSizeLen(v); ok {
			s.Width = l
		}
	}
	if v, ok := decls["height"]; ok {
		if l, ok := parseSizeLen(v); ok {
			s.Height = l
		}
	}
	if v, ok := decls["max-width"]; ok {
		if l, ok := parseSizeLen(v); ok {
			s.MaxWidth = l
		}
	}
```

In `StyleOf`, after `s.PadLeft = 0` (line 349), zero the new geometry with the rest (non-inherited):

```go
	s.PadLeft = 0 // geometry is not inherited
	s.Width, s.Height, s.MaxWidth = CSSLen{}, CSSLen{}, CSSLen{}
```

- [ ] **Step 6: Run the tests**

Run: `cd src && go test -count=1 -run 'TestCascadeImageSizingLengths|TestImageSizingLengthsDoNotInherit' ./lib/html/`
Expected: PASS.

Run: `cd src && go test -count=1 ./lib/html/`
Expected: PASS - nothing else reads the new fields yet, and the whole suite stays green.

- [ ] **Step 7: Commit**

```bash
cd /home/timebomb/git/opencode/notmutt && git add src/lib/html/html.go src/lib/html/img_test.go && git commit -m "feat(html): cascade width/height/max-width as px and percent lengths"
```

---

### Task 2: Sizing core - imgRes, specImg, imgExtentW, usedImg

**Files:**
- Modify: `src/lib/html/box.go` - `Box.res` field; `buildElement` allocates it for `RoleImg`.
- Create: `src/lib/html/img.go`.
- Modify: `src/lib/html/table.go` - `runExtents` uses `imgExtentW` for image atoms.
- Test: `src/lib/html/img_test.go` - the full probe-pinned sizing table.

This task is the heart of the plan: the pure replaced-element sizing that the probe appendix pins. It builds on Task 1's `CSSLen`. Extent integration (`imgExtentW` in `runExtents`) lands here so Task 4's table seating has its measure side; layout emission (used width into rows) is Task 4.

- [ ] **Step 1: Write the failing sizing test**

Append to `src/lib/html/img_test.go`:

```go
// boxWithImg builds a one-image body, returns the img box with its
// intrinsic set, for driving usedImg directly (a pure sizing function;
// probe appendix A-N).
func imgBox(t *testing.T, body string, iw, ih int) *Box {
	t.Helper()
	bs := buildBody(body)
	if len(bs) != 1 {
		t.Fatalf("body = %d boxes, want 1", len(bs))
	}
	b := bs[0]
	if b.Role != RoleImg {
		t.Fatalf("box role = %d, want RoleImg", b.Role)
	}
	if b.res == nil {
		b.res = &imgRes{}
	}
	b.res.iw, b.res.ih = iw, ih
	return b
}

func TestUsedImgSizingProbes(t *testing.T) {
	cases := []struct {
		name   string
		body   string // the <img ...> markup (CSS in style, attrs on the tag)
		iw, ih int    // intrinsic px (the probe's source image)
		avail  int    // containing-block px
		wantW  int
		wantH  int
	}{
		// A: neither specified -> intrinsic
		{"A auto -> intrinsic", `<img src="m60x30.png">`, 60, 30, 800, 60, 30},
		// B/C: attrs alone size (NOTMUTT extension; weasyprint ignores them)
		{"B width attr only", `<img src="m60x30.png" width="120">`, 60, 30, 800, 120, 60},
		{"C both attrs", `<img src="m60x30.png" width="120" height="40">`, 60, 30, 800, 120, 40},
		// D: CSS width beats the attr, ratio scales height
		{"D css width beats attr", `<img src="m60x30.png" width="120" style="width:90px">`, 60, 30, 800, 90, 45},
		// E/F: max-width clamps, ratio rescales height
		{"E max-width px", `<img src="m60x30.png" style="max-width:50px">`, 60, 30, 800, 50, 25},
		{"F max-width 100%", `<img src="m200x100.png" style="max-width:100%">`, 200, 100, 100, 100, 50},
		// G: % width resolves against avail
		{"G width 50%", `<img src="m200x100.png" style="width:50%">`, 200, 100, 200, 100, 50},
		// H: both css axes -> both honored, ratio dropped
		{"H both css axes", `<img src="m60x30.png" style="width:120px;height:40px">`, 60, 30, 800, 120, 40},
		// I: css height only -> width from ratio
		{"I height only", `<img src="m60x30.png" style="height:120px">`, 60, 30, 800, 240, 120},
		// J/K: fractional ratio rounds half away from zero
		{"J fractional up", `<img src="m300x200.png" style="width:250px">`, 300, 200, 800, 250, 167},
		{"K fractional up", `<img src="m300x200.png" style="width:100px">`, 300, 200, 800, 100, 67},
		// M/N: % height is auto
		{"M pct height alone", `<img src="m200x100.png" style="height:50%">`, 200, 100, 800, 200, 100},
		{"N pct height with width", `<img src="m200x100.png" style="width:120px;height:50%">`, 200, 100, 800, 120, 60},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := imgBox(t, tc.body, tc.iw, tc.ih)
			w, h := usedImg(b, tc.avail)
			if w != tc.wantW || h != tc.wantH {
				t.Fatalf("usedImg = %dx%d, want %dx%d", w, h, tc.wantW, tc.wantH)
			}
		})
	}
}

func TestImgExtentWidth(t *testing.T) {
	// px and intrinsic images contribute their width; a % width seats the
	// intrinsic natural width; nothing -> 0
	if b := imgBox(t, `<img src="m60x30.png" width="120">`, 60, 30); imgExtentW(b) != 120 {
		t.Fatalf("attr width extent = %d, want 120", imgExtentW(b))
	}
	if b := imgBox(t, `<img src="m60x30.png">`, 60, 30); imgExtentW(b) != 60 {
		t.Fatalf("intrinsic extent = %d, want 60", imgExtentW(b))
	}
	if b := imgBox(t, `<img src="m60x30.png" style="width:50%">`, 60, 30); imgExtentW(b) != 60 {
		t.Fatalf("pct width extent = %d, want intrinsic 60 (never a %% number)", imgExtentW(b))
	}
	if b := imgBox(t, `<img src="broken.png">`, 0, 0); imgExtentW(b) != 0 {
		t.Fatalf("no-intrinsic extent = %d, want 0", imgExtentW(b))
	}
}
```

- [ ] **Step 2: Run it to confirm it fails**

Run: `cd src && go test -count=1 -run 'TestUsedImgSizingProbes|TestImgExtentWidth' ./lib/html/`
Expected: FAIL - `imgBox` refers to `b.res` and `imgRes` that do not exist.

- [ ] **Step 3: Add `Box.res` and allocate it for images**

`src/lib/html/box.go`, in the `Box` struct after the memo fields (after `tblMeas`, line 43):

```go
	res      *imgRes // image geometry (RoleImg only): intrinsic + last resolved used size (img.go)
```

In `buildElement`, right after the box is built (after line 155 `b := &Box{...}`):

```go
	b := &Box{Role: role, Tag: tag, Node: n, St: st, WS: st.WS}
	if role == RoleImg {
		b.res = &imgRes{} // image geometry filled by ResolveImages/layout (img.go)
	}
```

- [ ] **Step 4: Create `src/lib/html/img.go`**

```go
// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import "math"

// imgRes is one RoleImg box's geometry. Intrinsic (iw/ih) is the decoded
// pixel size from ResolveImages (0 when the source has no loadable image);
// used (uW/uH/uSet) is the px size resolved at layout by usedImg, valid
// for the one layout pass that reached the box.
type imgRes struct {
	iw, ih int
	uW, uH int
	uSet   bool
}

// imgSpec is an image's effective specified geometry after the cascade and
// the width/height attribute hints.
type imgSpec struct {
	wPx, hPx int  // effective specified px; 0 = auto
	wPct     bool // width is a percentage of the containing width
	mwPx     int  // max-width px; 0 = none
	mwPct    bool // max-width is a percentage of the containing width
}

// specImg resolves an image's effective specified width/height/max-width.
// CSS width/height win over the same-named attribute; the attributes are a
// NOTMUTT extension - the pinned mail contract sizes declared-size images
// from them, and weasyprint ignores them (probes B, C). width/height:auto
// and width:0 are "no value", so the attribute hint then fills its axis.
// A percentage height is auto (weasyprint: a % height against an
// auto-height containing block is auto, probes M, N), so it never reaches
// hPx and the height attribute survives it.
func specImg(b *Box) imgSpec {
	var s imgSpec
	if b.Node != nil {
		s.wPx, s.wPct = attrLen(Attr(b.Node, "width"))
		s.hPx, _ = attrLen(Attr(b.Node, "height"))
	}
	if b.St != nil {
		if b.St.Width.Pct {
			s.wPx, s.wPct = b.St.Width.Px, true
		} else if b.St.Width.Px > 0 {
			s.wPx = b.St.Width.Px
		}
		if !b.St.Height.Pct && b.St.Height.Px > 0 {
			s.hPx = b.St.Height.Px // a % height is auto (M, N): never px, so the attr hint survives
		}
		if b.St.MaxWidth.Pct {
			s.mwPx, s.mwPct = b.St.MaxWidth.Px, true
		} else if b.St.MaxWidth.Px > 0 {
			s.mwPx = b.St.MaxWidth.Px
		}
	}
	return s
}

// attrLen folds a width/height attribute to a length: a positive px
// number, or a positive percentage (Pct true). Junk, 0, and other units
// are not values.
func attrLen(v string) (px int, pct bool) {
	v = strings.TrimSpace(v)
	if strings.HasSuffix(v, "%") {
		if n, err := strconv.Atoi(strings.TrimSuffix(v, "%")); err == nil && n > 0 {
			return n, true
		}
		return 0, false
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return n, false
	}
	return 0, false
}

// imgExtentW is the image's content-width contribution to an inline run
// measured at infinite available width: a specified px width, else the
// intrinsic width. A % width cannot resolve at infinite width and never
// forces a column wider than the image is, so it contributes the intrinsic
// natural width (0 when the image has none). Min equals max: an image is
// atomic (the widest unbreakable piece is the whole).
func imgExtentW(b *Box) int {
	s := specImg(b)
	if !s.wPct && s.wPx > 0 {
		return s.wPx
	}
	if b.res != nil && b.res.iw > 0 {
		return b.res.iw
	}
	return 0
}

// usedImg resolves an image's used px width/height at the available width
// and memoizes them on the box. Weasyprint-faithful on the CSS surface
// (probe appendix A-N): both axes specified -> both honored (ratio dropped,
// probe H); one axis -> the other scales by the intrinsic ratio (D, I); a
// percentage height is auto (M, N); neither specified -> intrinsic (A).
// max-width clamps the resolved width and rescales the height by the ratio
// (E, F). Rounding is half away from zero (math.Round), matching the
// measured fractional probes J/K.
func usedImg(b *Box, avail int) (w, h int) {
	s := specImg(b)
	iw, ih := 0, 0
	if b.res != nil {
		iw, ih = b.res.iw, b.res.ih
	}
	ratio := iw > 0 && ih > 0
	at := func(px int, pct bool) int {
		if pct {
			return px * avail / 100
		}
		return px
	}
	wSet := s.wPx > 0 || s.wPct
	switch {
	case wSet && s.hPx > 0:
		w, h = at(s.wPx, s.wPct), s.hPx
	case wSet:
		w = at(s.wPx, s.wPct)
		if ratio {
			h = int(math.Round(float64(w) * float64(ih) / float64(iw)))
		} else {
			h = ih
		}
	case s.hPx > 0:
		h = s.hPx
		if ratio {
			w = int(math.Round(float64(h) * float64(iw) / float64(ih)))
		} else {
			w = iw
		}
	default:
		w, h = iw, ih
	}
	if mw := at(s.mwPx, s.mwPct); mw > 0 && w > mw {
		w = mw
		if ratio {
			h = int(math.Round(float64(w) * float64(ih) / float64(iw)))
		}
	}
	if b.res != nil {
		b.res.uW, b.res.uH, b.res.uSet = w, h, true
	}
	return w, h
}

// ResolveImages walks the box tree once and fills each RoleImg box's
// intrinsic size from the caller's load. load maps the img's src attribute
// to its pixel dimensions; the caller owns src resolution and image decode
// (privacy and trust boundary: image bytes never enter this package). A
// nil load, an empty src, an ok=false, or a src that is not a decodable
// image leaves the box with no intrinsic (0) - it still lays out from
// CSS/attribute sizes or as a zero-width placeholder. O(#imgs); decode cost
// is the caller's. Layout without a prior ResolveImages measures every
// image at zero intrinsic.
func ResolveImages(boxes []*Box, load func(src string) (w, h int, ok bool)) {
	if load == nil {
		return
	}
	var walk func(cs []*Box)
	walk = func(cs []*Box) {
		for _, b := range cs {
			if b.Role == RoleImg && b.res == nil {
				b.res = &imgRes{}
			}
			if b.Role == RoleImg && b.res != nil && b.Node != nil {
				if src := Attr(b.Node, "src"); src != "" {
					if w, h, ok := load(src); ok && w > 0 && h > 0 {
						b.res.iw, b.res.ih = w, h
					}
				}
			}
			walk(b.Children)
		}
	}
	walk(boxes)
}
```

`img.go` needs `strings` and `strconv` imports too (used by `attrLen`).

- [ ] **Step 5: Route run-extent image width through `imgExtentW`**

`src/lib/html/table.go`, in `runExtents` (the loop over atoms, lines 74-87):

```go
		w := a.width(m)
		if a.img != nil {
			// extent width, never the last layout's used px: a measure
			// pass must not read a % width resolved at a narrower avail
			w = imgExtentW(a.img)
		}
```

`atom.width` itself still returns 0 for a not-yet-resolved image (Task 4 changes it), so this substitution is what makes an image seat a column today. (Once Task 4 resolves images in `LayoutInline`, `atom.width` for an image returns its used px; this `runExtents` override stays correct regardless of pass order.)

- [ ] **Step 6: Run the tests**

Run: `cd src && go test -count=1 -run 'TestUsedImgSizingProbes|TestImgExtentWidth' ./lib/html/`
Expected: PASS - all 14 probe cases.

Run: `cd src && go test -count=1 ./lib/html/`
Expected: PASS - the sizing core is read by nothing else yet; existing tests unchanged.

- [ ] **Step 7: Commit**

```bash
cd /home/timebomb/git/opencode/notmutt && git add src/lib/html/box.go src/lib/html/img.go src/lib/html/inline.go src/lib/html/img_test.go && git commit -m "feat(html): replaced-image sizing core (weasyprint-faithful used size and extent width)"
```

---

### Task 3: ResolveImages pre-pass with a loader

**Files:**
- Test: `src/lib/html/img_test.go`.
- (Implementation of `ResolveImages` already landed in Task 2 Step 4; this task tests it and its O(n) walk over nested boxes.)

- [ ] **Step 1: Write the failing test**

Append to `src/lib/html/img_test.go`. The fixtures are crafted so every assert is
load-bearing (a mutation-review finding): a loadable img sits at depth 3 so the
walk's descent is proven positively, and broken.png returns NONZERO dims with
ok=false so a dropped `ok` guard would visibly land 40x20 and fail its 0x0 assert
(a 0x0-false fixture would be indistinguishable from "not reached"). The
trailing src-less img is an adjacency/no-write check (the `src != ""` skip is not
separately pinned - see TODO.org).

```go
func TestResolveImagesFillsIntrinsics(t *testing.T) {
	load := func(src string) (w, h int, ok bool) {
		switch src {
		case "ok.png":
			return 60, 30, true
		case "deep.png":
			return 12, 6, true
		case "broken.png":
			return 40, 20, false // decode failed: nonzero dims must NOT land
		}
		return 0, 0, false // empty src, unknown
	}
	bs := buildBody(`<p><img src="ok.png"></p><div><ul><li><img src="deep.png"></li></ul></div><p><img src="broken.png"><img src=""></p>`)
	ResolveImages(bs, load)
	if b := bs[0].Children[0].res; b == nil || b.iw != 60 || b.ih != 30 {
		t.Fatalf("ok.png intrinsic = %+v, want 60x30", b)
	}
	// descend to the img under div > ul > li: the walk must reach depth 3
	div := bs[1]
	li := div.Children[0].Children[0]
	if b := li.Children[0].res; b == nil || b.iw != 12 || b.ih != 6 {
		t.Fatalf("deep.png intrinsic = %+v, want 12x6", b)
	}
	p3 := bs[2]
	if b := p3.Children[0].res; b == nil || b.iw != 0 || b.ih != 0 {
		t.Fatalf("broken.png intrinsic = %+v, want 0x0 (load ok=false)", b)
	}
	if b := p3.Children[1].res; b == nil || b.iw != 0 {
		t.Fatalf("empty-src intrinsic = %+v, want 0x0", b)
	}
}

func TestResolveImagesNilLoaderIsNoop(t *testing.T) {
	bs := buildBody(`<p><img src="ok.png"></p>`)
	ResolveImages(bs, nil) // must not panic
	if b := bs[0].Children[0].res; b == nil || b.iw != 0 {
		t.Fatalf("nil loader intrinsic = %+v, want untouched 0x0", b)
	}
}
```

This test needs to know the built shape: `<p><img>` puts the img as the p's only child (p is a block, img inline, no splitRuns since no block child - `p.Children[0]` is the img). `<div><ul><li><img>`: div block > ul block > li block > img. `<p><img><img>` two inline children, no splitRuns.

- [ ] **Step 2: Run it to confirm it fails**

Run: `cd src && go test -count=1 -run 'TestResolveImages' ./lib/html/`
Expected: FAIL - `ResolveImages` undefined.

- [ ] **Step 3: Add `ResolveImages` (already drafted in Task 2 Step 4)**

If you skipped ahead: copy the `ResolveImages` function from Task 2 Step 4 into `src/lib/html/img.go`. If Task 2 is committed, nothing to add here - run the tests.

- [ ] **Step 4: Run the tests**

Run: `cd src && go test -count=1 -run 'TestResolveImages' ./lib/html/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
cd /home/timebomb/git/opencode/notmutt && git add src/lib/html/img_test.go && git commit -m "feat(html): ResolveImages pre-pass fills intrinsic sizes from a caller loader"
```

---

### Task 4: Layout emits images at their used px

**Files:**
- Modify: `src/lib/html/inline.go` - `atom.width` (used-or-extent); `LayoutInline` resolves each image at the block's content width before emitting.
- Test: `src/lib/html/img_test.go` - lone-image rows, text-shared lines, % through layout, table column seating.

Images become real content: `LayoutInline` resolves each image's used px at the available width (the block's content width), emits the atom at that width, and the row's atoms carry the resolved `uW/uH` for stage 2. A lone image already emitted one Row (anonymous-run wrapping); it now carries a real width. An image on a line with text shares it (weasyprint-faithful; the terminal's own-row-per-image is plan 6).

- [ ] **Step 1: Write the failing tests**

Append to `src/lib/html/img_test.go`:

```go
// layoutImg lays out one body with the given fake loader at the width.
func layoutImg(t *testing.T, body string, load func(string) (int, int, bool), width int) []Row {
	t.Helper()
	bs := buildBody(body)
	ResolveImages(bs, load)
	return LayoutBlock(bs, width, mono(1), false)
}

// imgAtomIn scans rows for the first row whose Line carries an image atom.
func imgAtomIn(t *testing.T, rs []Row) (Row, atom) {
	t.Helper()
	for _, r := range rs {
		for _, a := range r.Line.Atoms {
			if a.img != nil {
				return r, a
			}
		}
	}
	t.Fatal("no row carries an image atom")
	return Row{}, atom{}
}

func TestLoneImageEmitsItsOwnRowAtUsedWidth(t *testing.T) {
	rs := layoutImg(t, `<p>a</p><img src="banner.png"><p>b</p>`,
		func(string) (int, int, bool) { return 60, 30, true }, 800)
	if len(rs) != 3 {
		t.Fatalf("rows = %d (%v), want 3: a, the image, b", len(rs), rowsText(rs))
	}
	r, a := imgAtomIn(t, rs)
	if r.Box.Tag != "" || len(r.Line.Atoms) != 1 {
		t.Fatalf("img row box/atoms = tag %q/%d, want the anonymous run holding just the img",
			r.Box.Tag, len(r.Line.Atoms))
	}
	if res := a.img.res; res == nil || res.uW != 60 || res.uH != 30 {
		t.Fatalf("img used = %+v, want 60x30 resolved at the 800px block width", res)
	}
}

func TestImageSharesATextLine(t *testing.T) {
	// weasyprint places an inline image on the text line; stage 1 does the
	// same (the terminal own-row choice is plan 6, recorded divergence).
	rs := layoutImg(t, `<p>x<img src="y.png">z</p>`,
		func(string) (int, int, bool) { return 60, 30, true }, 800)
	if len(rs) != 1 {
		t.Fatalf("rows = %d, want 1 (x, img, z on one line)", len(rs))
	}
	if got := rowsText(rs); len(got) != 1 || got[0] != "xz" {
		t.Fatalf("line text = %q, want [xz] (the img is atomic, no text)", got)
	}
	r, a := imgAtomIn(t, rs)
	if len(r.Line.Atoms) != 3 {
		t.Fatalf("img row atoms = %d, want x img z", len(r.Line.Atoms))
	}
	if res := a.img.res; res == nil || res.uW != 60 || res.uH != 30 {
		t.Fatalf("inline img used = %+v, want 60x30", res)
	}
}

func TestPercentWidthResolvesThroughLayout(t *testing.T) {
	// probe G shape at the block level: a 50% image resolves against the
	// available width (300 here - stage 1 does not consume block width, so
	// the layout width is the containing width)
	rs := layoutImg(t, `<img src="m.png" style="width:50%">`,
		func(string) (int, int, bool) { return 600, 400, true }, 300)
	if len(rs) != 1 {
		t.Fatalf("rows = %d, want 1 (the image)", len(rs))
	}
	_, a := imgAtomIn(t, rs)
	if res := a.img.res; res == nil || res.uW != 150 || res.uH != 100 {
		t.Fatalf("50%% img at 300px = %+v, want 150x100 (ratio 600:400)", res)
	}
}

func TestTableCellSeatsAnImageColumn(t *testing.T) {
	bs := buildBody(`<table><tr><td><img src="big.png"></td></tr></table>`)
	ResolveImages(bs, func(string) (int, int, bool) { return 600, 400, true })
	rs := LayoutBlock(bs, 800, mono(1), false)
	if len(rs) != 1 || len(rs[0].Cells) != 1 {
		t.Fatalf("rows/cells = %d/%d, want one grid row with one cell fragment",
			len(rs), len(rs[0].Cells))
	}
	frag := rs[0].Cells[0]
	var imgAtom *atom
	for i := range frag.Line.Atoms {
		if frag.Line.Atoms[i].img != nil {
			imgAtom = &frag.Line.Atoms[i]
		}
	}
	if imgAtom == nil {
		t.Fatal("cell fragment has no image atom")
	}
	if res := imgAtom.img.res; res == nil || res.uW != 600 || res.uH != 400 {
		t.Fatalf("cell img used = %+v, want 600x400", res)
	}
	if frag.W != 600 {
		// content width = column box (600 + 2px td padding) - 2px padding
		t.Fatalf("cell content width = %d, want 600", frag.W)
	}
}
```

- [ ] **Step 2: Run them to confirm they fail**

Run: `cd src && go test -count=1 -run 'TestLoneImageEmitsItsOwnRowAtUsedWidth|TestImageSharesATextLine|TestPercentWidthResolvesThroughLayout|TestTableCellSeatsAnImageColumn' ./lib/html/`
Expected: FAIL - image atoms lay out at zero width today (`atom.width` returns 0 for an image; `res.uW` stays 0).

- [ ] **Step 3: Resolve images in `LayoutInline` and give them width**

`src/lib/html/inline.go`, `atom.width` (lines 47-52):

```go
func (a atom) width(m Metrics) int {
	if a.img != nil {
		if r := a.img.res; r != nil && r.uSet {
			return r.uW // resolved at the line's width by LayoutInline
		}
		return imgExtentW(a.img) // not laid out yet: extent width
	}
	return m.Width(a.text)
}
```

In `LayoutInline`, the image dispatch (lines 199-201) resolves at the block's content width before emitting:

```go
		if a.img != nil {
			usedImg(a.img, width) // resolve px against this line width
			emit(a)
			continue
		}
```

(An image wider than the line overflows whole in author mode - it is atomic, like an over-wide word; the common lone-image case is unaffected.)

- [ ] **Step 4: Run the tests**

Run: `cd src && go test -count=1 -run 'TestLoneImageEmitsItsOwnRowAtUsedWidth|TestImageSharesATextLine|TestPercentWidthResolvesThroughLayout|TestTableCellSeatsAnImageColumn' ./lib/html/`
Expected: PASS.

Run: `cd src && go test -count=1 ./lib/html/`
Expected: PASS - the pre-existing `<img>` fixtures (box_test, inline_test) still pass: without `ResolveImages` an image has no intrinsic and no CSS width, so it resolves to 0x0 and lays out exactly as before.

- [ ] **Step 4b: Post-review hardening (Task 4 code-quality review)**

The four Step-1 tests pin structure and `res.uW`, but `res.uW` is written by the
dispatch's `usedImg` call regardless of what `emit` does with the atom, so a
regression that made images zero-width again in the line fill would pass them.
These two tests close that gap, and one comment states the memo's validity
contract. The wrap test MUST fail if `atom.width` returns 0 for an image
(verify by a transient mutation, then revert).

Append to `src/lib/html/img_test.go`:

```go
func TestImageConsumesUsedWidthInWrapBudget(t *testing.T) {
	// text after an inline image wraps only after the image's used px: x(1)
	// + sep + 60px img fill the 62px line, so y wraps to line 2. Laying the
	// image at zero width would keep all three on one line.
	rs := layoutImg(t, `<p>x <img src="b.png"> y</p>`,
		func(string) (int, int, bool) { return 60, 30, true }, 62)
	got := rowsText(rs)
	if len(got) != 2 || got[1] != "y" {
		t.Fatalf("rows = %v, want 2 with y on line 2 (the 60px image consumed the budget)", got)
	}
}

func TestRelayoutAtNewWidthReResolvesImages(t *testing.T) {
	// the memoized used px is valid per layout pass; a second LayoutBlock of
	// the same tree at a different width must re-resolve, not serve the old uW
	bs := buildBody(`<img src="p.png" style="width:50%">`)
	ResolveImages(bs, func(string) (int, int, bool) { return 100, 50, true })
	LayoutBlock(bs, 200, mono(1), false) // first pass resolves uW=100
	rs := LayoutBlock(bs, 400, mono(1), false)
	_, a := imgAtomIn(t, rs)
	if res := a.img.res; res == nil || res.uW != 200 {
		t.Fatalf("re-laid 50%% img at 400px = %+v, want uW 200 (re-resolved, not stale 100)", res)
	}
}
```

And strengthen the `atom.width` uW-branch comment in `src/lib/html/inline.go`
so the memo contract is explicit (the branch is valid only because
`LayoutInline` resolves immediately before emit at the same width; `runExtents`
overrides images on the measure side, so measure never reads a stale resolved
width from a different pass):

```go
		if r := a.img.res; r != nil && r.uSet {
			return r.uW // valid: LayoutInline resolves right before emit at this width
		}
```

Re-run the full `./lib/html/` suite, vet, gofmt. Then commit as in Step 5 but
with the message `feat(html): inline layout resolves images to used px and emits them at that width` plus a body line noting the hardening - or, if the Step-4 commit already landed separately, make this its own commit `feat(html): pin image used px in the wrap budget and re-resolve on re-layout`.

- [ ] **Step 5: Commit**

```bash
cd /home/timebomb/git/opencode/notmutt && git add src/lib/html/inline.go src/lib/html/img_test.go && git commit -m "feat(html): inline layout resolves images to used px and emits them at that width"
```

---

### Task 5: Full suite gate and divergence records

**Files:**
- Modify: `BUGS.org` (repo root) - image divergence entries.
- Modify: `TODO.org` (repo root) - minors from self-review.
- No code changes expected; if the gate or a review surfaces a real fix, it lands as its own code commit first.

- [ ] **Step 1: Run the full tagged suite**

Run: `cd src && go test -count=1 -tags "lua mcp" ./...`
Expected: PASS - every package, including the pinned mail walker tests (`html_*_test.go`), unweakened.

Run: `cd src && go vet ./lib/html/ && gofmt -l lib/html/`
Expected: vet clean, gofmt lists nothing.

- [ ] **Step 2: Record the divergences in `BUGS.org`**

Append under the table entries (repo root `BUGS.org`), three OPEN entries:

```org
* OPEN html images: px height never advances the vertical stream (stage-1 deferral)
Rows are terminal-height strips with no line-box height, so an image's used
uH is carried on the box for stage 2 and does not push later rows down;
weasyprint grows the line box. Fine for the terminal (a stage-2 image block
reserves its own rows); a PDF backend would need line-box height. Pointers:
src/lib/html/img.go usedImg, src/lib/html/block.go Row.

* OPEN html images: an inline image shares its text line (stage-1 deferral)
A <p>x<img>y</p> lays the image on the x/y line (weasyprint-faithful). The
mail walker renders every image on its own line; stage 2 (the plan-6
quantizer) decides terminal own-row emission, which must split the line
around the image atom. Pointers: src/lib/html/inline.go LayoutInline img
dispatch, plan 5 decisions.

* OPEN html images: a broken or unloadable image lays out at zero width
An img with no loadable src and no CSS width has no intrinsic, so it emits a
zero-width row; weasyprint paints the alt text and a broken-image box. Alt
text is a stage-2 render choice (the walker today drops it too). Pointers:
src/lib/html/img.go ResolveImages/imgExtentW, plan 5 decisions.
```

- [ ] **Step 3: Log the self-review minors to `TODO.org`**

Append four entries:

```org
* TODO width:auto on an image does not clear the width attribute hint
specImg treats CSS width:auto/0 as "no value", so the width attribute then
fills that axis: <img width="120" style="width:auto"> sizes 120px. CSS
auto over an attr should mean auto (intrinsic); the zero CSSLen cannot
distinguish "property absent" from "property auto". Rare (declared-size
mail never writes width:auto); would need a declared-but-auto flag.
src/lib/html/img.go specImg.
* TODO a pct-width image in a table seats its intrinsic as min and max content
runExtents cannot split one image's min (0: a % can shrink) from its max
(intrinsic), so a % image contributes its intrinsic to both. A lone
width:100% image therefore never lets its table shrink below intrinsic
under a narrow container. Fine for mail banners; revisit if table squeeze
probes demand it. src/lib/html/img.go imgExtentW.
* TODO no line-break before an over-wide image after a legal break point
LayoutInline emits an image unconditionally, so a 400px image after "words "
on a 300px line overflows whole instead of wrapping to the next line (text
does break there). An image sharing a line with text is already a recorded
divergence; add the break if inline-image mail ever matters. src/lib/html/inline.go.
* TODO no test pins an image's min-content contribution inside a squeezed cell
Task 4 seats a 600px image in an 800px table; the normalize-mode squeeze
(dist <= colMin char-break path) with an image in the cell is unpinned.
src/lib/html/img_test.go TestTableCellSeatsAnImageColumn.
```

- [ ] **Step 4: Commit the records**

```bash
cd /home/timebomb/git/opencode/notmutt && git add BUGS.org && git commit -m "docs(bugs): record plan-5 image stage-1 divergences"
```

(TODO.org stays dirty alongside sibling edits per convention; it is not committed here.)

---

## Self-review checklist

- **Spec coverage:** the plan covers every settled decision - cascade (Task 1), intrinsic decode pre-pass (Task 3), pure sizing + extent (Task 2), layout emission + table seating (Task 4). Probe expectations A-N map to named tests in Task 2 and 4.
- **Placeholder scan:** no TBD/TODO-in-plan; every code step is verbatim; the one deferred function (`ResolveImages` in Task 3) is fully drafted in Task 2.
- **Type consistency:** `CSSLen{Px, Pct}`, `imgRes{iw, ih, uW, uH, uSet}`, `imgSpec{wPx, hPx, wPct, mwPx, mwPct}`, and the signatures `specImg(*Box) imgSpec`, `imgExtentW(*Box) int`, `usedImg(*Box, int) (int, int)`, `ResolveImages([]*Box, func(string)(int,int,bool))` are used identically across tasks. `atom.width(m)` gains the used-or-extent branch in Task 4 after `runExtents` special-cases images in Task 2.
- **Existing test shape preserved:** no `RoleImg` reclassification, so `TestBuildRolesAndSkips` (anonymous-run inline child) and `TestFlattenKeepsBRAndImg` (img atom in sequence) keep their shape; images without a loader resolve 0x0 and render exactly as before.
