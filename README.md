# notmutt

notmutt is an async, command-line-first mail client built on notmuch.
Tags are the logical model: every view, filter, and trigger is a notmuch
query or tag operation; folders exist only for sync-tool compatibility.
Written in Go - bubbletea v2 TUI, go-message for mail parsing and
composition, TOML config, vim keybindings by default.

Requirements and architecture are normative in AGENTS.md; the design
decisions and their measurements live in docs/design-decisions.md.

Status: M1 (mailbox view) and M2 (staged tag ops, send dialogue) are
done, including the render-coalescing round. The notmuch CLI is the
runtime backend (measured 1.5s full walk vs the cgo binding's 8.7s on a
33k-thread inbox); the index is a materialized bbolt cache keyed by
notmuch's revision.

## Rendering

BubbleTea v2 re-renders the whole frame on every message, which is where
notmutt's rendering differs structurally from neomutt's - and why the
merge system can cost more than neomutt's in general:

1. The view's truth is a thread TREE (new mail inserts into existing
   threads, never a rebuild, R3); the flat row array is DERIVED by a
   flatten that costs O(rows) whenever the tree changes (8us at 100
   rows, 6.3ms at 33k). neomutt paints directly from a flat array and
   never pays that derivation in steady state.
2. Every message rebuilds the whole frame string in Go - every row
   re-styled - before the renderer can diff anything. v2's renderer
   itself writes incrementally (a cell-level diff against the previous
   screen, capped at 60fps), so the terminal write is cheap; the cost is
   the full styling pass, which runs per message regardless.
3. The loop renders once per message, so a held key was one full frame
   build per repeat, and every merge during a refresh marked the view
   dirty - the next paint paid the full flatten again.

The render-coalescing round addressed all three. Measured on the
33k-thread inbox:

| scenario | before | after |
| --- | --- | --- |
| held-key burst, 50 presses | 50+ full-frame paints | 6 (one per 8ms window + settle) |
| single press, full list | ~2.5ms frame build | 133us |
| pager resize, 20k-line document | 385ms | 44-74us |
| fill-window press, whole-fill batch | 2.61ms | 147us (17.7x) |

The mechanisms: SGR sequences precomputed at style resolution (was 58%
of the frame build), lazy pager styling (visible window only), the
50Hz legend tick killed on release-reporting terminals, a memoized
flatten rebuilt only on dirty marks with the refresh merging in
batches, and render coalescing: state updates land at input rate,
paints coalesce at an 8ms cadence (the debounce and the cadence are
one constant), the key release paints immediately to settle a hold.

The remaining structural differences are deliberate: full-frame
rebuilds are the price of a maintained BubbleTea model, and the thread
tree is the price of diff-and-insert refresh - neomutt's new-mail path
re-runs the whole query and rebuilds its thread tree
(notmuch/notmuch.c:2183-2308), which notmutt avoids entirely. Post-fix
steady state is sub-150us per press: the structural costs are
amortized below perception.
