# Stage-2 Terminal Wiring Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate the running mail HTML renderer (`src/mail/html.go` walker) off its one-pass DOM emitter onto the stage-1 px row stream from `src/lib/html` (`LayoutBlock` -> `[]Row`), quantizing px to terminal lines, so viewer output comes from one CSS-faithful layout instead of the walker's divergent approximations.

**Architecture:** The design spec (`docs/superpowers/specs/2026-09-03-html-layout-engine-design.md`, "Stage 2: terminal backend" + "Migration") is normative. Stage 1 (`lib/html`) is done through plan 5 and exports a flat `[]Row` stream of px geometry: `Row{Gap, X, W, Box, Line, HR, Markers, Cells}` with `LineBox{Atoms, Width, X}` lines of laid-out text pieces. Stage 2 (this plan) consumes that stream in `mail/` and emits `[]core.Line` for the pager. `mail/html.go`'s facade family (`RenderHTML`, `RenderHTMLWithLinks`, `renderHTML`) keeps its exact signatures - `renderMessage` in `thread.go:407` calls them and the view model must not change. Dark/theme adaptation, link labels, sanitize (F1), image byte resolution, tracking-pixel strip, the `htmlWrapWidth`/`maxHTMLLines` budgets stay in `mail/` exactly where the spec says they do. The old walker is deleted only in the final task, after the pinned suite passes on the new path.

**Tech Stack:** Go. Stage-1 `lib/html` (already built). `core.Line/Run/Image/ImagePos` is the emission target. No new dependencies.

---

## Key decisions (locked; derived from the spec's stage-2 + migration sections)

These fix the design so every task below is unambiguous. Where a decision changes a user-visible behavior the old walker produced, it is listed in the Divergences appendix at the end; the pinned `html_*_test.go` suite is the migration contract and must pass unweakened from the first switch (locked-regression rule).

### D1. The terminal px frame (mail-owned constants)

- `charW = 10` px per terminal cell (horizontal). This is forced by the LOCKED `TestImageDeclaredSizes`: `width:50%` at the 80-cell layout width must resolve to `DispW 400`; 50% of `80 * charW` is 400 only at `charW = 10`. It is also the value today's `imgSize` hardcodes (`n * layoutCells * 10 / 100`, mail/html.go:497).
- `lineH = 16` px per pager row (vertical blank unit), the base em. `p/ul/...` UA margins are `1em = 16px`, so a collapsed inter-block gap quantizes to exactly one blank row (the walker's "one blank line between content blocks"). The spec fixes `blankRows = round(gapPx / 16px)`.
- `htmlWrapWidth = 120` cells and `maxHTMLLines = 5000` stay as-is (mail/html.go:26-29). A layout width of `N` cells is `N * charW` px of content width for stage 1.
- Stage 1 is therefore given px widths and a **meter whose `Width(s)` returns runewidth-cell count times `charW`**; text wraps in px exactly where the old walker wrapped in cells, so no line-budget behavior changes.

### D2. The stage-1 consumer surface must become public

`Row` and `LineBox` are already exported, but `LineBox.Atoms []atom` is an unexported type with unexported fields - `mail` cannot read a line's text. Stage 2 needs, per line, the ordered display pieces (text runs with their computed style, inter-word spaces, inline image atoms) plus per-image geometry. Task 1 makes this real: the internal `atom` becomes the exported `Span` (the layout model IS the public line model - no second representation), and image geometry gains exported accessors on `*Box` backed by `imgRes`.

### D3. Declared image display size is separate from used/intrinsic px

`core.Image.DispW/DispH` is the email's **declared** display size in px (0 = decode fits the window); it is what the TUI targets on decode. The LOCKED `TestImageDeclaredSizes` proves it is NOT the used/intrinsic width: an attrless 10x10 `data:` image (bytes present) must report `{0,0}` even though a real decode would know 10x10. So stage 1 records, per image, the resolved **declared** px (`specImg`'s `wPx/hPx` with `%` resolved against the containing layout width, 0 when an axis is undeclared) - distinct from `usedImg`'s used px. Stage 2 reads declared px for `DispW/DispH` and used px only for reserving cells on the line. This replaces the old `imgSize` `%` hack (mail/html.go:485-503) with one coherent resolution inside stage 1, resolving `%` against the image's actual containing width (a cell, a blockquote) instead of always the page width.

### D4. The render path never decodes image bytes

`ResolveImages` fills intrinsics from a caller loader. On the mail render path no decode happens (privacy gate: bytes paint only on the render-images key, the TUI decodes later). So the facade does not call `ResolveImages` with a real loader - intrinsics stay 0 and images lay out from declared sizes only, exactly like today's walker (which knew no intrinsic at render). Stage 2 resolves each image's `src` to `core.Image{Data,URL,Alt}` from the mail `Attachment` list (cid:), inline `data:` base64, or a URL-only http(s) placeholder - the current `resolveImage` logic ported to read the image `*Box.Node`.

### D5. Blank-row quantization

For each `Row` in order: blanks above it = `round(Gap / lineH)` (round half away from zero, matching Go `math.Round`). **The first row of the whole stream drops its gap entirely** (the walker never leads with a blank; the pinned align/background tests read `lines[0]` as content). Interior gaps are preserved - that is the "interior blank-line preservation" parity item. Each blank `core.Line` carries `Bg = defaultBG` (pinned: `TestRenderHTMLBackground` blank lines must carry the mail bg). Stage-1 already drops a trailing block's final bottom margin, so no trailing blank appears.

Concretely, every UA vertical block margin is 16-25 px (`p`/`blockquote`/`pre` = 16, `h1`-`h6` = 21/20/19/21/22/25, ua.go `uaMargins`) and `round(25/16) = 1`, so the walker's "one blank between content blocks" holds exactly for paragraphs and headings; list items carry no UA margin (nested lists drop theirs), so items stay contiguous (no blank). Blank counts diverge from the walker only where an author sets a margin >= 32 px (2+ blanks) or in the hr row's own tight gaps (D11).

A row whose content is entirely dropped (a 1x1 tracking pixel, D9) contributes no blanks of its own; the pixel rows carry `Gap 0` so skipping them loses nothing.

### D6. Text -> runs, with the walker's punctuation binding

A `Span` is one of: a text piece (`Text`), an inter-word space (`Sep`, renders one `" "`), or an inline image (`Img`). Stage 1 emits spaces only where source whitespace actually existed, so adjacent leaves from separate inline elements have **no** space between them - `Reply <a>alpha</a>, or ...` already lays out `...alpha, or ...` with no binding fix needed. Stage 2 renders the span stream literally, then applies the walker's deliberate terminal divergence (`bindsLeft`, mail/html.go:369): when a text piece begins with a hugging character (`, . ; : ! ? % _ ) ] }`) and a `Sep` precedes it, that `Sep` is dropped so the punctuation hugs the preceding word. This reproduces the pinned `html_spacing_test.go` behaviors AND keeps the source-space-before-comma case hugging (a deliberate divergence from weasyprint, which prints the space - spec stage-2 section).

Control-strip (F1) runs over the text of every piece at render: `core.SanitizeControls`. A piece that sanitizes to an empty string (a bare `\x01` control node, the pinned `ControlNodeDoesNotPanic`) is dropped together with an adjacent `Sep` so it leaves no doubled space and cannot be indexed (the old bindsLeft panic).

Adjacent pieces whose effective run equals merge, exactly like the current `runWords` (mail/html.go:938-952): run equality gates merging, and the F-key label bit keeps label runs from merging with mail text.

### D7. Tab expansion inside preserved text

Stage 1 keeps tab runes verbatim in preserved (`pre`/`pre-wrap`) text. The walker expanded tabs to the 8-column stop before rendering (`expandTabs`, thread.go:356). F1 sanitize would drop an unexpanded tab entirely, so stage 2 expands tabs in every text piece before sanitize, using the same 8-column rule (`expandTabs` already lives in thread.go and is reused unchanged - no code move). This is the "tab expansion" parity item.

### D8. Alignment and indentation come from px X

