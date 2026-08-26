# notmutt - requirements and architecture

Workspace: mail client under construction. Source material:
`references/neomutt/` (async patches), `references/muttrc/` (live config),
`references/afew/` (per-account MailMover), `references/notmuch/` (notmuch
source), `neovim/` (UI async + Lua reference).

Goal: async mail client. All mail via notmuch.
Architecture constraints, not suggestions.

## The mail concept

Tags = logical model. Folders = physical storage. Every view, filter,
trigger = notmuch query or tag op; folders only for sync-tool
compatibility and physical moves.

### Hard-tag exclusivity (KNOWN PAIN - must be fixed)

Folder tags mutually exclusive: one message, one home (inbox, archive,
deleted, sent, draft, pending, spam). Config enforces by hand: every new
folder tag needs `-newtag` on every older rule + cross-untag rule.
Unacceptable.

Filter engine MUST support declarative exclusive tag groups:
`[tag-groups.folder] tags = ["inbox", "archive", "deleted", "sent",
"draft", "pending", "spam"]`. Applying any member removes the others
automatically. Soft tags in no group - unlimited, coexisting, never
moved. Adding a group tag must not touch existing rules.

### Idempotency

Every filter rule must carry a NOT guard: re-runs touch only new mail
(first backfill ~129k messages takes minutes; steady state must be
cheap).

### Per-account folder priorities

Move destinations resolve per account by existence: candidates in order,
first existing wins, `*` = globs (afew `folder_priorities`). Move engine
must keep this model.

### Lock handling

notmuch lock waits must be capped (`lock_timeout=10`): UI tag ops error
out instead of hanging behind a background index/send.

## Requirements

### R1. notmuch = single source of truth

No own DB. State, flags, threads, search from libnotmuch. Virtual views
= tag queries; folder state derived, never authoritative. ONE derived
store: index cache (R13) - bbolt mirror of the overview query,
revision-keyed, invalidated by lastmod, rebuilt from query output only.
Stale read re-syncs (startup O(changed); full walk only on cache miss
or revision mismatch).

cgo = the runtime backend (record 3); CLI behind `-tags cli` (the F10
escape hatch). The two backends are build-exclusive (`!cli` / `cli`) for
license separation: the cgo variant links GPL libnotmuch and is released
GPL-3.0, the CLI variant links nothing and is released Apache-2.0 (see
docs/licensing.md). Index fill = FULL walk
(record 29), one C pass, no stubs. In-reply-to refs ride the per-thread
fetch (refsfromterms build, record 30). cgo handle read-only; Tag
reopens read-write for the op only (persistent write handle blocks
other notmuch processes).

### R2. Filters and triggers through notmuch + afew

Filter interface must be a boundary: consumes notmuch docs + rules,
emits tag changes. Same contract: hooks/afew pipeline + integrated
engine replacing afew.

MailMover NATIVE (src/filter/mover.go; MailMover.py = reference logic,
never runtime).

Engine owns the FULL pipeline (folder rules, header rules, mover
in-process); muttrc hook + afew = reference shapes, not backends. Folder
rules DERIVED: hard tag candidates -> `folder:"<account>/<candidate>"`
OR-queries with auto NOT-guards; account tags from the folder prefix.
Exclusive groups per the concept section. Header rules = data (query +
add, guards by engine). Conditional rules explicit (delivery
untag-reversal, trash return-to-inbox). Read-only accounts never
classified: no folder tags, no account tag, no header tags, no moves.
Side effects on filter job completion.

Contract per-message (afew shape): snapshot in, tag ops out. Query rules
= data-driven impl; algorithmic filters (SpamFilter, DKIM - later) =
registered Go impls, `[filter.<name>] type = "..."`, unknown type = load
error. Group resolution after every filter. Content filters in-process,
no DB handles, no content out. Mover updates via own notmuch layer, not
a subprocess. DRY-RUN = first-class mode: resolve every target, write
nothing, report what-would-move. Flow: dry-run -> review -> live; first
real-mailbox runs always dry.

### R3. Async read/update with incremental thread views

All reads/updates async; UI never blocks. Query layer must support
thread-type queries (NM_QUERY_TYPE_THREADS): view holds thread objects;
refresh must INSERT new messages into existing visible threads, no full
rebuild. Diff-and-insert, not rebuild. notmutt must do better than
neomutt: async thread loading + diff-and-insert refresh.

### R4. Async send + dialogue state machine

