# M3: Immutable email IDs (AI/todo linkage) - design spec

Builds on M1/M2 (mailbox view, view/worker/bus, staged tag ops).
Normative text is AGENTS.md; this spec pins the mechanism.

## 1. Goal and acceptance

Every message gets an immutable, copyable ID that survives re-syncs and
folder moves, opens the message's thread from anywhere (org-mode,
taskwarrior, other apps, the command line), and feeds AI/todo pipelines
that need to process and reference emails.

Acceptance (scripted tests; items 6-10 are manual):

1. `EncodeID`/`DecodeID` round trip for message-ids with whitespace,
   quotes, unicode, and angle brackets stripped; the token is a pure
   function of the message-id (same mail re-synced = same token).
2. Malformed tokens (bad prefix, bad base64, overlong) fail with a
   clean error and exit 1; nothing is queried.
3. `notmutt open <token>` for a message present in the database boots
   the TUI and opens that message's thread; for a message that left
   the database (expired, deleted) it exits with "not in database".
4. `notmutt show <token>` prints the thread's messages as JSON lines
   to stdout and exits; the metadata-only shape excludes bodies unless
   `--body` is given; the fields match the thread fetch (id, thread,
   timestamp, author, subject, tags, paths).
5. `;` in the index and pager copies a token; the token references the
   newest message of the cursor row's thread (what the overview line
   represents), falls back through the thread fetch when the row
   carries no message id, and reports "copied nm1-..." on the message
   line.
6. Manual: `;` in the index copies a token; paste it in org-mode as
   `[[notmutt:TOKEN][subject]]` and follow it - a terminal opens with
   the thread.
7. Manual: taskwarrior round trip - `task add notmuttid:TOKEN`, then
   `notmutt open $(task _get <id>.notmuttid)` opens the thread.
8. Manual: mbsync re-sync and an afew folder move leave existing
   tokens valid (the message-id is untouched by both).
9. Manual: a link works even when the thread is not in the default
   view (the open path loads by thread id, view-independent).
10. Manual: `notmutt show TOKEN --body` prints the rendered text
    parts on stdout, piped into an AI/todo tool (the user's pipeline
    reads content it asked for; notmutt itself never logs bodies).

## 2. The ID: versioned token over the message-id

The ID is a pure function of the RFC 5322 message-id - a header that
is immutable by protocol, deduplicated by notmuch (`id:` search is the
identity surface), and untouched by folder moves, flag changes, or
re-syncs. No mapping store, no schema change, no cache extension:
R1's "no own database" holds because the ID derives from a
notmuch-exposed field, and the open path is stateless.

Format: `nm1-` + base64url(message-id without the surrounding `< >`,
unpadded). The `nm1` prefix versions the encoding (room for v2) and
gives the token a greppable shape in org/taskwarrior files.

Rejected alternatives:

- Content digest (sha256 of the raw file): one-way, so opening needs
  a mapping store (a new derived store or a cache schema extension,
  R13 scope creep); and mbsync can re-deliver the same message
  byte-differently (header order), breaking "same email, same ID".
- Internal database id: enumeration breaks on re-sync, and a
  synthetic authoritative id violates R1.
- Hash-only token (no message-id recovery): hides the sender domain
  but makes the open path stateful (a mapping store again). The
  message-id is not secret - it already travels in plaintext headers
  and in every References header of the thread - so the reversible
  token costs nothing and keeps the client stateless.

`EncodeID`/`DecodeID` live in `src/mail/idtoken.go` (pure functions,
no dependencies). Decode is strict: prefix check, base64url charset,
length cap (1024 bytes - message-ids are tens to a few hundred
bytes), and the decoded value must not be empty. Invalid input is an
error, never a query.

## 3. The open path: `notmutt open <token>`

Subcommand dispatch in `app.Run`, the `setup` pattern
(`os.Args[1] == "open"`):

