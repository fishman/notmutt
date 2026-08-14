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

Groups come from the config store: `[tag-groups.<name>]` with one field,
`tags` - the member list. Membership IS the hard-tag declaration: a tag
in a group is hard (exclusive, a physical folder mapped to a notmuch
tag); any tag not in a group is soft (unread, work, conference, ...) -
unlimited, coexists with everything, applied by header rules (R2,
filter-engine milestone), never touched by group resolution, never
triggers a move. unread is the canonical soft tag: a folder move must
not clear read state, and the unified-inbox view queries unread across
all folders - so unread never joins the folder group.

The default ships the reference folder set - inbox included, so the
muttrc `-inbox` folder rule and the conflict chain are both obsolete:

    [tag-groups.folder]
    tags = ["inbox", "archive", "deleted", "sent", "draft", "pending", "spam"]

One resolver, two call sites (R14: stage-time rendering and apply-time
op set) - a core function:

`resolveOps(tags []string, ops []TagOp, groups []TagGroup) (newTags []string, resolved []TagOp)`

- Applies the ops, then per group: if the ops touch the group (any op
  names a member), normalize to ONE member - the last member-ADD op
  wins, so moving is symmetric (`+archive` on a spam message untags
  spam, `+inbox` on an archived message moves it back); if no member
  was added, the sole remaining member stays; legacy mail already
  carrying two folder tags resolves to the first present in list
  order (deterministic). Pure mutual exclusion - no priority, no
  sticky, no implied removals.
- Ops that do not touch a group leave it untouched: pending mail at
  [pending, inbox, unread] keeps the inbox view until it is moved, and
  soft tags are never removed by folder moves.
- Rendering uses the newTags arm; apply uses the resolved arm - the
  minimal op set turning the applied tags into the resolved ones
  (symmetric difference), so staging r then a on [inbox, unread]
  emits `-unread -inbox +archive` in one batch, and a net no-op
  produces an empty batch and no ActTag.
- The resolver takes groups as data; the config section is the only
  producer - nothing else knows the member list.

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

Bindings are CONFIG DATA, never hardcoded in the TUI (R9; the staged
keys in the plan were examples, not the mechanism): the index context
is a `[bindings.index]` table mapping keys to action names, defaults in
the config store, injected into the model at construction. Rebinding a
key never touches code.

Actions come in two kinds. BUILT-IN actions (cursor-down, cursor-up,
quit, undo, apply) are the fixed TUI vocabulary. TAG actions stage a
tag on the cursor message; their name-to-tag mapping is config data
(`[tag-actions]`), and the handler kind DERIVES from the tag's group
membership - there are no per-tag cases anywhere in the TUI: a tag in
any tag group is a FOLDER tag and stages `+tag` (exclusive-group
resolution dedups at render and apply, section 4); a tag in no group is
a SOFT tag (unread is canonical, section 4) and TOGGLES from the
applied state (the read/unread flip). Two handler types are the entire
tag logic in the TUI; adding a tag action is a config-only change, and
the hard/soft distinction exists once (the group member lists), never
re-declared in code.

The app wiring validates at startup: every binding value must be a
builtin action or a declared tag action (unknown action = load error,
strict load), and a tag action may not collide with a builtin name - a
typo'd or dead binding fails loudly instead of silently doing nothing.

Defaults (the R9 binding map must keep these):

    [bindings.index]
    j = "cursor-down"
    k = "cursor-up"
    q = "quit"
    r = "toggle-read"   # tag action: unread (soft -> toggles)
    a = "archive"       # tag action: archive (folder -> +archive)
    d = "delete"        # tag action: deleted (folder -> +deleted)
    u = "undo"          # undo staged (cursor message)
    "$" = "apply"       # apply all (quoted TOML key)

    [tag-actions]
    "toggle-read" = "unread"   # soft tag: flips read state
    archive = "archive"        # folder tag
    delete = "deleted"         # folder tag

