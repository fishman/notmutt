---
layout: default
title: notmutt
nav_order: 1
---

# A mail client that never makes you wait

notmutt is an async, command-line-first mail client built on notmuch.
Every query, filter and tag trigger runs as a background job; the UI
never blocks on a sync, a search or a send. You keep reading and
composing while the mailbox updates around you.

Written in Go. tcell TUI, go-message for mail, TOML config, vim
keybindings out of the box.

<img width="2285" height="1309" alt="notmutt screenshot" src="https://github.com/user-attachments/assets/1d0626f7-1e78-4100-8d4c-dea5108a51a2" />

## Why switch

**Nothing blocks you.** neomutt's thread queries load synchronously and
new mail rebuilds the thread tree. notmutt loads threads async and
diff-and-inserts new mail into the threads you are already looking at.
A held background index, a filter run, a send - none of it pauses your
keystrokes.

**Tag operations you can undo.** Archive, delete, flag, read - every
action stages into a buffer and reaches notmuch only when you apply
it. A mis-tap is one `u` away. In neomutt, every tag application is
final and a wrong tag lands in your database as permanent state.

**One message, one home.** Folder tags form a declarative exclusive
group. Applying any member removes the others - inbox, archive,
deleted, sent, draft, pending, spam - with no hand-maintained `-tag`
chains across your config rules.

**Privacy is the default posture.** Remote images stay collapsed until
you press alt+i. Even then, 1x1 tracking pixels are dropped unless
you opt in. No telemetry, no account sync, no mail content leaves
your machine; encryption runs through your system `gpg`, never a
vendored crypto library.

**notmuch is the single source of truth.** The client owns no
database. The index is a revision-keyed cache that re-syncs from
notmuch's lastmod - startup touches only what changed. Folder state
is derived, never authoritative. Your tags stay queryable by any
notmuch tool.

**Images in your terminal.** Sixel by default (most terminals support
it), kitty opt-in. The pager decodes and paints inline images - on
demand, only when you ask.

**Configuration as data, not code.** TOML throughout: truecolor themes
with palette indirection, declarative per-context keybindings (the
help overlay derives from the binding map, so rebinds update the
hints), tag styles, glyphs. A search of `src/config/base.toml` shows
every default the client ships with.

## Quick start

Requirements: a recent Go toolchain and libnotmuch (the default build
links the cgo binding; `-tags cli` builds against the `notmuch` CLI,
same code, one build tag away).

```sh
git clone git@github.com:fishman/notmutt.git
cd notmutt
make
./notmutt
```

Your config lives at `~/.config/notmutt/config.toml` and overlays the
built-in defaults. Continue with [usage](usage.html) and
[features](features.html).

## Status

M1 (mailbox view) and M2 (staged tag ops, send dialogue) are done,
including the render-coalescing round: sub-150us per keypress on a
33k-thread inbox. The [FAQ](faq.html) is honest about what is not
there yet.
