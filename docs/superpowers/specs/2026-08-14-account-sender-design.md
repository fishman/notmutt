# Send/reply dialogue - design spec

Approved 2026-08-15 (the draft of 2026-08-14 is superseded; the
requirements history below is preserved). Milestone: R4 (async send +
dialogue state machine) with the R2 account data model. Normative text
is AGENTS.md; this spec is the implementation contract.

## 1. Problem

mutt's account selection for sending is send-hook based: regex hooks
matching on mailbox/folder context pick a `From:`. For a tag-based
notmuch client that is brittle: there is no folder context in a tag
view, a message can carry several account-relevant tags, and the hook
chain grows with every account. The client needs account selection
that is data, not hook code. The send itself must be async (R4):
background sync/filter runs must never interrupt composition.

## 2. Requirements (user, verbatim intent)

### Account selection

- Easy to switch accounts while composing.
- Default selection: the account that matches the received message -
  replying to a message received on account X sends from account X,
  without configuration per folder.
- Gmail allows arbitrary dots in the local part; Gmail plus addressing
  (+suffix). Normalization is FUTURE work (the From-address table
  below); milestone 1 matches the exact configured address.
- Custom-domain accounts may have wildcard senders (msmtp supports
  that sending case - no verification that the local part exists).

### Async send

- Send runs in the background (msmtp), but the client still waits for
  a completion notification - the user must not be left wondering.
- If the send fails, reopen the background/send dialog WITHOUT being
  destructive to the current action: the dialogue state (draft,
  attachments, flags) survives; failure reopens it for retry/edit.
- Launching the reopened dialogue as a background tab is an acceptable
  shape (R4: state is separate from UI, dialogues can be paused and
  restarted).

### Fuzzy selection, signatures

- A FUZZY selector for compose-time choices: account switcher,
  signature picker (later: keys, templates). In-process matcher, no
  fzf subprocess (no new exec surface, fits the extractable TUI).
- Signatures are a per-account FOLDER of signature files, selectable
  via the fuzzy picker. Selection result is part of the dialogue state
  (R4), not a config mutation.
- Send hooks are NOT a requirement now, but the account-selection
  model must not foreclose a data-driven hook-like seam later.

### Compose dialogue (user, 2026-08-15)

- R opens a reply dialogue, gr a reply-to-all, in a NEW TAB.
- The tab shows from/to/cc/subject/attachments and a mail preview
  (approved layout, section 5).
- The body is edited with $EDITOR; the signature attaches from the
  account.
- The reply account = the account the message was received on (its
  account tag); new compose uses the view's active account.

## 3. Pinned decisions (2026-08-15)

- **Send transport:** ONE configurable command, default
  `msmtp --read-envelope-from` (`[send] command` + `args`, section 9).
  msmtp reads the envelope sender from the message's own From header
  and selects its account by `from` matching in ~/.msmtprc - the
  client never sees the account table, and no per-account argv exists
  (F4: the From address never interpolates into the command line; the
  message goes to stdin).
- **Reply body:** mutt-style quote of the original in the $EDITOR
  buffer (section 6).
- **Sent copy:** fcc to the account's sent folder on successful send
  (`[accounts.<name>] sent_folder`), then `notmuch new` indexes it and
  the folder rule tags it sent (the muttrc record + nm_record_tags
  reference shape).
- **Compose layout:** form rows (From/To/Cc/Subject), attachment
  rows, body preview pane filling the rest; keyhint + status floating
  at the bottom (the existing frame discipline, section 5).
- **Header editing:** the $EDITOR buffer carries the header block
  (To/Cc/Subject) above the body (the mutt edit-headers shape) - one
  mechanism instead of inline inputs. Inline field editing is future
  surface.

## 4. Architecture

The R4 shape, mapped onto the existing app:

- `compose` package (NEW): the dialogue state machine. Pure Go, no UI
  code, no notmuch handle (R5 - the whole TUI layer must stay
  extractable). Owns the state transitions, the reply/forward
  prefill, the signature append/replace, the message assembly
  (go-message, vendored v0.18.2: `mail.CreateWriter`,
  `CreateSingleInline`, `CreateAttachment`, `GenerateMessageID`).
- `app` layer: the send job. Runs on the worker's lock-budgeted
  action path: assembly bytes -> transport exec (argv, stdin) with
  captured output -> on success fcc write + `notmuch New()` + tag the
  original. Emits events on the bus (`SendResult`); R15 progress
  events at batch boundaries.
