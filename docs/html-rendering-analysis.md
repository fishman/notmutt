---
layout: default
title: HTML rendering analysis
nav_order: 6
---

# HTML mail rendering: feasibility analysis (2026-08-17)

Question: render HTML mail in the pager with Go, stdlib-first, the main
risk being CSS. Claim to test: mail HTML uses a very small CSS subset,
so a subset parser + renderer is tractable.

## Current state (2026-09-04): the stage-2 terminal engine

The renderer is now a two-stage pipeline; the old one-pass DOM walker is
deleted (Task 6 cutover). `text/html` bodies render through
src/mail/html_stage2.go and are the default view for html mail.

Stage 1 (src/lib/html, px-pure) parses (x/net/html), cascades the
inline `style=""` + `<style>` subset (selectors matched with cascadia),
builds a box tree (`Build`), and lays it out into a flat px row stream
(`LayoutBlock -> []Row`). Stage 2 (src/mail/html_stage2.go, terminal)
quantizes that stream into `core.Line` pager lines. The pipeline:

```
x/net/html.Parse -> html.ParseStyleSheets -> html.Build (box tree)
        -> html.LayoutBlock (px Row stream) -> stage-2 quantize -> core.Line
```

The terminal frame constants (stage 2, mail-owned):

- `charW = 10` px per cell (horizontal); forced by the locked
  `TestImageDeclaredSizes` (50% of an 80-cell layout = 400px).
- `lineH = 16` px per pager row (the base em); blank quantization is
  `blankRows = round(gapPx / 16)`.
- `htmlWrapWidth = 120` cells and `maxHTMLLines = 5000` unchanged.

Stage 2 quantizes each `Row`: a gap becomes `round(Gap/16)` blank
`core.Line`s carrying the mail background; text spans become styled runs
(punctuation binding, tab expansion, F1 sanitize); a lone image becomes
an own-line image block, an image sharing its line an inline
`core.ImagePos`; list markers hang in their gutters; an `hr` becomes a
rule-glyph row; a table grid row (`Row.Cells`) becomes one horizontal
strip with each cell fragment at its absolute px column. Link mode (the
F key) injects `[N]` label boxes before layout so labels flow into line
building. Dark-mode adaptation (light-declared bg reflection onto the
theme bg, luma gate, fg inversion) stays in stage 2's run builder.

Mailcap (R6) shipped on the attachment-preview path: a
`copiousoutput`-style rule replaces the bytes with its output
(src/app/app.go, `previewMailcap`), with `<configdir>/mailcap`
overrides by type - the options table's fallback row, scoped to
attachments rather than the primary html path.

## Divergences from the old walker (recorded, deliberate)

The migration gate is the locked test suite, not byte parity. Each row is
a deliberate consequence of CSS-true layout; none is pinned by a locked
mail test.

| # | Behavior | Old walker | Stage-2 engine |
| --- | --- | --- | --- |
| A | `<hr>` | invisible block boundary | a visible `─` rule row |
| B | list items | blank line between items, marker inline at col 0 | contiguous items, 4-col gutter indent, hanging marker |
| C | inter-column gutter | 2 blank cells | px spacing (<1 cell at charW 10): columns abut |
| D | main-flow inline image | `x<img>y` splits to 3 lines | shares the line (inline ImagePos); isolated images still own-line |
| E | ordered lists | inline `1.` counter | hanging `N.` glyph from the box ordinal |
| F | heading/`blockquote` rhythm | one blank between any two content blocks | same, for UA margins (16-25 px round to one blank); author margins >= 32 px give 2+ blanks |
| G | list/quote insets | none (col 0 text) | content indented by the real 40px padding/margins |
| H | blank lines carry bg | yes | yes (unchanged, pinned) |
| I | bare URLs in pre-family text | never labeled (walker never linkifies in pre) | never labeled (collapse-text bare URLs are; real `<a href>` labels work in pre) - pinned by TestStage2PreTextBareUrlNotLabeled |
| J | inline-image reservation | never emitted inline image rows | ImagePos reserves the alt placeholder's cells; X = placeholder text column - pinned by TestStage2InlineImageXAtPlaceholderColumns |

Two recorded follow-ups live in TODO.org: list-marker glyph transforms
are a mail-side data map today (a config override is R11 future work);
and `Style.Label` is a renderer-synthesized flag a future Lua/RPC
consumer must never treat as authored content.

### Images-on geometry (recorded decision, 2026-09-04)

The plan-6 facade shipped with "the mail path never calls ResolveImages":
every image laid out at intrinsic 0, alt placeholders glued into the
flow, and the alt+i toggle stayed pager-side (decode + vertical
expansion only). The toggle now re-lays-out at real geometry, which is
the explicit extension of that posture:

