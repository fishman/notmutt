# HTML layout engine design (weasyprint-shaped, two-stage)

Date: 2026-09-03

## Context

Two HTML renderers exist:

- On-screen: `src/mail/html.go` - a DOM-stream walker (x/net/html parse,
  `lib/html` cascade, one-pass line emission). It diverges from a real
  browser layout in exactly the areas weasyprint gets right.
- Export: `src/app/export.go` runs the `weasyprint` subprocess over the
  raw html part with `printDoc`'s injected stylesheet (`@page`, paper
  size, white-space normalization).

The print normalization fix (commit history, printDoc) exposed the
divergence surface: nowrap lines that set a page-wide min-content, images
clipped at the paper edge, `<pre>` spacing lost, table widths not capped.
A study of `references/WeasyPrint` (with tinycss2/tinyhtml5/cssselect2 as
the parse/cascade references) extracted weasyprint's decision rules for
four semantic dimensions: tables, wrapping & whitespace, block flow &
margins, images & replaced elements.

This spec reworks the on-screen renderer into a weasyprint-shaped layout
engine so viewer output aligns with what weasyprint outputs for the same
mail. Approved decisions are normative: two-phase box tree (approach A),
weasyprint-faithful stage 1 with a terminal stage 2, side-by-side
migration, pinned behaviors preserved.

## Goals

- Viewer renders mail the way weasyprint prints it for the four chosen
  dimensions: auto-layout tables capped at the available width,
  per-column sizing, colspan/rowspan; a real white-space model (normal,
  pre, pre-wrap, pre-line) with cross-boundary collapse; CSS margins with
  sibling collapse for block rhythm; replaced-image sizing with intrinsic
  ratio and max-width cap.
- Restructure the renderer into weasyprint-shaped modules under
  `lib/html`, so each decision rule has one home.
- Split the renderer into two stages: stage 1 is CSS-faithful layout in
  px (no terminal knowledge); stage 2 maps that layout to terminal lines
  (quantization, cells, theme). The split is the enabler for a parked
  stretch goal (below); it is not speculative machinery.

## Non-goals (this plan)

- No Go PDF backend. Replacing the `weasyprint` subprocess with Go code
  is a parked stretch goal that motivates stage-1 purity; no PDF work
  happens here. Stage 1 stays pure so the PDF backend can be a second
  consumer of the same layout unchanged.
- No floats, absolute positioning, flex, grid, or page fragmentation.
  WeasyPrint builds those boxes; we never build them (the mail
  dimensions that matter do not need them).
- No new HTML parser: x/net/html stays the trust boundary (fuzzed).
  tinycss2/cssselect2 equivalents already exist (`lib/html` cascade,
  cascadia). The rework is layout, not parse.
- No weasyprint CSS support beyond the whitelist the cascade already
  holds plus the additions this spec names (margin, width/max-width,
  white-space values, font-size, list-style).

## Architecture

Three layers:

```
lib/html   stage 1 (layout): DOM -> UA + cascade -> box tree ->
                             positioned boxes and lines in CSS px.
                             No terminal knowledge.
mail/      stage 2 (backend): walk stage 1 fragments -> terminal lines.
                             Quantization, px -> cells, theme/dark,
                             marker glyphs, punctuation binding.
(app)      (parked) Go PDF backend, same stage 1; weasyprint subprocess
                             removed. Not built here.
```

Stage 1 must not know about terminal cells, blank rows, or the pager
line model beyond what `core` provides (the base types are shared). Stage
2 owns every terminal-specific decision.

### Module map (weasyprint -> Go)

| WeasyPrint source | Go | Content |
| --- | --- | --- |
| tinycss2 + cssselect2 + css/ | `lib/html/html.go` (keep) | cascade, style, links |
| css/html5_ua.css | `lib/html/ua.go` (new) | UA defaults: display, white-space, margins, markers, hr, th |
| formatting_structure/build.py | `lib/html/build.go` (new) | DOM -> box tree, anonymous-box repair, blockification |
| text/line_break.py + layout/inline.py | `lib/html/inline.go` (new) | white-space model, atomizing, line fill, char-break policy |
| layout/block.py + margins | `lib/html/block.go` (new) | vertical flow, margin collapse, hr box |
| layout/table.py + preferred.py | `lib/html/table.go` (new) | auto layout, column widths, colspan/rowspan |
| html.py img + layout/replaced.py + min_max.py | `lib/html/img.go` (new) | replaced sizing, intrinsic ratio, max-width cap |
| (backend) | `mail/html.go` (rewritten) | stage-2 terminal mapping + facade |

