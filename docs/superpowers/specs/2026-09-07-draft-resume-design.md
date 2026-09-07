# Resume a saved draft: retire on send, update in place on re-save

## Status

Design approved 2026-09-07. A saved draft reopens for editing on a
dedicated key, sends through the existing core (the per-account `no_fcc`
rule governs the Sent copy exactly as it does any send), and its stored
file leaves the draft folder only when delivery actually succeeds.
Re-saving an open resumed draft replaces the stored draft in place - one
draft per composition, never duplicates.

## Context

Today a compose abort's d key writes the buffer into the account draft
folder and nothing ever reopens it - there is no resume path, so a saved
draft is stranded. This design adds it. The delivery core
(`deliverSend`) already honors `no_fcc` and is shared by live and
scheduled sends; the draft lifecycle rides that same core rather than
adding a parallel path.

## 1. The resumed identity: ResumePath

`compose.State` gains `ResumePath string`: the file path of the stored
draft this dialogue edits; empty for every fresh/reply/forward compose
(and AI/CRM drafts that were never saved).

- Distinct from `OriginalID` (reply/forward tagging stays untouched - no
  replied/forwarded tag fires on a resumed send) and from
  `MessageID`/`References` (a resumed draft is a standalone send: Mode
  stays compose, and `Assemble` already issues a fresh Message-ID and
  only threads when MessageID is set).
- The path survives both round trips the dialogue takes: the schedule
  spool serializes the whole State as JSON, so a scheduled resumed
  draft carries ResumePath to delivery automatically; `compose/event.go`
  ToEvent/FromEvent map it through `core.ComposeOpened` (the field
  joins the explicit list) so an edit-unschedule reopen keeps it.

## 2. Opening: the resume seam

- A new action `resume-draft` in the mail contexts (index; the pager
  inherits via contextParents), bound to a free key in the default
  scheme and shown in the mail-actions keyhint. Enter stays a read-only
  pager.
- Dispatch mirrors `openReply`: resolve the current message, gate on it
  (a row must be present), and call a new `SetResumeHandler` seam.
- The app side gates on the message's tags: only a message carrying the
  `draft` folder-group tag resumes - any other row no-ops (the
  crm-row-action guard pattern). The gate lives with the account rules,
  not the TUI.
- `resumeDraft(cfg, view, worker, msg, root)` mirrors `replyPrefill`:
  resolve the file (msg.Paths[0]; a path-less row - a pager link
  rehydration - resolves via a flat id query), derive the account and
  From through `accountFrom`, then build the State from the stored draft
  (section 3). The account default signature is NOT injected on resume -
  the stored tail (section 3) is authoritative, and a draft saved
  without one opens with none. Publish ComposeOpened; the tab opens
  exactly like a reply.

## 3. Draft parse: compose.Resume

The stored draft is the client's own `Assemble` output (a bare text/plain
body, or multipart/mixed with the body as the first part), so the parse
is exact for the common case and degrades gracefully on foreign shapes
(a sync-tool draft; a multipart/alternative or html-only draft still
yields its plain text or an empty body rather than failing).

- Recipients: To, Cc, Reply-To, Subject from the parsed headers; Bcc
  read from the raw header block (ParseMessage drops Bcc - envelope-only
  on the wire, but the stored draft keeps it).
- Body: the first text/plain part, read as bytes (NOT through
  `splitBody`, whose `expandTabs` would mutate the tabs the user typed)
  and CRLF-normalized. The signature tail detaches structurally: at the
  first line that is exactly `-- `, everything after becomes
  SignatureBody (name `""` - assembly keys on the body only, so the
  block still emits) and the text above is Body. A body that merely
  contains a `-- ` line mis-splits but loses nothing: re-assembly
  re-appends the same tail, so the round trip is byte-faithful either
  way. No quote prefixing, no Re:/Fwd:, no account signature re-add -
  what was saved is what opens.
- Attachments: each remaining MIME part extracts to a per-dialogue temp
  dir (0700; MimeTypeOf + base name + size as a normal Attachment). The
  dir path rides the compose event and is removed by the same
  dialogue-close path that removes BodyPath. Send reads the extracted
  paths exactly like any fresh compose.
- Security resets to none: a resumed draft's crypto is re-decided at
  send; the compose does not guess.

## 4. Retirement on successful send

`deliverSend` (shared by live and scheduled delivery) gains the
retirement: after the transport succeeds, a non-empty ResumePath drops
the draft from the folder:

- worker `ActRemovePaths` with the one ResumePath (drops the index doc
  for that file - the mover's primitive), then `os.Remove(ResumePath)`.
- Order mirrors the mover: index link first, file second. A removal
  failure (already gone, moved by a sync tool) rides the send note - a
  delivered message never fails on cleanup (a retry would double-send).
  Transport failure: nothing happens, the draft stays. Retirement is
  success-only, exactly what successful-send tracking asked for.
- `no_fcc` untouched: the fcc branch is separate and already governed by
  the account flag. no_fcc off = draft gone, Sent copy stays; no_fcc on
  = draft gone, no copy.

## 5. Update-in-place re-save

An abort-to-save on a resumed draft (the same d key) must not duplicate.
`saveDraft` writes the new file (writeFcc now returns the written path),
retires the previous ResumePath (ActRemovePaths + os.Remove), and hands
back the state with ResumePath advanced to the new file - the open
dialogue keeps editing exactly one stored draft, and the next send
retires that one. The draft-handler seam returns the updated state so
the TUI stores it. A discard (no save) leaves the stored draft
byte-identical and untouched.

## 6. Tests

- compose.Resume round trip: a saved-draft fixture with Bcc, an
  attachment, a signature block, and tabs in the body parses back to the
  authored fields; the re-assembled text/plain body and envelope match
  the authored composition byte-for-byte (ignoring the fresh Date and
  Message-ID).
- deliverSend retirement: a successful transport retires ResumePath
  (index link gone, file gone); a failing transport leaves the draft.
- saveDraft in place: re-save retires the old draft, advances
  ResumePath, and leaves one draft file.
- TUI gating: resume-draft on a draft row publishes ComposeOpened; on a
  non-draft row it no-ops.
- Workflow: resume a saved draft, send, and the draft message is gone
  from the draft view while the Sent copy exists (or not, under no_fcc).
