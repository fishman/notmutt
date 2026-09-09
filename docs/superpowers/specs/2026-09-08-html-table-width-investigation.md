# HTML table width / min-width gap - investigation findings

Status: investigation, not a spec. Reproduced on `src/testdata/html/ucsfhealth.html` (2026-09-08). Written for pickup in a fresh context. No code changed, no fix shipped.

## Symptom

The footer band of the ucsfhealth sample renders center-aligned at a different
column than the rest of the page. Measured at width 120 cells (10px/cell):

- body-centered lines (`- MC, Cole Valley`, `health concerns.`) center at
  ~col 60 (the page center);
- footer lines (`View in Browser`, `GoHealth Urgent Care`,
  `Copyright (c) 2026`) center at ~col 23.5.

User quote: the section is "center aligned, but not in the same center as the
rest of the page."

## Reproduction

Render the fixture through the stage-2 path:

```
RenderHTML(body, nil, 120)   // body = ../testdata/html/ucsfhealth.html
```

The footer region is the dark `#2D3740` band (the source `<table>` at fixture
line ~489, inside the `width="600"` `.container` that opens line 152 and
closes line 545). To isolate it: parse with `x/net/html`, find the table whose
style/bgcolor contains `2D3740`, serialize the subtree with
`xhtml.Render`, and render that alone. The artifact reproduces standalone
(the footer is intrinsic, not an ancestry interaction).

## Measured facts

- Isolated footer still centers at ~col 21-22 (about 2 cells left of the
  in-page case - the outermost inset), while the legal-text paragraph in the
  same subtree (`Please consult your physician...`, `The information in this
  email is not medical advice...`) renders full width from ~col 0. The
  centered lines and the legal paragraph live in different containing boxes
  within the same footer table.
- The centered lines sit in auto (`width:auto`) wrapper tables that the engine
  shrink-wraps to their content width (~43 cells - the widest `<br>`-separated
  line, `Unsubscribe or Update Email Preferences`). `text-align: center`
  then centers inside that ~40-cell box (`applyAlign`, lib/html/inline.go:349).
- The legal paragraph's wrapper does not shrink because its prose max-content
  exceeds the available width, so the auto-table path falls to "fill"
  (lib/html/table.go:274-279: shrinkwrap when `available >= tableMax`, fill
  otherwise).

## Root cause

These are Salesforce/Marketing Cloud style wrapper tables. They set
`style="min-width: 100%"` and rely on the browser honoring it to stretch an
auto table to the full content width. lib/html does not support `min-width`
at all, so the tables shrink-wrap to content and their centered text centers
against the shrunken box.

Broader: CSS `Style.Width` / `MaxWidth` / `CSSLen` (lib/html/html.go:76-88)
is consumed **only** by the replaced-image path (`lib/html/img.go:52-63`).
Table and block boxes never read CSS `width`, `max-width`, or `min-width`.
Table width comes entirely from the auto-table column algorithm
(`columnWidths`/`assignColumns`, lib/html/table.go), driven by content
min/max and the available width. Consequences confirmed empirically:

- Forcing `width="100%"` onto the footer wrapper tables - as an HTML
  attribute or injected into the style string - changes nothing (their CSS
  width is never applied to table boxes).
- The fixture's `width="600"` `.container` table is likewise unenforced: the
  mail renders ~full-bleed (body text reaches ~col 120), so the only thing
  making the footer read as "off center" is its shrink-wrapped ~40-cell box
  versus the full-width body around it.

The engine is intentionally best-effort block flow; fixed/percent table widths
were never in its model. This is a layout-fidelity gap, not a fixture bug -
editing the HTML to "fix" the sample would be papering over the engine.

## Fix direction (not yet scoped)

The correct general fix is CSS width support on table and block boxes, in
particular:

1. `min-width` (at least the common `100%` and `0`): an auto table/block whose
   min-width is 100% must resolve its used width to the containing width, not
   shrink-wrap to content. This is what the marketing mail actually ships.
2. Likely also CSS `width` (px and %) on table and block boxes, and the HTML
   `width` attribute mapping - so the 600px container constrains descendants.
   That is a separate, larger behavior change (whole-mail width), and needs
   deciding whether containers should start constraining (changes the look of
   every wide render, including the accepted ucsfhealth body layout).

Relationship to `2026-09-08-html-media-queries-design.md`: both touch width
semantics in lib/html; the media-query spec threads a `widthPx` through
`StyleOf`/`Build`. Plan them so the min-width fix and the width threading do
not conflict (min-width resolution needs the same containing-width notion).

## How it was verified

- Temporary scratch test in `src/mail` (since removed): rendered the fixture,
  printed each `core.Line.Text` with its leading-space start column and width,
  and rendered the isolated `#2D3740` subtree as-is and with two width-forcing
  transforms. Neither transform moved the centered lines, ruling out the
  width-attribute/style hypothesis and confirming `Style.Width` is image-only.
- Pinned test `TestRenderUcsfLayout` (src/mail/ucsf_test.go) still passes and
  is unaffected by this finding.

## Open questions for the next context

- Scope: fix only `min-width: 100%` (mail-safe, minimal) or add full
  table/block `width` + `min-width` support?
- If container widths start being enforced, re-baseline the accepted renders
  (ucsfhealth body is currently ~full-bleed, not 600px) and their pins.
- `max-width` support scope (the `.container {width: 100% !important}`
  media rules from the media-query spec interact here).