`send_command` async. Dialogue = state machine, state SEPARATE from UI
rendering: fields, attachments, send progress, error, output. Dialogues
PAUSE and RESTART (state survives); composition TABBED (multiple
dialogue states alive concurrently). Send = background job, captured
output kept for review.

Background sync + filter runs must NEVER interrupt an active
composition; anything touching mail state while a dialogue is open must
go through the async layer - never block, never invalidate in place.

### R5. TUI-first, extractable

TUI. Core (dialogue state machines, event handling, rendering
primitives) must be structured so the whole TUI layer extracts into a
standalone library; client = reference consumer. No UI code in core; no
core logic in UI.

### R6. Mail parsing/composition from a library

Do NOT port neomutt's C parsing. Library must cover RFC 5322 + MIME
parse AND compose; port only if a library falls short. mailcap must be
supported for attachments/HTML. Chosen: emersion/go-message (record 4).

### R7. Language: Go

go-message for mail, tcell + lipgloss for TUI (record 23; lazygit's
pairing: renderer only, no widget layer), goroutines for async,
libnotmuch via cgo (aerc's in-tree binding pattern). Full language
analysis: record 1.

Idiomatic Go = design rule: stdlib first, gofmt-clean, clear over
clever. DRY = architectural: a concept exists once - account + preset
data drives both derived folder rules and the mover; canonical sort
comparator = one function, shared by the worker's batch emission and the
view's diff merge-walk; thread display order = flatten-time reversal of
the rows (record 32), never a reorder of stored sets. Duplicated
concepts = design errors.

Supply-chain policy (hard):
- Minimal deliberate deps; every dep must earn its place - large,
  established, audited projects over small convenience ones.
- Pin exact versions; audit + vet in CI; review dep diffs on upgrade;
  no auto-bump bots.
- Vendor the build. Reproducible builds.
- Never accept a dep with unclear provenance or authorship.
- AI code allowed, but ALL code owned by its human author: no commit
  carries an AI marker; every line reviewed like any contribution
  (tests proving the edge cases).

CI standard (mirror `references/neomutt-docs/docs/actions.md`): build + test on
every commit, sanitizers (ASAN/UBSAN), static analysis, fuzzing on the
mail-parsing boundary - the parser is the trust boundary and must be
fuzzed.

### R8. Config TOML; Lua bindings later

All config TOML; schema must allow Lua later without breaking it: TOML
= declarative config, Lua = scripting on top (hooks, filters, UI
callbacks). Follow the neovim model (event loop, RPC/msgpack, Lua as
extension language, UI via protocol).

Lua runtime = build-tag-gated (the R12 pattern): adapter + gopher-lua
only under the `lua` tag; default builds carry no Lua. Plugins = files
in `<configdir>/lua`; initial surface = one `body_render(lines)` per
plugin (record 20), run on the open job under the chain deadline via
SetContext - a busy-looping plugin falls back, never freezes the UI. VM
sandbox = lib whitelist (no os/io/debug).

Config model: the file shape IS the schema shape; files unmarshal 1:1
into typed structs. Neomutt ConfigSet properties as requirements, not
mechanism: typed values, validators, defaults, observers. Load strict -
unknown keys = load errors. Store = single write path: typed per-section
setters notify per-section observers on the event bus; async core never
reads config ad-hoc.

### R9. Keybindings: vim by default, emacs as an option, configurable

Keybindings = declarative data. Binding map per-context (global, index,
pager, compose, compose-editor, compose-review, terminal - aerc
binds.conf contexts). Default: vim/mutt-style. Emacs-style = config
choice, not a fork: `[ui] keymap = "vim" | "emacs"`, per-context
overrides on top. Every action must be bound in default scheme - works
with zero keybinding config. Keyhint/help derives from binding map;
rebinding updates hints.

### R10. PGP and S/MIME

Crypto = Provider interface, backend per algorithm by the real constraint
(secret handling, not tooling symmetry). Trust boundary = system tool for
anything touching a private key; S/MIME verification has no secret, so it
runs in-process and the client owns its trust policy.

PGP via `gpg` CLI (aerc gpgbin pattern: `--status-fd`, parsed status): the
agent/passphrase machinery is the reason for the subprocess - that backend
is for PGP only. S/MIME is internal-only: `go.mozilla.org/pkcs7` + stdlib
`crypto/x509`, in-process, roots from `[crypto] ca-file` when set (strict
pinning); an empty ca-file with `[crypto] use-system-pool = true` (default)
trusts the system CA pool - the mainstream out-of-the-box posture, the
emailProtection EKU gate still enforced. `use-system-pool = false` fails
closed: no system pool, no verification. No gpgsm backend - a gpg
subprocess would reintroduce the CLI/argv path and a second trust model
with no benefit on the verify path (no secret, no agent). The CMS parse and
the cert policy are the S/MIME trust boundary (R7 fuzz targets cover the
parse; the policy below is normative).

S/MIME verification policy (in-process, owned here, never openssl defaults):
- Trust roots: an empty `[crypto] ca-file` trusts the system CA pool - the
  mainstream default (Thunderbird/Outlook trust OS roots), acceptable because
  the emailProtection EKU gate and identity-match below bound it. Set
  ca-file to a PEM bundle to pin mail trust to specific roots for
  high-assurance mail - never rely on the bare system pool for a trust
  decision you care about (web PKI proves nothing about a sender). PGP
  web-of-trust and S/MIME CA hierarchy are not interchangeable.
- EKU `emailProtection` enforced on every verification (openssl's
  `-purpose smimesign` equivalent) - a TLS/CA cert must never pass as a mail
  signer.
- Revocation policy chosen explicitly and rendered honestly: CRL/OCSP
  (stdlib + golang.org/x/crypto/ocsp), fail-open vs fail-closed decided per
  account, and an unsigned "revocation not checked" state when skipped -
  never claim a check you did not run.
- Verification is two independent results: crypto-valid AND
  identity-match (signer cert email vs the From/Sender header). A valid
  signature from someone else's cert renders as a warning, not green.

- Sign/encrypt = transform stage between MIME assembly and the send job:
  assemble -> sign/encrypt per dialogue flags -> fcc -> send. Key
  resolution (locate/recv) async - can hit key servers. S/MIME sign/encrypt
  uses pkcs7 in-process; passphrase-protected keys route through the shared
  prompt path (below) like gpg.
- Decrypt/verify = async job on the read path; view model carries
  decrypted body + signature status; pager renders both.
- Passphrase: gpg-agent + external pinentry with TUI suspend/resume -
  the ONLY prompt path. No loopback mode (Go cannot zero secrets;
  smartcard PINs fail under loopback). Provider takes a PromptFunction -
  never prompts itself.
- Key selection = selector dialogue state (R4), fed by keyring queries
  (`gpg --list-secret-keys --with-colons`).

### R11. Truecolor theming engine

mutt's color surface, configured better. Objects that must exist
(from `references/muttrc/theme/onedark.muttrc` + `references/muttrc/base.colors`):
normal, indicator, status, tree, tilde, prompt, message, progress,
error, search; index + index_number/author/subject/date/flags;
hdrdefault, header (rotating list - names an open set), quoted0-5,
body (regex: URLs, emails, *bold* _underlined_ /italic/), signature,
attachment; compose_header + compose_security_encrypt/sign/both;
sidebar_new/flagged/ordinary/indicator; index_tag + index_tags.

Better configuration, all in TOML:
- Palette indirection: `[palette]` named colors + per-variant overrides;
  styles use names OR raw hex. Resolution: style hex > variant > base.
  Truecolor; no 256-color mapping.
- Styles inherit from `normal` (fg/bg/attrs); attrs unified per style.
  Theme states only differences.
- Index coloring TAG-driven: `[index.tag.<name>]` styles, composing with
  exclusive tag groups (R2) + base style; conflicts by group priority.
- Index row = fixed-slot template (`[index.row]` slots: number, flags,
  attachment, date, author, subject, count, tags). Optional slots ALWAYS
  reserve their column, render blank when absent - alignment never
  shifts per row.
- Tag slots: `[index.tags] max = N` fixed cells, display priority list
  (hard group first); glyph transforms = config data, never hardcoded.
- Column widths in terminal cells (wcwidth), not runes - emoji
  double-width, truncation/padding count cells.
- Regex rules: ordered, last match wins.
- Light/dark variants in one theme file; switching = config-store
  notification - live re-render, zero reload.
- onedark = reference port; base16 collection = import source.

### R12. Dark/light sync via DBus (optional build tag)

Build-tag-gated: `//go:build linux && dbus` - code + godbus/dbus ONLY in
the `dbus` build; default builds, macOS, Windows DBus-free.

- Reads scheme via xdg-desktop-portal (`Settings.Read(...,
  "color-scheme")` + `SettingChanged`); GSettings/GTK fallback. Session
  bus only; value = strict enum (0/1/2), fallback to default (SECURITY.md
  F11).
- Change arrives as ColorSchemeChanged(dark|light) on the event bus;
  theme store resolves variant; observers re-render. Same async path as
  everything else.
- No portal/DBus: `:theme` switches manually.

### R13. Derived caches (bbolt)

Two derived stores behind one `Cache` interface (Get/Put/Delete,
pluggable backends, pure-Go bbolt default; interface first, same
boundary discipline as the filter engine).
Both 0600 (SECURITY.md F5); cached strings (subjects, attachment
filenames - attacker-influenced) always pass the same
sanitize/render/mailcap paths as fresh data - never trusted by virtue of
being cached.

Index cache (read surface; R1): mirrors the overview query output
(thread id, timestamp, author, subject, tags, per-thread counts), keyed
by thread id, ingested in batch (one bbolt transaction per emitted
chunk). Revision-keyed; startup syncs the lastmod delta (O(changed));
full walk only on cache miss or revision mismatch. Mirrors tag state,
never mutates it.

MIME cache (per-message content metadata): attachment presence, list,
structure, sizes - the only per-message data notmuch cannot serve (needs
a file open and parse). Keyed by (path, size, mtime): renames and edits
invalidate naturally; steady state hit-only.

Client-local state = tags, not flags: reply/forward markers =
+replied/+forwarded tags (R1). Nothing else needs storing.

### R14. Staged tag operations (apply and undo)

UI tag ops never write at keypress time; stage into a per-session
buffer, notmuch sees them only on apply. Buffer = undo mechanism -
immediate writes final, group conflicts land as DB state instead of a
reversible stage.

- Tag action on the cursor message STAGES a pending op: view re-renders
  the staged state immediately (with R2 group resolution, so staging
  archive drops inbox from the render); notmuch untouched. unread =
  soft tag - never a group member, survives folder moves (a move must
  not clear read state), leaves the render only via its own staged op.
- Staged state visually distinct and themable: `[index.staged]` style +
  staged glyph in the flags slot (config data, default `*`). Undo clears
  style and glyph together with the ops.
- APPLY (default `$`, mutt sync semantics) flushes the buffer: one
  ActTag batch per message with the fully resolved op set (pending ops +
  group removals), on the worker's lock-budgeted action path. Failed ops
  stay staged - retry or undo. Folder-tag op must resolve its physical
  move BEFORE the tag lands: account folder space must cover the paths,
  winner tag must have move candidates, account must not be readonly;
  failure refuses the apply with an error naming the config fix.
  Mover-level skips logged on diag, never silent.
- UNDO (`u`) discards the cursor message's staged ops - pure buffer
  drop, free of DB traffic.
- Refresh never clobbers the buffer: merges apply snapshot truth first,
  then replay pending ops (reconcile-then-replay). Staged ops survive
  view switches and refreshes; session-local, lost on exit (persistence
  = future work). Filter-engine writes (R2) bypass the buffer; where a
  filter retags a staged message, the user's pending ops win the render,
  and apply recomputes against current state.
- Buffer entries keyed by message identity, never position.

### R15. Async progress display

Background work reports progress on the bus; TUI renders a bar in the
bottom right corner, row above the status line, while the user keeps
navigating or composing.

- Jobs emit Progress events (kind, done/total, label) at batch
  boundaries: refresher fetches (page budget), cache scan (visible
  rows), filter job (message batches), send/crypto jobs (R4). Worker
  action loop not a progress source - jobs report own totals.
- Widget subscribes to the bus, repaints on Progress like the index on
  ViewDiff (same async channel, R3). Bar (done/total cells) + kind-
  derived label, right-aligned in a fixed-width region above the status
  line (the R11 slot-reservation rule). Completion event clears the bar.
  Never takes focus, never blocks; labels job-kind derived, never mail
  content (F6).
- Theming: `progress` style (R11 machinery) + filled-cell glyph as config
  data (default block, the tag-transforms rule).

## Reference code in this workspace (advisory)

Reference repos prove mail concept, config intent, failure modes - not
implementation. Decisions made for notmutt (Go, TOML, async), never
inherited from C tools. Cited reference behavior must say why it serves
notmutt, never cite authority.

- `references/neomutt/` - R4 job model (background/), async send +
  failed-send retry (send, branch async_send), dialogue state split
  (compose/shared_data), thread backend gaps (notmuch/), Lua (lua/).
- `neovim/` - event loop, RPC, Lua API, UI protocol (R8).
- `references/aerc/` - MAIL HANDLING only: worker action loop, CLI
  integration, go-message (R3/R4); binds.conf (R9).
- `references/lazygit/` - Go TUI architecture (R5/R9): view models,
  keybinding controllers, config-driven binding map, background tasks.
  Not the renderer: tcell + lipgloss (record 23).
- `references/afew/MailMover.py` - per-account folder priorities (R2).
- `references/muttrc/notmuch/tags` + `post-new` + `afew/config` - live
  classification pipeline (R2 reference).
- `references/matcha/` - gopher-lua plugin system, R8 Lua ONLY (record
  20): orchestrator VM, Protect-then-log, lib whitelist, deferred side
  effects. Its gaps = notmutt's constraints, not defaults.

## Agent working rules (Claude Code and Pi both)

- Scope (hard rule): only write code under `src/`. `references/` is
  read-only source material - never edit, move, or add to it; consult
  it as needed, copy patterns out as needed, leave it untouched.
  Anything outside `src/` is doc/config unless the task names it.
- Privacy (hard rule, overrides everything): NEVER submit mail content
  (bodies, headers, whole .eml/.mbox files) to the LLM. To read a
  subject or field from inside mail, extract it with a script first
  (pattern: `references/muttrc/bin/dedupe-mail`), pass only the extracted value.
  Include a checksum (sha256, or faster md5/xxhash) when correlating or
  verifying message identity. Config files are not mail content and may
  be read freely; mail files are not.
- Commits: Conventional Commits (`type(scope): subject`), brief lowercase
  imperative. ALL code owned by the human author - code commits carry no
  AI marker and no co-author line, whether or not AI drafted them (an AI
  marker is like mail typed on an iPhone - the device produced the words,
  the owner answers for them). Doc/spec commits carry
  `Co-Authored-By: Deepseek` (the model that drafted them); review
  responsibility stays with the human either way.
- Testing: treat AI output like firmware - assume it fails in production
  until exercised. Non-trivial logic leaves ONE runnable check. No test
  frameworks beyond what the project already uses. Test data generated,
  never personal: fabricated names (alpha, atlas, acme,
  sender@example.com).
- Regression tests fail first (TDD): a regression fix starts by writing
  the failing test against the current code, running it to confirm it
  FAILS, then fixing. Never commit a regression fix without that red
  run - a test that passed before the fix proves nothing.
- Style: clear, concise, direct; ASCII only (no unicode dashes/quotes)
  in all output and code. No unnecessary comments; only non-obvious
  constraints get a comment.

Security (SECURITY.md normative for trust boundaries; hard rules):
- argv exec only. Never interpolate mail content, filenames, or queries
  into shell strings - tokenize commands at config load, pass data as
  argv (F4).
- Sanitize rendered mail content: strip ESC/C0 control chars and OSC
  before it reaches the terminal (F1).
- Never log message bodies, headers, or passphrases (F6).
- 0600 on files, 0700 on dirs, for everything written (F5, F7).
- Parser-adjacent code passes the fuzz targets in SECURITY.md before
  acceptance (F1-F4, F10).

### MCP data boundary (locked)

The [mcp] server is an LLM-agent boundary: every tool result is metadata
an agent may act on. The scope config is the boundary, and it is
deny-by-default:

- `[mcp] accounts` - the account folder spaces the server may see. Each
  entry grants its folder prefix AND its account tag
  (`folder:/^<name>\// AND tag:<name>`, subfolders included) - the
  physical location and the logical identity, both required. A read-only
  account can never match (its mail carries no account tag) and is a
  load error, not a silent empty grant.
- `[mcp] tags` - the soft tags whose mail is reachable; a message must
  carry at least one allowed tag. The account tag is part of the account
  grant, not this list.
- Empty `accounts` or `tags` serves nothing. Enforcement is per-tool:
  search/count intersect the query with the scope, thread_info projects
  only in-scope messages, attachments refuses out-of-scope ids before
  any file open.

The correctness test for this boundary is `TestMCPScopeEnforcement`
(src/app/mcp_test.go). It is LOCKED: it must never be loosened, weakened,
or removed without explicit user approval stated in the conversation - a
change to the boundary must start with that approval, not end with a
test edit.

## Non-goals

- No IMAP/POP3 implementation (external sync tools: mbsync/vdirsyncer).
- No own mail storage format.
- No GUI (TUI only; extractable TUI library may gain GUI backends later).
