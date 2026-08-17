# HTML mail rendering: feasibility analysis (2026-08-17)

Question: render HTML mail in the pager with Go, stdlib-first, the main
risk being CSS. Claim to test: mail HTML uses a very small CSS subset,
so a subset parser + renderer is tractable.

## Current state

`text/html` parts are silently dropped: the MIME walk keeps only
`text/plain` inline parts (src/mail/thread.go, the `ct == "text/plain"`
filter). An HTML-only mail renders as an empty body. The reference
paths (muttrc mailcap, neomutt's attachment view) are not ported yet
(R6).

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

## Architecture sketch (in-client renderer)

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

## Scale and sequencing

Phase 1 is the honest first cut: parse + inline-style whitelist + block
flow + table column alignment. It covers the transactional mail shape
(the bulk of HTML mail). Phase 2 (style blocks, class selectors,
inheritance) is driven by evidence from real mail - the inline-first
reality of the ecosystem means phase 1 already handles most of what
Gmail/Outlook-compatible senders emit.

Rendering HTML mail is a resolved "how" (parse + subset CSS + flow
walk) with a staged "how much" (phase 1 first). The remaining unknown
is not feasibility but the fidelity bar the user wants on their own
mail - which the interactive pass measures.
