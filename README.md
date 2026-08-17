# notmutt

notmutt is an async, command-line-first mail client built on notmuch.
Tags are the logical model: every view, filter, and trigger is a notmuch
query or tag operation; folders exist only for sync-tool compatibility.
Written in Go - tcell TUI (lipgloss for layout math), go-message for
mail parsing and composition, TOML config, vim keybindings by default.

Requirements and architecture are normative in AGENTS.md; the design
decisions and their measurements live in docs/design-decisions.md.

Status: M1 (mailbox view) and M2 (staged tag ops, send dialogue) are
done, including the render-coalescing round. The notmuch CLI is the
runtime backend (measured 1.5s full walk vs the cgo binding's 8.7s on a
33k-thread inbox); the index is a materialized bbolt cache keyed by
notmuch's revision.

Keybindings: `o` opens a thread (marks it read), `p` previews it in a
popup over the index without marking it read, `$` applies staged tag
ops, `u` undoes them. The binding map is declarative per context; the
help overlay (`?`) derives from it.

## Rendering

notmutt renders with tcell (screen cell buffer, internal cell diff,
input) on top of lipgloss (layout math and border/box strings in the
frame builders). The first versions of the client used BubbleTea v2 as
the renderer; the model architecture did not move with the flip - the
frame builders, the paint gate, and the async core are renderer-
agnostic by design (R5), and the flip touched only the tui package's
render path.

The structural differences from neomutt are unchanged by the flip:

1. The view's truth is a thread TREE (new mail inserts into existing
   threads, never a rebuild, R3); the flat row array is DERIVED by a
   flatten that costs O(rows) whenever the tree changes (8us at 100
   rows, 6.3ms at 33k). neomutt paints directly from a flat array and
   never pays that derivation in steady state.
2. Every message rebuilds the whole frame string in Go - every row
   re-styled - before the renderer can diff anything. tcell's
   Screen.Show() diffs the frame against the previous cell buffer, so
   the terminal write is cheap; the cost is the full styling pass,
   which runs per message regardless.
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
| frame rebuild, all rows cached (40 visible @ 5k list) | 182us uncached | 24us (7.7x) |

The mechanisms: SGR sequences precomputed at style resolution (was 58%
of the frame build), lazy pager styling (visible window only), the
50Hz legend tick killed on release-reporting terminals, a memoized
flatten rebuilt only on dirty marks with the refresh merging in
batches, and render coalescing: state updates land at input rate,
paints coalesce at an 8ms cadence (the debounce and the cadence are
one constant), the key release paints immediately to settle a hold.
On top of that: a content-addressed row-string cache (a cursor move
restyles only the two rows whose selection flips; every other row
concatenates from the cache) and region layers for the keyhint,
status, and help rows, which rebuild only when their inputs change.
All of it survived the tcell flip - these are model-side costs, and
the paint gate that triggers them is the same gate that pushed frames
through the v2 renderer.

### Why tcell (2026-08-17, decision record 23)

The trigger was the vendored BubbleTea v2 renderer being the wrong
trust boundary. An out-of-bounds frame bug (2026-08-15) was fixed
model-side (record 16's padding rule), and the vendored ultraviolet
diff engine was then verified byte-correct - but verifying it required
a decoder test harness that re-implements what tcell's Screen.Show()
does natively: a cell buffer you can dump and reason about directly.
That debugging cost repeats on every renderer artifact, real or
suspected.

tcell is the mature version of the same idea - screen cell buffer,
internal diff, Show - with a decade of production use; lazygit, this
project's R5/R9 architecture reference, is tcell directly, which
validates the pairing. tview was rejected in the R7 record for
coupling app state into its primitives; tcell has no such layer, it
is a screen and an event source, nothing more.

The frame builders still emit SGR strings; a small adapter
(pushFrame) parses them into per-cell tcell styles at the screen
boundary. Only the client's own frames arrive there, so the parser is
a strict param-walk over known sequences, not a general terminal
emulator.

The remaining structural differences are deliberate: full-frame
rebuilds are the price of a maintained model, and the thread tree is
the price of diff-and-insert refresh - neomutt's new-mail path re-runs
the whole query and rebuilds its thread tree
(notmuch/notmuch.c:2183-2308), which notmutt avoids entirely. Post-fix
steady state is sub-150us per press: the structural costs are
amortized below perception.
