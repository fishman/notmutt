---
layout: default
title: Design decisions
nav_order: 5
---

# notmutt - design decisions

Decision record: what was chosen, why, and what was measured. The
architecture (what exists) lives in DESIGN.md; AGENTS.md is normative
(requirements R1-R15). This document is the WHY - the reasoning and the
measured verdicts behind each choice, with the decision date.

## 1. Go over Rust/Zig (R7)

Decision: Go. The full comparison table is AGENTS.md R7; the decisive
factors: go-message is aerc's production parser (the same worker model
notmutt mirrors, R3/R4), goroutines make the async model native, and the
vendored go.notmuch fork carries DB.Revision() for the refresh cycle.
Rust's strengths (ratatui, mlua, tokio) are real; they lost on
integration surface - mirroring aerc's proven stack beats best-in-class
per part. Zig is greenfield on every dimension.

## 2. notmuch is the single source of truth (R1)

Decision: no own database. The one derived store is the bbolt index
cache - a materialized view of the overview query, revision-keyed,
invalidated by notmuch's lastmod, rebuilt from query output only, never
written independently. Every mutation goes through notmuch; a read that
finds the cache stale re-syncs from notmuch (startup is O(changed), a
full walk only on cache miss or revision mismatch).

## 3. cgo as the runtime backend (R1, 2026-08-14, flipped 2026-08-16)

Decision: cgo (go.notmuch, vendored fork) IS the runtime backend; the
CLI backend survives behind `-tags cli` as the F10 escape hatch - the
same binary code, the same interface, one build tag away. Measured on
the inbox (33k mails / 32952 threads, NOTMUCH_BENCH=1):

| operation | CLI | cgo (per-message) | cgo (batched walk) |
|---|---|---|---|
| peek (50) | 16ms | - | 11ms |
| full walk | 1.58s | 8.7s | 1.65s |
| thread fetch | 18ms | - | 8ms |

Flip-day re-run (2026-08-16): cgo full walk 1.645s vs CLI 1.534s -
parity, within run-to-run noise; the cgo peek (50) is 12ms and the
thread fetch 8ms vs the CLI's 19ms. The 8.7s was the per-message
iterator overhead (~230us per row); the batched walk keeps the query
and threads iterator alive in C across chunks and packs per-thread
summaries into one buffer per boundary crossing (ThreadsWalk in the
vendored binding), so the per-thread header-cache reads amortize
C-side exactly like `notmuch search` does. Both backends emit
identical per-thread summaries (thread id, newest date, authors,
subject, tags), so the merge path is shared; per-message data still
comes from Thread, on open only. The CLI's write-at-end JSON wall
(measured 1.4s) is mitigated by the two-step fill, not by chunk
streaming - json0 is unsupported, so the parse buffers; chunk slicing
alone cannot make the first paint early.

Lock footprint: the cgo handle opens READ-ONLY and stays read-only for
the fill; Tag reopens read-write for the op only (one atomic
transaction, like `notmuch tag`) and reopens read-only after. A
persistent write handle would hold Xapian's exclusive flock for the
process lifetime and block every other notmuch process (including the
sync hook's `notmuch new`) - the reopen pattern keeps the CLI
backend's exact lock footprint. The DB path resolves at open via
`notmuch config get database.path` (argv-only, F4) when not configured.

## 4. Mail parsing/composition: go-message (R6, 2026-08-14)

Decision: emersion/go-message v0.18.2. Re-checked before M1: the only
candidate with parse AND part-level compose in one library
(ProtonMail/go-mime is parse-only; its composer lives inside
proton-bridge, not importable). Proven in aerc - the reference worker
architecture. Dependency tree: golang.org/x/text only. mailcap support
is client-side logic over its part model in all cases.

## 5. TUI: BubbleTea v2, lazygit as the architecture reference (R7)

Decision: BubbleTea v2. lazygit is the reference for HOW to structure a
Go TUI - view models separated from rendering, actions as
keybinding-driven controllers, config-driven binding maps, cancellable
background tasks - not the renderer. The one vendored change to the
BubbleTea loop is the ShouldRender gate (see 14).

## 6. Config: TOML is the schema shape (R8)

Decision: config files unmarshal 1:1 into typed structs; load is strict
(unknown keys are errors); the store is the single write path; runtime
mutations go through typed per-section setters that notify per-section
observers on the event bus. neomutt's ConfigSet is requirements (typed
values, validators, defaults, observers), not mechanism - TOML's
document shape needs struct-mapped tables, not dotted keys.

## 7. Keybindings are data, chains expire (R9, M2 task 15)