Stage 1 folds text-align lead and box insets into `Row.X` px (content `x0` + `line.X`). Stage 2 starts content at column `round(X / charW)`, emitting that many leading blank cells as a leading space run. For left-aligned body text `X = 0`; for centered lines `X` is the alignment lead (the pinned align tests' nonzero/zero `leftPad` derive from it, with no leading blank before `lines[0]`). Explicit-left-clears-inherited-center/right is already a stage-1 layout property (`applyAlign` only offsets on an explicit `AlignSet` center/right at the aligned block), so the align tests pass from stage-1 output without mail-side alignment code.

### D9. Images: own-line vs inline, tracking-pixel strip

- A non-table `Row` whose line holds a **single image `Span` and nothing else** emits an own-line `core.Line{Image: img, Text: alt}`, `Bg = defaultBG` - the pinned `TestImageDeclaredSizes`/`TestTrackingPixelStripped` isolated-image lines.
- An image `Span` that shares its row with text emits **inline**: the `core.Image` rides a `core.Run.Image` whose `Text` is the alt, plus a `core.ImagePos{X}` at the image's cell offset on the same line - the pinned `TestDisplayNotInheritedRender` cell icon (a `display:block` anchor's img stays inline in a cell).
- In a table strip (`Row.Cells`), every cell image is inline on the strip line at its fragment column (the strip is one horizontal line; there is no full-width concept).
- **Tracking-pixel strip**: an image whose declared px is exactly `1x1` (attr or style - the pinned `TestTrackingPixelStripped`) drops at stage 2 before emission: a lone-pixel own-line row is skipped entirely; an inline pixel span is dropped from its line (no run, no `ImagePos`, no width). This reproduces the old `isTrackingPixel` (mail/html.go:267-270) with stage-1 declared px instead of `imgSize`.
- `DispW/DispH` come from D3 declared px; a 0-declared image still emits its row (the placeholder alt line - the third `TestImageDeclaredSizes` line is `{0,0}`).

### D10. Markers and the ordered-list ordinal

Stage 1's `RowMarker{Type, X}` carries the glyph type (`disc|circle|square|decimal`) and the owning item's content-edge px, but **not the ordinal of an `ol` item**. The old walker numbered ordered lists itself. Stage 2 must render `1.`/`2.`..., so Task 1 extends `RowMarker` with the item's 1-based ordinal (`Ord`, meaningful only when `Type == "decimal"`), stamped by the box builder on direct `li` children of an `ol` and carried onto the marker by block flow. Glyph transforms are a mail-owned data map (`disc -> "•"`, `circle -> "◦"`, `square -> "▪"`, `decimal -> "N."`), the R11 config-data shape; a future config override is noted in TODO.org, not built here.

A list item's text row sits at the list's 40px gutter indent (`X = 40`, col 4 at `charW 10`); its marker hangs in the gutter to the LEFT of the content edge, right-aligned so the marker's last cell is the cell just before the content column. A marker-only row (empty `li`) still emits a line with the glyph and nothing after it.

### D11. Horizontal rules become visible

Stage 1 emits an `hr` as `Row{HR: true}` with a px gap above (`Row.Gap`) and content width `Row.W`. Stage 2 quantizes the gap (D5) then emits one rule line: `round(W / charW)` cells of a rule glyph (`─`) as a plain run. The old walker rendered `<hr>` as nothing at all (an invisible block boundary). No mail test pins `hr`; this is a recorded, weasyprint-true divergence (the spec says hr renders as a theme rule row).

### D12. Table grid rows are one strip line

