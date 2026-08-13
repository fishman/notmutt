# notmutt - requirements and architecture

Workspace: a mail client under construction. This workspace holds the source
material: `neomutt/` (fork carrying async patches), `muttrc/` (live config,
the reference mail setup), `afew/` (fork with the per-account MailMover),
`notmuch/` (notmuch source), `neovim/` (neovim checkout, reference for UI
async + Lua integration).

The goal: an async, command-line-first mail client. All mail handled by
notmuch. Read these rules as architecture constraints, not suggestions.

## The mail concept (derived from muttrc + afew + notmuch configs)

Tags are the logical model. Folders are physical storage. Every view,
filter, and trigger is a notmuch query or tag operation; folders exist only
for sync-tool compatibility (mbsync/vdirsyncer) and for physical moves.

Current classification pipeline (the reference flow):

1. transport: mbsync/vdirsyncer deliver to per-account maildirs
2. index: `notmuch new` (new mail gets `new.tags`: unread, inbox)
3. folder rules: `notmuch tag --input` - mail in folder X gets the
   corresponding hard tag (archive, sent, spam, draft, deleted, pending)
   plus per-account tags (gmail, jelveh, toptal, dynamia via
   `folder:/^gmail\//` regex)
4. header rules (post-new hook): content-based soft tags (work, xolo,
   meeting, cfp, conference, exhibition, receipt, newsletter)
5. physical move: afew `--move-mails` with per-account `folder_priorities`
6. side effects: untag-reversal on delivery, address cache for query
   completion, notification

### Hard-tag exclusivity (KNOWN PAIN - must be fixed)

Folder tags are mutually exclusive: one message has exactly one home
(archive > deleted > sent > draft > pending > spam). The current config
enforces this by hand: every new folder tag requires adding `-newtag` to
every older rule and a cross-untag rule. This is unacceptable.

The new client's filter engine MUST support declarative exclusive tag
groups: `[tag-groups] hard = ["archive", "deleted", "sent", "draft",
"pending", "spam"]`. Applying any tag in a group removes the others
automatically. Adding a tag to a group must not require touching existing
rules.

### Idempotency

Every filter rule must carry a NOT guard so re-runs touch only new mail
(first backfill on ~129k messages takes minutes; steady state must be
cheap). Applied at every stage of the pipeline.

### Per-account folder priorities

Physical move destinations resolve per account by existence: candidates are
tried in order, first existing folder wins, `*` candidates are globs
(afew `folder_priorities`; `muttrc/afew/config`). The new client's move
engine must keep this model.

### Lock handling

notmuch lock waits must be capped (config has `lock_timeout=10`) so UI tag
operations error out instead of hanging behind a background index/send
holding the lock.

## Requirements

### R1. notmuch is the single source of truth

No own database. Mailbox state, flags, threads, search all come from
libnotmuch. The client is a notmuch front-end. Virtual views are tag
queries; folder state is derived, never authoritative.

### R2. Filters and triggers through notmuch + afew

Rules live in notmuch hooks (post-new) and afew. afew may later be replaced
by an integrated filter engine, so the filter interface must be a boundary:
a module that consumes notmuch documents + a rule set and produces tag
changes. Same contract for both implementations.

### R3. Async read/update with incremental thread views

