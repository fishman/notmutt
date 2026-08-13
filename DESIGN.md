# notmutt - design document

notmutt = notmuch + email. It is not mutt and it is not neomutt: it is a
new, async, command-line-first mail client built on notmuch.

Requirements and working rules: AGENTS.md (normative). This document is the
architecture. Source material: aerc/ (production Go notmuch client - the
primary Go reference), neomutt/ (async patches), muttrc/ (live config),
afew/ (per-account mover), neomutt-docs/ (ConfigSet, CI), neovim/ (UI
async + Lua reference).

## 1. System context

The client is one stage of a pipeline; it must fit in without owning the
other stages:

```
mbsync / vdirsyncer        transport (maildir per account)
        |
        v
notmuch new                index (new.tags: unread, inbox)
        |
        v
post-new hook              folder rules + header rules + side effects
        |
        v
afew --move-mails          physical move (per-account folder priorities)
        |
        v
notmutt                    reads tag queries, writes tag changes
```

notmutt runs `notmuch new` / sync / filter as background jobs from inside
the client too (timer-less operation), but transport stays external.

## 2. Principles

1. notmuch is the single source of truth. No own database. Folders are
   derived state for sync-tool compatibility.
2. Async everywhere. The UI never blocks on a query, a tag change, a send,
   a sync, or a filter run.
3. State is separate from rendering. Every dialogue is a state machine
   living in the core; the UI is a view onto it. Sync/filter runs retag and
   re-render mailboxes while composition keeps working (AGENTS.md R4).
4. Incremental views. Views hold thread objects; refresh inserts, updates,
   and removes entries in place - no full rebuild (AGENTS.md R3).
5. Declarative rules. Exclusive tag groups are data, not cross-wired rules
   (AGENTS.md, hard-tag exclusivity).
6. The TUI is a library in waiting. Core has zero terminal dependencies.
7. Minimal, vetted dependencies. Supply chain policy in AGENTS.md R7.
8. Trust boundaries are explicit and fuzzed, never trusted (SECURITY.md:
   argv-only exec, terminal sanitization, log redaction, 0600).

## 3. Layer structure

```
+-------------------------------------------------------------+
|  tui/          BubbleTea views/components: index, pager,    |  <- extractable,
|                dialogue boxes, tab bar, menu, statusline    |     publish later
+---------------------------+---------------------------------+
                            |
+---------------------------v---------------------------------+
|  app/            BubbleTea models, event loop, routing,     |
|                  window management; renders dialogue        |
|                  states, no logic                          |
+---------------------------+---------------------------------+
                            |
+---------------------------v---------------------------------+
|  core/           dialogue state machines, tabs, view model, |
|                  event bus, config store                    |
+--------+------------------+------------------+--------------+
         |                  |                  |
+--------v--------+  +------v------+  +--------v--------+
| notmuch layer   |  | send layer  |  | filter engine   |
| async queries,  |  | async send  |  | tag rules,      |
| thread views,   |  | jobs,       |  | exclusive       |
| tag ops         |  | output      |  | groups, mover   |
+-----------------+  | review      |  +-----------------+
                     +-------------+

Config store (TOML, typed, observable) feeds all layers.
Lua scripting layer (future, AGENTS.md R8) wraps core APIs.
```

Rules: `core/` must not import anything terminal-specific. `tui/` must
not contain domain logic. BubbleTea's own Model/Update/View triad IS the
state/UI split: core exposes state + pure transitions; tui renders. The
`tui/` component set is extracted as-is into a published library; the
client becomes its demo. (BubbleTea's opinionated loop must not be allowed
to swallow core logic - the split is enforced by layout, not by the
framework.)

## 4. Async model

Goroutines + channels; no polling anywhere. The pattern is the aerc
worker (mail-handling reference, `aerc/worker/notmuch/worker.go`): a
worker owns a resource (a notmuch database, a send command), receives
messages on its action channel, and emits results on a response channel.
notmutt generalizes it to jobs.