- `tui` layer: compose mode renders the active tab's state (form +
  preview), the fuzzy picker renders as a popup overlay, keyhints
  derive from the compose/fuzzy binding contexts (R9). The $EDITOR
  run pauses the TUI via tea exec passthrough (v2 `tea.ExecProcess`),
  the dialogue state survives (R4 pause/restart).
- `config`: `[send]` transport argv, `[accounts.<name>]` gains
  `from`, `sent_folder`, `default_signature`.

Tabs: the model holds a stack of dialogue states
(`[]*compose.State` + active index); composition is tabbed (R4), the
index/pager stay behind, `[`/`]` switch tabs.

## 5. Dialogue state and the compose tab

```go
type State struct {
	ID          string         // tab identity
	Mode        Mode           // compose | reply | reply-all | forward
	Account     string         // selected account name
	From        string         // resolved sender address (account data)
	To, Cc      []string       // recipient addresses
	Subject     string
	Body        string         // edited body (without the signature)
	Attachments []Attachment   // {name, path, size}
	Signature   string         // signature name ("" = none)
	Original    *core.Message  // replied/forwarded message (nil for compose)
	Phase       Phase          // editing | aborting | sending | failed
	Output      string         // send job captured output (failed)
}
```

The compose tab renders:

```
From:     <from>                    [<account>]
To:       <to, one per row>
Cc:       <cc>
Subject:  <subject>
---
[ ] <attachment name> (<size>)     <- one row per attachment
---
<preview: body + signature, sanitized through the F1 render path>
---
<keyhint row derived from the compose bindings>
<status line as always>
```

The preview reuses the sanitized render path (F1): the quoted
original is mail content and must not reach the terminal un-sanitized
- the existing pager sanitizer applies. The body is never submitted to
any external service.

Phase transitions: editing -> (y) sending -> sent (tab closes) | failed
(Output kept, tab stays, e retries). q arms aborting; a second q
confirms and discards the state; any other key cancels (an accidental
q must not lose a long draft).

## 6. Reply/forward flows

