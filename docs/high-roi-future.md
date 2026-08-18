# Future work: high-ROI backlog

Ranked by ROI (impact / effort), collected from AGENTS.md requirements not yet
built, spec decision records, and review residuals. Effort is S/M/L relative
to the send-dialogue milestone. AGENTS.md is normative; a spec and plan are
prerequisites before implementation per the project's workflow.

## Tier 1: foundational, build next

### 1. Filter engine with exclusive tag groups (R2) - effort L, impact highest

The AGENTS.md "KNOWN PAIN - must be fixed": hard-tag exclusivity is currently
enforced by hand in muttrc (every new folder tag requires editing every older
rule). The client's filter engine must replace the muttrc post-new hook and
afew entirely:

- Declarative exclusive tag groups (`[tag-groups.folder] tags = [...]`):
  applying any member removes the other members present. Adding a tag to the
  group must not touch existing rules.
- Folder rules DERIVED from account + preset data (`[accounts.<name>] folder`,
  `preset = "gmail"`); header rules stay data (query + add, engine-enforced
  NOT guards); conditional rules stay explicit (delivery-gated untag-reversal,
  trash return-to-inbox).
- The MailMover becomes NATIVE: per-account tag -> folder maps, first-existing
  wins, `*` globs, copy-then-delete, DRY-RUN as a first-class job mode.
- Filter contract is per-message (afew's shape); algorithmic filters
  (SpamFilter, DKIM) plug in later behind the same interface.
- Side effects (address cache, notification) subscribe to the filter job's
  completion event.

Impact: the classification pipeline becomes the client's own, the muttrc pain
dies, and the client reaches feature parity with the reference setup.
Pointers: AGENTS.md R2; `references/muttrc/notmuch/tags`, `references/muttrc/notmuch/post-new`,
`references/muttrc/afew/config` (reference shapes); `references/afew/MailMover.py` (reference
logic). M1's account-sender work (2026-08-14-account-sender-design.md) is the
account model this derives from.

### 2. Crypto providers: PGP and S/MIME via system tools (R10) - effort M-L

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

### 3. MIME cache (R13) - effort M

The only per-message data notmuch cannot serve: attachment presence, list,
structure, sizes (needs a file open and parse). Keyed by (path, size, mtime) -
renames and edits invalidate naturally; steady state is hit-only. The index
row's attachment slot (R11) fills in; the pager gains an attachment list.

Impact: attachment glyphs in the index and an attachment UI without touching
every message per refresh. Pointers: AGENTS.md R13; the index cache in
`src/cache/` is the sibling pattern (bbolt, 0600, revision-keyed). The cache
is READ-ONLY for AI agents without explicit approval (see the standing rule).

## Tier 2: strong ROI, schedule when a slot opens

### 4. Fuzz the mail-parsing boundary (SECURITY.md) - effort M

The mail parser is the trust boundary; SECURITY.md's fuzz targets are a CI
standard not yet built. Go fuzzing (native `go test -fuzz`) over the
parse-back and assemble paths, plus the sanitizer discipline (F1-F4, F10).

Impact: the stated firmware attitude applied to the one component that parses
attacker input. Pointers: SECURITY.md, `src/mail/`, `src/compose/buffer.go`
(parse-back).

### 5. Fix the redraw_test harness race, enable -race in CI - effort S

`TestCursorMovePartialRepaint` (src/tui/redraw_test.go) races the vendored
renderer goroutine against the test goroutine under `-race`; verified
pre-existing at the milestone base. The fix is a harness-side synchronization
(join or poll the renderer), then `-race` becomes the CI standard without a
skip list.

Impact: removes the one known red flag; CI runs race-clean like the rest of
the suite. Pointers: src/tui/redraw_test.go.

### 6. Staged-tag persistence (R14) - effort M

Staged tag ops and the undo buffer are session-local today; persistence is
explicit future work in R14. A session record (applied ops per message id)
would make undo survive restarts and make the apply path resumable.

Impact: undo becomes durable; mis-taps stop being permanent after a restart.
Pointers: AGENTS.md R14, `src/tui/model.go` stage/apply/undo.

### 7. Theme: onedark port + base16 palette converter (R11) - effort S-M

The onedark theme in `references/muttrc/theme/onedark.muttrc` is the reference port; the
base16 collection in `references/muttrc/themes/palette/` is the import source. A
converter script (base16 -> TOML palette) is a stated future task.

Impact: theme parity with the reference mutt setup, delivered as data.
Pointers: AGENTS.md R11, `src/config` theme store, `references/muttrc/theme/onedark.muttrc`.

### 8. Send retry reopens the compose dialogue with the failed message (R4) - effort S-M

neomutt's `bg_send_retry` seed: a failed send retains the Email and re-opens
the compose dialog with the failed message. The PhaseFailed path exists (e
retries in place); the missing piece is reopening with the failed state and
its captured output shown.

Impact: send failures stop being lossy - the user edits and re-sends the
actual failed message. Pointers: `references/neomutt/send` (async_send branch,
`bg_send_retry`), `src/app/send.go`, `src/compose/state.go` PhaseFailed.

### 9. Address cache + notifications as filter side effects (R2) - effort S-M

The reference pipeline's side effects: address cache for query completion and
mail notification. They subscribe to the filter job's completion event (they
are not hook steps).