Decision: declarative binding maps per context (global, index, pager,
compose...); vim default, emacs as a config choice; the keyhint/help UI
derives from the map, so rebinding updates the hints. Multi-key
bindings are space-joined keys ("g g": cursor-top) resolved by a chain
state machine with a timeout (one second, a var tests can zero) that
expires the chain; counted prefixes (12g) feed a count. '?' lists the
current context's bindings - and an armed prefix lists its chained
continuations.

## 8. Crypto via system tools (R10)

Decision: zero crypto code in the client. gpg CLI with --status-fd
parsing, S/MIME via openssl smime, passphrase only through gpg-agent +
external pinentry with TUI suspend/resume - the ONLY prompt path. No
loopback mode: Go cannot zero secrets, smartcard PINs fail under
loopback. The crypto layer takes a PromptFunction; it never prompts
itself.

## 9. Async bus with last-value snapshots (M1-M2)

Decision: inter-layer communication goes through a bus with bounded
subscriber channels - Publish drops on a full channel (64 slots) rather
than blocking, and the latest event of each kind is kept as a snapshot
for late joiners. M2 extended it with per-tab last values (SendResult
per dialogue id, the opened-set for one-shot ComposeOpened) so a
completion racing a tab switch is never lost; a send re-arm clears the
snapshot so a stale failure cannot re-open a closed dialogue. Residual
(a channel-delivered failure racing a retry dispatch, sub-ms) is
accepted; send-epoch stamping is the future hardening.

## 10. Staged tag operations (R14)

Decision: tag actions stage into a per-session buffer; notmuch sees
them only on APPLY; the buffer IS the undo mechanism. Exclusive-group
resolution runs at stage time (for the render) and again at apply
against current state. The refresh never clobbers the buffer: snapshot
truth merges first, pending ops replay on top. M1's immediate toggle
was the placeholder this supersedes.

## 11. Dialogues are state machines, gated (R4, M2)

Decision: compose state (fields, attachments, send progress) lives
outside the UI, so a background filter run can retag and re-render
while the user keeps typing. Phases: Editing -> Sending -> Failed |
closed; q arms Aborting, a second q confirms. The M2 fix round closed
the in-flight race: send sets PhaseSending unconditionally inside the
gate, abort no-ops during Sending, detach/attach/edit are gated on
PhaseSending. A failed send retries from the same dialogue (PhaseFailed,
e) - state survives the pause.

## 12. Filter engine as a boundary (R2)

Decision: filters are a module that consumes a message snapshot and
produces tag ops; query rules are data, algorithmic filters (bayes,
DKIM) plug in behind the same interface later. Exclusive tag groups
resolve after every filter, so algorithmic output normalizes into
hard-tag exclusivity. The mover is native - per-account tag -> folder
maps, first-existing-wins, `*` globs, copy-then-delete; it updates the
DB through the client's own notmuch layer, so afew's stale-handle
workaround does not exist here. DRY-RUN is a first-class job mode.

## 13. Derived caches: bbolt, interface-first (R13)

Decision: both caches (index overview, MIME metadata) sit behind the
same Cache interface (Get/Put/Delete); bbolt is the embedded default,
0600. The index cache ingests the CLI's JSON output as typed structs in
batch - one bbolt transaction per emitted chunk. A JSON-ingesting DB
(sqlite json_each) was considered and rejected: the parse is ~10ms, and
the write-at-end mset wall is untouched by the ingestion mechanism.

## 14. Render coalescing (2026-08-15, the lag round)

Decision: BubbleTea v2's loop renders unconditionally on every message -
the model's View() builds the full frame string per message; the
renderer's 60fps tick only throttles the terminal write (cell-level
diff), never the build. No aggregation of key repeats (one KeyPressMsg
per physical repeat): a held key was one full frame build per repeat.
The vendored gate:

    if r, ok := model.(interface{ ShouldRender() bool }); ok && !r.ShouldRender() {
        // tick paints at cadence
    } else {
        p.render(model)
    }

State updates land at input rate (the "calculate where I land" part);
paints coalesce at an 8ms cadence - the debounce and the cadence are
ONE constant by design; the key release paints immediately to settle
the hold; all non-navigation messages paint immediately. A press
between paints is free; intermediate positions are sub-8ms apart and
never perceived.

Measured on the 33k-thread list, held-key burst of 50 presses: 50+
full-frame paints -> 6 (one per frame window plus the settle paint).
The same round: pager resize on a 20k-line document 385ms -> 44-74us
(lazy window styling); SGR sequences precomputed at style resolution
(was 58% of the frame build); the 50Hz legend tick killed on
release-reporting terminals; the flatten memoized and dirty-batched
(see 15). Steady state: 133-148us per press.