Account resolution (reply): the replied-to message's account tag
(the account it was received on - the user's rule); fallback the
view's active account (the status bar account); fallback the first
account. The message's account tag resolves through the existing
`accountTag` logic (status line machinery).

Prefill rules (all data, no prompts):

| | R (reply) | gr (reply-all) | F (forward) | new compose |
|---|---|---|---|---|
| account | original's account tag -> active | same | same | view's active account |
| To | original From | original From | - | - |
| Cc | - | original To+Cc minus the account's `from` address (milestone 1 has exactly one address; the address table is future work) | - | - |
| subject | one "Re: " prefix (strip repeated Re:/Fwd: first) | same | one "Fwd: " prefix | - |
| body | quoted original + signature | same | quoted original + signature | signature only |

Quoting (mutt-style):

```
On <Tue, Aug 14 2026>, <author> wrote:
> line 1
> line 2
```

The attribution date derives from the original's Timestamp; the
quoted lines are the original body with "> " prefixed (quoted
paragraphs keep their depth; a cap of 5 like the renderer). The
signature block follows:

```
(blank line)
-- 
<signature content>
```

Signature handling on re-edit after a signature switch: if the buffer
ends with the previously attached signature block (exact match of
"-- \n" + previous content), replace it with the new block; otherwise
the tail was edited - it is the user's text now and stays.

Reply headers: In-Reply-To = original message-id, References =
original References + original message-id (core.Message carries both).

## 7. Editor flow

e runs `$EDITOR` (fallback `vi`) on a temp buffer:

```
To: <to list>
Cc: <cc list>
Subject: <subject>

<body>
(blank line)
-- 
<signature>
```

The TUI pauses (tea exec passthrough on the same terminal); the
dialogue state survives (R4). On exit the buffer parses back: the
header block (up to the first blank line) updates To/Cc/Subject; the
rest is the body (signature stripped per section 6 for storage, kept
for the preview). The written format is the contract: a buffer
without the separator blank line parses as all-headers, an empty
body. Buffer I/O is local; the temp file is 0600 (F5).

## 8. Send job

y (send) transitions the state to sending; the job runs on the
worker's action path:

1. Assemble: go-message writer into a buffer - From, To, Cc, Subject,
   Date, Message-ID, In-Reply-To/References (reply), text body part
   (signature appended), one attachment part per attachment. The
   assembly is pure and unit-tested (compose package, bytes in/out).
2. Transport: exec the `[send]` argv (default
   `msmtp --read-envelope-from`) with the assembled message on stdin,
   stdout/stderr captured (R4: output kept for review; the failed
   dialogue shows it). No shell, no interpolation (F4).
3. Success: write the fcc copy into the account's sent maildir
   (maildir file naming: unique, 0600), then `notmuch New()` so the
   copy indexes and the folder rule tags it sent (the R2 filter
   engine is its own milestone - the copy is physically in the sent
   folder regardless). Tag the original `+replied` (reply modes) or
   `+forwarded` (forward) through the worker's tag path. Close the
   tab. The view refreshes.
4. Failure: phase = failed, Output kept, the tab stays open with the
   error visible; e re-edits and y retries.

Order is transport first, then fcc: what was not delivered is not
stored. A missing `sent_folder` skips fcc with a visible status note.
A missing `from` on the selected account is a send-time error.

## 9. Config surface

```toml
[send]
command = "msmtp"
args = ["--read-envelope-from"]

[accounts.gmail]
folder = "gmail"
from = "Reza Jelveh <reza.jelveh@gmail.com>"
sent_folder = "[Gmail]/Sent Mail"
default_signature = "gmail"
```

- `[send]`: the transport argv, tokenized at load, exec'd as argv
  (F4). Defaults as above.
- `[accounts.<name>] from`: the account's sender address. Optional;
  sending without one is a clear error.
- `[accounts.<name>] sent_folder`: the sent maildir name relative to
  the account's mail root (the muttrc record reference). Optional;
  missing skips fcc with a note.
- `[accounts.<name>] default_signature`: the default signature file
  name in `~/.config/notmutt/signatures/<account>/`; missing = no
  signature.
- Signatures: `~/.config/notmutt/signatures/<account>/` - one file
  per signature. Files are the user's own text (not mail content);
  they render in the preview through the sanitize path like any body.

## 10. Keybindings (R9)

New `compose` context in both schemes:

- vim: j/k move the form cursor, e edit body, a attach (path prompt
  dialogue), d detach, c account/signature (fuzzy picker), y send,
  q abort (two-press confirm), [ / ] tab switch.
- emacs: same action letters, ctrl+n/ctrl+p move.

New `fuzzy` context (the selector dialogue - future selectors for
keys/templates reuse it): type-to-filter, j/k or ctrl+n/ctrl+p move,
enter select, esc close.

The keyhint/help UI derives from the binding map (R9) - nothing
hardcoded.

## 11. Testing

- compose package unit tests: account resolution chain (reply uses
  the original's account tag, fallbacks), prefill rules (To from
  original From, reply-all Cc minus own addresses, subject prefix
  stripping), quoting (depth cap, attribution), signature append and
  exact-tail replace, editor-buffer parse-back round trip, assembly
  structure (headers, parts, Message-ID), argv construction (the
  transport argv is data - a test pins that no mail content ever
  interpolates into it), phase transitions (aborting confirm, send
  failure retains state).
- app send job: scripted against a stub transport - `[send] command`
  points at a capture script; the test asserts the assembled message
  on stdin, the fcc file lands in the sent folder with 0600, New
  runs, the original gets its tag. Failure path: stub exits non-zero,
  the job emits SendResult{failed, output}.
- tui: compose mode render (fields, attachments, preview with
  signature, keyhint bar from the compose bindings), the fuzzy popup
  render, tab switch, the q two-press abort.
- Real sends stay scripted (stub transport) in CI; the user's first
  live sends are their own.

## 12. Non-goals (this milestone)

- No SMTP client implementation (transport stays msmtp).
- Gmail dot/plus normalization and the per-account From-address table
  (the draft's section 3 model) - future work; milestone 1 matches
  the exact configured `from`.
- Inline field editing in the tab; the editor buffer owns headers.
- Attachments from the fuzzy picker (a path prompt is the milestone-1
  shape); crypto (R10) transforms between assembly and the send job.
- Draft/pending save and postpone - future work.
- S/MIME rendering: deferred by the user (2026-08-15) - detect
  detached smime.p7s parts, verify via `openssl smime -verify` (R10
  boundary), surface the verdict in the view model.