A `Row` with `Cells` is one visual line holding per-cell fragments side by side at their own absolute px `X`. Stage 2 renders the strip as one `core.Line`: walk the fragments in order, place each at its cell column (`round(X / charW)`, padding the gap since the previous fragment's content end with blank space runs), render each fragment's own spans/markers/images inline, and carry the fragment box's declared `Bg` on its runs (a cell `bgcolor` paints its own runs - pinned `TestRenderHTMLBackground` cell case). Fragments nest: a fragment that itself hosts a nested table has `Cells`; recursion renders the nested strip inside the outer fragment's span region at its shifted X. Gaps inside a table are flattened by stage 1 (each strip is one horizontal line), so strip-to-strip blank rows come only from the strip row's own `Gap`.

Inter-column px gaps (`tablePad`/`tableSpacing`, 1+2+1 = 4px) are below one cell at `charW 10`, so adjacent columns abut in cells - recorded divergence from the walker's 2-space gutters (a w3m artifact, not CSS truth).

### D13. The F-key link-label render is a stage-2-injected pre-layout transform

Link mode (`RenderHTMLWithLinks`) must weave an inline `[N]` label at the start of every link - anchor `href` (any display) and bare URL word - in document order, and return the `links` list. The labels must participate in layout (a link whose text wraps must carry its label on the right line at its start), so mail cannot paste them onto finished lines. Instead, when `labelLinks` is on, mail runs a **box-tree transform** after `Build` and before `LayoutBlock`:

- Every `Box` with `Tag == "a"` and a nonempty `href` (sanitized) gets a synthesized text box `"[N]"` inserted as the lead inline child of the anchor (descending into the first anonymous run when the anchor blockified), numbered in document order; the anchor `href` is appended to `links`.
- Every `RoleText` box whose text contains a URL token (`html.Links(f, true)` per whitespace field, the old detection) is split into pieces with a `"[N]"` label box inserted before the URL token, numbered in the same document order.
- Each label box is `RoleText` with `Text == "[N]"` and a **dedicated style copy** of its anchor/text style with the new `Style.Label` bit set (added in Task 1). Stage 2 sets `core.Run.Label` from `Span.St.Label`; the bit also keeps a label run from merging with adjacent mail text (D6), reproducing the current `runWords` behavior where label runs never merge.

The unlabeled render (`RenderHTML`) skips the transform entirely, so no labels and no `Style.Label` bits appear (pinned: "the unlabeled render must carry no labels").

### D14. Dark mode / theme adaptation stays in mail

The dark-mode gating logic (`runFor`, mail/html.go:961-993: light-declared bg reflects onto `themeBG`, its fg inverts via `AdaptFG`, the luma gate keeps a dark-declared mail dark, unstyled fg stays `""`) moves verbatim to the stage-2 run builder. The page background resolution (html/body declared `Bg` -> `defaultBG`, with the dark reflection gate; unstyled-text contrast fg via `ContrastFG` when the page declares no fg) is ported from the walker's html/body handling (mail/html.go:163-195) and runs once per render from the document's computed body style. Task 1 exports `BodyStyle` from lib so mail reads the same body style stage 1 laid out with (single cascade, no second StyleOf walk). `defaultBG` still defaults to the theme bg in dark mode and `#ffffff` in light mode, and every `core.Line` (content and blank) carries it (pinned `TestRenderHTMLBackground`/`html_dark_test.go`).

---

## File map

- Modify: `src/lib/html/inline.go` (atom -> Span), `src/lib/html/block.go` (RowMarker.Ord, Row doc), `src/lib/html/html.go` (Style.Label, BodyStyle), `src/lib/html/img.go` (imgRes declared px + Box accessors), `src/lib/html/box.go` (ol ordinal stamp), `src/lib/html/table.go` (any Span renames).
- Tests touched mechanically only: `src/lib/html/*_test.go` that reference `atom`/`.text` field access (values and assertions unchanged; this is a rename, gated by the full suite).
- Rewrite: `src/mail/html.go` (stage-2 renderer + facade). The old walker code is deleted in Task 6.
- Unchanged callers: `src/mail/thread.go:407,422` (`renderHTML`), the `RenderHTML`/`RenderHTMLWithLinks` signatures, `src/tui` (pager).
- New tests: `src/mail/html_stage2_test.go` (or per-task files), plus lib-side pins for each new export.
- Docs: `docs/html-rendering-analysis.md` (updated in Task 6 to describe the stage-2 engine), this plan (records committed with `Co-Authored-By: Deepseek`).

---

### Task 1: Export the stage-1 consumer surface

**Files:**
- Modify: `src/lib/html/inline.go`, `src/lib/html/block.go`, `src/lib/html/html.go`, `src/lib/html/img.go`, `src/lib/html/box.go`, `src/lib/html/table.go`
- Test: `src/lib/html/inline_test.go`, `src/lib/html/block_test.go`, `src/lib/html/table_test.go`, `src/lib/html/img_test.go`, `src/lib/html/box_test.go` (mechanical field renames only)

Pure rename + additive exports. Behavior must be byte-identical; the gate is the whole `lib/html` suite plus the tagged full suite (the mail walker still renders through its own path and must be untouched by this task).

- [ ] **Step 1: Rename the internal `atom` type to the exported `Span` with exported fields**

In `src/lib/html/inline.go` (atom declared at line 38):

```go
// Span is one laid-out piece of a filled line, in order: a text run, an
// inter-word space, or an inline image. It is the stage-2 consumer surface
// (the terminal backend renders spans into pager runs). Text is raw - the
// consumer sanitizes (F1), expands tabs, and applies terminal punctuation
// binding at render. A break never reaches an emitted line (it forces a
// flush inside the package), so no external consumer ever sees one.
type Span struct {
	Text string // the run's text (a word, a preserved-space chunk, or a single space when Sep)
	St   *Style // computed style (never nil)
	WS   WS     // effective white-space class of the source run
	Sep  bool   // renders as one space unless the following text binds left
	Img  *Box   // inline image (RoleImg); Text is empty
	br   bool   // unexported: forced break, consumed by LayoutInline's flush (never emitted)
}
```

The `br` flag stays unexported on purpose: it is a pre-fill token only. `flattenInline` emits `br` spans, `LayoutInline` turns them into flushes (never onto a line), and `runExtents` (table.go:63) skips them during measure - all in-package consumers. An external package cannot set a private field and never receives a `br` Span, which is exactly the contract stage 2 needs. Only the type name and the exported field names change; no structural `piece` wrapper type is introduced.

Replace every `atom` reference in `src/lib/html/` (inline.go, table.go, block_test.go, inline_test.go, table_test.go, img_test.go) with `Span`, every field access `a.text` -> `a.Text`, `.st` -> `.St`, `.ws` -> `.WS`, `.sep` -> `.Sep`, `.br` stays `.br`, `.img` -> `.Img`, and every literal `atom{...}` -> `Span{...}`. Keep `func (s Span) width(m Metrics) int` (the existing `(a atom) width` body; for a `Sep` span it returns one space's width, for an `Img` span the resolved or extent width).

Update every internal consumer: `runExtents` (table.go), `LayoutInline`'s `emit`/`flush`/`charBreak` closures, `cutText`/`breakAtSpace` calls (they take `text`/`m`, unaffected), and the `rowsText` helper in `block_test.go` (reads `r.Line.Atoms[i].Text`).

- [ ] **Step 2: Run the lib suite - it must pass with no assertion changes**

Run: `cd src && go test -count=1 ./lib/html/`
Expected: PASS. Every failure must be a compile error from the rename (field `.text` etc.), never a changed expectation.

- [ ] **Step 3: Add the image declared-px record and accessors**

In `src/lib/html/img.go`, extend `imgRes`:

```go
type imgRes struct {
	iw, ih    int // intrinsic decoded px (ResolveImages); 0 = none
	uW, uH    int // used px resolved at the last layout that reached the box
	uSet      bool
	dispW     int // declared display px resolved against that layout width (0 = axis undeclared)
	dispH     int
}
```

In `usedImg` (img.go ~105-151), after `s := specImg(b)` and before the memo write, record the declared axis resolved at `avail`:

```go
wSet := s.wPx > 0 || s.wPct
...
if b.res != nil {
	if wSet {
		b.res.dispW = at(s.wPx, s.wPct)
	}
	b.res.dispH = s.hPx // a px height is never a % (pct height is auto); 0 = undeclared
	b.res.uW, b.res.uH, b.res.uSet = w, h, true
}
```

`dispW` must be 0 when the axis is undeclared even if the used width came from the intrinsic (D3). Add exported accessors on `*Box` (img.go):

```go
// ImgDisp returns the image's declared display px (DispW/DispH target): the
// width/height the author declared (attr or CSS), % resolved against the
// containing layout width. An undeclared axis is 0. Valid after the box was
// laid out at some width (usedImg ran).
func (b *Box) ImgDisp() (w, h int) {
	if b.res == nil {
		return 0, 0
	}
	return b.res.dispW, b.res.dispH
}

// ImgUsed returns the image's used px at its last layout (0, 0, false when
// never laid out).
func (b *Box) ImgUsed() (w, h int, set bool) {
	if b.res == nil {
		return 0, 0, false
	}
	return b.res.uW, b.res.uH, b.res.uSet
}
```

- [ ] **Step 4: Pin the declared-px record**

Add to `src/lib/html/img_test.go` a load-bearing case. The existing `TestUsedImgSizingProbes` style calls `usedImg` on a box; add a focused test:

```go
func TestImgDeclaredDispSeparateFromUsed(t *testing.T) {
	// the declared display px (a decode target) is 0 per undeclared axis even
	// when used px came from the intrinsic: an attrless 10x10 data: image must
	// report declared 0x0 (locked TestImageDeclaredSizes) though used is 10x10.
	b := imgBox(`<img src="x.png">`) // helper builds a RoleImg box from a parse
	html.ResolveImages([]*Box{b}, func(string) (int, int, bool) { return 10, 10, true })
	usedImg(b, 800)
	if w, h := b.ImgUsed(); w != 10 || h != 10 {
		t.Fatalf("used = %d,%d, want intrinsic 10,10", w, h)
	}
	if w, h := b.ImgDisp(); w != 0 || h != 0 {
		t.Fatalf("declared = %d,%d, want 0,0 (undeclared)", w, h)
	}

	b2 := imgBox(`<img src="x.png" width="600" height="400">`)
	usedImg(b2, 800)
	if w, h := b2.ImgDisp(); w != 600 || h != 400 {
		t.Fatalf("declared = %d,%d, want attr 600,400", w, h)
	}

	b3 := imgBox(`<img src="x.png" style="width:50%;height:300px">`)
	usedImg(b3, 800)
	if w, h := b3.ImgDisp(); w != 400 || h != 300 {
		t.Fatalf("declared = %d,%d, want pct 400,300 at 800", w, h)
	}
}
```

(If `imgBox` in your read of `img_test.go` does not carry a `style`/attrs path, extend the helper or build via `buildBody` + `LayoutInline` like `layoutImg` does; match the existing helper idioms.) Mutation check: delete the `dispW`/`dispH` write in `usedImg` -> the `ImgDisp` assertions must FAIL.

- [ ] **Step 5: Add `Style.Label` and the ordered-list ordinal**

In `src/lib/html/html.go` `Style`, add:

```go
	Label bool // synthesized F-key link marker run (mail injects label boxes); not inherited content
```

In `src/lib/html/box.go` `Box`, add:

```go
	Ord int // 1-based ordinal when the box is a direct li child of an ol (0 otherwise)
```

In `fillFlowChildren` (box.go ~194), when numbering direct list-item children of an ordered list:

```go
	if (b.Tag == "ul" || b.Tag == "ol") && isListItem(child) {
		child.Marker = listMarker(b.Tag, nextDepth)
		if b.Tag == "ol" {
			ord++
			child.Ord = ord
		}
	}
```

(with `ord := 0` declared above the child loop). Numbering restarts per `ol` box; `start`/`reversed` attributes are not honored (the old walker ignored them too).

In `src/lib/html/block.go`, extend `RowMarker` and the marker attach in `flow`:

```go
type RowMarker struct {
	Type string // disc|circle|square|decimal
	X    int
	Ord  int // 1-based ordinal for a decimal marker of an ordered list (0 otherwise)
}
```

`flow`'s two marker attachments (block.go ~110-119) copy the ordinal from the owning box:

```go
	rows[first].Markers = append(rows[first].Markers,
		RowMarker{Type: c.Marker, X: cx, Ord: c.Ord})
```

(both the marker-only row and the attached case.)

- [ ] **Step 6: Pin the ordinal + Style.Label export shapes**

Add to `src/lib/html/block_test.go`:

```go
func TestBlockOrderedListNumbersItems(t *testing.T) {
	bs := buildBody(`<ol><li>one</li><li>two</li></ol>`)
	rs := LayoutBlock(bs, 200, mono(1), false)
	if len(rs) != 2 {
		t.Fatalf("rows = %d, want 2", len(rs))
	}
	for i, ord := range []int{1, 2} {
		if len(rs[i].Markers) != 1 || rs[i].Markers[0].Type != "decimal" || rs[i].Markers[0].Ord != ord {
			t.Fatalf("row %d markers = %+v, want decimal Ord %d", i, rs[i].Markers, ord)
		}
	}
	// a second ol restarts at 1
	bs = buildBody(`<ol><li>a</li></ol><ol><li>b</li></ol>`)
	rs = LayoutBlock(bs, 200, mono(1), false)
	if len(rs) != 2 || rs[1].Markers[0].Ord != 1 {
		t.Fatalf("second ol must restart at 1: %+v", rs)
	}
}
```

Mutation check: drop `child.Ord = ord` -> fails.

- [ ] **Step 7: Export `BodyStyle`**

In `src/lib/html/box.go`, factor the html/body style resolution (currently lines 75-84, the head of `Build`) so mail can read the body style stage 1 laid out with (D14), single cascade, without re-deriving it. `Build` needs both the body node (to walk its children) and the style, so the factored helper returns both; `BodyStyle` is the one-value public wrapper:

```go
// bodyCascade resolves the body element and its computed style under the
// cascade (html-element style inherited first when present). Build lays out
// content with it; BodyStyle exposes the style for stage 2's page
// background/contrast decision.
func bodyCascade(doc *xhtml.Node, rules []CSSRule) (*xhtml.Node, *Style) {
	body := findBody(doc)
	if body == nil {
		return nil, nil
	}
	root := &Style{}
	if body.Parent != nil && body.Parent.Type == xhtml.ElementNode && body.Parent.Data == "html" {
		root = StyleOf(body.Parent, root, rules)
	}
	st := StyleOf(body, root, rules)
	st.WS = effectiveWS("body", st)
	return body, st
}

// BodyStyle returns the body's computed style under the cascade, or nil for
// a document without a body. Stage 2 reads the page background and the
// contrast default from it.
func BodyStyle(doc *xhtml.Node, rules []CSSRule) *Style {
	_, st := bodyCascade(doc, rules)
	return st
}
```

Rewrite `Build`'s head (lines 74-84) to call `bodyCascade` and keep `findBody` called only there. Gate: `lib/html` suite still PASS.

- [ ] **Step 8: Run the gates and commit**

Run: `cd src && go test -count=1 ./lib/html/ && go test -count=1 -tags "lua mcp" ./... && go vet ./lib/html/ && gofmt -l lib/html/`
Expected: all PASS/clean. The mail walker path is untouched, so `src/mail` is byte-identical (its suite passing is the check).

Commit (code, no trailer): `feat(html): export the span line model, image display px, and list ordinals`

Self-review note in the commit body: none (trailers forbidden on code).

---

### Task 2: Stage-2 frame - constants, meter, facade skeleton, page background

**Files:**
- Create: `src/mail/html_stage2.go` (the new engine, additive; the old walker in `html.go` stays until Task 6)
- Modify: `src/mail/html.go` (route `renderHTML` to the new engine behind a switch) - temporarily
- Test: `src/mail/html_stage2_test.go`

**Context:** `renderMessage` (`thread.go:407`) calls `renderHTML(body, atts, width, labelLinks, dark, themeBG)`. The new engine must produce identical output on the pinned fixtures that do not touch the recorded divergences (plain paragraphs, backgrounds, alignment, punctuation, dark). Task 2 wires the frame and proves it on the vertical-flow + background pins before quantization of tables/images/lists is added.

- [ ] **Step 1: Write the failing frame test against the pinned paragraph/background behavior**

The LOCKED suite is the contract; do not edit it. Add `src/mail/html_stage2_test.go` with the entry that Task 3 will grow, starting with behavior already reachable by a plain-text vertical flow:

```go
package mail

// The stage-2 engine is exercised through the same facade as the walker; the
// locked html_*_test.go suite is the real contract. These tests pin the
// stage-2-only frame decisions (blank quantization, page background) before
// tables/images/labels land.

import (
	"strings"
	"testing"

	"notmutt/core"
)

func TestStage2ParagraphBlankAndBackground(t *testing.T) {
	lines := RenderHTML(`<body style="background-color:#f0f0f0"><p>a</p><p>b</p></body>`, nil, 0)
	// first row drops its gap: no leading blank
	if len(lines) == 0 || lines[0].Text != "a" {
		t.Fatalf("first line = %q, want content (no leading blank)", firstText(lines))
	}
	// exactly one blank between the paragraphs, carrying the mail bg
	if len(lines) != 3 || lines[1].Text != "" || lines[1].Bg != "#f0f0f0" || lines[2].Text != "b" {
		t.Fatalf("want a,b with one blank between: %q", linesText(lines))
	}
	if lines[0].Bg != "#f0f0f0" || lines[2].Bg != "#f0f0f0" {
		t.Fatalf("content lines must carry the mail bg")
	}
}

func TestStage2NoTrailingBlank(t *testing.T) {
	lines := RenderHTML("<p>one</p>", nil, 0)
	if len(lines) != 1 || lines[0].Text != "one" {
		t.Fatalf("a lone paragraph renders one line, no trailing blank: %q", linesText(lines))
	}
}
```

(Implement `firstText`/`linesText` helpers alongside, or reuse the `renderText` helper already in `html_spacing_test.go`.)

Run: `cd src && go test -count=1 ./mail/ -run TestStage2` - Expected: FAIL (the new frame does not exist yet; `RenderHTML` still routes to the walker, which actually already passes these two - see Step 4 for the real gate).

- [ ] **Step 2: Build the frame in `html_stage2.go`**

New file skeleton - constants, meter, and the page-background/`defaultBG` resolution (ported from the walker's html/body handling, D14). Layout width: clamp `width` to `[1, htmlWrapWidth]` cells, px = `width * charW`.

```go
package mail

// Stage-2 terminal renderer: consumes lib/html's px Row stream (LayoutBlock)
// and quantizes it to core.Line pager lines. All terminal knowledge lives
// here; lib/html stays px-pure. See docs/superpowers/specs/2026-09-03-html-layout-engine-design.md
// stage 2 + migration, and the stage-2 plan 6.

import (
	"strings"

	xhtml "golang.org/x/net/html"

	"notmutt/core"
	"notmutt/lib/html"
)

const (
	// charW is the px width of one terminal cell: the horizontal px<->cell
	// scale. It is forced by the locked TestImageDeclaredSizes (50% of the
	// 80-cell layout = 400px).
	charW = 10
	// lineH is the px height of one pager row; blank quantization divides
	// collapsed margin gaps by it. The base em is 16px, so a 1em gap is one
	// blank row.
	lineH = 16
)

// cellMeter measures text for stage-1 layout in px, where each runewidth
// terminal cell is charW px. Wrapping in px then equals the old cell wrapping.
type cellMeter struct{}

func (cellMeter) Width(s string) int { return html.TextWidth(s) * charW }

func (cellMeter) RuneWidth(r rune) int { return html.RuneWidth(r) * charW }
```

`html.RuneWidth` is added in Step 3 of this task - write Step 2 and Step 3 before the first gate run (the file does not compile until then). `cellMeter` implements `Metrics` (`Width`) and `RuneStepper` (`RuneWidth`) per metrics.go:12-22, so `cutText` steps per rune instead of re-measuring prefixes.

The engine (parsing stays in the facade, Step 4 - this file holds only renderStage2 and its helpers):

```go
func renderStage2(doc *xhtml.Node, atts []Attachment, widthPx int, labelLinks, dark bool, themeBG string) ([]core.Line, []string) {
	rules := html.ParseStyleSheets(doc)
	boxes := html.Build(doc, rules)
	if len(boxes) == 0 {
		return nil, nil // caller falls back to the raw text
	}
	bs := html.BodyStyle(doc, rules)
	bg, fg := pageColors(bs, dark, themeBG)
	q := &stage2{atts: atts, defaultBG: bg, defaultFG: fg, dark: dark,
		themeBG: themeBG, linesLeft: maxHTMLLines}
	if labelLinks {
		q.injectLinkLabels(boxes) // Task 5: numbered labels, runs before layout
	}
	rows := html.LayoutBlock(boxes, widthPx, cellMeter{}, true)
	q.emitRows(rows) // Tasks 3-4
	if q.truncated {
		q.lines = append(q.lines, core.Line{Text: "[content truncated]", Kind: core.LineBody})
	}
	if len(q.lines) == 0 {
		return nil, q.links
	}
	return q.lines, q.links
}
```

// pageColors resolves the page background and the default foreground for
// unstyled text (the walker's html/body handling, D14): the mail's declared
// bg (light-declared reflects onto themeBG in dark mode; dark-declared passes
// through the luma gate); unstyled text reads the contrast fg on that bg in
// light mode, the theme text ("") in dark mode (the page bg IS the theme bg).
func pageColors(bs *html.Style, dark bool, themeBG string) (bg, fg string) {
	if bs != nil && bs.Bg != "" {
		if dark && html.IsLight(bs.Bg) {
			bg = html.AdaptBG(bs.Bg, themeBG)
		} else {
			bg = bs.Bg
		}
	} else if dark {
		bg = themeBG
	} else {
		bg = "#ffffff"
	}
	if !bs.FgSet {
		if dark {
			fg = ""
		} else {
			fg = html.ContrastFG(bg)
		}
	} else {
		fg = bs.Fg
	}
	return bg, fg
}
```

(Note: this mirrors the walker's logic at mail/html.go:163-195. The body style from `BodyStyle` already carries an html-element background by inheritance, so one read covers both - `bs.Bg` nonempty means the page declared one.)

- [ ] **Step 3: Add `RuneWidth` to lib/html's public text helpers**

`cellMeter.RuneWidth` needs a lib export. In `src/lib/html/html.go` next to `TextWidth`:

```go
// RuneWidth reports a rune's terminal cell width (0 for control chars, 2 for
// wide/emoji) - the rune-level twin of TextWidth for per-rune stepping.
func RuneWidth(r rune) int { return runewidth.RuneWidth(r) }
```

(`runewidth` is already imported in html.go.) Add a tiny lib test:

```go
func TestRuneWidth(t *testing.T) {
	if html.RuneWidth('a') != 1 || html.RuneWidth('界') != 2 || html.RuneWidth(0x01) != 0 {
		t.Fatalf("RuneWidth wide/control mismatch")
	}
}
```

(in the internal `package html` test file - call it `RuneWidth('a')` directly.)

- [ ] **Step 4: Wire `renderHTML` in `mail/html.go` to the new engine**

This is the temporary side-by-side switch. In `mail/html.go`, `renderHTML` is the walker entry (`htmlWalker`). Rename the existing one to `renderHTMLWalker` and add:

```go
// renderHTML routes to the stage-2 engine. The walker (renderHTMLWalker)
// stays until Task 6; flip this to compare outputs on real mail.
func renderHTML(body string, atts []Attachment, width int, labelLinks, dark bool, themeBG string) ([]core.Line, []string) {
	if width <= 0 || width > htmlWrapWidth {
		width = htmlWrapWidth
	}
	return renderStage2HTML(body, atts, width, labelLinks, dark, themeBG)
}
```

Define `renderStage2HTML` (the public-shaped stage-2 entry that parses and clamps like the old facade head, mail/html.go:50-56, then runs the engine). Keep `renderHTMLWalker` reachable for the parity harness (Task 6) - do not delete anything yet. The locked `html_*_test.go` tests call `RenderHTML`/`renderHTML`/`RenderHTMLWithLinks`, which now route to stage 2.

```go
// renderStage2HTML is the stage-2 facade entry: parse + clamp (mirrors the
// old renderHTML head) then the engine. width is in cells; renderStage2 lays
// out at width*charW px.
func renderStage2HTML(body string, atts []Attachment, width int, labelLinks, dark bool, themeBG string) ([]core.Line, []string) {
	doc, err := xhtml.Parse(strings.NewReader(body))
	if err != nil {
		return nil, nil // x/net/html recovers from malformed input by spec; guard anyway
	}
	return renderStage2(doc, atts, width*charW, labelLinks, dark, themeBG)
}
```

The routing `renderHTML` in `html.go` (Step 4's first hunk) clamps `width` to `htmlWrapWidth` before the call, so `renderStage2HTML` receives a width already in range.

- [ ] **Step 5: Implement the minimal vertical-flow emitter (plain rows only)**

For Task 2, `emitRows` handles only non-table, non-HR, single-line text rows without markers or images - enough for `<p>`/`<div>` bodies (Task 3 generalizes). The generic structure you will grow:

```go
type stage2 struct {
	atts      []Attachment
	lines     []core.Line
	links     []string
	linesLeft int
	truncated bool
	defaultBG string
	defaultFG string
	dark      bool
	themeBG   string
	firstRow  bool // first content row's gap drops (D5)
}

func (q *stage2) emitRows(rows []html.Row) { ... } // Tasks 3-4 dispatch
```

Implement a `blankRows(gapPx int) int` = `int(math.Round(float64(gapPx) / lineH))` and the drop-first-gap rule, plus a text-row emitter that maps each `Row.Line` span to runs (Task 3 fills in binding/tabs/images) - for Task 2 just the plain `Text` -> run path with `defaultFG`/`dark` applied, alignment lead from `Row.X`.

Run: `cd src && go test -count=1 ./mail/ -run 'TestStage2|TestHTML|TestRenderHTML'`
Expected: PASS on `TestStage2ParagraphBlankAndBackground`/`TestStage2NoTrailingBlank` (written Step 1) and on the LOCKED paragraph/background pins (`TestRenderHTMLBackground`, `html_align_test.go`, `html_dark_test.go`, the parts of `htmlparse_test.go` that do not yet need images/labels). Any that still fail must fail only because tables/images/labels are not yet handled (Tasks 3-5) - list those in your report, do not weaken them.

- [ ] **Step 6: Gates + commit**

Run: `cd src && go test -count=1 ./mail/ && go test -count=1 -tags "lua mcp" ./...`
Commit: `feat(html): stage-2 facade frame with px quantization and page colors`

---

### Task 3: Quantize text rows (binding, tabs, markers, hr, images)

**Files:**
- Modify: `src/mail/html_stage2.go`
- Test: `src/mail/html_stage2_test.go` (new pins), plus the locked `html_spacing_test.go`/`htmlparse_test.go` image pins as the contract

**Goal:** the full text-row emitter: span stream -> runs with D6 binding and empty-span handling, D7 tab expansion + F1 sanitize, D9 image own-line vs inline + tracking-pixel strip + `core.Image` resolution, D10 markers, D11 hr, D8 alignment lead. A non-table `Row` has one `Line` (or is marker-only/hr); table strips (`Cells`) are Task 4.

- [ ] **Step 1: Write the failing pins for the stage-2-only behaviors**

Add to `html_stage2_test.go` (each fails before its logic exists; keep them; they pin the plan decisions, not the walker):

```go
func TestStage2BindsSourceSpaceBeforePunctuation(t *testing.T) {
	// a source space before a comma hugs in the terminal (deliberate
	// divergence from weasyprint; the old walker's bindsLeft)
	lines := RenderHTML(`<p>Reply <span>alpha</span> <span>, beta</span> now</p>`, nil, 0)
	text := renderText(lines)
	if strings.Contains(text, "alpha ,") || !strings.Contains(text, "alpha, beta") {
		t.Fatalf("space before punctuation must hug: %q", text)
	}
}

func TestStage2TabExpandsInPreservedText(t *testing.T) {
	lines := RenderHTML("<pre>a\tb</pre>", nil, 0)
	text := renderText(lines)
	if strings.Contains(text, "\t") {
		t.Fatalf("a literal tab must not reach the pager (F1): %q", text)
	}
	if !strings.Contains(text, "a       b") { // tab to the 8-column stop
		t.Fatalf("tab must expand to the 8-column stop: %q", text)
	}
}

func TestStage2EmptySpanAfterControlDoesNotDoubleSpace(t *testing.T) {
	lines := RenderHTML("<p>lead \x01 tail</p>", nil, 0)
	text := renderText(lines)
	if strings.Contains(text, "  ") || strings.Contains(text, "\x01") {
		t.Fatalf("a sanitized-empty control node must leave single spacing: %q", text)
	}
}

func TestStage2ImageOwnLineAndInline(t *testing.T) {
	// isolated image -> own Image line; image sharing its line -> inline ImagePos
	lines := RenderHTML(`<p>before <img src="https://x.example.com/i.png" width="24" height="24"> after</p>`, nil, 80)
	own, inline := 0, 0
	for _, l := range lines {
		if l.Image != nil {
			own++
		}
		inline += len(l.Imgs)
	}
	if own != 0 || inline != 1 {
		t.Fatalf("a shared-line image must be inline (0 own, 1 inline), got own=%d inline=%d", own, inline)
	}
	lines = RenderHTML(`<img src="https://x.example.com/j.png" width="200" height="100">`, nil, 80)
	own, inline = 0, 0
	for _, l := range lines {
		if l.Image != nil {
			own++
		}
		inline += len(l.Imgs)
	}
	if own != 1 || inline != 0 {
		t.Fatalf("an isolated image must be its own line (1 own, 0 inline), got own=%d inline=%d", own, inline)
	}
}

func TestStage2MarkerGutterAndIndent(t *testing.T) {
	lines := RenderHTML(`<ul><li>one</li><li>two</li></ul>`, nil, 0)
	// each item is one contiguous line (no blank between); the text starts at
	// col 4 (the 40px ul gutter at charW 10) and its hanging marker occupies
	// the gutter cell just before (col 3 for a 1-cell disc): the render is
	// "   •one" / "   •two"
	if len(lines) != 2 {
		t.Fatalf("want two contiguous item lines: %q", renderText(lines))
	}
	for i, want := range []string{"one", "two"} {
		text := lines[i].Text
		if j := strings.Index(text, want); j != 4 {
			t.Fatalf("line %d = %q: %q must start at col 4 (40px gutter), found col %d", i, text, want, j)
		}
	}
}
```

(The marker-glyph assertion is deliberately loose here - Task 3 pins the exact glyph data separately in a map test.)

- [ ] **Step 2: The span-to-run emitter**

Replace Task 2's minimal text-row path with the full one, factored so Task 4's strip fragments reuse it. The shared unit is a horizontal line accumulator plus a row-content emitter:

```go
// acc is one horizontal pager line under construction. Runs sit at absolute
// cell columns; pad() materializes the blanks before a column as a space run
// so core.Line.Text stays the concatenation of the runs' text (the pager
// paints runs over Line.Text). Blank lines have Text "" and only a Bg.
type acc struct {
	col  int // current text-end column
	runs []core.Run
	imgs []core.ImagePos
}

func (a *acc) pad(to int) {
	if to <= a.col {
		return
	}
	a.runs = append(a.runs, core.Run{Text: strings.Repeat(" ", to-a.col)})
	a.col = to
}

// emitRowContent renders one row into a: its hanging gutter markers (D10,
// each at its own marker column), then the line's spans with D6 binding, D7
// tab expansion + F1 sanitize, and D9 images. ownImages lets a top-level row
// claim an isolated image (the sole span, no markers) as an own-line image,
// which it returns non-nil; strip fragments always pass false so their images
// stay inline. A declared 1x1 pixel (D9) drops inline (no run, no width); a
// whole row of one dropped pixel is skipped by the dispatch (skipRow), never
// here. The caller reached the content column with a.pad(round(r.X/charW)).
func (q *stage2) emitRowContent(a *acc, r html.Row, ownImages bool) *core.Image
```

Behavior (D6, D7, D9, D13, D14):
- Alignment/inset lead (D8): the row's content column is `c := round(r.X / charW)`. First lay down each marker at its own column: marker column = `round(mk.X/charW) - glyphCells(mk)` (the glyph hangs in the gutter, its last cell one before its item's content edge - for a decimal, glyph text is `fmt.Sprintf("%d.", mk.Ord)`, else `markerGlyphs[mk.Type]`); `a.pad(glyphCol)` then append the glyph run. Then `a.pad(c)` reaches the content column (a no-op after markers whose X equals r.X).
- Text span: expand tabs (`expandTabs`), then `core.SanitizeControls`. If empty after sanitize: drop it AND clear `pendingSpace` (an emptied span leaves no doubled space) - the pinned control test. Else if `pendingSpace` and the sanitized text's first rune binds left (`. , ; : ! ? % _ ) ] }`), DROP the pending space (the punctuation hugs); then commit the pending space (if any) as a one-space run, append the text, and reset `pendingSpace`.
- `Sep` span: set `pendingSpace = true` (do not commit yet - a following binding or empty span may drop it; commit it before the next text that does not bind, or at flush).
- Image span: resolve the image (`boxImage`, Step 3). If declared `1x1` (tracking pixel, D9): skip it entirely. Otherwise: if `ownImages` and this row's spans are exactly `[this image]` and it has no markers => return the `*core.Image` (own-line; the caller emits the image line). Else inline: append a `core.Run{Text: img.Alt, Image: img}` (its `Bg` per runFor) and a `core.ImagePos{img, X: a.col}` at the current column, advancing `a.col` by the image's used cells `ceil(uW/charW)`.
- Run style: `q.runFor(span.St)` (the ported dark/theme adaptation, D14) applies `Fg/Bg/attrs`, folding in `q.defaultFG` for unstyled light text, plus the `Label` bit (Task 5). Merge adjacent runs whose effective `core.Run` equals (`==`), except label runs never merge into non-label text and vice versa (the `Label` bit is part of the value).
- Truncation: decrement `q.linesLeft` per emitted line; stop at 0 (set `truncated`).

The row emitter and the line appenders:

```go
func (q *stage2) emitTextRow(r html.Row) {
	var a acc
	if img := q.emitRowContent(&a, r, true); img != nil {
		q.addLine(core.Line{Image: img, Text: img.Alt, Bg: q.defaultBG})
		return
	}
	q.addLine(core.Line{Text: a.text(), Runs: a.runs, Imgs: a.imgs, Bg: q.defaultBG})
}