All notmuch reads and updates happen asynchronously; the UI never blocks on
a query. The query layer must support thread-type queries
(`nm_query_type` = NM_QUERY_TYPE_THREADS; this is the "nm_message_type
threads" concept): the view holds thread objects, and a refresh must be
able to INSERT new messages into existing visible threads between entries
without a full rebuild. No full-refresh-on-new-mail. Diff-and-insert, not
rebuild.

Neomutt's current state (the gap to close): thread queries load
synchronously (`notmuch/notmuch.c:1074-1119`), and the mail check re-runs
the whole query with a message-level search even for thread mailboxes,
marks everything inactive, merges, then fires NT_MAILBOX_INVALID which
rebuilds the thread tree (`notmuch/notmuch.c:2183-2308`). notmutt must do
better on both: async thread loading and diff-and-insert refresh.

### R4. Async send + dialogue state machine

`send_command` is asynchronous. The dialogue (compose dialog) is a state
machine whose state is SEPARATE from the UI rendering: fields, attachments,
send progress, error, output. Dialogues can be PAUSED and RESTARTED (state
survives the pause). Composition is TABBED: multiple dialogue state
instances alive concurrently. The send runs as a background job with
captured output kept for review after completion.

Rationale beyond rendering: background sync + filter runs (notmuch new,
tag pipelines, afew moves) must NEVER interrupt an active composition.
Because the dialogue state lives outside the UI, a filter run can retag
and re-render the mailbox while the user keeps typing in the compose tab;
the dialogue only observes mailbox state through the same async channel.
Anything that touches mail state while a dialogue is open must go through
the async layer - never block, never invalidate in place.

### R5. TUI-first, extractable

The UI is a TUI. Architecture rule: the core (dialogue state machines,
event handling, rendering primitives) must be structured so the whole TUI
layer can be extracted into a standalone library and published. The client
is the reference consumer. No UI code in the core; no core logic in the UI.

### R6. Mail parsing/composition from a library

Do NOT port neomutt's C mail parsing. Search first for a mail library and
use it; only fall back to porting neomutt code if a library falls short.
Candidate parsers in the selected language must cover RFC 5322 + MIME
parse AND compose (header + MIME construction). mailcap must be supported
for attachments/HTML viewing.

### R7. Language: Go

Analysis (see decision record below). Go with go-message (emersion) for
mail, BubbleTea for TUI, goroutines for the async model, libnotmuch via
cgo (aerc's in-tree binding pattern). Supply-chain policy is mandatory
(below).

### R8. Config TOML; Lua bindings later

All configuration is TOML. The TOML schema must be designed so Lua bindings
can be added later without breaking it: TOML is declarative config, Lua is
scripting on top (hooks, custom filters, UI callbacks). Follow the neovim
model (neovim/ checkout in this workspace): libuv event loop, RPC
(msgpack), Lua as extension language, UI attached via protocol. UI async
design should be compatible with that model.

Config model: mirror neomutt's ConfigSet design
(`neomutt-docs/docs/config.md`): every key is a typed value (string,
number, bool, enum, path, list, regex, sort) with a validator, a default,
and an observer/notification system. The TOML layer is a typed, validated,
observable store - config changes notify the async core, never read
ad-hoc from disk.

### R9. Keybindings: vim by default, emacs as an option, configurable

Keybindings are declarative data, not code. The binding map is per-context
(global, index, pager, compose, compose-editor, compose-review, terminal -
mirror aerc's binds.conf contexts). Default scheme is vim/mutt-style (j/k
navigation, q quit, etc.). An alternative scheme (emacs-style) is a config
choice, not a fork: `[ui] keymap = "vim" | "emacs"`, with per-context
overrides on top. Every action must be bound in the default scheme - the
client works out of the box with zero keybinding config (aerc
`config/binds.conf` is the reference: `<key> = :command`, contexts as
sections). The keyhint/help UI derives from the binding map, so
rebinding updates the hints automatically.

### R10. PGP and S/MIME via system tools, not libraries

Crypto is a Provider interface with CLI backends - zero crypto code in the
client. PGP via the system `gpg` CLI (aerc `lib/crypto/gpg/gpgbin`
pattern: `--status-fd`, parsed status); S/MIME via `openssl smime` or
gpg's CMS mode. Trust boundary is the system tool, never a vendored
crypto dependency.

- Sign/encrypt is a transform stage between MIME assembly and the send
  job: assemble (go-message) -> sign/encrypt per dialogue flags -> fcc ->
  send job. Key resolution (locate/recv keys) is async - it can hit key
  servers.
- Decrypt/verify is an async job on the read path; the view model carries
  decrypted body + signature status (valid/invalid, signer, key id);
  the pager renders body and status.
- Passphrase: gpg-agent + external pinentry with TUI suspend/resume
  (aerc `lib/pinentry`) - the ONLY prompt path for the gpg backend.
  No loopback mode for gpg: passphrase in client memory (Go cannot
  zero secrets), prompt path becomes client security surface, smartcard
  PINs fail under loopback. Native PromptFunction prompting only for
  the in-process provider. The Provider takes a PromptFunction - the
  crypto layer never prompts itself.
- Key selection is selector dialogue state (R4 machinery), fed by keyring
  queries (`gpg --list-secret-keys --with-colons` etc.).
- neomutt references: `ncrypt/cryptglue.c` (backend registration per
  crypto family), `ncrypt/gnupgparse.c` (keyring parsing), `smime/smime.c`
  (S/MIME via openssl), `ncrypt/dlg_gpgme.c` (passphrase dialog concept).

Keybindings are declarative data, not code. The binding map is per-context
(global, index, pager, compose, compose-editor, compose-review, terminal -
mirror aerc's binds.conf contexts). Default scheme is vim/mutt-style (j/k
navigation, q quit, etc.). An alternative scheme (emacs-style) is a config
choice, not a fork: `[ui] keymap = "vim" | "emacs"`, with per-context
overrides on top. Every action must be bound in the default scheme - the
client works out of the box with zero keybinding config (aerc
`config/binds.conf` is the reference: `<key> = :command`, contexts as
sections). The keyhint/help UI derives from the binding map, so
rebinding updates the hints automatically.

## Language decision record (R7)

Question: Rust, Zig, or Go?

| dimension | Rust | Go | Zig |
|---|---|---|---|
| notmuch bindings | `notmuch` crate (0.8.0, wraps C API, stale ~3y but works); FFI direct | official bindings in notmuch contrib/ | none; hand-written FFI |
| mail parse+compose | mail-parser + mail-builder (Stalwart, RFC 5322/MIME, zero-copy, fuzzed, production mail server, actively maintained 2026) | go-message (emersion, used by aerc) | none |
| TUI | ratatui (mature, widget lib - UI extracted by design) | bubbletea/tview (fine, but app-state coupled) | none mature |
| async | tokio (mature) | goroutines (built-in, simple) | none; std.event.Loop is barebones, userland |
| Lua | mlua (mature, used by neovim) | gopher-lua (stale-ish) | none |
| supply chain (user concern: AI-generated code) | crates.io has the worst AI-generated-crate flood; mitigated by tiny dep set + vetting + cargo-audit + vendoring | module proxy + checksum db, typosquatting still exists | smallest surface (no registry sprawl, vendoring natural) |

Decision: Rust. Decisive factors: mail-parser/mail-builder and ratatui are
the best-in-class for the two hardest parts (MIME + extractable TUI); mlua
is the same Lua binding neovim uses (aligns with R8); tokio is the only
mature async story of the three. Zig would be greenfield on every single
dimension. Go is viable (aerc proves the model) but weaker on the
extractable-TUI requirement.

Supply-chain policy (the user's stated concern is real; treat it as a hard
constraint):
- Keep the dependency set minimal and deliberate. Every dependency must
  earn its place; prefer large, established, audited projects over small
  convenience crates.
- Pin exact versions; use cargo-audit and vet (cargo-vet or supply-chain
  review) in CI.
- Vendor the build. Reproducible builds.
- Review dependency diffs on upgrade; no auto-bump bots.
- Never accept a dependency whose provenance or authorship is unclear.
- AI-generated code is allowed in this repo only with an `AI-assisted`
  commit trailer, and must be reviewed like any other contribution
  (tests proving the edge cases).

CI standard (mirror `neomutt-docs/docs/actions.md`): build + test on every
commit, sanitizers (ASAN/UBSAN), fuzzing on the mail-parsing boundary,
static analysis in CI. The mail parser is the trust boundary - it must be
fuzzed like the firmware it is.

### R11. Truecolor theming engine

Theming covers mutt's color surface but is configured better. Mutt
objects that must exist (from `muttrc/theme/onedark.muttrc` +
`muttrc/base.colors`): normal, indicator, status, tree, tilde, prompt,
message, progress, error, search; index + index_number/author/subject/
date/flags; hdrdefault, header (per-header regex), quoted0-5, body
(regex rules: URLs, email addresses, *bold* _underlined_ /italic/),
signature, attachment; compose_header + compose_security_encrypt/sign/
both; sidebar_new/flagged/ordinary/indicator; index_tag + index_tags.

Better configuration, all in TOML:

- Palette indirection with escape hatches: `[palette]` holds named
  colors; per-variant `[palette.dark]`/`[palette.light]` override
  single names when variants genuinely diverge, without touching
  styles. Styles reference palette names OR raw hex - a one-off color
  pins hex directly and never pollutes the palette. Resolution order:
  style hex > variant palette > base palette. Truecolor is the
  baseline; no 256-color mapping.
- No repetition: styles inherit (a base `normal` style; fg/bg/attrs
  default to normal unless overridden). A theme states only what
  differs.
- Attrs unified per style (bold/italic/underline/reverse) - not mutt's
  separate attribute objects.
- Index coloring is TAG-driven, not mutt's `~X` patterns: `~l <tag>`
  and `index_tag` become declarative `[index.tag.<name>]` styles
  (muttrc/base.colors already colors by notmuch tags; notmutt makes it
  data). Tag styles compose with the exclusive tag groups (R2/R6) and
  with the base index style; conflicts resolve by the group priority.
- Index row layout is a fixed-slot template, not mutt's format string.
  `[index.row]` lists slots in order (number, flags, attachment, date,
  author, subject, count, tags - the muttrc `index_format` is the
  reference for the slot list, never the mechanism). Optional slots
  (attachment, tag slots) ALWAYS reserve their column and render blank
  when the content is absent - alignment never shifts per row.
- Tag slots: `[index.tags] max = N` fixed cells, filled by a display
  priority list (hard tag group first) - at most N glyphs show,
  whichever tags are present, blank-padded otherwise. Glyph transforms
  (`tag-transforms` in muttrc) are config data; the raw emoji/strings
  never hardcode in code.
- Column widths are in terminal cells (wcwidth), not runes: emoji
  glyphs are double-width, so truncation and padding count cells or
  alignment breaks exactly like mutt's conditional format.
- Regex rules keep mutt's semantics: ordered, last match wins, more
  specific overrides less.
- Light/dark variants in one theme file: `[theme.dark]` / `[theme.light]`,
  `default` selects. Switching is a config-store notification - the
  same observer path as any config change, so the UI re-renders live
  with zero reload.
- The onedark theme in `muttrc/theme/onedark.muttrc` is the reference
  port; the base16 palette collection in `muttrc/themes/palette/` is
  the import source (a converter is a future task, not M1).

### R12. Dark/light sync via DBus (optional build tag)

A build-tag-gated DBus integration that switches the theme variant with
the system. `//go:build linux && dbus`: the code and the godbus/dbus
dependency exist ONLY in the `dbus` build; default builds, macOS, and
Windows are DBus-free (darwin/windows excluded by build constraints).

- Reads the system color scheme via xdg-desktop-portal:
  `org.freedesktop.portal.Settings` - `Read("org.freedesktop.appearance",
  "color-scheme")` plus the `SettingChanged` signal for live updates.
  GSettings/GTK-theme-name as fallback if the portal is absent.
- The scheme change arrives as an event on the event bus
  (ColorSchemeChanged(dark|light)); the theme store resolves the
  variant; observers re-render. Same async path as everything else.
- No portal/DBus available: `:theme` command switches manually.
- Supply chain: godbus/dbus is a dependency only in the dbus build;
  it is pinned and vetted like everything else (R7 policy).

## Reference code in this workspace

- `neomutt/background/` - the background job model that R4's send-job
  design comes from. Concrete state: fixed job table `Jobs[MAX_JOBS]`
  (`background/background.c:55`, `private.h:30-44`), reaping via
  non-blocking `waitpid` + `WNOHANG` in an event-loop timeout observer
  (`background/background.c:281-317`, `516-519`), output kept for review
  in a 10-job ring (`background/background.c:53-60`, `f2f246718`),
  non-blocking wait by macro parking (`background/background.c:386-396`,
  `bda3d6ff0`), dedicated dialog that renders live state
  (`background/dlg_background.c`, `126b53b26`, `ccafd3068`). Known gaps
  to fix: no job-state enum, completion is polled not evented, no
  per-job observers.
- `neomutt/send` (branch `async_send`, commit aa4478969) - async send:
  `$sendmail_async` skips the parent's waitpid and hands the pid back
  (`send/sendmail.c:246-255`); `bg_send_register` tracks the job;
  deferred Fcc on reap; failed sends RETAIN the Email and `bg_send_retry`
  re-opens the compose dialog with the failed message - the pause/restart
  seed for R4.
- `neomutt/compose/shared_data.{c,h}` - ComposeSharedData splits dialogue
  state (email, attachments, fcc, return code) from the dialog window.
  Source of R4's state/UI split.
- `neomutt/notmuch/` - notmuch backend: `$nm_query_type` (MESSAGES vs
  THREADS, `notmuch/query.h:36-38`), progressive time-sliced filling for
  message mode (`notmuch/notmuch.c:983-1043`, `2137-2174` - budgeted
  iterator slices, not threads), synchronous thread loading
  (`notmuch/notmuch.c:1074-1119`), full re-query refresh
  (`notmuch/notmuch.c:2183-2308`). Reference for R3; the gaps are R3's
  reason to exist.
- `neomutt/lua/` - module registering Lua commands (config-level Lua).
  Reference for R8's Lua layer.
- `neovim/` - full neovim checkout. Reference for R8: event loop, RPC,
  Lua API, UI protocol, fast events.
- `aerc/` - the production Go notmuch client. Design reference for MAIL
  HANDLING ONLY (not the UI): the notmuch worker action loop
  (`worker/notmuch/worker.go` - `Run()` + `handleMessage` dispatch,
  worker factory registration with build tags in `worker/handlers`),
  cgo-free notmuch integration via the CLI (`worker/notmuch/lib`), and
  go-message for parsing. The worker model is the R3/R4 async reference.
  Its per-context keybinding config (`config/binds.conf`) is the
  keybinding-config model for R9.
- `afew/MailMover.py` - per-account folder priority resolution.
- `muttrc/notmuch/tags` + `muttrc/notmuch/post-new` + `muttrc/afew/config`
  - the live classification pipeline (R2 reference).

## Agent working rules (Claude Code and Pi both)

- Privacy: NEVER submit mail content (bodies, headers, whole .eml/.mbox
  files) to the LLM. To read a subject or field from inside mail, extract
  it with a script first and pass only the extracted value. Include a
  checksum (sha256 or faster) when correlating or verifying message
  identity.
- Commits: if AI generated any part of a change, add an
  `AI-assisted: <tool name>` trailer to the commit message.
- Testing: treat AI-generated output like firmware - assume it fails in
  production until exercised. Non-trivial logic leaves ONE runnable check
  (assert-based self-test or a single small test). No test frameworks
  beyond what the project already uses.
- Code style: no unnecessary comments; self-documenting names; ASCII only
  (no unicode dashes/quotes) in all output and code.
- Context: read files with limit/offset where possible; prefer Edit over
  Write; match existing patterns verbatim.

Security (SECURITY.md is normative for trust boundaries; the hard rules):
- argv exec only. Never interpolate mail content, filenames, or queries
  into shell strings - tokenize commands at config load, pass data as
  argv (F4).
- Sanitize rendered mail content: strip ESC/C0 control chars and OSC
  before it reaches the terminal (F1).
- Never log message bodies, headers, or passphrases (F6).
- 0600 on files, 0700 on dirs, for everything written (F5, F7).
- Parser-adjacent code passes the fuzz targets in SECURITY.md before it
  is accepted (F1-F4, F10).

## Non-goals

- No IMAP/POP3 client implementation (transport stays mbsync/vdirsyncer or
  external sync tools).
- No own mail storage format.
- No GUI (TUI only; the extractable TUI library may gain GUI backends
  later, but that is out of scope).
