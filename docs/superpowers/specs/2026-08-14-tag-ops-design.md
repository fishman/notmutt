# M2: Staged tag operations + async progress - design spec

Builds on M1 (mailbox view, view/worker/bus, reconcile-then-replay).
Implements R14 (staged tag apply/undo) and R15 (async progress display).
Normative text is AGENTS.md; this spec pins the mechanism.

## 1. Goal and acceptance

Tag actions never write to notmuch at keypress time. They stage into a
per-session buffer; the view renders the staged state immediately; `$`
applies, `u` undoes. Background work reports progress on the bus and the
TUI renders a bar in the bottom right, above the status line.

Acceptance (scripted tests; items 5-7 are manual):

1. Stage r then a on a message in inbox: the row renders archive state
   (no inbox/unread flags), notmuch is untouched (soak asserts no tag
   change until apply).
2. u restores the last-applied state (inbox+unread), buffer empty.
3. `$` sends exactly one ActTag batch per staged message carrying the
   fully resolved op set (pending ops + exclusive-group removals); the
   view baseline becomes the resolved state without waiting for a
   refresh; the buffer clears.
4. A refresh landing mid-stage does not clobber staged ops
   (reconcile-then-replay; race-clean).
5. Manual: progress bar appears during the initial load and any slow
   job, updates while the index stays navigable, and clears on
   completion.
6. Manual: staging is visibly distinct (`*` in the flags slot), styled
   via the R11 `[index.staged]` identifier.
7. Manual: `$` with nothing staged and `u` with nothing staged are
   no-ops.

## 2. Staged buffer (view-owned)

The buffer lives in core.View under its lock - the view is the single
mutable mailbox state (R1 front-end), and the buffer must move with
merge reconciles.

- `view.Stage(msgID string, op TagOp)` - appends the op; staging an op
  identical to one already staged for that message CANCELS it (toggle
  semantics: r twice is a no-op, r then a keeps both). Unknown msgID:
  no-op (the message left the view).
- `view.StagedOps() map[string][]TagOp` - snapshot for the apply path.
- `view.Undo(msgID)` - discards all staged ops for the message.
- `view.HasStaged() bool`, `view.IsStaged(msgID) bool`.
- Entries are keyed by message identity (R14), never position.

TagOp is `{Tag string; Add bool}` - the same shape the worker's ActTag
takes; intent is recorded verbatim, resolution is a separate step
(section 4).

## 3. Rendering staged state

Rows must show the staged state without mutating the applied baseline
(the message's own Tags field is the applied state - SetTags/reconcile
own it, per the M1 lock discipline).

- `core.Row` gains `Staged bool` and `StagedTags []string`.
- rowsLocked computes, per row, the display tags = applied tags with
  pending ops applied and the exclusive group resolved (section 4),
  into StagedTags when the message has staged ops; the message's Tags
  field is never touched.
- renderRow uses StagedTags for the flags slot and tag glyphs when
  Staged, and renders the staged glyph (default `*`, config data per
  R11 tag-transforms - hardcoded default until the theming milestone).
  Staged rows carry the `[index.staged]` style identifier (hardcoded
  default style until the theming milestone; R14).
- Ghost rows never carry staged state (they have no Msg).

## 4. Exclusive-group resolution

One resolver, two call sites (R14: stage-time rendering and apply-time
op set) - a core function:

`resolveOps(tags []string, ops []TagOp, groups [][]string) (newTags []string, resolved []TagOp)`

- Applies ops, then for each exclusive group touched, keeps the op
  applied LAST in group-priority order (R2 list order, archive >
  deleted > sent > draft > pending > spam) and emits the removals for
  the rest.
- The hard-tag group is a core constant for this milestone
  (`core.HardTags`, the R2 priority order verbatim); the `[tag-groups]`
  config section lands with the filter-engine milestone and replaces
  the constant. The resolver takes groups as data - nothing else knows
  the group list.
- Rendering uses the newTags arm; apply uses the resolved arm. Staging
  archive therefore renders without inbox/unread, and `$` emits
  `-inbox -unread +archive` in one batch.

## 5. Apply (`$`)

1. `view.StagedOps()` snapshot; nothing staged -> no-op.
2. For each message: resolve ops against the CURRENT applied tags.
3. One ActTag per message (id:"..." query + resolved ops) on the
   worker's lock-budgeted action path (R2 lock handling).
4. On success: write the resolved tags as the new applied baseline via
   the existing locked setter, clear the buffer entry - no flash-back
   to the pre-staged render while the refresh lags. The next refresh
   merge reconciles/confirms.
5. On failure: the entry stays staged (retry or undo); the failure
   surfaces as an error event on the bus (R15 progress/error surface).

Apply does not wait for the refresh; WorkerDone -> cycle is unchanged.

## 6. Undo (`u`)

`view.Undo(cursorMsgID)` + repaint. Pure local operation, no DB
traffic, before or after apply (after a partial apply, undo clears
whatever is still staged; applied state stays applied).

## 7. Keybindings

Hardcoded in the TUI model for this milestone (the M1 j/k/q/t pattern);
the R9 data-driven binding map is a later milestone and must keep these
keys as defaults:

- `r` toggle-read stage (flip from the applied state)
- `a` archive stage (+archive)
- `d` delete stage (+deleted)
- `u` undo staged (cursor message)
- `$` apply all

Ghost-row cursor guards apply to all staging keys (same check as
toggleRead's `row.Msg == nil`).

## 8. Progress display (R15)

- `core.Progress{Job string; Done, Total int}` on the bus, published at
  batch boundaries by the jobs that know their totals:
  - refresher.fetchThreads: total = thread ids, done = fetched;
  - cache scanVisible: total = rows in the page, done = scanned;
  - the send/crypto jobs when R4 lands.
  The worker action loop is not a progress source (R15).
- The TUI renders a bottom row: left status area (view name + visible
  count), right-aligned progress bar (fixed-width region, ~20 cells:
  `refresh 87/200 ##########----`) using the `progress` style
  identifier and a config-data filled-cell glyph (default block) -
  hardcoded defaults until the theming milestone. Completion
  (Done == Total) clears the bar.
- The widget repaints on the same event channel as the index; it never
  takes focus, never blocks. Labels are job-kind derived (R15/F6).
- The initial-load case is the primary visible one: the M1 async start
  shows "refresh n/m" as the first page streams in.

## 9. Testing

- Unit (core): stage/cancel/undo; resolveOps (single group, last-wins,
  removals emitted, untouched groups unaffected); display-tags
  computation never mutates Msg.Tags.
- Unit (app): apply snapshot -> exactly one ActTag per staged message
  with resolved ops (fakeWorker captures, the T14 pattern); apply
  writes the baseline and clears the buffer; failure keeps the entry.
- Concurrency: stage/undo/apply vs merge under -race (the
  TestToggleReadConcurrent pattern).
- Soak addition: stage without apply leaves notmuch unchanged;
  `$` then a refresh lands the exact resolved tags.
- The M1 toggleRead immediate-ActTag path is replaced by staging;
  its tests are rewritten to the staged contract.

## 10. Knobs (not this milestone)

- `[tag-groups]` config section and account presets (filter-engine
  milestone; the resolver already takes groups as data).
- Persisted pending buffer across restarts (R14 future work).
- Multi-selection staging (no selection model exists yet).
- R9 binding map, R11 theme data: keys/styles stay hardcoded defaults
  with the R11/R9 identifiers.

## 11. Out of scope

Filter engine, mover, compose/send (R2/R4), status-line spec beyond the
anchor row, error dialogs. All consume the same bus when they land.