Ghost-row cursor guards apply to all staging actions (the same
`row.Msg == nil` check).

Macros (a key bound to a sequence of actions) and the `[ui] keymap`
scheme (vim/emacs) stay with the R9 milestone; the binding and
tag-action tables are their substrate, so nothing here forecloses them.

## 8. Progress display (R15)

- `core.Progress{Job, View string; Done, Total int}` on the bus,
  published at batch boundaries by the jobs that know their totals:
  - refresher.fullReload: one publish per page; total = the fill's
    count query result (threads), done = threads accumulated across
    pages;
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
- Progress is scoped per VIEW: the bus keeps the latest Progress as a
  snapshot keyed (job, view) - a write that never drops under subscriber
  backpressure - and the bar renders the CURRENT view's snapshot (ticked
  on a 200ms cadence while a job is on). Accounts and virtual folders
  (inbox, unread, sent, drafts) each track their own fill; switching
  views swaps the bar.
- The initial-load case is the primary visible one: the M1 async start
  shows "refresh n/m" as each page of the query result streams in. Full
  reloads PAGE the whole result (R3 progressive fill): a `notmuch count`
  up front fixes the bar's total (the user requirement: the bar reflects
  the total even as it updates periodically), then the refresher fetches
  the query page by page (`--limit`/`--offset`), merges each page into
  the view with a ViewDiff before fetching the next, and ends when a
  page returns fewer than the requested budget (or an error). The first
  page is 200 (fast first paint), then the steady page of 1000. Count
  failure degrades to per-page totals with the base reset, so the bar
  never exceeds its total. The changed-set cycle (lastmod diff) is
  unchanged and reconciles mail that lands mid-fill (one-cycle lag).
- Ingestion is TWO-STEP (the user directive, 2026-08-14: "step one
  read the message and then step two read the headers or content. no
  need to batch all at once"): the fill reads the INDEX - one
  `notmuch search` page per fetch (thread summaries: thread id, date,
  authors, subject, tags - DB-side data, zero file opens), so the
  whole list loads in seconds. Per-thread round trips were the load
  wall: 129k threads meant 129k backend calls; the fill no longer
  fetches threads at all.
- Step two is the per-message data (message ids, references, paths):
  the visible window hydrates its stub rows right after the fill
  (per-thread `notmuch show`, budgeted, the only file opens in the
  load path - ~40 threads, one process each), and everything else
  loads on open (R13). The incremental changed-set cycle keeps the
  same per-thread fetch (small N). No Batch action exists - the
  paged search query IS the high-speed ingestion interface.
- The index rows ARE search summaries: a stub message (empty id)
  renders author/subject/tags directly and is cursorable (anchored
  by row index - no id to track). The hydrate merge replaces it in
  place: the message diff removes the stub and inserts the real
  messages, and the cursor lands on the thread root (the same
  logical message). Stub rows cannot be staged - the apply path
  needs a real message id, and the stub is transient (one guard in
  the TUI, same shape as the ghost-row guard).

## 9. Testing

- Unit (core): stage/cancel/undo; resolveOps (single group, last
  member-ADD wins, removals emitted, untouched groups unaffected,
  legacy two-member tiebreak); display-tags computation never mutates
  Msg.Tags.
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

- Account presets (tag -> folder maps) and the mover (filter-engine
  milestone; the group list is already config data).
- Soft-tag header rules (query + add, the muttrc/notmuch/post-new
  shape) - filter-engine milestone. Soft tags need no client-side
  support in M2: they are simply not in any group.
- Persisted pending buffer across restarts (R14 future work).
- Multi-selection staging (no selection model exists yet).
- R9 binding map, R11 theme data: keys/styles stay hardcoded defaults
  with the R11/R9 identifiers.

## 11. Out of scope

Filter engine, mover, compose/send (R2/R4), status-line spec beyond the
anchor row, error dialogs. All consume the same bus when they land.
