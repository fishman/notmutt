---
layout: default
title: FAQ
---

# FAQ

## Why another mail client? Why not neomutt / mutt / aerc / notmuch.el?

**neomutt** is the ancestor and the honest comparison. It is mature,
fast and battle-tested - but its notmuch integration has structural
costs that motivated this project: thread queries load synchronously,
new mail triggers a full re-query that rebuilds the thread tree, and
every tag application is immediate and final. If you mis-tag, that
state is in your database, and nothing in the client undoes it.
notmutt's differentiators are exactly those three: async thread
loading, diff-and-insert refresh, and staged undoable tag operations.

**aerc** is the closest relative - the production Go notmuch client,
whose worker model notmutt's async core mirrors. The differences are
deliberate: notmutt is notmuch-first (aerc's maildir/imap workers are
a different surface), the UI is a single pager+index surface instead
of the compose/message split, and the tag model - exclusive folder
groups, staged ops - is notmutt's reason to exist.

**mutt (non-notmuch)** indexes nothing; notmuch query power is the
baseline here.

All of the above are more finished than notmutt today. The honest
status is below.

## What is the current status?

M1 (mailbox view: thread tree, index cache, pager, search, default
plain/html views) and M2 (staged tag ops, send dialogue with attach
commands and preview, async send) are done. A 33k-thread inbox walks
in ~1.6s with sub-150us per keypress afterwards. The classification
pipeline (folder rules, header rules, per-account moves) runs
in-process; the theme, binding and config systems are data-driven.

## What is not there yet?

- **Crypto send**: the compose dialogue has a Security field (none /
  sign / encrypt / sign+encrypt) that cycles and displays, but the
  send path does not wire a gpg/openssl transform into assembly yet.
  Crypto runs through your system `gpg` when it lands - no vendored
  crypto library, ever.
- **Filter engine**: header rules run declaratively; algorithmic
  filters (bayes spam, DKIM validation) are a registered-interface
  plan, not implemented.
- **Lua scripting**: a build-tag-gated layer exists with one plugin
  surface (body rendering transforms). Hooks and UI callbacks are the
  roadmap, not the present.
- **Emacs keymap**: the scheme exists as config data; the vim scheme
  is the reference and the tested one.
- **GUI**: explicitly out of scope. The TUI layer is structured for
  extraction as a library, but the client is a terminal client.
- **IMAP/POP3 transport**: notmutt does not sync mail. mbsync /
  vdirsyncer deliver into your maildirs and notmuch indexes them;
  the client reads what notmuch sees.

## Does it work with my existing notmuch setup?

Yes - that is the point. If `notmuch` sees your mail, notmutt reads it.
Your tags, your views, your queries stay yours and stay queryable by
every other notmuch tool; the client's cache is derived from notmuch,
never authoritative.

## Does it touch my mail?

The filter pipeline and the mover operate through the client's own
notmuch layer. Read-only accounts (set per account in the config) are
never classified: no folder tags, no account tags, no moves - the
client writes nothing to their mail. Physical moves follow the
per-account folder priorities (first existing folder wins, globs
allowed) and are copy-then-delete: sources are deleted only after all
copies succeeded.

## Privacy: what leaves my machine?

Nothing from your mail, ever, in any direction. No telemetry, no
phone-home, no spellcheck services, no external rendering. Remote
images in mail do not fetch unless you press alt+i, and 1x1 tracking
pixels are dropped even then (config opt-in lifts the block). The
only outbound traffic is what you explicitly trigger: fetching a
remote image, opening a link, sending a message, gpg key lookups.

## Why does it need libnotmuch? Can I build without it?

The default build links the cgo binding for performance (a full
33k-thread walk in ~1.6s). If you cannot link it, `-tags cli` builds
against the `notmuch` CLI - same code, same interface, one build tag
away. The CLI backend exists precisely as the escape hatch.

## Why Go?

The language decision record (AGENTS.md, R7) compared Rust, Go and
Zig on bindings, mail libraries, TUI maturity, async model and
supply-chain surface. Go won on integration surface: go-message is
aerc's production mail library (the same worker architecture notmutt
mirrors), goroutines make the async model native, and the cgo
binding is vendored and pinned. The Rust column's strengths (ratatui,
mlua, tokio) are real; they lost on integration surface.

## Does it support my terminal?

Truecolor is the baseline; `COLORTERM=truecolor` resolves natively
with no terminfo database. Images need a sixel-capable terminal
(foot, mlterm, xterm -ti, ...) - tmux does not pass image protocols
through, so images paint outside tmux. Kitty graphics is supported
as an opt-in protocol for kitty-family terminals.

## How do I report a bug?

Open an issue on the repository. Include the client's session log
(`~` opens it in-app) and your config if relevant. Do not paste mail
content into issues - reproduce with fabricated mail if the bug is
message-specific.
