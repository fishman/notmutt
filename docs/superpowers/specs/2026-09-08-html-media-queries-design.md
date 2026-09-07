# HTML media queries (width-aware CSS) - design

## Context

Mail HTML ships responsive CSS: `@media only screen and (max-width:
480px) { ... }` blocks restyle the mail for narrow viewports. The
stage-2 engine today drops at-rule bodies wholesale (`ParseStyleSheet`,
fixed 2026-09-08 to skip them as units instead of leaking their inner
rules). Dropping is correct for `prefers-color-scheme` - the client owns
theming, a mail's dark block must never override the TUI theme - but a
TUI HAS a viewport width, and a 40-cell pager is exactly the narrow
phone the mobile blocks were written for. The mobile rules would
genuinely improve narrow rendering: full-width stacked cells, enlarged
tap-target text, downsized images.

This spec makes width-based media queries evaluate against the render
width. Everything else about the cascade is unchanged.

## Scope and policy

Evaluated queries (the only ones a TUI can truthfully answer):

- `(min-width: Npx)`, `(max-width: Npx)`, `(width: Npx)` - matched
  against the layout width in px (cells x charW, today 10px/cell).
- Media type: `screen` and no-type match; `print` and any other named
  type never match (a TUI is a screen or nothing).
- `only`/`not` prefixes per CSS. `and` chains: the query matches when
  every conjunct holds.

Never matched (documented, not silently ignored):

- `prefers-color-scheme` and every other feature not listed above -
  client-owned (dark handling is the theme engine's, 2026-09-01 spec).
- `min-height`/`max-height`/`orientation`/`resolution` - no viewport
  dimension exists for them yet. Adding height later is additive.
- Nested `@media` inside `@media` - dropped (mail never sends it).

`!important` handling stays as fixed: stripped at declaration parse.

## Parser change (lib/html/html.go)

`ParseStyleSheet`'s at-rule branch stops skipping `@media` bodies and
instead:

1. Strips the `@media` keyword; the prelude is the query text.
2. Parses the prelude into a `mediaCond` with a small hand-rolled
   matcher (no new dep - tdewolff/parse/css tokenizes at-rules but
   leaves the query an untyped token stream, so the semantics would be
   hand-written either way; verified 2026-09-08):
   - split on `and` outside parentheses; each conjunct is a feature
     `(name: value)` or a type word;
   - `min-width`/`max-width`/`width` with an integer px value fold into
     `minW`/`maxW` bounds (min = max of mins, max = min of maxes -
     conjuncts intersect);
   - any conjunct the matcher does not recognize makes the whole query
     never-match;
   - `not` inverts the result of the query it prefixes.
3. Emits the block's inner rules with that `mediaCond` attached -
   `CSSRule` gains a `cond` field (zero value = always). Inner rules
   keep their own selector specificity; the cascade sort is untouched.

```go
type mediaCond struct {
    minW, maxW int // px bounds; 0 = unbounded
    never      bool
}
func (c mediaCond) matches(w int) bool
```

`CSSRule` gains `cond mediaCond`; `matches(w)` is O(1) per rule.

## Width threading

Style resolution needs the render width, so it threads through the
build:

- `StyleOf(n, parent, rules, widthPx)` - a rule applies only when
  `cond.matches(widthPx)` and the selector matches. One `int` param.
- `Build(doc, rules, widthPx)` - passes it down. The production call
  site is `renderStage2` (mail/html_stage2.go), which already has
  `widthPx` clamped to [10, 1200] before `html.Build` - one call site.
- Width 0 = "no viewport": no media query matches. The test helper
  `buildBody` keeps passing 0, so every existing test keeps today's
  media-free behavior without edits. Production never sends 0.
- No caching concern: every render rebuilds the tree at its own width,
  and the extents memo (tblMeas) is per-tree, so a width change is a
  fresh build - nothing stale.

## Semantics of a matching mobile rule

The mobile blocks mail sends are mostly:

- size rules (img max-width, font-size bumps) - apply through the
  cascade exactly like any other rule;
- `display:block` on cells (`.responsive-td { display:block
  !important }`) - this is the flattening pattern. It demotes the cell
  box to RoleBlock; the grid (buildGrid) skips non-cell children, so a
  demoted cell and its content drop out of the table. That is the one
  consequence to decide at plan time:

  - Option A (mail-safe flattening): when a real `<td>` computes a
    non-cell display, render it as a full-width stacked row (block
    content emitted between the grid rows) instead of dropping it. This
    is what email clients do and what the mail author asked for.
  - Option B (never drop content): suppress display-override rules from
    media queries on table-family tags only (cells keep cell role; the
    size rules still apply). Narrow rendering then relies on the
    engine's own wrapping, which already measures columns to the
    available width.

  The spec leans A - the author's narrow layout is strictly more
  faithful - with B as the fallback if A's grid interaction turns out
  non-trivial. The plan picks one with a failing test first.

`prefers-color-scheme` blocks keep dropping: the mail's dark rules must
not fight the TUI theme (AdaptBG is the client's dark path).

## Component changes

- `src/lib/html/html.go` - `CSSRule.cond`, `mediaCond`, prelude matcher
  in `ParseStyleSheet`, width param on `StyleOf`/`Build` and on
  `bodyCascade` (body-level media rules - the sample's 480px block
  restyles `body` - resolve at the same width).
- `src/lib/html/box.go` - `buildElement` passes width through;
  unchanged role logic (demotion consequence above is explicit).
- `src/mail/html_stage2.go` - `renderStage2` passes `widthPx` to
  `html.Build` (one line).
- Tests: `lib/html/html_test.go` (cond matcher table test, cascade
  applies-at-width / does-not-apply-at-width), a narrow-render fixture
  test, `mail` pin extension for a responsive fixture.

## Testing strategy (failing first)

- Cond matcher: table test over preludes - `max-width: 480px`,
  `min-width: 640px`, `screen and (max-width: 480px)`,
  `only screen and (max-width: 480px)`, `not print`,
  `(prefers-color-scheme: dark)` (never), `(max-height: 600px)`
  (never), nested `@media` (dropped), garbage prelude (never).
- Cascade: one rule inside `@media (max-width: 480px)` - applies at
  400px, not at 1200px; specificity ordering unchanged (a media rule
  with higher specificity still beats a non-media rule).
- Narrow render: a responsive fixture at 40 cells takes the mobile
  rules (stacked cells or enlarged text, per the plan's option); the
  same fixture at 120 cells does not.
- Existing pinned tests: `TestMediaQueryRulesDoNotApply` (html_test.go,
  pinned 2026-09-08) asserts the old drop-everything behavior - this
  feature changes exactly that. Editing it is a LOCKED-test change and
  needs explicit user approval at plan time (project rule); the plan
  must ask, not silently rewrite it. `TestImportantSuffixStripped` and
  `TestRenderUcsfLayout` stay green untouched.
- `FuzzCSSDeclarations` keeps exercising the prelude matcher for
  panic-freedom.

## Risks and non-goals

- The demoted-cell consequence (Semantics above) is the only real
  correctness fork; the plan resolves it with a failing test before
  touching the grid.
- No height/orientation queries - the TUI's row count is not a CSS
  viewport until a mail asks for it.
- No re-Build caching across width changes - out of scope, same as
  today.
- The matcher stays hand-rolled: no new dependency (tdewolff/parse/css
  leaves query semantics untyped anyway).