- One worker per notmuch database. Actions: query (streaming), tag, get
  message. Queries stream results over channels; the UI applies them as
  they arrive (R3).
- Background jobs (sync, filter, send) follow the neomutt background model
  (neomutt/background/): job table, captured stdout/stderr kept for
  review, job dialog (AGENTS.md R4). Two deliberate improvements over the
  C model: a proper state enum (queued, running, done, failed, killed -
  neomutt only has a running bool plus exit code) and evented completion
  (`exec.Cmd.Wait` in a goroutine, completion sent on a channel -
  neomutt polls waitpid on every UI timeout, `background/background.c:281-317`).
- Job events are plain messages on the event bus: JobStarted, JobOutput,
  JobDone(exit), JobFailed(error). The UI subscribes; the core does not
  know the UI exists.
- Send jobs extend the model with the neomutt async-send pattern
  (branch async_send, aa4478969): the send command's pid is handed to the
  job layer, Fcc is deferred to completion, and a FAILED send retains the
  composed Email so the dialogue can restart from it - that is the
  pause/restart seed for R4.

### Incremental thread views (R3)

libnotmuch is snapshot-based: thread-type queries (`nm_query_type` =
NM_QUERY_TYPE_THREADS - the "nm_message_type threads" concept) return
immutable thread handles. There is no incremental API - incrementality is
built in the client's view model. Neomutt's current behavior is the
baseline to beat: threads load synchronously (`notmuch/notmuch.c:1074-1119`)
and every mail check re-runs the whole query as a message-level search,
marks everything inactive, merges, and fires NT_MAILBOX_INVALID so the
index rebuilds its thread tree (`notmuch/notmuch.c:2183-2308`). notmutt:

- The view model is an ordered tree of threads/messages built from the
  last query.
- A refresh re-runs the query asynchronously and diffs against the view
  model: inserted messages (new replies in a visible thread) become
  insert events, tagged-out messages become remove events, reordered
  threads become move events. The renderer applies the changeset; the
  cursor and scroll stay stable.
- Neomutt rebuilds the whole index on refresh; notmutt must not. The diff
  must be cheap enough that auto-refresh on new mail is continuous, not
  periodic.

## 5. Dialogue state machines (R4)

A dialogue is a struct in `core/` with:

- state: fields, attachments, recipients, fcc, send progress, error,
  captured output
- transitions: pure functions of (state, event) -> (state, effects)
- events: UI-independent (KeyPressed is a UI event; the core maps it)

Properties:

- Pause/restart: pause detaches the dialogue from the screen and keeps the
  state alive; restart re-attaches. A paused send job keeps running and
  the dialogue shows its progress on resume.
- Tabs: `[]DialogueState`, one active, rendered by the tab bar. Each tab
  holds a full dialogue; switching tabs never destroys state. (aerc's
  tab bar - `app/app.go` `NextTab/PrevTab/PinTab` - is the proven
  interaction; the dialogue model here is ours.)
- Send: `send_command` submits the composed message to the send layer as a
  background job; completion/error events land on the dialogue's event
  queue. Send output stays reviewable after completion (neomutt
  f2f246718 pattern).
- Sync/filter runs never touch dialogue state. They emit mailbox events;
  dialogues react only through their own state transitions.

## 6. Filter engine (R2)

Contract shared with afew:

```
filters: query -> tag changes
```

Two backends, same contract: afew (initial) and an integrated engine
(later). The integrated engine:

- Rules are data (TOML): `[filter.<name>] query = ...  add = [...]
  remove = [...]`.
- Idempotency is enforced by the engine: every rule runs with the
  NOT-guards it needs; re-runs only touch new mail.