Go packaging: one `lib/html` package with one file per weasyprint area.
Subpackages would force exported APIs for shared box/style types and
create import-cycle risk (table needs preferred needs inline); the Python
module tree does not translate to Go packages cleanly.

Dependency rule: `lib/html` depends only on `core` (a leaf). Mail types
(Attachment, thread) never enter stage 1; image bytes and cid/url
resolution come in through a loader callback (weasyprint's
`get_image_from_uri` shape).

## Stage 1: box model and build (`build.go`)

DOM -> box tree. Box types: container (block, flows vertically); table /
row-group / row / cell / caption (column grid); inline element
(span/a/b/i/u that stays inline); text (leaf run with computed style);
br (forced break atom); image (replaced, block or inline-atomic).

Two rules make the tree earn its keep:

- Blockification of inline containers. An inline element whose in-flow
  children include a block-level box becomes a block container
  (weasyprint splits and wraps the inline runs into anonymous blocks
  around the block child). This gives a principled home to the current
  per-branch special cases: display:block anchors (`READ IN APP` in the
  current walker), mixed inline/block content, block content inside a
  table cell.
- Anonymous table repair (weasyprint's rule set). Whitespace-only text
  nodes in table/row context drop; a stray td wraps in an anonymous row;
  a stray tr wraps in an anonymous group. Cell content structure comes
  out of this stage clean so table layout does not re-derive it.

`display:none` elements and head/script/style/meta/title/link/base/
template drop here (display:none skip, not parse skip). Each box carries
its computed style (`StyleOf`), so layout modules never re-run the
cascade.

## Stage 1: UA floor (`ua.go`)

The study showed lib/html has no UA floor beyond four emphasis rules,
while weasyprint's html5_ua.css is what gives pre its whitespace, p its
1em margins, ol/ul their markers, hr its rule. `ua.go` fills computed
values only where the author did not set them, in CSS truth (em/px), not
pre-quantized:

- display classification (moves out of mail blockTags/skipTags): the
  block set, li = list-item, the table family, none for head elements,
  else inline.
- white-space: pre gets pre (fixes the bare-`<pre>` collapse bug at the
  root).
- margins: p/ul/ol/dl/pre/blockquote/figure 1em top+bottom; nested lists
  0; dd/blockquote/figure 40px side; ul/ol padding-left 40px; hr .5em.
  Heading margins are resolved to px at UA values (h1 .67em of a 2em
  font .. h6 2.33em of a .67em font, base 16px) so stage 1 stays CSS
  truth without a font-size property.
- markers: ul disc, ul ul circle, ul ul ul square, ol decimal.
- th bold; a underline + color.

No font-size computed property in this plan: nothing in stage 1 needs one
(the base 16px em is the only length scale, and heading margins carry
their UA-resolved px). A future PDF backend that renders real fonts adds
font-size then; adding it now is speculative.

## Stage 1: inline layout and whitespace (`inline.go`)

White-space model, weasyprint's two booleans (wrap, collapse):

- normal / nowrap: collapse runs, newline becomes a space. wrap: on for
  normal, off for nowrap.
- pre / pre-wrap: no collapse, newline is a forced break. wrap: off for
  pre, on for pre-wrap.
- pre-line: collapse runs to one space but keep newlines; wrap on.

Collapse algorithm: CRLF -> LF; spaces around a newline collapse onto
the newline; remaining newline handling per the class; runs of spaces ->
one space; a leading space after a prior trailing space drops but stays a
wrap opportunity. Cross-boundary collapse across inline boxes is part of
this stage.

Normalization toggle: an engine option, default normalize for notmutt.
Normalize implements the print-fix semantics in code instead of injected
CSS text: wrap all text, preserve pre/pre-line/pre-wrap structure, cap
widths at the content box, and `overflow-wrap: anywhere` emergency
char-break on overflow. `printDoc` then shrinks to @page/paper. Author
mode honors white-space exactly, for fidelity testing. Both backends run
normalize, so terminal and export share line structure by construction.

Metrics interface: measure text and report break opportunities. The
terminal backend provides monospace metrics (each char = charW px), so
stage-1 px widths and stage-2 cells agree exactly. A PDF backend later
provides real font metrics. Without the interface, stage 1 would bake in
monospace and the PDF consumer could not reuse line breaks (proportional
fonts wrap differently).

Char-break policy: no mid-word break by default; emergency char-break on
actual overflow under normalize (matches overflow-wrap:anywhere). The
current walker's unconditional hard-split of over-wide words is that
policy; it becomes a layout decision, not an addWord side effect.

Text atom model: a block's inline content flattens to an ordered stream
of atoms (text runs with style, br, atomic inlines) at layout time, and
wraps to lines at the block's used width. This replaces the walker's
pending-words buffer and flush().

## Stage 1: block flow and vertical rhythm (`block.go`)

Blocks stack vertically with real CSS margins in px, collapsed per
weasyprint block.py: adjacent siblings collapse to max; parent-child
top/bottom collapse; empty-block collapse-through. hr is a 2px rule box
whose borders stop margin collapse (about 1.5em total gap). Margins are
between boxes, never inside a box's line stack.

List items lay out with a hanging marker: li content at the list's 40px
padding, the marker in the gutter. This replaces the current
pendingMark-prepend with no hanging indent.

No floats and no clear: weasyprint's float exclusion machinery is
dropped wholesale; in-flow block stacking needs none of it.

## Stage 1: tables (`table.go`)

Auto layout, not the shared-cap w3m model. Per-column min-content and
max-content computed from all rows (weasyprint preferred.py); used widths
by distribution within the available width. Tables cap at the content
box width (weasyprint shrink-to-fit), never overflow once the
normalize char-break applies. Per-column caps replace the single colCap.
The 2-cell gutter becomes the UA 2px border-spacing default.

colspan/rowspan get a real grid: colspan width distributes onto its
spanned columns; rowspan occupies its column in later rows (content in
the start row). th is bold and not centered (UA). Cell vertical-align is
not modeled (no sub-row in a terminal; the backend's concern anyway).

Nested tables stop flattening into the cell line and lay out as real
block tables within the cell, indented. This is what weasyprint outputs
and moves the viewer toward parity. It visibly changes legacy
Outlook-era mail that the current walker flattens into one line (the
READ IN APP flatten comment and collectCell/walkRow machinery are
deleted). Accepted as a deliberate consequence of parity.

## Stage 1: images (`img.go`)

Replaced sizing per the weasyprint study, in px:

- Intrinsic size from decoded dims (loader returns bytes; decode header
  via image.DecodeConfig). Ratio = width / height.
- width/height attributes are low-priority px hints (weasyprint maps
  them to presentational-hint declarations below author CSS).
- max-width (100% under normalize) caps the used width at the containing
  block; height:auto recomputes from the intrinsic ratio. Both specified
  obey both, then normalize's max-width cap applies and height:auto
  rescales (the print behavior).
- Percent width resolves against the containing block width.
- Unresolvable images fall back to alt text; a missing src renders the
  alt (weasyprint handle_img). Remote http(s) srcs stay URL-only (the
  fetch is a keypress step). Declared 1x1 tracking pixels drop before
  the fetch path (current isTrackingPixel behavior).

## Stage 2: terminal backend (`mail/html.go` rewritten)

The backend consumes stage-1 fragments and emits []core.Line. All of
this is stage-2 and none of it enters lib/html:

- Quantization: collapsed margins map to whole blank rows. With the base
  em at 16px and one pager row per content line, blankRows = round(gapPx /
  16px), so a collapsed 1em (or heading-class) gap renders one blank row,
  li (0) keeps list items contiguous, and nested-list and inter-cell gaps
  stay tight. hr renders as a theme rule row with its own height.
- px -> cells via the monospace char width chosen for stage 1.
- Colors and dark mode: theme/dark adaptation (AdaptBG/AdaptFG, the
  [html] dark-mode setting, defaultBG carry) stays here, where it is
  today.
- Markers: glyphs (disc/circle/square/decimal) render in the hanging
  gutter; glyph transforms remain config data per AGENTS R11.
- Punctuation binding and underscore-rejoin (current bindsLeft) live
  here, not stage 1: they are deliberate divergences from weasyprint
  (which prints the space before a comma). Stage 1 emits the space;
  stage 2 drops it before punctuation.
- Image lines reserve rows/cells; DispW/DispH hints derive from stage-1
  px boxes through one coherent px->cell mapping, replacing the current
  raw-px/percent confusion in imgSize. The exact core.Image/ImagePos/
  tui/img.go contract is verified during implementation.
- Label/link mode (F key), the width cap at htmlWrapWidth, the
  maxHTMLLines budget with [content truncated], and sanitize (F1) all
  survive unchanged at this boundary.

The facade keeps its signature family (RenderHTML, RenderHTMLWithLinks)
so callers in the view model do not change; internally it supplies the
loader callback, dark/theme context, and normalize mode, runs stage 1
then stage 2.

## Migration (side-by-side, then delete)

- Build the new engine under lib/html. The mail facade switches on a
  debug key so a developer can diff the old and new output on real mail
  in place.
- The pinned html_*_test.go tests are regression tests: they encode
  deliberate behavior (punctuation hugging, underscore fragments, dark
  adaptation, declared image sizes, tracking-pixel strip, display
  non-inheritance, block anchors, link labels, alignment clears). They
  must pass against the new engine from the first switch, unweakened,
  per the locked-regression-test rule. Where a behavior is stage-2-owned
  (punctuation binding), the test follows it to the stage-2 surface.
  New tests sit beside them; none are edited except to relocate to the
  same public behavior.
- Delete the old walker (html.go's htmlWalker, table/cellRows/
  collectCell/wrapWords, blockTags/skipTags, addText/addWord/flush,
  imgSize/resolveImage internals) only after parity is demonstrated on
  real mail. Until then both live.

Pinned tests to carry: html_align_test.go (ExplicitLeft clears
center/right, CenterStillPads, NestedRightCellDoesNotLeak),
html_spacing_test.go (InlineBoundaryHugsPunctuation,
UnderscoreFragmentHugs, ControlNodeDoesNotPanic), html_dark_test.go
(Dark, DarkDeclared, DarkOff), htmlparse_test.go (ParseShapes,
ImageDeclaredSizes, RenderHTMLLinks, BlockAnchorLinks,
DisplayNotInheritedRender, TrackingPixelStripped, RenderHTMLBackground).

## Testing strategy

- TDD per dimension: each stage-1 module gets tests that pin a decision
  rule (whitespace collapse cases, margin collapse pairs, per-column
  table widths, colspan grid, image max-width/ratio, hr spacing, nested
  tables). Test data fabricated (alpha/atlas/acme senders, no personal
  content).
- Behavioral parity tests: a small corpus of html shapes renders through
  both the new engine and weasyprint (the weasyprint probe harness from
  the print fix, measuring content edges) to lock the normalize rules
  that prevent clipping.
- The full suite stays green including locked tests:
  cd src && go test -count=1 -tags "lua mcp" ./... and make vet.

## Docs

- docs/html-rendering-analysis.md updated when the migration lands (the
  current doc describes the walker).
- This spec and its implementation plan commit with Co-Authored-By:
  Deepseek (doc commits). Code commits carry no marker.

## References

- references/WeasyPrint: formatting_structure/build.py (box generation),
  css/html5_ua.css (UA floor), css/__init__.py (cascade + presentational
  hints), text/line_break.py + layout/inline.py (whitespace/wrapping),
  layout/block.py (margins), layout/table.py + preferred.py (tables),
  layout/replaced.py + min_max.py + html.py (images).
- AGENTS.md R5 (extractable TUI), R7 (stdlib first, dep policy), R11
  (config-data glyphs).