- An images-on render calls `html.ResolveImages(boxes, loader)` between
  `Build` and `LayoutBlock` (stage 2), so text flows around real boxes.
  Images-off keeps the markers exactly as before - image-blind layout,
  unchanged bytes.
- The loader sizes each src with `image.DecodeConfig` (dimensions only,
  never a pixel decode). Embedded `cid:`/`data:` images are measured in
  the worker from bytes the parse already holds; a render with the
  images flag makes a dimension read of bytes already in the message.
- Remote `http(s)` bytes never enter the worker. The TUI DecodeConfigs
  the fetched bytes (the same call `src/app/imgfetch.go` already makes)
  and passes only the measured px map back on a "refine" reopen; the
  image seats once its fetch lands.
- The pixel decode/render boundary is unchanged: pixels decode only in
  the TUI (`prepareImages`), never in a render. The privacy gate that
  was "no ResolveImages on the mail path" is now "no pixels on the mail
  path" - geometry (px dimensions) may cross, decoded pixels may not.

Standalone images fill + center (2026-09-04): an image that owns its
line - a lone own-line image, or an inline placeholder on an otherwise
empty text row (how a table cell's single chart emits) - drops the
authored disp cap at decode (`pager.standaloneLine`, `prepareImages`),
so it fills the window's text column at natural px (no upscale, capped
at the 100-cell paint cap) instead of staying at a browser-column
width; `visibleImages` centers such a block. An image that shares its
row with text keeps its authored disp and flow offset. Markers and the
images-off layout are untouched - this is decode/paint-only in the
TUI. Inline row geometry is unchanged; a wide decode paints over the
image's own blank rows, never text.

Height divergence remains (BUGS.org): intrinsic px height still does not
advance stage-1 rows; vertical room is the pager's decode expansion
(`relayout`), which already pushes following lines down.

## What "go stdlib html" actually is

- `html` (stdlib): escaping/unescaping only. No parser, no DOM, no CSS.
- `golang.org/x/net/html`: the de-facto standard HTML5 parser (Go team,
  implements the HTML5 tree-construction spec, error-tolerant by spec -
  malformed input never fails the parse, it recovers). This is the
  package every Go HTML consumer uses; it qualifies under the R7
  supply-chain bar (Go team, vendored, one package, no transitive deps).
- CSS: nothing in stdlib or in the Go team's x/ tree. The subset parser
  is hand-written - which is the point of the analysis: the subset is
  small enough that this is ~200-300 lines, not a vendored engine.

## Why the subset claim holds

Mail HTML is a fossilized 1998-era document type, shaped by what mail
clients will render:

1. Inline styles dominate. Gmail and Outlook historically strip or
   ignore `<style>` blocks and classes, so senders inline everything.
   Consequence: the cascade problem mostly disappears - inline styles
   are the top priority, and there are no stylesheets to resolve in the
   common case.
2. Layout is tables. `table/tr/td` grid + block flow (`p`, `div`,
   `h1-h6`, `ul/li`) + inline runs (`span`, `b`, `i`, `a`, `br`). No
   float, no absolute positioning, no flex/grid, no z-index - the
   layout engine shrinks to block flow + table cells + inline text
   (CSS 2.1 flow layout, not a browser).
3. Properties that actually appear (the whitelist, ~22):
   color, background-color, font-family, font-size, font-weight,
   font-style, text-decoration, text-align, line-height, letter-spacing,
   margin, padding, border, border-collapse, border-spacing, width,
   max-width, height, display, vertical-align, list-style-type,
   white-space. Everything else (position, float, transform, animation,
   flex, grid, media queries) can be dropped on the floor - it either
   never appears or only as dark-mode variants that are ignorable.
4. Selectors in `<style>` blocks (when they exist): element, `.class`,
   `#id`, simple descendant chains, comma groups. No attribute
   selectors, no pseudo-classes beyond `:hover` (dead in static mail),
   no pseudo-elements, no media queries. Specificity counting for that
   set is ~20 lines.

## Architecture sketch (in-client renderer) - superseded by the stage-1/stage-2 pipeline above (2026-09-04 cutover)

```
x/net/html.Parse  ->  DOM (never fails, HTML5 recovery)
        |
        v
style pass: inline style="" attrs + <style> block rules,
            cascade inline > #id > .class > element,
            inherit the inherited properties only (color, font-*,
            line-height, text-align)
        |
        v
flow pass: walk the DOM, block elements break lines, inline runs
          accumulate, tables lay out as column-aligned blocks
          (w3m's approach: pad each column to its widest cell)
        |
        v
emit core.Line rows: text wrapped to the pager width, per-line
          style as SGR fragments, everything through
          core.SanitizeControls first (F1)
```