1. Strict decode; malformed token: stderr usage error, exit 1.
2. Resolve: one limit-1 `ActQuery` with `id:"<decoded>"` (the
   `idQuery` escaping from the apply path - DRY), asking notmuch
   whether the message exists and which thread it belongs to.
   A miss: "notmutt: no message with id ... in the database", exit 1.
3. Boot the TUI with an initial-open request for the thread id
   (passed into `tui.New`; the model fires the existing open seam
   once its first rows land - the same ActThread + ThreadLoaded path
   as the `enter` key). Opening by thread id is view-independent:
   the link works whether or not the thread is in the default view.
4. Read-only: a token never grants tag mutations. open/show share
   only the read paths (ActQuery/ActThread).

`notmutt open` renders a TUI, so it needs a terminal. The org-mode
integration wraps it in a terminal (`kitty notmutt open TOKEN`, see
section 6) - that wrapper is user-side data, not client behavior.

## 4. The show path: `notmutt show <token>`

The processing surface for AI/todo pipelines: same decode + resolve
as `open`, then print and exit - no TUI. Output is one JSON object
per message of the thread (the thread fetch's field set: id, thread,
timestamp, author, subject, tags, paths), newline-delimited on
stdout. Bodies are excluded by default - the metadata locates the
message and its files, and a pipeline reads content from the paths
under its own rules. `--body` is an explicit opt-in that includes
the rendered text parts (body + signature, F1-clean); the user's
tooling asked for the content, notmutt itself never logs it (F6).

Exit codes: 0 found, 1 malformed token, 2 not in database.

## 5. The TUI copy path: `copy-id`

New action `copy-id`, bound to `;` in the vim and emacs index and
pager schemes (free in both; mutt's tag-query slot). Implementation:

- The cursor row is a thread summary without message ids (the reply
  bug's root cause, fixed by thread fetch). `copy-id` resolves the
  same way: use the row's Message.ID when present, else one ActThread
  fetch and the newest message - the exact selection the reply path
  uses, extracted into the shared `newestMessage(rpl)` helper (DRY
  with `replyPrefill`).
- Encode the message-id, then deliver through a configurable
  clipboard command: `[ui] copy-cmd = ["wl-copy"]` (default) or
  `["xclip", "-selection", "clipboard"]` - tokenized at load,
  executed argv-only (F4), never shell-interpolated. Unset
  `copy-cmd` skips the external step.
- Feedback: the token is shown on the message line
  ("copied nm1-..."), so a copy works on a terminal without any
  clipboard tool and the user sees what the ID looks like.

## 6. Integration data (user-side reference configs)

The client ships no integration code; these are reference shapes for
the config the user keeps (the muttrc pattern):

org-mode (`~/.emacs.d/init.el`):

```elisp
(org-link-set-parameters "notmutt" :follow
  (lambda (tok) (start-process "notmutt" nil "sh" "-c"
                 (format "kitty notmutt open %s" (shell-quote-argument tok)))))
```

Links: `[[notmutt:nm1-...][subject]]`.

taskwarrior (`~/.taskrc`):

```
uda.notmuttid.type=string
```

```
task add +inbox notmuttid:nm1-...
notmutt open $(task _get 123.notmuttid)
```

## 7. Security

- Strict decode (section 2): a crafted token cannot reach the query
  layer as anything but a decoded string, and the query itself is the
  `idQuery`-escaped form - argv-only, never shell text (F4).
- The token is a reference, not a credential: it opens your own
  local mailbox and nothing else. It is read-only by construction.
- The token is header-derived, so it is as sensitive as the message-id
  itself (visible in plaintext headers in transit). The sender
  domain is recoverable by design - that is the stateless-open
  tradeoff, and it is accepted because the message-id is not secret.
- Tokens in org/taskwarrior files are user data by design. notmutt
  never logs a token or a body (F6); the message line shows the token
  only on explicit copy.
