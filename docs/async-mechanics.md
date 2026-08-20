---
layout: default
title: Async mechanics design
nav_order: 9
---

# notmutt - async mechanics: rendering and hydration

Decision records for the client's read-path mechanics: how threads get
from notmuch into the visible tree, how the tree fills in, and how mail
HTML is rendered. AGENTS.md is normative (R1-R15); design-decisions.md
carries the core WHY (language, backend, cache). This page is the WHY
behind the mechanics added since: hydration, coalescing, batching, and
the HTML renderer.

## 1. Thread hydration: stub rows, waves, no job-side state (2026-08-19)

Decision: the view holds stub rows (search summaries with no message
ids) and a threadJob fills them into real trees. Hydration state IS the
view - a thread is hydrated when its rows carry real message ids, so
the job keeps no hydration table and a view reset (reload, view switch)
re-fetches by construction. Scans advance in `scanPage` waves over the
whole view; the budget bounds in-flight fetches, never coverage.
Collapsed threads hydrate too: their root row is visible, so C-expand
becomes instant (MergeThread keeps the collapse state on the thread
object). A `pending` set dedupes fetches by thread id, never row
position; a failed fetch clears the gate so the next scan retries.

Why: the index must paint in seconds on the 33k-thread inbox, but a
real thread tree costs 40 serialized fetches per page. Stub rows make
first paint cheap; content arrives as the wave progresses. The TUI
repaints on the per-fetch Progress event, so the trees fill in under
the cursor while the user keeps navigating (R3, R15). Keeping hydration
state in the view instead of the job removes an entire class of
stale-state bugs: there is no view/job copy to drift.

## 2. The hydration storm: one scan per wave, one flatten per wave (2026-08-20)

Measured failure: the naive loop was `1 ViewDiff -> scan (up to 40
fetches) -> 40 MergeThreads -> 40 ViewDiffs -> 40 fresh scans` - a
self-sustaining chain. Each fetched thread published its own ViewDiff,
each event spawned a fresh scan, each scan re-seeded the progress bar
with a different total (the "jumping" bar), and every scan queued up to
40 serialized ActThread calls on the worker. The chain terminates only
when a scan page holds no stubs (whole view hydrated) - minutes of
churn on the 33k-thread inbox, worker queue saturated throughout.

Fix (coalescing): a scan trigger drains the bus channel before
scanning - `drainEvents` collapses the whole wave into one scan;
events published during the scan trigger the next one. The bar now
advances once per thread under one total and clears at the scan end
(the R15 batch boundary), failed fetches included.

Fix (batching): the scan wraps its wave in `BeginMerge`/`EndMerge`,
and `MergeThread` honors the merge depth like `MergeThreads` already
did - the dirty flag batches under a depth, so the view flatten
rebuilds once per wave instead of once per thread. Same discipline,
both merge paths.

Verification: TestThreadJobHydratesOnce (80 stubs, a ViewDiff burst
pumped until all hydrate) pins both properties - every stub hydrates,
and the fetch count never exceeds the stub count (the amplification
regression: 1 event -> 40 fetches -> 40 events -> 40 scans).

Open finding, not fixed: reads have no lock budget - the worker's
budget applies to writes only, so a hung ActThread wedges the serial
worker with no recovery path. Coalescing reduces how often that path is
entered; it does not remove it.

## 3. HTML rendering: the in-client flow renderer (2026-08-19)

Decision: render HTML mail in-process with a stdlib-first flow
renderer - parse with x/net/html, cascade with a small hand-rolled
CSS engine over cascadia selectors, emit pager lines. Full analysis,
the shipped-state description, and the measured verdicts live in
html-rendering-analysis.md; the decisions that page established:
mail HTML uses a tiny CSS subset, so a whitelist of ~22 properties
covers it (position/float/flex/media queries drop); the HTML5 parser
is never reimplemented; the renderer is a trust boundary, so it is
fenced - a 5000-line budget, link mode `[N]`, images as placeholders
behind the privacy gate, and fuzz targets on the render and the CSS
declaration parser (SECURITY.md F1-F4, F10).

Why: the alternatives were heavier - wkhtmltoimage/w3m piping (a
subprocess per message, styling drift, layout loss) or vendoring a
browser engine (the supply-chain policy says no). The pager stays the
security boundary: rendered output passes the same F1 sanitize path
as text mail, and the strict whitelist keeps the CSS surface small
enough to fuzz and to audit by hand.