// addLine appends one line and decrements the render budget.
func (q *stage2) addLine(l core.Line) {
	if q.linesLeft <= 0 {
		q.truncated = true
		return
	}
	q.linesLeft--
	q.lines = append(q.lines, l)
}

func (a *acc) text() string {
	var sb strings.Builder
	for _, r := range a.runs {
		sb.WriteString(r.Text)
	}
	return sb.String()
}
```

A blank line is `q.addLine(core.Line{Bg: q.defaultBG})` (empty Text, no runs). Own-line image lines (`core.Line.Image` set) and inline `core.Line.Imgs` both carry the mail bg exactly like the walker's image lines.

- [ ] **Step 3: Resolve a box's image**

Port `resolveImage`/`imgSize` to read the image `*Box` (D3, D4):

```go
// boxImage resolves an image box to its core.Image: cid:/data: bytes or a
// remote URL-only placeholder (the render never decodes - the TUI does on the
// render-images key). DispW/DispH are the DECLARED display px from the box
// (stage-1 resolved, % against the actual containing width); nil when the src
// resolves to nothing.
func (q *stage2) boxImage(b *html.Box) *core.Image
```

Behavior: read `src := html.Attr(b.Node, "src")`; cid: looks up `q.atts` ContentID (the current `resolveImage` match); `data:image/...;base64,` decodes; http(s) is URL-only. No src/data/url => nil (an unresolved image still renders its alt/`[image]` text as a text run - it is NOT an image line). Alt from the `alt` attr, else `[image]` (sanitized). `DispW, DispH` from `b.ImgDisp()` (declared px, D3). Note the old `resolveImage` returned nil only for un-resolvable src; a resolvable-but-declared-size image always returned a `core.Image`. Match that: an image whose `src` resolves to nothing renders `[image]`/alt as plain text (old walker `addWord("[image]")`), NOT a `core.Image` line.

- [ ] **Step 4: Markers, hr, own-line images in `emitRows`**

Extend the row dispatch:

```go
func (q *stage2) emitRows(rows []html.Row) {
	for _, r := range rows {
		if len(r.Cells) > 0 {
			q.emitStrip(r) // Task 4
			continue
		}
		if q.skipFor(r) { // tracking-pixel-only row (D9): no blanks, no line
			continue
		}
		if !q.firstRow && r.Gap > 0 {
			q.blankLines(r.Gap) // D5; the first row drops its gap
		}
		q.firstRow = true
		switch {
		case r.HR:
			q.emitHR(r)
		case len(r.Line.Atoms) > 0:
			q.emitTextRow(r)
		default:
			q.emitMarkerRow(r) // marker-only (empty li / textless nested)
		}
	}
}
```

- `blankLines(gapPx)` (D5): `n := blankRows(gapPx)` = `int(math.Round(float64(gapPx) / lineH))`; append `n` `core.Line{Bg: defaultBG}` through `q.addLine` (budget enforced there). `skipFor` runs first so a dropped pixel row contributes no blanks.
- `skipFor(r)`: report whether the row's only content is a single 1x1-declared pixel - `len(r.Line.Atoms) == 1 && r.Line.Atoms[0].Img != nil && len(r.Markers) == 0` and `w, h := Atoms[0].Img.ImgDisp(); w == 1 && h == 1`. Such a row emits nothing (the pinned `TestTrackingPixelStripped` drop).
- `emitHR(r)` (D11): one rule line built in an `acc`: a single run of `round(r.W/charW)` copies of `ruleGlyph` (`var ruleGlyph = "─"`, next to the marker glyph data). `Bg` `defaultBG`; `r.W` is the content px width, so the rule spans the whole content box.
- `emitTextRow(r)` is Step 2's: `var a acc; if img := q.emitRowContent(&a, r, true); img != nil { q.addLine(core.Line{Image: img, Text: img.Alt, Bg: q.defaultBG}); return }; q.addLine(core.Line{Text: a.text(), Runs: a.runs, Imgs: a.imgs, Bg: q.defaultBG})`. The D8 alignment/inset lead and the D10 gutter markers live inside `emitRowContent`, not here.
- `emitMarkerRow(r)` (D10): a row with markers but no spans (empty `li`, or an item whose content collapsed away - stage-1 emits it so the marker still gets a line). Build an `acc`, lay down each marker glyph exactly as `emitRowContent` does (glyph col = `round(mk.X/charW) - glyphCells(mk)`), then end the line - no trailing pad toward a content column. `q.addLine(core.Line{Text: a.text(), Runs: a.runs, Bg: q.defaultBG})`.
- Own-line and inline image lines (D9) both carry `Bg = defaultBG` (Step 2's appenders set it), so the pinned image lines match the walker's.

Glyph data map (D10):

```go
var markerGlyphs = map[string]string{
	"disc":   "•",
	"circle": "◦",
	"square": "▪",
}

