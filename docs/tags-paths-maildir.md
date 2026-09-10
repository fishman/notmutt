---
layout: default
title: Tags, paths, and maildir sync
nav_order: 9
---

# Tags, paths, and maildir

notmutt sits on two mail models that do not fit each other. The whole
set of integration caveats below comes from that one mismatch.

**notmuch** keeps two independent facts per message: its **tags** (the
logical model, our folder tags included) and its **filenames** (the
physical paths it is indexed under). Tags are a set; paths are a list.

**maildir** has neither. A maildir folder is a directory, and the
message's remaining state - seen, replied, forwarded, flagged, draft,
passed - is encoded in its *filename* (`cur/1:2,S`) and its location.
Flags, not tags, which is exactly the semantic gap notmuch closes.

So the folder model is a *derived* convention: a folder tag such as
`tag:archive` names a maildir directory, and the mover keeps the two in
step. Every caveat in this document is a place where "keep them in
step" cannot be a single atomic step.

## A folder move is not delete-then-index

The mover (src/filter/mover.go) relocates a file between maildir folders,
e.g. inbox to archive. It never deletes a file first and then tells
notmuch. It **copies** and keeps both files on disk through the index
update:

1. **copy** `src` -> `dst`. Both files now exist on disk. The copy
   lands in a dot-prefixed temp and renames over the final name, so a
   concurrent `notmuch new` never indexes a half-published file.
2. **index update** - `AddPaths(dst)` then `RemovePaths(src)`:
   - `AddMessage(dst)` opens the new file to index it. It is there -
     the copy just created it. There is no "file not found".
   - `RemoveMessage(src)` is a pure database op: it drops the filename
     from the message's record and never opens the file.
3. **delete `src`** - the physical source goes only after notmuch no
   longer references it.

A file is therefore never indexed before it exists, and never deleted
while the index still points at it.

## Why AddPaths runs before RemovePaths

`RemovePaths(src)` on a message whose only file is `src` deletes the
whole message record and its tags. `AddPaths(dst)` first guarantees the
message always holds at least one file, so the message record - and
every tag on it - survives the path swap.

## Why the physical delete is the last step

The delete used to run before the index update. If `AddPaths` or
`RemovePaths` then failed (a lock-budget timeout, an add error), the
source file was already gone while notmuch still listed it: the message
stayed searchable at a path with no file behind it. That is data loss
in the shape "file is in notmuch but the physical file is gone".

Reordering to index-first makes the failure degrade safely:

- an add or remove error returns with every source file intact;
- the worst case is an orphan duplicate on disk, never a missing file;
- only a committed index release (or the cli backend, below) deletes.

## The tag and the move are two steps

The folder tag (`tag:archive`) is applied separately, after the move
returns (`ActTag` in the apply flow). A tag does not tell notmuch where
a file lives - the path update in step 2 is what does that. Nothing
makes the pair atomic, so the applied tag is resolved *after* the move
lands, never before, and the next poll's location-wins rule agrees with
the result instead of reverting it.

## Flag ops rename files too

notmuch maildir sync (cgo `TagMessages` -> `TagsToMaildirFlags`) writes a
flag tag into the file name: marking a message read renames `cur/1` to
`cur/1:2,S`, unreading back. Not a folder move, but the same path change.
The C call updates the DB in one step (drop the old name, add the new), so
the index never lists a gone file - the mover's guarantee, extended to
in-place renames.

This half is *conditional*: it holds when `maildir.synchronize_flags` is
on. A store that keeps flags elsewhere (a backend that is not maildir)
renames nothing on a tag write, so the client can skip the path
bookkeeping. The backend answers this as an optional capability
(`notmuch.flagSyncer`, probed once at open), the client carries it as
`applyEnv.flagSyncOff`, and any backend that does not answer reports
"flags rename files" - the safe direction, since the skip saves one
lookup while a wrong skip strands a row on a deleted file.

## The client must re-point its rows

A view row keeps the path it was built with; `SetTags`/`reconcileMsg` never
rewrite it, so a rename or move leaves the row naming a deleted file until
the refresh re-fetches the thread. A reopen can outrun that, and the open
path reads the view row first (rows-first, no worker round trip for a
view-resident thread), so `ParseMessage` can open a renamed-away file until
a restart.

One seam covers every rename-capable op out-of-band of a refresh:
**`tagWrite`** (src/app/apply.go) - land the op, then for every view row
holding the identity net-apply it with that view's own tag groups, write
the row, notify, and `SetPaths` the fresh path (`ActSnapshots` ->
`refreshPaths`/`currentPaths`). Every writer goes through it, with no
per-action special case:

- the **open read-mark** (src/app/app.go);
- the **reply/forward mark on a send** (src/app/send.go) - the original is
  what gets renamed, so its rows must follow;
- the **staged apply flush** (`applyStaged` -> `execApply`) - which first
  resolves any folder move, then calls the same seam;
- the **MCP ops path** (src/app/mcp.go) - no live views, so a pure DB write.

Thread identities skip the path repoint: a hydrated thread self-heals on
the next fetch. Pinned by `TestApplyRefreshSeamRepointsPaths`
(src/app/apply_test.go) and `TestSendReplyReconcilesOriginalRow`
(src/app/send_test.go); the gate's two halves by `TestTagWritePathRepointGate`
(same file).

The repoint runs when a flag tag renamed the file *or* a folder move ran:
a move relocates whatever the flag-sync setting, so the mover's call
passes that signal explicitly (`moved`) rather than gating on the store's
flag behavior. `SetPaths` is idempotent - repointing to the paths a row
already holds writes nothing - so a store we cannot classify repoints
harmlessly.

## The cli backend

The cli build has no path operations (`ErrUnsupported`): it copies,
deletes the sources, and its `notmuch new` reconciles the move on the
next poll. There is no in-process index update to fail, so the delete
is unconditional there.

## The guarantee

A crash or error at any point leaves either both files present and
consistent with the index, or a committed index that no longer names
the deleted source. It never leaves the index naming a file that is
gone. Pinned by `TestMoverKeepsSourceOnDbError`
(src/filter/filter_test.go): a failed add step must return with the
source file intact. The same holds for flag renames, and the seams
above re-point client rows so no open reads a renamed-away path.