## 15. The fill's dirty-mark granularity (2026-08-15)

Decision: the refresher wraps each emitted chunk in BeginMerge/EndMerge,
so the view marks dirty once per batch and the memoized flatten rebuilds
once per batch end. Per-chunk measures at noise level vs no batching,
because the flatten cost is paid at render and each chunk already lands
one ViewDiff - the batching only helps when merges are NOT accompanied
by renders.

The measurements that settled it:
- whole-fill batch: 147us per press in the fill window vs 2.61ms
  (17.7x), but the list appears in one jump - progressive reveal lost.
- per-page (1000-row) batching: REJECTED after the measurement - the
  backend's steady chunk is 5000 rows, so 1000-row pages mean 33
  dirty-marks per fill against the committed state's 7: more flattens,
  not fewer. The earlier "1000 entries per page" budget belonged to
  offset paging, which measurement killed (~40s for 33 paged calls vs
  ~5s for one walk).

## 16. Render caches: content-addressed rows and region layers (2026-08-15)

Decision: the frame's regions cache model-side. BubbleTea v2 has no
layer API (the View is one flat string; the vendored source's layer
doc mention is stale), so the cache lives on the model: the keyhint,
status, and help regions each rebuild only when their inputs change
(mode, width, progress values, style version), and index rows cache
content-addressed by the row's address plus every style-affecting
parameter (numWidth, tagWidth, width, styleVer, selected, atts).

Why the keying works: merges, tag changes, and staged ops reflatten
and churn row addresses (auto-miss); SetAtts mutates the shared
message without a reflatten, so the atts bool (the attach icon reads
only len(Atts) > 0) covers it; styles resolve at two sites, covered by
one styleVer bumped on theme changes. The cache is bounded (8192
rows - a full walk at a few widths fits; overflow clears wholesale
once per large scroll, never per press). The program holds the model
by value, so render-time cache writes persist only through reference
fields (the map and the layer pointers) - the shape the cache design
is built on.

Measured (5000-row list, 40 visible): hit path 23.6us vs 182us
cleared per frame build (7.7x) - the steady-state press was 133-148us
before the cache. The benchmark trap: a never-moved model resolves
the cursor via the view's flattening CursorRow per frame (the
documented page-key stall at 33k rows), which dwarfs the row cost -
the benchmark arms the cursor-id scan with one real move first.

## 17. Privacy and trust boundaries

Decision: mail content (bodies, headers, whole .eml/.mbox files) is
never submitted to an LLM; a field inside mail is extracted by script
first and only the extracted value is passed; message identity
correlates by checksum. Rendered mail passes the sanitizer (ESC/C0/OSC
stripped) before reaching the terminal (F1); exec is argv-only - mail
content never interpolates into shell strings (F4); everything written
is 0600/0700 (F5/F7); bodies, headers, and passphrases are never logged
(F6).

## 18. Supply chain

Decision: minimal, deliberate dependency set - every dependency must
earn its place; exact pins; the build is vendored and reproducible; the
go.notmuch is pinned at the v0.40.0 release, go.sum-verified, vendored;
Spec/doc commits carry the AI-assisted trailer; code is
reviewed like any other contribution, with tests proving the edge
cases.

## 19. Preview popup vs open-reads (2026-08-15)

Decision: two surfaces with different read semantics. `p` (preview)
opens a popup over the index that does NOT mark the thread read - the
index mode stays put, the box borrows the pager surface (scroll keys
are the pager bindings, R9 data-first: activeBindings flips to the
pager map while the popup is open, so the keyhint derives the same
way). `o` (open) is the full pager AND tags the thread -unread (R1:
read is a tag, the refresh cycle reconciles it into the view).

The async shape: the preview fetch is a regular ActThread with a
Preview flag on the ThreadLoaded reply. The model drops a preview
reply that no longer targets the armed popup (closed or re-targeted
meanwhile) - a stale reply can never force a full open. The preview
target's reply re-asserts the popup over a racing full-open reply
(FIFO ordering). `o` inside the popup promotes it: the pager keeps
content and scroll position (the idempotent reload guard treats the
already-loaded thread as a no-op) and re-sizes to the full frame; an
empty in-flight preview rebuilds fresh. Failed preview loads close
the box silently; a failed mark-read on open publishes a JobError and
keeps the thread open.

Found by test: index mode renders short lists shorter than the
window (only the empty view pads to height), so the whole-line splice
in the popup shrank to nothing over a short mailbox. The popup pads
the list section before the keyhint/status tail, so the box always
splices a full-height frame.

