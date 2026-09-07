# Reconciling poll classification - design

## Problem

The filter poll classifies only the (pre, cur] lastmod bracket that its
own `notmuch new` discovers (`pollDiff`, app.go:1402). When the run
finds nothing new it returns before any classification. That gate is
wrong: the notmuch database revision advances on every write - tag ops,
path adds/removes, another process's own `notmuch new` - but `New()`
reports only the bracket of files it newly indexed, so mutations that
never pass through `notmuch new` discovery leave no mark on the poll's
window. The next poll sees cur == pre, classifies nothing, and the
mutation is never reconciled.

Sites that hit the hole, in one session:

- a client-written draft / sent fcc (already indexed by its own
  out-of-band `ActNew`, so a later poll's new finds nothing - fixed at
  the write site by `indexWrite`, send.go:134);
- the edit-pass retire (`ActRemovePaths`, saveDraft's replace);
- a folder move / apply (apply's `ActTag` + mover `ActAddPaths` /
  `ActRemovePaths`);
- a sibling `notmutt poll` or second instance whose `notmuch new`
  indexes a file first - no site code can bracket another process.

The invariant R2 wants is "the poll owns classification": whatever
mutated the database since the last classify gets the folder rules run
on it. The discovery-bracket gate cannot deliver that. The fix is to
gate on the database revision, not on `new`'s bracket.

## Design summary

The poll classifies the lastmod window (L, cur] where L is the last
revision the filter has already applied tags through - a persisted
high-water mark - and cur is the database revision after this poll's
own `notmuch new` indexes whatever new mail arrived. `new` still runs
first (its indexing bumps the new files' lastmod into the window), but
the window is no longer `new`'s bracket: it spans new files AND existing
messages whose revision moved for any other reason (a tag, a path
change, a sibling process). After an applied run the poll persists cur
as the new L. A quiet mailbox (cur == L) classifies nothing, exactly as
today.

L means "folder tags are applied up to this revision". Every applied
classify run that reaches cur leaves every message with lastmod <= cur
location-consistent; any later mutation advances the revision past L
and the next applied poll reconciles it. The delete case falls out for
free: a retired message is gone from the window's query, and the
replacement written in the same pass is inside it.

The one new store is a small 0600 state file holding the L revision,
sharing the poll stamp's cache directory (app.go:1474) so the CLI poll
and every running client read and advance the same floor. Cross-process
writers make overlap possible; classification is idempotent (folder
rules converge), so a lost race costs a redundant re-run, never a miss.

## The last-classified revision (L)

- Location: `os.UserCacheDir()/notmutt/last-classify` (the poll stamp's
  home; already shared across processes and created 0700/0600).
- Content: one line, the revision as decimal. Written atomically (temp
  file + rename); read tolerantly - a missing file is "no L yet", a
  corrupt file warns and is treated as no L (the safe choice: re-classify
  the discovery bracket, never a full backfill).
- Advance rule: persist L only after a classify run that APPLIED
  (`rep.DryRun == false`). A dry-run poll reports (L, cur] and leaves L
  where it is - the reviewer sees the full pending set until they apply,
  and a mailbox that was only ever dry-run has never had its tags really
  written, so its L must not move.
- Write rule: always write the observed post-run revision, never a stale
  pre. Re-read the file just before renaming and write the higher of the
  two, so a concurrent process's advance is not regressed (benign even
  if it is: idempotent re-run).

## First run and the backfill boundary

The first poll on a mailbox (no L file) must not mass-reclassify the
~129k-message store. It sets L to the current database revision (the
pre of its own new) and then runs the normal reconcile: the window (L,
cur] is exactly the fresh discovery, because nothing else mutated since
L was set to the present. Backfill stays the explicit windowed replay
(`notmutt poll --from --to`); that path is unchanged, classifies its
fixed bracket, and never touches L.

## Component changes

- pollDiff, fresh branch (app.go:1402): replace "cur == pre -> return,
  no classification" with:
  1. ActNew (index new mail; ErrUnsupported degrades as today);
  2. load L; if absent, set L = new's pre;
  3. cur := ActRevision;
  4. if cur <= L -> no window, no classify (the quiet poll);
  5. classifyDelta(L, cur) via the existing engine and mover;
  6. final := ActRevision (the classify's own tag/path writes advance
     the revision past cur);
  7. if the run applied (not rep.DryRun), persist L = final.
- runPoll (app.go:1357) and runFilterPipeline (filterjob.go:91) get the
  reconcile behavior for free through pollDiff. The windowed
  (`spec.windowed`) branch is untouched: fixed bracket, no L.
- indexWrite (send.go:134) is unchanged in behavior but stops routing
  through the reconciling fresh branch: it classifies only the write's
  own discovery bracket and must neither read nor advance L. Reuse
  classifyDelta directly (or a fixed-window pollDiff). It stays the
  write site's immediacy; the reconcile covers the same messages again
  at the next poll, idempotently.
- New: a classify-state store (read/write the L revision on a path),
  injected into the poll path. The tests inject a temp path so no unit
  test touches the real cache file; the CLI poll and the refresher's
  filter job use the real cache path.

## Semantics

- Location-wins is unchanged: the wider window re-runs the same folder
  and header rules. A message whose only change since L was a user tag
  op is re-checked against its file's folder and left consistent; a
  message whose file moved follows its new folder. The engine's rules
  and the mover are untouched - only the window they run over widens.
- The mover inside classify (classifyDelta's NewMover) advances the
  revision past cur; step 6-7 absorb that so the moved message does not
  re-enter the next poll's window.
- The quiet poll is preserved: new finds nothing AND no revision moved
  since L -> cur == L -> no classify, no FilterDone, no notify. The
  notify gate is unchanged (notifyEntries: unread inbox entries only),
  so a poll that merely re-ran rules over a tag-only bump stays quiet.
- FilterDone now fires whenever any revision movement since L triggered
  a classify, including a zero-entry reconcile. That is a classify that
  genuinely ran; the event count reports zero entries honestly.
- A soft-tag-only bump (unread toggle) re-runs the folder rules over
  that message. Idempotent and cheap; matches the model.

## Testing strategy (failing first)

- Regression, the core claim: a worker whose ActNew reports nothing new
  (bump = 0) but whose revision already advanced past L because of an
  out-of-band tag/move must still classify (L, cur]. Fixture: L = 5,
  rev = 10, bump = 0, delta/snapshots = one moved message -> the poll
  returns changed and the message gains its folder tag. Current code:
  ActNew (10, 10), cur == pre, returns before classifying -> RED.
- L persists only on applied runs: a dry-run reconcile leaves the store
  unchanged.
- First run does not backfill: no L file, a far-behind mailbox (rev at
  100k) -> L baselines to the present revision; only the discovery
  window classifies.
- Windowed replay never touches L.
- Existing tests that route through pollDiff (the filter-job suite, the
  send/saveDraft suite via indexWrite) keep passing: quiet-on-bump=0
  holds once the fixture seeds L at the worker's revision, and indexWrite
  no longer depends on the fresh-branch reconcile.

## Risks and non-goals

- The poll now classifies on any revision movement, not just new mail.
  Worst case is a redundant idempotent re-run of a small window; the
  backfill-size case only appears if the mailbox sat unpolled while L
  aged - and that is exactly the reconcile the poll exists to perform.
- No changes to the filter engine, the mover, folder-rule derivation, or
  the exclusive-group model. This is the poll's gate and one state file.
- indexWrite stays a narrow instant-classify; it does not become a
  reconciler, so a draft save never stalls on a stale-L window.
