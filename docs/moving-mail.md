---
layout: default
title: Moving mail between folders (mover mechanics)
nav_order: 9
---

# Moving mail: copy, re-index, then delete

A folder move (src/filter/mover.go) relocates a file between maildir
folders, e.g. inbox to archive. notmuch keeps two independent facts
about a message: its **tags** (the logical model) and its **filenames**
(the physical paths it is indexed under). Moving mail is keeping the
second honest without disturbing the first.

## The move is not delete-then-index

The mover never deletes a file first and then tells notmuch. It
**copies** and keeps both files on disk through the index update:

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

## Tags are not paths

The folder tag (`tag:archive`) is applied separately, after the move
returns (`ActTag` in the apply flow). A tag does not tell notmuch where
a file lives - the path update in step 2 is what does that. The two are
independent and never combined into one step.

## Flag ops rename files too

notmuch maildir sync (cgo `TagMessages` -> `TagsToMaildirFlags`) writes a
flag tag into the file name: marking a message read renames `cur/1` to
`cur/1:2,S`, unreading back. Not a folder move, but the same path change.
The C call updates the DB in one step (drop the old name, add the new), so
the index never lists a gone file - the mover's guarantee, extended to
in-place renames.

## The client must re-point its rows

A view row keeps the path it was built with; `SetTags`/`reconcileMsg` never
rewrite it, so a rename or move leaves the row naming a deleted file until
the refresh re-fetches the thread. A reopen can outrun that, and the open
path reads the view row first (rows-first, no worker round trip for a
view-resident thread), so `ParseMessage` can open a renamed-away file until
a restart.

Each rename-capable tag op out-of-band of a refresh re-points its rows on
success:

- the **apply flush** (src/app/apply.go) - snapshot the applied id
  (`ActSnapshots`) and `SetPaths` the fresh path into every view holding it;
- the **open read-mark** (src/app/app.go) - a full open that marks an unread
  message read renames its file, so the same refresh runs after the mark.

Both share `refreshPaths`/`currentPaths` (src/app/apply.go). Thread
identities skip the seam: a hydrated thread self-heals on the next fetch.
Pinned by `TestApplyRefreshSeamRepointsPaths` (src/app/apply_test.go).

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