## 20. Lua plugin integration: what matcha proves (2026-08-15)

Source studied: matcha (floatpane) - a production Go mail client with a
gopher-lua plugin system (`plugin/` package, `docs/Features/Plugins.md`,
main.go orchestrator). The closest existing proof of R8's Lua-on-top
model inside a Go mail client. The design is carried into notmutt's Lua
layer on these points, with the gaps matcha leaves flagged below.

1. One VM, one goroutine. matcha's plugin.Manager is explicitly not
   concurrency-safe by design: every hook callback, keybinding, and API
   call dispatches from the single orchestrator goroutine; there are no
   locks. notmutt's Lua VM lives on the async core's event loop; the
   TUI never calls into it (the R3/R4 channel discipline).
2. Protect-then-log dispatch. Every callback runs with Protect: true;
   an error logs and the NEXT callback still runs. Load errors log and
   skip the plugin; a plugin counts as loaded only after a clean
   DoFile. A throwing plugin degrades; it never kills the client.
   Hooks chain in registration order; the one returning hook (body
   render) threads output through the chain and ignores non-string
   returns.
3. Sandbox is a lib whitelist. SkipOpenLibs, then open only
   package/base/table/string/math. No os/io/debug: no filesystem, no
   exec. HTTP is the only capability, with hard limits: 10s timeout, 1
   MB response cap (LimitReader), http/https schemes only.
