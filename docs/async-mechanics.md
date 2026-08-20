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
behind the mechanics added since: the full-walk hydration, the open
path, and the HTML renderer.

## 1. Full-walk hydration: one C pass, real rows, no hydrator (2026-08-21)

Decision: the index fills from one full-walk pass over the result -
the binding emits each thread's summary AND every message row from a
single C iterator (header-cache reads only, zero file opens), so the
view holds real rows from the first chunk: no stub rows, no per-thread
hydration job, no row-position scan cursors. The full walk replaces
the two-phase design (summary walk -> stub rows -> threadjob
per-thread fetches through the bounded worker queue).

Why: the two-phase design failed twice. The threadjob flood measured
~4.5 minutes on the 33k-thread inbox (the "hydration storm": one scan
per wave, every fetch its own event, the chain terminates only when a
scan page holds no stubs). And the stub machinery had a failure mode
that lost mail: two threads went missing from the index until a tag
op reconciled them - a stub whose hydration the row-position scan
cursor could never reach. One pass eliminates the whole class: no
stubs, no cursors, no queue, no chain - a row is either emitted or
the thread is not in the result. The full walk measured
5.7s warm until the refs fallback (docs/refs-from-terms.md) dropped
the per-message references/in-reply-to reads - get_header on those two
headers file-parses every message (they have no value slots); the walk
now ships empty chains and runs ~1.75s (33,256 threads / 38,508
messages). The index tree renders structure-less threads flat (the
[...] marker stays for genuine multi-root threads); the per-thread
fetch still carries the chain, and the libnotmuch getter fix
(records in refs-from-terms.md) re-adds the reads. First paint still
lands in ~20ms (the 100-thread first-chunk cadence), full cover in
under two seconds. The binding addition (FullWalk) lives in the fishman
go.notmuch fork (v0.40.1); the summary-only walk stays intact for the
consumers that need it.

## 2. Open while the walk runs: rows-first, worker fetch as fallback (2026-08-21)

Decision: the walk owns the worker for seconds, so the read seams
resolve the opened message's set rows-first from the registered views
before any worker round trip: open, render toggle, attachment view
and save read the thread's messages straight off the view (the walk
already loaded headers and paths - the same header-cache data an
ActThread fetch returns). The worker fetch is the fallback when the
thread is in no view (a closed tab's pager, a view reset race). The
read-mark ActTag still queues behind the walk - it is a write, and
the refresh cycle's reconcile-then-replay lands it in the view.

Why: the walk is the worker's only occupant for ~6s after every view
switch. An open that queued an ActThread behind it would feel dead
for seconds exactly when the list has visible rows to open; the walk
emits every message with its paths, so the open's content needs (the
file parse and the render) need nothing from notmuch that the view
does not already hold.

Open finding, not fixed: reads have no lock budget - the worker's
budget applies to writes only, so a hung ActThread wedges the serial
worker with no recovery path. Rows-first removes the read path's
exposure to the queue; it does not remove the wedge itself.

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