Impact: query completion and notifications arrive with the filter pipeline.
Pointers: AGENTS.md R2, `references/muttrc/afew/config` (the reference side effects).

## Tier 3: later, smaller, or gated

- **Lua bindings (R8)** - effort L. The neovim model (event loop, RPC,
  msgpack, Lua as extension language); TOML schema was designed for this.
  Prereq: the core interfaces stabilize.
- **DBus dark/light sync (R12)** - effort S-M, build-tag-gated (`dbus`),
  godbus dependency only in that build.
- **Preview popup vs open-reads distinction** - effort S. Existing pending
  task in the backlog (the pager opens reads; a preview popup is separate).
- **Whole-fill dirty batch on refresh** - effort S. Wrap the phase-2 emit
  loop in one Begin/EndMerge pair (src/app/refresh.go). Measured 147us per
  press in the fill window vs 2.61ms (17.7x) at the cost of the progressive
  reveal - the list appears in one jump. Decision and numbers:
  docs/design-decisions.md 15.
- **Send-epoch stamping on SendResult** - effort S. Closes the one accepted
  residual from the M2 snapshot fix: a channel-delivered failure racing a
  retry dispatch can re-apply the stale failure for one Update.
- **UX nits accepted in the send milestone** - effort S each. Abort hint
  overpromises ("any other key to cancel" - only j/edit/y cancel; parking
  does not reset the armed abort); formIdx not clamped per tab; compose frame
  overflows below height 11; `opened` set / bus `openLast` grow per session.
- **Extractable TUI library (R5)** - effort L, architectural. When the TUI
  stabilizes; the core has no UI code by design, so extraction is packaging.
- **cgo binding re-evaluation** - effort M, gated. The CLI stays the runtime
  backend while go.notmuch's per-message iterator costs ~8.7 s vs the CLI's
  1.5 s full walk (2026-08-14 bench). Re-check when the binding closes the
  gap; the vendored fork already carries DB.Revision().
- **MIME cache compression knob** - effort S, only after measuring (R13:
  "compress first" is explicitly a future knob, not a requirement).

## Process notes

- Every Tier 1/2 item needs a spec (docs/superpowers/specs/) then a plan
  (docs/superpowers/plans/) before implementation, per the project workflow.
- `src/cache/*`, `src/app/cachejob.go`, and (unless an exception is granted)
  `src/core/view.go` stay READ-ONLY for AI agents; Tier 1 item 3 and Tier 3
  items touching the cache need an explicit approval.
- The benchmark harness from the lag investigation (real tea.Program, end-to-
  end per-press cost) should be kept as a reusable perf gate when the TUI
  gains features that touch the render path.