4. Deferred side effects (the pending-* pattern). Plugin API calls
   never mutate UI or mail state directly: notify, set_compose_field,
   mark_read, prompt, suppress_auto_read all SET pending values the
   orchestrator drains after the hook returns and converts into its
   own async commands ("the change is applied after the hook
   returns"). notmutt's equivalent: plugin effects are BUS EVENTS -
   the plugin job emits, the app consumes on the async channel, never
   mid-render. suppress_auto_read is the one read-back value (a flag
   consumed immediately after the email_viewed hook, not queued) -
   the exact shape notmutt's open-reads suppression would take.
5. Per-plugin identity threading. currentPlugin is set around load and
   every callback (defer-restored), so every API binding knows its
   caller: storage is per-plugin KV files (name validated
   ^[a-zA-Z0-9_-]+$ - a path-traversal guard - 0700 dir, atomic
   tmp+rename flush, the F5/F7 discipline); bindings and settings
   record their plugin.
6. Plugin-declared settings. matcha.settings({key={type,default,label,
   description}}) at load + matcha.get_setting(): a plugin configures
   itself inside its own file, so the core config schema never needs
   unknown-key tolerance. Adopted: plugin settings stay OUT of the
   strict TOML load (R8); a [plugins.<name>] TOML section is a load
   error unless the plugin declares it via the settings API.
7. Keybindings merge as a fallback layer. bind_key(key, area,
   description, fn): plugin keys are a separate registry checked after
   core bindings (core keys win, a plugin cannot shadow them), and the
   descriptions merge into the derived help bar - R9's data-first rule
   extended to plugin keys.

Gaps matcha leaves open (design around these):

- Render-path hooks run inline. email_body_render executes Lua inside
  the view-render flow on the orchestrator goroutine; a busy-loop
  plugin freezes the UI - only HTTP has timeouts, hook execution has
  none. FIXED (2026-08-15): notmutt's body-render hooks run on the
  async open job (app.BodyRenderHook registry, `applyBodyRenderHooks`
  in app/app.go) with a chain deadline and fall back to the un-hooked
  render on error or budget overrun; the render itself moved with the
  hooks - the open job renders the thread (mail.RenderThread) and the
  TUI attaches the lines from the ThreadLoaded event, it never parses
  mail on its event path. The hook context carries the deadline
  (context.WithTimeout), the wire point for gopher-lua's SetContext
  kill switch (v1.1.2, unused by matcha); the adapter landed
  (src/app/lua_plugin.go, `//go:build lua`, the R12 build-gating
  pattern - default builds stay Lua-free) with plugins from
  `<configdir>/lua` and one `body_render(lines)` hook per plugin,
  deadline-killed and falling back like any Go hook. Rendering left
  the UI path entirely, so the inline-render freeze class matcha
  guards against only partially cannot exist at all.
- No hot reload. Plugins load at startup; edits need a restart. Fine
  for the first Lua milestone; reload is a future knob.
- Render hooks see mail content by design. matcha passes raw bodies
  into Lua (email_body_render). notmutt keeps the R2 boundary: the
  filter-engine Lua hooks (content-consuming) run in the filter job
  in-process and never send content anywhere; the UI-side render hook
  gets the already-rendered (sanitized, F1) display string plus a
  preview-budgeted raw slice, never whole mail files (F6).

## 21. Platform notifications: beeep, auto-detected (2026-08-17)

Decision: the [notify] side effect (R2) gains a platform backend
alongside the argv command. "beeep" (gen2brain, v0.11.2) is the
backend: cross-platform by construction (Linux via esiqveland/notify
on DBus, macOS via osascript, Windows via toast), static title, body
built client-side. The backend is AUTO-DETECTED, not configured or
build-gated (the R12 build tag was dropped at the user's direction -
the reference ~/.config/mutt/notmuch-notification.sh needs no setup
and neither should the client): empty `[notify] backend` (the
default) resolves once at startup to "beeep" when a notification
daemon is reachable, "command" otherwise; explicit config always
wins. The probe is a session-bus call to
org.freedesktop.Notifications.GetServerInformation with a 1s budget
(darwin always - osascript is part of the OS; elsewhere never).
beeep keeps its own dbus -> notify-send -> kdialog fallback per show
either way, so a daemon that dies mid-session degrades, never
silently drops.

Why beeep over alternatives: a pure DBus notification client
(godbus+org.freedesktop.Notifications) is fewer lines today but
Windows/macOS would need a second backend - the user's stated
forward goal is macOS support; beeep is one pinned dependency with a
platform switch inside. The argv backend stays reachable: any
command (dunst, notify-send, swaync) keeps working, tokens
{count}/{subjects} in argv - the notmuch-notification.sh shape
(count + up to N newest subjects via notify-send) is the payload
reference, not a copy.

Payload: the count plus the subjects of entries carrying a priority
tag ([notify] priority = ["urgent", "work"] - soft tags, matched
against the entry's RESOLVED tag set, so group exclusivity counts),
capped at [notify] max (default 3, 0 disables subjects). Subjects are
the maximum content the notification ever carries (F6 - never bodies
or ids); the subject arrives on the delta query (the snapshot fetch,
zero file opens) and flows Entry -> FilterDone -> notify. The beeep
body is the same payload, newline-joined.

The references/aerc/matcha survey verdict (2026-08-17): neither has a
classifiable notification surface; aerc notifies via command hooks,
matcha has no notify path. The argv backend (the references/muttrc/notify
command shape) is the reference, not a library.

## 22. Config precedence: config.toml is the main file (2026-08-17)

Decision: the config dir holds any number of *.toml files -
config.toml, accounts.toml, filters.toml split freely, one file as
the degenerate case. The merge precedence is FIXED, not
alphabetical: the optional splits (accounts.toml, filters.toml,
anything else) merge first in sorted name order, then config.toml
merges LAST and wins any conflict. The main file is authoritative;
the splits are partitions of its sections. A key set in both
config.toml and a split resolves to config.toml; a key present only
in a split survives (the setup-generated accounts.toml is the
canonical example - [accounts.*] lives only there and loads
unchanged). Rationale: the single-file user writes everything into
config.toml and must not observe different behavior than the split
user; alphabetical ordering would let filters.toml silently override
the main file, which inverts the mental model "config.toml is the
config". Strict load (R8) still names the FILE carrying an unknown
key, so attribution survives the merge.

## 23. TUI: tcell + lipgloss (2026-08-17, flips 5)

Decision: move the TUI render path from BubbleTea v2 to tcell, keeping
lipgloss. The model architecture does not move with it - the frame
builders stay, the async core stays, R5's view/rendering split stays.
Only the trust boundary changes: tcell owns the screen buffer, the
cell-level diff, and input; lipgloss keeps what it already does (layout
math, border/box strings, string styling in the frame builders); R11
gains a tcell.Style emitter alongside its SGR emitter. Record 5
(BubbleTea v2) is superseded; the AGENTS.md R7 table and the language
decision record amend when the move lands.

Why: the vendored v2 renderer is the wrong trust boundary. The
out-of-bounds episode (2026-08-15) ended in a model-side fix (frame
height, record 16's padding rule), and the vendored ultraviolet diff
engine was verified byte-correct - but verifying it required a decoder
test harness that re-implements what tcell's Screen.Show() does natively:
a buffer you can dump and reason about directly. That debugging cost
repeats on every renderer artifact, real or suspected. tcell is the
mature version of the same idea (screen cellbuf + internal diff + Show)
with a decade of production use; lazygit - this project's R5/R9
architecture reference - is tcell directly with its own theme struct,
which validates the pairing. tview was rejected in the R7 record for
coupling app state into its primitives; tcell has no such layer, it is
a screen and an event source, nothing more.

Styling is where the flip is cheapest, not dearest: the R11 engine
already produces styles as data (palette -> fg/bg/attrs). Today that
data is rendered into SGR strings for the bubbletea styled-string
parser; with tcell it renders into tcell.Style per cell - the natural
representation, no SGR round-trip, no string walking. lipgloss's role
is unchanged (it already lives in the frame builders, spliceBox and
friends); the R11 theme records (records 14/16) survive verbatim.

What the migration touches (when it happens): the tui package render
path only - tea.NewProgram/Update/Cmd become a tcell.Screen plus a
hand-rolled event loop (tcell events feed the same keybinding dispatch;
lazygit's controllers pattern is the reference); tea.ExecProcess (the
editor/attach command flow) becomes a tcell.Suspend/resume around the
subprocess, the pattern lazygit's task system uses; the paint gate
(frame cache, record 14) survives as the trigger that pushes a frame to
the screen. core, app, filter, compose are untouched - R3/R4's async
model rides the bus, not the renderer.

Costs, stated plainly: the render path rewrite and its test churn; the
kitty keyboard protocol and altscreen setup move from bubbletea's
scaffolding into tcell's equivalents (tcell >= 2.8 carries enhanced
keyboard support - verify at implementation time); the frame-string
builders either grow a string-to-screen adapter (SGR runs -> tcell
cells, parse only OUR frames, trusted input) or skip the adapter and
emit tcell.Style directly. There is no live renderer bug driving this -
it is insurance and simplification, not a fix.

## 24. go-i18n: embedded catalogs, Lua bindings (2026-08-18)

Decision: go-i18n v2.6.1 catalogs ship INSIDE the binary. The
`src/i18n` package embeds `locale/*.toml` via `go:embed` and loads them
with `Bundle.LoadMessageFileFS` at startup (the v2.3.0+ fs.FS loader,
verified in the pinned v2.6.1 source) - never `LoadMessageFile`, never
a runtime read from the config dir or a data dir.

Why: single-binary distribution. The client already links libnotmuch
and reads config from one directory; a locale file tree would be a
second runtime data dependency with its own missing-file and stale-file
failure modes, and the config dir stays free of non-config content.
Catalog files are build input, like base.toml: the `goi18n
extract/merge` CLI regenerates them, and a new language is one toml
file in the embed tree plus the `[ui] language` resolution already in
the config store. The R8 constraint holds - TOML catalogs stay TOML,
the Lua story does not change the catalog format.

Lua bindings (R8, the R12 build-tag pattern): plugins get a
`translate(id)` function on the VM, registered under the `lua` build
tag like `register_attach_command` (src/app/lua_plugin.go vs the
`!lua` stub) and backed by the SAME bundle - plugin-provided labels
localize through the same catalogs as core strings, and a plugin
language is still selected by `[ui] language`, never by plugin
config. The core i18n package imports nothing Lua; the binding lives
in the lua-gated adapter. Default builds carry no VM and no binding.

## 25. AI: no vendor SDKs, stdlib HTTP + pass_cmd (2026-08-18)

Decision: the AI provider layer (src/app/ai, `[ai.<name>]` config) talks
to anthropic and OpenAI-compatible endpoints with the stdlib net/http
client only - no `anthropic-sdk-go`, no `go-openai`, no other vendor
library.

Why: we do not trust them. An SDK is a supply-chain liability, not a
convenience: every one of them adds a dependency tree that must be
pinned, vendored, and audited (R7 policy) for the sake of a POST with
headers and a JSON body - the vendor SDKs exist to sell their
platform's convenience features, and their value per byte of trusted
code is the worst in the tree. The protocol surface is two shapes
(anthropic /v1/messages NDJSON, OpenAI /v1/chat/completions SSE) that
a request reader can see in full; the stream parsing is a bounded-line
scanner, not a library-sized problem. The same reasoning already
applies to crypto (R10: system tools, never vendored crypto) - the
provider boundary is a network protocol, and the client keeps the
smallest auditable surface between the user's secrets (the pass_cmd
key) and the wire.

pass_cmd follows the same distrust: the key never lives in client
memory beyond one request (fetched per request, cleared after), never
appears in a log, and is referenced only where the auth header
requires it. argv-only exec (F4) - a secret-bearing command is
tokenized at config load, never shell-interpolated.

## 26. Lua network access: endpoint allowlist and the data policy (2026-08-20)

Decision: a plugin's network gate ([lua.network.<plugin>]) grants
REST access as host targets plus "METHOD /path" endpoint rules - and
a network-enabled plugin VM never sees mail content.

The verb allowlist does not exist. A verb alone is meaningless
without the path it applies to: "get" on a host that exposes
destructive GETs, or a list of verbs that must be re-verified against
every route the vendor adds, is security theater with a config file.
Each paths entry is one rule unit - verb (case-insensitive) plus
path, exact or trailing-`*` prefix - checked against the parsed URL
path before any dial, and re-checked on every redirect hop. A verb
that was never allowlisted against a path cannot be used on it;
adding a path the plugin should not reach requires a config change,
not a route discovery. Malformed entries are load errors (the strict
load rule: a dead allowlist that denies everything is safe but
invisible; an empty section that allows everything is the opposite
regression - neither may pass silently).

The data policy is the second half of the gate, and it is structural,
not observational: what a plugin can send is bounded by what ever
enters its VM. A plugin with a network section gets the metadata-only
ctx - thread_id, thread_info, search, count, the same projection the
MCP server exposes (one shared table builder) - and mail_lines (the
full thread plain text) is never registered on that VM. Bodies cannot
cross the allowlist because they are never present to cross it. The
rule is not configurable: content-plus-network would be an explicit
future decision, not a config knob.
## 27. Attachment categorization: manual, metadata-only Lua hooks (2026-08-20)

Decision: attachment categorization is a manual, headless command
(`notmutt attachments [--dry-run] [query]`), not a pipeline stage, and
its Lua `categorize(msg, att)` hooks see a metadata projection only.

The pipeline side effect was designed and rejected: an automatic
download pass on every poll would write files the user never asked
for, on mail they may not have inspected, and the filter job's
dry-run/review flow does not extend to file writes (dry-run mode
exists precisely because writes are irreversible). Attachment
downloads stay a deliberate act: run the backfill, review the
`save`/`skip` lines, re-run with a narrower query. The idempotency
check (existing target -> skip before the extract) makes re-runs
cheap and safe.

The hook contract is the privacy boundary's own shape: `msg` is
from/subject/date, `att` is name/mime/size - no paths, no message
ids, no content. A plugin cannot leak what it never receives. The
first non-empty category across hooks wins; a hook error falls
through to the next hook and surfaces in the save report when
nothing decided. Filenames and categories pass one sanitizer
(control runes dropped, `/` and `\` become `_`, empty/`.`/`..`
rejected) so a name is always a single path segment - traversal is
structurally impossible. The month folder comes from the message
date in local time; files are 0600, dirs 0700.
## 28. Mail handles and the MCP whitelist (2026-08-20)

Decision: the categorize contract passes an opaque mail handle plus a
metadata projection; the plugin fetches the attachment list from the
handle with the library command `get_attachments(handle)`. The MCP
server keeps a metadata-only default surface (thread_info, search,
count) and serves content-adjacent tools (attachments) only when
`[mcp] allow` names them.

The handle closes the sandbox gap: a plugin that cannot open files
still needs the message's attachment list, and passing the parsed
list per attachment would lose the ordinal model (categories are
keyed to positions, not names - two attachments may share a name).
The client registers the parsed list under an opaque per-message
handle for the duration of the save pass; the Lua binding reads that
registry. The plugin never opens a file - the list is exactly what
the client parsed, and the handle dies with the message pass.

The return shape is ordinal-keyed for the same reason: an attachment
is identified by its position in the message, so the mapping is
unambiguous even for duplicate names, and the client range-checks the
ordinal against the parse before extracting.

The MCP surface keeps the record 27 boundary in server form: default
tools are metadata-only, and anything content-adjacent is gated
behind the config's `[mcp] allow` list (unknown names are startup
errors). The gated attachments tool projects name/mime/size - never
bytes - and its ctx binding is registered in the per-call MCP VM,
never in the shared metadataCtxTable that network-enabled plugin VMs
see: a plugin VM structurally cannot reach attachment data, only the
stdio server's own tool chunks can, and only when whitelisted.
## 29. Full-walk hydration: one C pass replaces stub rows and the threadjob (2026-08-21)

Decision: the index fills from one full-walk pass over the query
result - the go.notmuch binding emits each thread's summary AND every
message row from a single C iterator (header-cache reads only, zero
file opens), and the client consumes it chunk by chunk with the
progressive-fill machinery (R3). The view holds real rows from the
first chunk: no stub rows, no per-thread hydration job, no row-cursor
scans. The full walk replaces the two-phase design (summary walk ->
stub rows -> threadjob per-thread fetches through the bounded worker
queue); the summary-only walk (ThreadsWalk) stays in the binding for
the consumers that need it.

The trigger was a regression (6c4042b9) that lost mail from the
index: two threads went missing until a tag op reconciled them - a
stub whose hydration the row-position scan cursor could never reach
(the cursor parked inside hydrated blocks). The stub machinery also
measured ~4.5 minutes to hydrate the 33k-thread inbox (the "hydration
storm": one scan per wave, every fetch its own event, the chain
terminates only when a scan page holds no stubs). One pass eliminates
the whole class by construction: no stubs, no cursors, no queue, no
chain - a row is either emitted or the thread is not in the result.
The walk's stall root cause is moot with it: the row-position cursor
that parked inside hydrated blocks is gone.

Measured tradeoff (33,261 threads / 38,532 messages, seven chunks):
the full walk was 5.7s warm until the references fallback dropped the
per-message references/in-reply-to reads - get_header on those two
headers file-parses every message (they have no value slots; only
from/subject/message-id read from slots under
NOTMUCH_FEATURE_FROM_SUBJECT_ID_VALUES), ~4s of the walk. The walk
now ships empty chains and runs 1.77s; the client ingest is 2.07s and
the app-level fullReload 2.72s (measured 2026-08-21). First paint
stays fast - the 100-thread first-chunk cadence lands in ~20ms - so
the cost lands on full cover, not on the first rows.

The references fallback is a deliberate stopgap; docs/refs-from-terms.md
specs the real fix. The index tree renders structure-less threads as a
flat forest: buildTree marks the synthetic root Forest when every
message is a root (no chains shipped), so no [...] markers appear on
structure-less threads while genuine multi-root threads keep them.
Reply prefill builds one-hop References chains (the reply still
threads to the original via In-Reply-To; the per-thread fetch keeps
the full chain, and the pager never needed it - RenderThread renders
sequential blocks). The data is already in the DB term list at index
time (the replyto term and the reference prefix terms); the version
script hides the private accessors, so the real fix adds two public
getters to libnotmuch (notmuch_message_get_in_reply_to /
notmuch_message_get_references, exported free via the notmuch_* glob)
and the binding re-adds the two reads - no client changes needed
(refsSplit trims brackets the term getters never emit). Deferred:
the client must not depend on a custom-built libnotmuch.

The client pays the walk with an open path that does not queue behind
it (record: rows-first resolution from the registered views with the
worker fetch as fallback, the walk data already carries headers and
paths - the open, the render toggle, and the attachment handlers
never touch the worker for a view-resident thread; only the read-mark
write queues, and the refresh cycle reconciles it). The FullWalk
addition (405 lines) lives in the fishman go.notmuch fork, tagged
v0.40.1 by the author of record; the client pins and vendors it like
any dependency (R7).

## 30. References from the DB term list: public getters, queued (2026-08-21)

Decision: the references/in-reply-to reads move from the message file
to the DB term list via two new public libnotmuch getters -
notmuch_message_get_in_reply_to (a one-line wrapper over the private
accessor) and notmuch_message_get_references (a lazily cached
space-joined chain) - which wrap the private accessors internally and
export automatically under the version script's `notmuch_*` glob (no
notmuch.sym edit). The full spec - signatures, join shape, the
invalidate_metadata extension, the binding change, verification - is
docs/refs-from-terms.md. QUEUED, not done: the client must not depend
on a custom-built libnotmuch, so the record 29 fallback stays live
until the user rebuilds their notmuch with the getters.

Why: get_header("references") and get_header("in-reply-to")
file-parse every message - those headers have no value slots (only
from/subject/message-id, and only under
NOTMUCH_FEATURE_FROM_SUBJECT_ID_VALUES) - ~4s of the 5.74s full walk.
The data is already in the DB term list at index time (the replyto
term = the strict-parsed parent id; the reference prefix terms = the
chain minus the message's own id), decompressed once per message by
the walk's own message-id read; the private accessors
(_notmuch_message_get_in_reply_to / _notmuch_message_get_references)
return them at zero file cost. The version script (lib/notmuch.sym:
`notmuch_*` glob + `local: *`) hides _notmuch_* symbols - nm -D on the
system lib shows zero exports - so the binding cannot link the private
symbols across the DSO; the public wrappers are the minimal export
surface that serves the consumer.

Expected effect: the walk returns to ~1.75-1.9s WITH chains (the
both-skipped probe measured 1.745s as the floor of what the refs
reads cost; the getters add ~0 - the term list is already decompressed
by the message-id read, the join is O(chain length)). The binding
re-adds the two reads (fork commit, tag, revendor); no client changes
- refsSplit trims brackets the term getters never emit, and the flat
forest/one-hop-chain degradation of the record 29 fallback reverses
on its own.