Trust boundary (SECURITY.md F1/F4/F10):
- The parser is the boundary: x/net/html is the hardened, fuzz-exercised
  piece; our code only walks its DOM.
- `<script>` and `<iframe>` are skipped as content; nothing executes,
  nothing is fetched (no external URLs, no images - images go through
  the attachment/mailcap path or render as a placeholder).
- The CSS subset parser drops unknown properties and values; style
  data is never emitted raw (generated SGR only after sanitize).
- Fuzz targets on the CSS parser and the renderer walk (the F10
  standard for parser-adjacent code).
- Test corpus is synthetic fixtures only (the mailbox privacy rule).

## Options compared

| option | code | fidelity | deps | verdict |
|---|---|---|---|---|
| in-client DOM-walk renderer, inline styles only (phase 1) | ~500-800 lines + tests/fuzz | colors, bold, links, tables column-aligned | x/net/html | recommended: covers ~90% of mail |
| phase 1 + `<style>` blocks, classes, table alignment (phase 2) | +200-300 lines | near-complete for the subset | x/net/html | add if phase 1 proves short on real mail |
| full box-layout engine (backgrounds, padding, borders) | 3-5x phase 1 | browser-ish | x/net/html | overkill - mail doesn't need it, the pager's plain-text presentation is the reference |
| lynx/w3m -dump via mailcap (R6 path) | ~50 lines | text only, no styling | subprocess runtime dep | fallback/companion, not the primary |

## go-css evaluation (2026-08-17)

Candidate: github.com/napsy/go-css ("a very simple CSS parser", BSD-3,
v1.0.0 2025-05, 61 commits, single maintainer, no dependency tree).

Safety (the trust-boundary bar): the parser is a hand-written state
machine over text/scanner tokens; no regex. No panics on malformed
input, no infinite loops (the token list drains, EOF terminates),
memory linear in input. CPU is O(n^2) worst case on pathological inputs
(selector/value string concatenation, per-rule style merges) - a slow
burn, not a bomb. Pass on the safety basics.

Fit (the mail use case): fails on the two decisive axes.

1. No inline style support. The API parses a whole stylesheet into
   map[Rule]map[string]string; a style="" attribute cannot be parsed.
   Inline styles are the dominant carrier of mail CSS (Gmail/Outlook
   strip <style> blocks, senders inline everything), so the ~40-line
   hand-written declaration parser is needed regardless.
2. No x/net/html integration. The output is a selector-keyed map with
   no DOM matching. Pairing with x/net/html still requires a selector
   matcher - cascadia (github.com/andybalholm/cascadia, BSD-2, the
   standard companion: Parse/Query/QueryAll over x/net/html nodes,
   specificity.go for cascade resolution, fuzzed, used by goquery).
   With cascadia in, go-css contributes only the stylesheet parse.

Correctness gaps in the subset it does parse:
- comma-separated selector groups unsupported (its own TODO) -
  "h1, h2 { }" is common in mail <style> blocks
- url() values with colons break (background: url(http://x) splits
  at the colon)
- unclosed { or an unterminated comment silently discards the tail -
  recoverable, but styles vanish without a trace
- keys are the raw scanned text - case folding is the consumer's job
- 3 open issues, no recent activity; hobby-scale maintenance under
  the R7 supply-chain bar (no audit history)

Verdict: not usable as the mail CSS engine. The hand-written subset
parser remains the plan for <style> blocks; cascadia covers selector
matching if phase 2 ships (it is the mature, fuzzed piece - take it,
don't reimplement it). go-css is useful only as a reference shape for
the state machine.

Outcome (2026-08-19): as concluded - css.go is the hand-written subset
parser (inline style="" + style blocks), cascadia does the selector
matching, go-css is not vendored.

## Scale and sequencing

Phase 1 is the honest first cut: parse + inline-style whitelist + block
flow + table column alignment. It covers the transactional mail shape
(the bulk of HTML mail). Phase 2 (style blocks, class selectors,
inheritance) is driven by evidence from real mail - the inline-first
reality of the ecosystem means phase 1 already handles most of what
Gmail/Outlook-compatible senders emit.

Rendering HTML mail was a resolved "how" (parse + subset CSS + flow
walk) with a staged "how much" - and the build delivered both phases
at once: style blocks and class selectors are in the shipped renderer,
not a follow-up. The remaining question is the fidelity bar on real
mail; the interactive pass answers it live. Gaps surface as render
bugs against the user's own corpus, and the property whitelist and
table alignment are the extension points.
