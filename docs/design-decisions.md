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

## 3. CLI over cgo (R1, 2026-08-14)

Decision: the notmuch CLI is the runtime backend; go.notmuch (cgo) is
compiled and measured but not the default. Measured on the 33k-thread
inbox: CLI full walk 1.5s vs the binding's 8.7s (~230us per-message row -
the per-message iterator overhead). The batch-iteration work in
go.notmuch stays dropped until the binding closes the gap. The CLI's
write-at-end JSON wall (measured 1.4s) is mitigated by the two-step
fill, not by chunk streaming - json0 is unsupported, so the parse
buffers; chunk slicing alone cannot make the first paint early.

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

## 16. Privacy and trust boundaries

Decision: mail content (bodies, headers, whole .eml/.mbox files) is
never submitted to an LLM; a field inside mail is extracted by script
first and only the extracted value is passed; message identity
correlates by checksum. Rendered mail passes the sanitizer (ESC/C0/OSC
stripped) before reaching the terminal (F1); exec is argv-only - mail
content never interpolates into shell strings (F4); everything written
is 0600/0700 (F5/F7); bodies, headers, and passphrases are never logged
(F6).

## 17. Supply chain

Decision: minimal, deliberate dependency set - every dependency must
earn its place; exact pins; the build is vendored and reproducible; the
go.notmuch fork is vendored and pinned by replace, never fetched from
the proxy. Spec/doc commits carry the AI-assisted trailer; code is
reviewed like any other contribution, with tests proving the edge
cases.
