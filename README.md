# notmutt

An async, command-line-first mail client built on notmuch. Tags are the
logical model: every view, filter, and trigger is a notmuch query or tag
operation; folders exist only for sync-tool compatibility. Written in Go
- tcell v3 TUI (lipgloss v2 for layout math), go-message for mail
parsing and composition, TOML config, vim keybindings by default.

Requirements and architecture are normative in AGENTS.md; the design
decisions and their measurements live in docs/design-decisions.md.
User documentation (features, installation, usage) lives on the project
site: <https://fishman.github.io/notmutt/> - the pages are in `docs/`.

## Why notmutt

- **Nothing blocks you.** Sync, filtering, tag pipelines and sends run
  as background jobs; composing, reading and navigating never wait on a
  query or a network round trip. A filter run can retag and re-render
  the mailbox while you keep typing in the compose tab.
- **Every tag op is undoable.** Actions (archive, delete, flag, read)
  stage into a buffer and hit notmuch only when you apply (`$`). A
  mis-tap is one `u` away - neomutt makes every tag application final.
- **One message, one home.** Folder tags form a declarative exclusive
  group: applying any member removes the others. No hand-maintained
  `-tag` chains in your config, no conflicting folder tags.
- **Privacy by default.** Remote images stay collapsed until the
  load-remote-images key (alt+i) - and 1x1 tracking pixels drop even
  then. No telemetry, no account sync, no mail content ever leaves
  your machine (crypto runs through your system `gpg`, never a
  vendored library).
- **notmuch is the only truth.** No own database; the index is a
  revision-keyed cache that re-syncs from notmuch's lastmod. Folder
  state is derived, never authoritative.
- **Terminal images.** Sixel by default, kitty opt-in - rendered
  inline in the pager, decoded only on demand.
- **Config as data.** Everything is TOML - themes with palette
  indirection, declarative per-context keybindings (the help overlay
  derives from the binding map), tag styles, glyphs.

Status: M1 (mailbox view) and M2 (staged tag ops, send dialogue) are
done. The cgo binding is the runtime backend (1.645s full walk vs the
CLI's 1.534s on a 33k-thread inbox; the CLI survives behind `-tags
cli`); the index is a materialized bbolt cache keyed by notmuch's
revision. See [docs/faq](docs/faq.md) for what is and is not there yet.

## Quick start

Requires a recent Go toolchain and libnotmuch (the default build links
the cgo binding; `-tags cli` builds against the `notmuch` CLI instead,
same code, one build tag away):

```sh
git clone git@github.com:fishman/notmutt.git
cd notmutt/src
go build -o ../notmutt .
cd ..
./notmutt
```

Config lives at `~/.config/notmutt/config.toml` (the built-in defaults
in `src/config/base.toml` are the reference - search it first, the
user file only overlays). See the [usage page](docs/usage.md) for the
keybindings and configuration.

Keybindings: `enter` opens a thread (marks it read), `P` previews it
in a popup over the index without marking it read, `v` (in the pager)
toggles the plain/html part view, `ctrl+u` (in the pager) shows the
html part's raw source, `h` toggles the full raw header block
(delivery headers included - Received, DKIM-Signature, SPF - not the
curated from/date summary), `alt+i` loads remote images (see HTML
rendering), `F` enters the easyjump link mode (see HTML rendering),
`$` applies staged tag ops, `u` undoes them. The binding map is
declarative per context; the help overlay (`?`) derives from it.

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
   never pays that derivation in steady state. Cursor resolution was
   the same hazard once removed: every render resolved the cursor
   through a view method that re-flattened the list per call. The
   cursor index now lives in the view - moves write it, merges
   re-anchor it once at materialization - so a press never touches
   more than the visible window.
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
| keypress on the full 30k list (cursor resolve) | ~8ms flatten+scan per paint | 12us (O(1) index read) |

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

Upgraded to tcell v3 (the lazygit pin) and lipgloss v2 the same day
this record was written: v3 deleted the terminfo database - color
support is env-heuristic, so TERM=tmux-direct/foot-direct and
COLORTERM=truecolor resolve truecolor natively (the R11 baseline)
without any terminfo entries. The v2 fork (row-dirty skip, truecolor
env fix) is retired from go.mod; v3's native per-cell dirty skip and
its env detection cover both. The port cost: SimulationScreen is gone,
so the loop tests drive a ~110-line fake screen behind the Screen
interface, events arrive on the Screen's EventQ channel (no
ChannelEvents forwarder), and key releases need an explicit Pressed()
filter. lipgloss v2 (charm.land vanity path) renders through
colorprofile internally, so termenv is no longer a dependency and the
v1 profile pinning is deleted; its abbreviated "\x1b[m" reset is
stripped alongside the v1 form when an SGR open is derived from a
style.

The remaining structural differences are deliberate: full-frame
rebuilds are the price of a maintained model, and the thread tree is
the price of diff-and-insert refresh - neomutt's new-mail path re-runs
the whole query and rebuilds its thread tree
(notmuch/notmuch.c:2183-2308), which notmutt avoids entirely. Post-fix
steady state is sub-150us per press: the structural costs are
amortized below perception.

## HTML rendering

HTML mail renders inline in the pager - parsed and laid out in Go
(x/net/html, error-tolerant and fuzz-exercised), never a browser. The
layout is CSS 2.1 block flow with inline runs and column-aligned
tables (docs/html-rendering-analysis.md); everything outside it
(position, float, flex, media queries, scripts) drops. The mail only
needs a subset of CSS, and the renderer understands exactly that
subset: color and background-color (hex only - gradients, transparent
and current-color fall back to inherit), font-weight (bold), font-style
(italic), text-decoration (underline), text-align, display
(block/none), white-space (pre/pre-wrap/pre-line), plus the tag
defaults (b/strong bold, i/em italic, u underline). That covers the
common mail shape. Layout is budgeted: wraps at 120 columns and caps
at 5000 lines, so a hostile or broken doc cannot balloon the thread.

Images render as placeholders - the bytes travel with the line and the
TUI decodes them only on the load-remote-images key (alt+i, a privacy
gate). Remote image srcs fetch on that key too, through the same gate:
`[pager] image-protocol` selects the terminal protocol (sixel by
default, kitty opt-in - most terminals do not speak kitty's graphics
protocol), fetches are size-capped and time-bounded, and 1x1 tracking
pixels drop unless `[pager] allow-tracking-images = true`. Note: tmux
does not pass either image protocol through - images paint on a
sixel-capable terminal outside tmux.

The easyjump link mode (`F`): every link - an anchor href or a bare
URL word - gets an inline [N] label, and the key input is just numbers
and backspace - no prompt, the selection IS the feedback: the [N] of
the number under entry highlights reversed as you type. A digit that
completes a number (nothing longer can extend it) opens that link on
the spot through the configured urlopener (`opener = [...]` in the
config, `xdg-open` by default); enter confirms the highlighted link.
A message without links reports "no links in this message" instead of
arming a dead entry. While the label mode is active the pager scroll
keys stay live - j/k, space, pgdn/pgup, ctrl+d/ctrl+u, g/G - so links
below the fold are reachable; the label list is the document order,
independent of the scroll position. esc or F again exits without
opening. In the plain view `F` lists the visible links in the fuzzy
picker instead.
