# Refresh merge complexity - bottleneck analysis and fix (design record)

Date: 2026-08-14. Status: fixed, verified by the test suite and a real
cold load.

Normative requirements: AGENTS.md R3 (async, incremental thread views,
diff-and-insert, never full-refresh-on-new-mail). This record documents
why the refresh merge path went quadratic at ~10k threads on the real
129k-message mailbox, and the fix. Related: the M1 spec section 7
(refresh policy) and section 8 (diff mechanics) predicted O(n+m) diffs;
this record is the correction to what that spec's claim covered.

## 1. The design intent

The refresher fills the view progressively (R3): one `notmuch search`
walks the whole result, and the backend emits it in chunks (200 first,
then 1000 - cli.go). Each chunk merges into the view and publishes a
ViewDiff, so the first paint lands in the first 200 rows and the load
is visible while it streams.

The merge is replace-semantics: `View.MergeThreads(snapshot)` diffs
the view's current thread set into the incoming FULL accumulated
snapshot (removals reconcile only when the snapshot says so - a
partial feed would evict every unmentioned thread). So the contract
is: per chunk, merge O(n) threads, where n grows to the full result
size.

## 2. The bottleneck

`MergeThreads` is O(n+m) per call, but with a hidden quadratic: the
per-thread reconcile loop located each incoming thread with
`findThread`, a linear scan over the view's thread list:

```
for _, in := range sorted:            // n iterations
    cur := findThread(v.Threads, in.ID)   // linear scan, ~n/2 compares each
```

Per chunk: n * n/2 string comparisons. Across the whole load the
snapshot is re-merged every chunk, so the total is:

```
sum over chunks of n_k^2 / 2,  n_k = 200 + 1000*(k-1)

33k-thread inbox:   ~6e9 compares    (seconds to tens of seconds)
129k-thread inbox:  ~3.6e11 compares (minutes)
```

The observable signature is a knee, not a flat slowdown: per-chunk
cost at 10k threads is ~50ms (feels fast), at 129k it is seconds PER
CHUNK. A cold load of the 129k mailbox ran fast through the first
~10k threads, then ground to a crawl - exactly the sum above. The
same quadratic sat in the incremental cycle() path, which also
re-merges the full snapshot (plus the changed set) every cycle.

The spec's O(n+m) claim (M1 spec section 8) was correct about the
diff itself - DiffSorted/Apply are linear. The violation was in the
reconcile step that the spec's section 8 did not describe: it assumed
"recurses into each unchanged thread" as if thread lookup were free.

## 3. The fix

One map build per merge, O(1) lookups after:

```
byID := map[threadID]*Thread, built once from the post-Apply thread list
for _, in := range sorted:
    cur := byID[in.ID]
```

Per chunk: DiffSorted O(n+m), map build O(n), reconcile O(n+m), final
sort O(n) (input is already sorted - pdqsort single pass). The load
total drops to the dominant remaining term, Apply's per-insert shifts:

```
Apply shifts: sum over chunks of chunkSize * n_k / 2 = O(n^2 / 2)
129k threads: ~8.4e9 pointer copies, a few seconds total, one-time
```

The 10k knee is gone; steady-state incremental cycles stay O(changed
+ n) as designed. Both the full-reload path and the cycle path are
fixed by the same change (they share MergeThreads).

## 4. Verification

- `TestMergeManyThreadsInBatches` (view_test.go): three 3000-thread
  batches re-merging the full accumulated snapshot each time, then a
  full-snapshot re-merge asserting no duplication. Encodes both the
  map-lookup correctness and the replace-semantics contract the
  refresher relies on.
- Full suite green: core, app, cache.
- Real cold load on the 129k mailbox: fast through the full result
  (user-observed after the fix; a NOTMUCH_BENCH=1 timing run of the
  load phases is the permanent gate).

## 5. Related fix in the same load path: cache write batching

The cache fill had the same class of bug one level down: `cachejob`
wrote every scanned message with `cache.Put`, and each `Put` was its
own bbolt transaction - one fsync per message (bbolt's default
NoSync=false). bbolt's ~300k ops/s batch figure is no-fsync; with a
fsync per commit it runs at ~1k/s on SSD. A cold-cache fill of 129k
messages was ~130s of pure disk sync.

Fix: `PutBatch` on the Cache interface (R13 keeps the backend
swappable), one transaction per scan run. bbolt commits the batch in
one `Update`; `Put` delegates to it. The cache job collects entries
under its existing mutex and flushes once per scan run. Same fix
serves the index cache's batch ingestion (AGENTS.md R1: one bbolt
transaction per emitted chunk).

## 6. Remaining knob (do not touch yet)

`Apply` inserts chunk rows with `insertAtIdx`, shifting O(n) pointers
per insert - O(chunk*n) per chunk, the current dominant term of a
cold load (a few seconds at 129k). A bulk-insert apply would remove
it, but only if a measured cold load says the remaining seconds
matter. Measure first (AGENTS.md: benchmark before optimization).