- Exclusive tag groups are declared once:
  `[tag-groups] hard = ["archive", "deleted", "sent", "draft", "pending",
  "spam"]`. Applying any tag in a group removes the others automatically,
  engine-side, after all rules ran. Adding a tag to a group requires no
  rule changes - this fixes the muttrc cross-untagging pain.
- Account tags derive from folder paths (folder:/^gmail\//) - same rule
  shape as today.
- The mover keeps afew's `folder_priorities` model: per-account candidate
  lists, first existing folder wins, `*` globs.
- Triggers: notmuch post-new hook for mail indexed outside the client;
  in-client: after `notmuch new`, after sync jobs, on tag ops that affect
  folder tags.

## 7. Crypto (R10)

PGP and S/MIME behind one Provider interface; backends are system CLIs,
not libraries (aerc `lib/crypto` shape, neomutt `ncrypt/cryptglue`
registration split per crypto family):

```
crypto.Provider (core interface, PromptFunction hooks)
  -> crypto/gpg     system gpg: --status-fd 2 --batch, parsed status (aerc gpgbin)
  -> crypto/smime   openssl smime (neomutt smime.c) or gpg CMS mode
  -> crypto/pgp     OPTIONAL in-process OpenPGP via ProtonMail go-crypto
                    (gopenpgp), aerc's "internal" provider: auto | gpg |
                    internal, default gpg. Only for standalone keyrings /
                    headless batch crypto; interop and key management
                    are the client's problem in this mode. Never the
                    default.
```

- Compose path: dialogue flags {sign, signKeyId, encrypt, encryptTo} ->
  MIME assemble (go-message) -> sign/encrypt transform (async job) -> fcc
  -> send job. Key resolution (locate/recv keys, possible key-server
  contact) is async.
- Read path: decrypt/verify as an async job; view model carries
  {decrypted body, sig status, signer, key id, error}; pager renders body
  + status.
- Passphrase: gpg-agent + external pinentry with TUI suspend/resume
  (aerc `lib/pinentry`) - the only prompt path for the gpg backend.
  NO loopback mode for gpg: the passphrase would enter client memory
  (Go cannot zero secrets), the prompt path becomes the client's
  security surface (masking, scrollback, logs, crash dumps), and
  smartcard PINs fail under loopback. gpg-agent passphrase caching
  (long --max-cache-ttl) makes pinentry rare anyway. Native
  PromptFunction prompting is used ONLY by the in-process provider
  (crypto/pgp), where no external agent exists.
- Key selection = selector dialogue state (section 5 machinery) fed by
  keyring queries (gpg --list-secret-keys --with-colons).
- New crypto must be a dialogue: composing with a sign/encrypt decision
  in flight, pause/restart semantics apply to the crypto transform too.

## 8. Theming and dark mode (R11, R12)

Theme = palette + styles + variants, resolved at load into a readonly
style table; resolution errors are load errors (unknown palette name,
bad hex, unknown object - fail loudly, not at render).

```
[palette]        named truecolor colors (onedark reference: muttrc/theme/onedark.muttrc)
[palette.dark]   per-variant overrides of single names
[theme.dark]     styles: fg/bg/attrs referencing palette names or raw hex
[theme.light]    optional override variant
[theme.default]  = "dark"
```

- Styles inherit from `normal`; per-object overrides state only diffs.
- Two escape hatches so indirection never blocks: a style may pin raw
  hex (one-off color, no palette entry), and a variant palette
  overrides a name when variants genuinely diverge. Resolution order:
  style hex > variant palette > base palette.
- Index coloring: `[theme.dark.index.tag.deleted]` style maps notmuch
  tags to styles (base.colors' `~l`/index_tag concept as data). Tag
  styles layer over the base index style; exclusive-group priority
  resolves conflicts.
- Regex styles (body URLs/emails, header ^From:) ordered, last wins
  (mutt semantics).
- The resolved style table feeds BubbleTea styles at render; content
  sanitization (SECURITY.md F1) stays a separate layer - styling never
  sees raw mail bytes.
- Variant switch = config-store notification (ColorSchemeChanged event
  from the dbus watcher, or `:theme` command): observers re-render,
  no reload. The store already has the observer machinery (section 9).

### Index row model (R11)

The index line is a fixed-slot template, not a format string. Mutt's
`index_format` conditionals (`%?X?yes&no?`, `%<GA?%GA&>`) shrink the
row when content is absent - the author column shifts per row and the
row length varies with tag count. The slot model reserves every
column; only content is per-row.

```
[index.row]              # slots in order (muttrc base.muttrc index_format is the reference)
number  = { width = 2 }
flags   = { width = 1 }
attach  = { width = 2, optional = true }   # glyph or blank; column always reserved
date    = { width = 16, format = "%y/%m/%d %I:%M%p" }
author  = { width = 15, truncate = "left" }
subject = { fill = true }
count   = { width = 6 }                    # "(1234)" - parens are part of the slot
tags    = { max = 2, order = ["archive", "deleted", "sent", "draft", "pending", "spam", "inbox", "unread"] }

[tag-transforms]         # glyph table as config data (muttrc tag-transforms)
deleted = "[x]"
archive = "[A]"
```

- Optional slots always render their width: attach column shows the
  glyph or blanks; the two tag cells show up to two glyphs, whichever
  tags are present, blank-padded.
- `tags.order` is the display priority (hard tag group first by
  default): with more than `max` tags present, the highest-priority
  ones win, deterministically.
- Widths are terminal cells (wcwidth): emoji glyphs are double-width;
  go-runewidth (or equivalent) sizes and truncates, never rune counts.
- The row template lives in the view model; the tui index renders it
  from core state (section 3).

DBus watcher (R12): build tag `dbus`, linux-only build constraint.
Godbus/dbus pinned; watches xdg-desktop-portal
org.freedesktop.portal.Settings for color-scheme changes, emits
ColorSchemeChanged on the event bus. Absent by default, absent on
macOS/Windows. No watcher: manual `:theme`.

## 9. Config (R8)

TOML files (e.g. ~/.config/notmutt/config.toml, accounts/*.toml). The
parser feeds a typed, validated, observable store in the ConfigSet mold
(neomutt-docs/docs/config.md): every key has a type, validator, default;
changes notify observers on the event bus. No ad-hoc disk reads.

Schema draft:

```toml
[accounts.jelveh]
primary_email = "reza@jelveh.me"
folders = ["jelveh/*"]

[tag-groups]
hard = ["archive", "deleted", "sent", "draft", "pending", "spam"]

[filter.folder-archive]
query = "folder:gmail/Archive or folder:gmail/Archives or ..."
add = ["archive"]

[view.inbox]
query = "tag:inbox and not tag:deleted and not tag:spam"
threads = true

[mover.folder_priorities]
archive = ["Archives", "Archive"]
deleted = ["[Gmail]/Trash", "Trash", "Deleted Items"]

[ui]
keymap = "vim"          # "vim" (mutt-style) or "emacs"; per-context overrides below

[bindings.compose]      # context sections mirror aerc's binds.conf model
"s-t" = "send"
"C-r" = "compose restart"
```

Lua (future): TOML stays declarative config; Lua binds the core API for
hooks and custom filters. The event bus and dialogue APIs are the stable
Lua surface (neovim model: event loop, RPC-shaped API, Lua as extension
language).

## 10. TUI (R5)

BubbleTea. The `tui/` package provides: index view, pager view, dialogue
boxes, tab bar, menu, statusline - all pure renderers of core state
(BubbleTea View functions of core state; models are thin adapters). The
client wires them. Extraction path: `tui/` is versioned separately from
day one (own module, own README); publishing is a packaging step, not a
refactor.

## 11. Mail library (R6)

Use libraries, do not port neomutt C code:

- Parse: `go-message` (emersion) - RFC 5322 + MIME parsing, production
  use in aerc.
- Compose: `go-message` (mail package) - header + MIME construction,
  attachments, multipart/alternative.
- mailcap: parse and dispatch attachments/HTML viewing through mailcap
  entries; fallback to configured external viewers.

If a library falls short on a requirement (e.g. exotic MIME), port the
specific neomutt piece, not the whole parser.

## 12. Library stack (R7)

| concern      | choice                                         |
|--------------|------------------------------------------------|
| language     | Go (AGENTS.md decision record)                 |
| async        | goroutines + channels (no framework)           |
| TUI          | BubbleTea (charm) + Lip Gloss                  |
| mail parse   | go-message (emersion)                          |
| mail compose | go-message (mail package)                      |
| crypto       | NO library - system gpg + openssl CLIs (R10)   |
| notmuch      | aerc's cgo-free pattern: notmuch CLI via exec (worker/notmuch/lib), or in-tree cgo bindings - decide at M1 by benchmarking |
| cell width   | mattn/go-runewidth (wcwidth for aligned rows)  |
| config       | BurntSushi/toml or pelletier/go-toml           |
| lua (later)  | gopher-lua                                     |
| cli          | cobra (or stdlib flag if it suffices)          |

## 13. Directory layout

```
notmutt/
  core/          dialogue state machines, view model, filter engine, config store
  notmuch/       async notmuch layer (query, threads, tags)
  send/          async send jobs
  crypto/        Provider interface + gpg/openssl backends (system CLIs)
  filter/        rule engine, exclusive groups, mover
  tui/           extractable TUI library (BubbleTea views/components)
  app/           client binary: models, event loop, windows, bindings
  cli/           headless commands (tag, move, filter) for scripting
  config/        TOML schema + typed store
  lua/           (future) scripting bindings
```

## 14. Data flow: new mail arrives

1. Job `sync` runs mbsync + notmuch new (background).
2. Job `filter` runs post-new pipeline (folder rules, header rules,
   exclusive-group resolution, mover) - afew initially, engine later.
3. Mailbox events land on the bus; view model diffs; index widget inserts
   the new messages into visible threads.
4. User opens a new message: async fetch, pager renders from the parsed
   mail (go-message), attachments dispatch via mailcap.
5. User replies in a compose tab; send runs as a job; the dialogue shows
   progress; fcc lands via notmuch.

## 15. Milestones

- M1 skeleton: config store (typed, observable), event loop, notmuch
  query layer with message-mode, minimal index + pager.
- M2 thread mode: thread views with diff-and-insert refresh; auto-refresh
  on mail events.
- M3 dialogues: compose state machine, tabs, pause/restart, async send
  with output review.
- M4 filter engine: rules, exclusive tag groups, mover with per-account
  folder priorities; afew backend behind the same contract.
- M5 extraction: publish `tui/` module as a standalone Go module; Lua
  bindings on the core API.

## 16. Risks

- notmuch access pattern: cgo-free CLI-per-query (aerc's current worker)
  pays process spawn per query but avoids cgo complexity and is exactly
  what the muttrc pipeline already scripts; in-tree cgo bindings pay
  build complexity but stream faster. Decide at M1 with a benchmark on
  the real 129k-message database; the worker interface hides the choice.
- Supply chain (Go module proxy + sumdb are the trust infrastructure;
  typosquatting and AI-generated modules still exist): policy in
  AGENTS.md R7 - minimal deps, pinning, govulncheck, vendoring, review.
- Incremental diffs against large views: diff cost must stay proportional
  to changes, not view size; keep per-thread timestamps for cheap
  change detection.
- notmuch snapshot semantics: thread handles die when the database
  changes; the view model must re-resolve by (thread_id, message-id), not
  hold pointers across refreshes.
- BubbleTea swallows app logic: the opinionated loop makes it easy to put
  domain code in models. Layout rule from section 3 is enforced by code
  review, not by the framework.
