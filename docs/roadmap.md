---
layout: default
title: Roadmap
nav_order: 8
---

# Future work: backlog

Ranked by impact over effort, collected from AGENTS.md requirements not yet
built, spec decision records, and review residuals. Effort is S/M/L relative
to the send-dialogue milestone. AGENTS.md is normative; a spec and plan are
prerequisites before implementation per the project's workflow.

## Tier 1: foundational, build next

### 1. Crypto providers: PGP and S/MIME via system tools (R10) - effort M-L

No sign/encrypt/decrypt on the send or read path yet. Zero crypto code in the
client: gpg CLI with `--status-fd` parsing (aerc's gpgbin pattern), S/MIME via
`openssl smime`, gpg-agent + external pinentry with TUI suspend/resume (the
only passphrase path; no loopback mode - Go cannot zero secrets).

- Sign/encrypt is a transform stage between go-message assembly and the send
  job (assemble -> sign/encrypt per dialogue flags -> fcc -> send).
- Decrypt/verify is an async job on the read path; the pager renders body and
  signature status.
- Key selection is a selector dialogue (R4 machinery) fed by
  `gpg --list-secret-keys --with-colons`.
- go-pgpmail builds on go-message for the transform stage (verified in the
  mail-library decision record).

Impact: the client becomes usable for signed/encrypted mail - table stakes for
this user's workflow. Pointers: AGENTS.md R10, `references/neomutt/ncrypt/*`,
`references/neomutt/smime/smime.c`, `references/aerc/lib/crypto/gpg/gpgbin`.

## Tier 2: schedule when a slot opens

### 2. Fix the redraw_test harness race, enable -race in CI - effort S

`TestCursorMovePartialRepaint` (src/tui/redraw_test.go) races the vendored
renderer goroutine against the test goroutine under `-race`; verified
pre-existing at the milestone base. The fix is a harness-side synchronization
(join or poll the renderer), then `-race` becomes the CI standard without a
skip list.

Impact: removes the one known red flag; CI runs race-clean like the rest of
the suite. Pointers: src/tui/redraw_test.go (fake_screen_test.go - the
renderer goroutine's SetContent vs the test goroutine's Get, re-verified
2026-08-19 under -race: TestLoopCursorMoveRepaints and
TestFrameChromeSurvivesRefresh both race).

### 3. Staged-tag persistence (R14) - effort M

Staged tag ops and the undo buffer are session-local today; persistence is
explicit future work in R14. The design is spec'd: a durable, multi-writer
buffer (one file per op, ULID ids, crash-isolated) that MCP/CLI/Lua/TUI
all append to and the session picks up - see
[docs/staged-ops.md](staged-ops.html).

Impact: undo becomes durable; mis-taps stop being permanent after a restart;
the MCP server gains a stage-only write surface without touching notmuch.
Pointers: AGENTS.md R14, docs/staged-ops.md, `src/tui/model.go` stage/apply/undo.

### 4. Send retry reopens the compose dialogue with the failed message (R4) - effort S-M

neomutt's `bg_send_retry` seed: a failed send retains the Email and re-opens
the compose dialog with the failed message. The PhaseFailed path exists (e
retries in place); the missing piece is reopening with the failed state and
its captured output shown.

Impact: send failures stop being lossy - the user edits and re-sends the
actual failed message. Pointers: `references/neomutt/send` (async_send branch,
`bg_send_retry`), `src/app/send.go`, `src/compose/state.go` PhaseFailed.

### 5. Address cache as a filter side effect (R2) - effort S-M

The reference pipeline's remaining side effect: the address cache for query
completion (notifications landed with the filter job's completion event,
`src/app/notify_beeep.go`). It subscribes to the same event.

Impact: query completion arrives with the filter pipeline.
Pointers: AGENTS.md R2, `references/muttrc/afew/config` (the reference side effects).

## Tier 3: later, smaller, or gated

- **DBus dark/light sync (R12)** - effort S-M, build-tag-gated (`dbus`),
  godbus dependency only in that build.
- **Send-epoch stamping on SendResult** - effort S. Closes the one accepted
  residual from the M2 snapshot fix: a channel-delivered failure racing a
  retry dispatch can re-apply the stale failure for one Update.
- **UX nits accepted in the send milestone** - effort S each. formIdx not
  clamped per tab; compose frame overflows below height 11; `opened` set /
  bus `openLast` grow per session.
- **Extractable TUI library (R5)** - effort L, architectural. When the TUI
  stabilizes; the core has no UI code by design, so extraction is packaging.
- **MIME cache compression knob** - effort S, only after measuring (R13:
  "compress first" is explicitly a future knob, not a requirement).

## Landed (removed from the ranks)

Implemented since this backlog was drafted; kept here as the audit trail.

- **Filter engine with exclusive tag groups (R2)** - `src/filter/` (filter.go,
  mover.go), the [filter] job with DRY-RUN as a first-class mode, the
  [tag-groups] config, derived folder rules from account + preset data. The
  muttrc post-new hook and afew are reference shapes only. Algorithmic
  filters (bayes, DKIM) remain future plug-ins behind the same per-message
  contract.
- **MIME cache (R13)** - `src/cache/`: bbolt, keyed by (path, size, mtime)
  to attachment metadata, hit-only in steady state.
- **Fuzz targets on the mail-parsing boundary** - `src/mail/fuzz_test.go`.
- **Theme onedark port (R11)** - `defaultTheme`/`defaultPalette` in
  src/config/config.go (the muttrc onedark reference port); the base16
  palette converter stays a future task (R11).
- **Lua bindings (R8)** - the plugin layer (`src/app/lua_plugin.go`,
  lua_action.go) and the build-tag-gated AI provider layer (`src/app/ai/`)
  shipped on the neovim model; TOML stays the config language.
- **Preview popup vs open-reads distinction** - the p key opens a popup
  preview over the index (src/tui/preview.go); enter promotes it to a full
  open.
- **Whole-fill dirty batch on refresh** - the phase-2 emit loop runs inside
  one BeginMerge/EndMerge pair (src/app/refresh.go); the measured 17.7x
  trade is design-decisions record 15.
- **cgo binding re-evaluation** - resolved: the batched ThreadsWalk closed
  the gap (~1.6s vs the CLI's 1.5s full walk) and cgo IS the runtime
  backend; the CLI survives behind `-tags cli` as the escape hatch (AGENTS.md
  R1, decision record 3).
- **Notifications as a filter side effect** - `src/app/notify_beeep.go`,
  wired to the filter job's completion event (filterjob.go); the address
  cache half remains Tier 2 item 5.

## Process notes

- Every Tier 1/2 item needs a spec (docs/superpowers/specs/) then a plan
  (docs/superpowers/plans/) before implementation, per the project workflow.
- `src/cache/*`, `src/app/cachejob.go`, and (unless an exception is granted)
  `src/core/view.go` stay READ-ONLY for AI agents; items touching the cache
  need an explicit approval.
- The benchmark harness from the lag investigation (real tea.Program, end-to-
  end per-press cost) should be kept as a reusable perf gate when the TUI
  gains features that touch the render path.