// glyphText renders one marker's display text: the numbered "N." for an
// ordered item (Ord is 1-based; 0 defended as 1 since stage-1 always stamps
// it), the glyph-map shape otherwise.
func glyphText(mk html.RowMarker) string {
	if mk.Type == "decimal" {
		if mk.Ord == 0 {
			mk.Ord = 1
		}
		return fmt.Sprintf("%d.", mk.Ord)
	}
	return markerGlyphs[mk.Type]
}

// glyphCells is a marker glyph's terminal width: its hang ends one cell
// before the owning item's content edge, so the glyph occupies
// [col-glyphCells, col) in the gutter.
func glyphCells(mk html.RowMarker) int { return html.TextWidth(glyphText(mk)) }
```

- [ ] **Step 5: Gates - the locked spacing + image + dark pins**

Run: `cd src && go test -count=1 ./mail/`
Expected: PASS on `TestHTMLInlineBoundaryHugsPunctuation`, `TestHTMLUnderscoreFragmentHugs`, `TestHTMLControlNodeDoesNotPanic`, `TestImageDeclaredSizes`, `TestTrackingPixelStripped`, `TestDisplayNotInheritedRender`, `TestRenderHTMLBackground`, `TestRenderHTMLDark*`, the align tests, and your Task-3 pins. These LOCKED tests passing is the migration contract; do not edit them. If one fails, the stage-2 rule it names is wrong - fix the rule, never the test. Report which pins you could not satisfy and why.

- [ ] **Step 6: Gates + commit**

Full: `cd src && go test -count=1 ./mail/ && go test -count=1 -tags "lua mcp" ./...`
Commit: `feat(html): stage-2 quantization of text rows, images, markers, and hr`

---

### Task 4: Table strips and the remaining pinned table behavior

**Files:**
- Modify: `src/mail/html_stage2.go`
- Test: `src/mail/html_stage2_test.go`, locked `htmlparse_test.go` table pins

**Goal:** render `Row.Cells` strips (D12) - the last major render surface. A strip is one horizontal line; per-cell fragments (each a `Row` with its own `X`/spans/markers/image and possibly nested `Cells`) render at their absolute cell columns; a cell's declared `Bg` paints its runs (locked `TestRenderHTMLBackground` cell `bgcolor` case). Cell alignment (`right`/`center`) is already in stage-1 `X`; the pinned `TestHTMLNestedRightCellDoesNotLeak` must pass from stage-1 geometry (the nested table's right cell sits at a larger `X`, content after it returns to the outer cell's left `X` - verify, do not re-implement the walker's one-shot split).

- [ ] **Step 1: Write the failing strip pins**

```go
func TestStage2CellBackgroundPaintsItsRuns(t *testing.T) {
	lines := RenderHTML(`<table bgcolor="#dddddd"><tr><td>cell</td></tr></table>`, nil, 0)
	found := false
	for _, l := range lines {
		for _, r := range l.Runs {
			if r.Bg == "#dddddd" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("the bgcolor cell's run must carry the cell bg")
	}
}

func TestStage2MultiColumnStripPlacesCells(t *testing.T) {
	// two narrow cells: both texts present on one line, second cell starts at
	// a column >= the first cell's text end
	lines := RenderHTML(`<table><tr><td>aa</td><td>bb</td></tr></table>`, nil, 0)
	if len(lines) != 1 {
		t.Fatalf("want one strip line, got %d: %q", len(lines), renderText(lines))
	}
	text := lines[0].Text
	i, j := strings.Index(text, "aa"), strings.Index(text, "bb")
	if i < 0 || j <= i+1 {
		t.Fatalf("cells must both render, second after the first: %q", text)
	}
}
```

Run: fail (strips unhandled).

- [ ] **Step 2: Implement `emitStrip`**

```go
// emitStrip renders one table grid row (Row.Cells) as one horizontal line:
// fragments side by side at their absolute px X (D12). The strip row owns the
// table's seam gap (D5), then each fragment renders at its own column through
// appendRow; recursion places nested strips inline at their shifted columns.
// A fragment's cell bg rides its runs: a cell's text leaves share the cell
// box's style pointer, so the run adaption (runFor) already paints the cell
// bg - no extra region machinery. The strip line itself carries defaultBG.
func (q *stage2) emitStrip(r html.Row) {
	if !q.firstRow && r.Gap > 0 {
		q.blankLines(r.Gap) // D5
	}
	q.firstRow = true
	var a acc
	for _, f := range r.Cells {
		q.appendRow(&a, f)
	}
	q.addLine(core.Line{Text: a.text(), Runs: a.runs, Imgs: a.imgs, Bg: q.defaultBG})
}

// appendRow places one fragment (a Row) into a at its absolute column, then
// renders its content. A fragment whose Cells are set hosts a nested table:
// its own fragments are already shifted to absolute X (shiftRow), so render
// them into a directly. A cell is either table flow or inline content at the
// strip level - cellRows separates them into rows - so a fragment is not both
// (guard on Cells first anyway). ownImages is false: a strip has no own-line
// image; every cell image is inline on the strip (D9).
func (q *stage2) appendRow(a *acc, f html.Row) {
	a.pad(round(f.X / charW))
	if len(f.Cells) > 0 {
		for _, inner := range f.Cells {
			q.appendRow(a, inner)
		}
		return
	}
	q.emitRowContent(a, f, false) // markers/lead/spans, images inline only
}
```

`appendRow` reuses the exact emitter from Task 3: `emitRowContent` pads to the fragment's own column, lays down its hanging markers in the gutter, and renders its spans with the same binding/tabs/sanitize/inline-image rules - only `ownImages` differs (false on a strip, so an isolated cell image stays inline and never claims an own-line `core.Line.Image`).

Placement rounding: fragment `X` includes the cell's pad/border offset; `round(X/charW)` may make two adjacent narrow fragments share or abut a column - recorded divergence (D12), do not force a minimum gutter.

A strip emits its fragments on one line; a later grid row that belongs only to a taller (nested-table or multi-line) cell is its own `Row.Cells` strip carrying that cell's next fragment, which `emitRows` dispatches normally.

- [ ] **Step 3: Verify the locked table pins + NestedRightCell**

Run: `cd src && go test -count=1 ./mail/`
Expected: PASS on `TestRenderHTMLLinks` (cell links render), `TestDisplayNotInheritedRender`, `TestTrackingPixelStripped` (cell pixel), `TestHTMLNestedRightCellDoesNotLeak`, `TestRenderHTMLBackground` (cell bg). Report the actual right-cell and cell-image geometry you observe against the pins.

- [ ] **Step 4: Gates + commit**

Full suite green. Commit: `feat(html): stage-2 table strip quantization`

---

### Task 5: Link labels (F key) via the box-tree transform

**Files:**
- Modify: `src/mail/html_stage2.go`
- Test: `src/mail/html_stage2_test.go`, locked `htmlparse_test.go` link pins (`TestRenderHTMLLinks`, `TestBlockAnchorLinks`)

**Goal:** `RenderHTMLWithLinks` produces inline `[N]` labels in document order and the ordered `links` list, identical to the locked pins. The transform (D13) runs before layout when `labelLinks` is on; the unlabeled render never sees a label.

- [ ] **Step 1: Write the failing stage-2 label pins**

Keep them light (the locked `TestRenderHTMLLinks`/`TestBlockAnchorLinks` are the contract):

```go
func TestStage2LabelsAreOwnRuns(t *testing.T) {
	lines, _ := RenderHTMLWithLinks(`<p><a href="https://a.example.com/x">alpha</a> and beta</p>`, nil, 80)
	// the label "[1]" precedes the anchor text and is its own run (never merged
	// with "alpha")
	joined := renderText(lines)
	if !strings.Contains(joined, "[1]alpha") {
		t.Fatalf("label must sit at the link start: %q", joined)
	}
	found := false
	for _, l := range lines {
		for _, r := range l.Runs {
			if r.Text == "[1]" && r.Label {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("no standalone label run found")
	}
}
```

- [ ] **Step 2: Implement `injectLinkLabels`**

Walk the box tree in document order (pre-order; box order mirrors DOM order through Build). Maintain `q.links` and a counter `n = len(q.links)`.

```go
func (q *stage2) injectLinkLabels(bs []*html.Box) { q.injectInto(bs, 0) }
```

- Anchor labels: for a box with `Tag == "a"` and `href := html.Attr(b.Node, "href")` nonempty, sanitize the href, append to `q.links`, and insert a label box at the anchor's lead inline position. Insertion target: descend into `b.Children`; if the first child is a `RoleBlock` anonymous run (`Tag == ""`, role block) insert at the front of ITS children, else insert at the front of `b.Children`. The label box:

```go
// labelBox is a synthesized F-key marker: its own style copy with Label set,
// so stage-2 run building keeps it a separate run (D13).
func labelBox(parent *html.Box, text string) *html.Box {
	cp := *parent.St // the anchor/text style; do not mutate the shared pointer
	cp.Label = true
	return &html.Box{Role: html.RoleText, St: &cp, Text: text}
}
```

`Role` and `RoleText` are exported (box.go). The insert must keep the anchor's own children consistent (if `b.Children` are uniformly inline leaves, prepend the label as a leaf; if the anchor blockified into anonymous runs, prepend into the first run so the label leads the anchor's content on its line).

- Bare-URL labels: visit every `RoleText` box anywhere; tokenize its `Text` on whitespace; for each token `f`, if `ls := html.Links(f, true); len(ls) > 0`, append `ls[0]` to `q.links` and insert a `labelBox` before the token. Inserting inside a `RoleText` box requires splitting it into `[before][label][tokenText][after]` boxes; replace the single text box with that sequence in its parent's `Children`. `RoleText` boxes share the run's style pointer - each piece keeps the same `St` (share it; do not mutate). Only when a token is a URL does the token text still render (old walker added the label AND the token).

Both insertions assign `"[N]"` with `N = len(q.links)` at the moment of the append (document order across anchors and bare URLs, mirroring the old single DOM walk).

- [ ] **Step 3: Wire the Label bit through run building**

In Task 3's `runFor`, set `r.Label = span.St.Label` and, when merging adjacent runs, treat the label bit as part of the run value (two adjacent identical label runs may merge; a label run never merges with non-label text because the bits differ). `Style.Label` must never propagate into ordinary content (it is only set on synthesized boxes; `StyleOf` never sets it).

- [ ] **Step 4: Verify locked link pins**

Run: `cd src && go test -count=1 ./mail/`
Expected: PASS on `TestRenderHTMLLinks` (labels in doc order, unlabeled render carries none) and `TestBlockAnchorLinks` (display:block anchors in cells still label). These are the contract.

- [ ] **Step 5: Gates + commit**

Full suite green. Commit: `feat(html): stage-2 link-label injection for the F-key render`

---

### Task 6: Cutover - delete the walker, finish the suite, update docs

**Files:**
- Modify: `src/mail/html.go` (delete `renderHTMLWalker` and all walker machinery), `src/mail/html_stage2.go` (final tidy), `docs/html-rendering-analysis.md`
- Test: full locked suite; a temporary side-by-side parity harness if needed

**Goal:** the walker is gone; the stage-2 engine is the only renderer; every locked mail test passes; docs describe the new engine.

- [ ] **Step 1: Confirm parity on the pinned fixtures**

Before deleting, run a one-off comparison of `renderHTMLWalker` vs the stage-2 path across every fixture body in the locked `html_*_test.go` files and report the exact output diff. Every diff must be explainable by a recorded divergence (D5-D13 + appendix) or be empty. Where a diff is NOT explainable by a decision, treat it as a stage-2 bug and fix it (never weaken a locked test). Record the per-fixture diff table in the task report.

- [ ] **Step 2: Delete the walker from `mail/html.go`**

Delete: `htmlWalker`, `walk`, `addText`, `addWord`, `flush`, `emitLine`, `runWords`, `runFor` (superseded by the stage-2 `runFor`), `word`, `cellLine`, `image`, `resolveImage`, `imgSize`, `isTrackingPixel`, `bindsLeft` (if unused by stage 2 - the stage-2 binding is its own), `table`, `cellRows`, `collectCell`, `wrapWords`, `blockTags`, `skipTags`, `isBlock`, `blockAlign`, `anchorLabel`, `collectStyleBlocks`, `cellWidth`, `joinWordText`. Keep `sanitize`, the facade signatures, and anything the stage-2 engine still shares. Move any shared helper the engine needs into `html_stage2.go`. Confirm `html.go` now holds only the facade, constants, and helpers; `thread.go`'s `renderHTML` call is unchanged.

- [ ] **Step 3: Full locked suite**

Run: `cd src && go test -count=1 ./mail/ && go test -count=1 -tags "lua mcp" ./... && go vet ./... && gofmt -l .`
Expected: all PASS. Every locked test (`TestRenderHTMLBackground`, the align/spacing/dark/image/link pins) passes unweakened - none edited except the mechanical atom-rename in Task 1 and relocation notes where a behavior moved surface (spec migration: "the test follows it to the stage-2 surface"; here none moved because the facade signatures are unchanged).

- [ ] **Step 4: Docs**

Update `docs/html-rendering-analysis.md` to describe the stage-2 engine (the current doc describes the walker - spec Docs section): parse -> Build -> LayoutBlock -> Row stream -> stage-2 quantization, the divergence appendix table, and the px frame constants. Record the Divergences appendix below into the doc.

- [ ] **Step 5: Gates + record commit**

Full suite + vet + gofmt. Commit code first: `refactor(html): delete the walker, stage-2 engine is the renderer`. Then commit the docs update with `Co-Authored-By: Deepseek`.

---

## Appendix: recorded divergences (old walker -> stage-2 engine)

Each is a deliberate consequence of CSS-true layout; none is pinned by a locked mail test. The migration gate is the locked suite, not byte parity.

| # | Behavior | Old walker | Stage-2 engine |
| --- | --- | --- | --- |
| A | `<hr>` | invisible block boundary | a visible `─` rule row (D11) |
| B | list items | blank line between items, marker inline at col 0 | contiguous items, 4-col gutter indent, hanging marker (D10) |
| C | inter-column gutter | 2 blank cells | px spacing (<1 cell at charW 10): columns abut (D12) |
| D | main-flow inline image | `x<img>y` splits to 3 lines | shares the line (inline ImagePos); isolated images still own-line (D9) |
| E | ordered lists | inline `1.` counter | hanging `N.` glyph from the box ordinal (D10) |
| F | heading/`blockquote` rhythm | one blank between any two content blocks | the same, for UA margins: they are all 16-25 px and round to exactly one blank; only author-set margins >= 32 px give 2+ blanks (D5) |
| G | list/quote insets | none (col 0 text) | content indented by the real 40px padding/margins (D8) |
| H | blank lines carry bg | yes | yes (unchanged, pinned) |

Also record in `TODO.org` (never auto-commit over sibling edits):
- marker glyph transforms are a mail-side data map today; a config override (R11) is future work.
- `Style.Label` is a renderer-synthesized flag; a future Lua/RPC consumer must not treat it as authored content.

## Final gate (after Task 6)

- `cd src && go test -count=1 -tags "lua mcp" ./...` all green, locked `html_*_test.go` unweakened.
- `go vet ./...`, `gofmt -l` empty.
- Side-by-side diff table from Task 6 Step 1 recorded in the plan/summary.
- BUGS.org: append any newly-found OPEN renderer divergences discovered during cutover.

## Self-review (writing-plans)

- **Spec coverage:** stage-2 section (280-311): quantization D5, px->cells D1/D8, colors/dark D14, markers D10, punctuation D6, image rows/reserve D9/D3, label/link + budgets D13 (kept unchanged); Migration section (313-338): side-by-side -> Tasks 2/6, pinned suite carried unweakened -> the locked tests are each task's contract, walker delete after parity -> Task 6, facade signatures -> D1/file map.
- **Placeholder scan:** no TBD/TODO-in-plan; each port names its source lines or the plan decision; the two `html_stage2.go` open functions (`emitStrip` nesting, `renderStage2HTML` parse) are drafted in their tasks.
- **Type consistency:** `Span{Text,St,WS,Sep,Img}`, `piece{s Span; br bool}`, `RowMarker{Type,X,Ord}`, `Box.Ord`, `Style.Label`, `imgRes.dispW/dispH`, `Box.ImgDisp()/ImgUsed()`, `html.RuneWidth`, `BodyStyle` are defined once and used consistently across tasks; the stage-2 names (`stage2`, `emitRows`, `emitStrip`, `emitTextRow`, `emitMarkerRow`, `emitHR`, `rowSpans`, `boxImage`, `runFor`, `pageColors`, `injectLinkLabels`, `labelBox`, `blankRows`) are introduced in Task 2 and reused unchanged.
