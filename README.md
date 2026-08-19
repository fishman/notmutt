# notmutt

[![Build and test](https://github.com/fishman/notmutt/actions/workflows/build-and-test.yml/badge.svg)](https://github.com/fishman/notmutt/actions/workflows/build-and-test.yml)
[![CodeQL](https://github.com/fishman/notmutt/actions/workflows/codeql.yml/badge.svg)](https://github.com/fishman/notmutt/actions/workflows/codeql.yml)
[![Govulncheck](https://github.com/fishman/notmutt/actions/workflows/govulncheck.yml/badge.svg)](https://github.com/fishman/notmutt/actions/workflows/govulncheck.yml)

An async, command-line-first mail client built on notmuch. Tags are the
logical model: every view, filter, and trigger is a notmuch query or tag
operation; folders exist only for sync-tool compatibility. Written in Go
- tcell v3 TUI (lipgloss v2 for layout math), go-message for mail
parsing and composition, TOML config, vim keybindings by default.

<img width="2285" height="1309" alt="1787024932790608185" src="https://github.com/user-attachments/assets/1d0626f7-1e78-4100-8d4c-dea5108a51a2" />

## Try it now

Requirements: a recent Go toolchain, libnotmuch, and a notmuch-indexed
mailbox (mbsync or vdirsyncer into maildirs plus `notmuch new`).

```sh
git clone git@github.com:fishman/notmutt.git
cd notmutt/src
go build -o ../notmutt .
cd ..
./notmutt
```

If notmuch sees your mail, notmutt reads it. Your tags, views, and
queries stay yours and stay queryable by every other notmuch tool. The
built-in defaults live in `src/config/base.toml` (search it first);
`~/.config/notmutt/config.toml` overlays them. The setup walkthrough is
in [docs/installation.md](docs/installation.md); keybindings and
configuration in [docs/usage.md](docs/usage.md).

Keybindings: `enter` opens a thread (marks it read), `P` previews
without marking read, `v` toggles the plain/html view, `alt+i` loads
remote images, `F` enters the easyjump link mode, `$` applies staged
tag ops, `u` undoes them. The help overlay (`?`) derives from the
binding map, so rebinds update the hints.

If notmutt works for you, [star the repository](https://github.com/fishman/notmutt).
When something breaks, [open an issue](https://github.com/fishman/notmutt/issues)
- reproduce with fabricated mail if the bug is message-specific.

## What is notmutt

notmutt starts from the neomutt pain its author lived: neomutt's notmuch
integration loads threads synchronously, rebuilds the whole thread tree
on new mail, and makes every tag application final. notmutt inverts all
three: async thread loading, diff-and-insert refresh, and staged
undoable tag operations. A 33k-thread inbox walks in ~1.6s, and
steady-state keypresses are sub-150us.

Status: M1 (mailbox view: thread tree, index cache, pager, search,
default plain/html views) and M2 (staged tag ops, send dialogue with
attach commands and preview, async send) are done. On the roadmap:
crypto send through your system gpg, algorithmic filters (bayes, DKIM),
Lua hooks and UI callbacks, an emacs keymap scheme. GUI and IMAP/POP3
transport are out of scope - notmutt is a terminal client that reads
what notmuch sees.

## Where is notmutt

- Source: https://github.com/fishman/notmutt
- Documentation: https://fishman.github.io/notmutt/ (the pages are in `docs/`)
- Issues: https://github.com/fishman/notmutt/issues
- Releases: https://github.com/fishman/notmutt/releases

## Features

| Name | Description |
| --- | --- |
| Staged tag operations | Archive/delete/flag/read stage into a buffer and hit notmuch only on `$` (mutt's sync). A mis-tap is one `u` away - neomutt makes every tag application final |
| Diff-and-insert refresh | New mail inserts into visible threads between entries - no full rebuild on new mail |
| Exclusive folder tag groups | One message, one home: applying any group member removes the others, inbox included. No hand-maintained `-tag` chains in your config |
| Async send and compose | The compose dialogue is a state machine separate from the UI - background sync and filter runs never interrupt typing; sends run as background jobs with output kept for review |
| Terminal images | Sixel by default, kitty opt-in. Remote images fetch only on `alt+i` (a privacy gate), and 1x1 tracking pixels drop unless opted in |
| Config as data | TOML everything: themes with palette indirection, declarative per-context keybindings (the help overlay derives from them), tag styles, glyphs |
| notmuch is the only truth | No own database - a revision-keyed bbolt cache mirrors query output and re-syncs from notmuch's lastmod |
| Lua plugins | Build-tag-gated gopher-lua layer with a lib whitelist sandbox; plugins register body-rendering transforms |

## Commits and AI assistance

All code in this repository is owned by its human author: no code
commit carries any AI marker or co-author line, whether or not an AI
drafted it. Doc and spec commits carry a `Co-Authored-By: Deepseek`
line (the model that drafted them). Either way the line is like mail
typed on an iPhone - the device produced the words, you answer for
them, and blaming the device for a dumb decision is not acceptable.
Review responsibility stays with the human.

## Design decisions

The full records with measurements live in
[docs/design-decisions.md](docs/design-decisions.md); the short version:

- Go over Rust/Zig (R7): integration surface. go-message is aerc's
  production mail library - the same worker architecture notmutt
  mirrors; the cgo binding is vendored and pinned, never fetched from
  the proxy.
- tcell v3 over BubbleTea (record 23): the vendored v2 renderer was the
  wrong trust boundary - an out-of-bounds frame bug was fixed
  model-side, and verifying the diff engine meant re-implementing what
  tcell's Screen.Show() does natively. tcell is a screen cell buffer
  and an event source, nothing more; lazygit pairs it with the same
  state/UI architecture.
- cgo binding over the notmuch CLI (record 3): a batched threads walk
  closed the gap - 1.645s full walk vs the CLI's 1.534s on a 33k-thread
  inbox, with an 11ms peek. The CLI backend survives behind the `-tags
  cli` build tag as the escape hatch; the cgo handle stays read-only,
  reopening read-write only for a tag op.
- Render coalescing: state updates land at input rate, paints coalesce
  at an 8ms cadence, a content-addressed row cache restyles only the
  rows whose selection flips. Measured on the 33k-thread inbox:

| scenario | before | after |
| --- | --- | --- |
| held-key burst, 50 presses | 50+ full-frame paints | 6 (one per 8ms window + settle) |
| single press, full list | ~2.5ms frame build | 133us |
| pager resize, 20k-line document | 385ms | 44-74us |
| fill-window press, whole-fill batch | 2.61ms | 147us (17.7x) |
| frame rebuild, all rows cached (40 visible @ 5k list) | 182us uncached | 24us (7.7x) |
| keypress on the full 30k list (cursor resolve) | ~8ms flatten+scan per paint | 12us (O(1) index read) |

- The index cache is a materialized view (R13), not a second truth:
  revision-keyed, invalidated by notmuch's lastmod, rebuilt from query
  output only, never written independently.

## Credits

The design derives from reading these projects' source; the
`references/` tree keeps the checkouts. Concepts were studied, not
copied - with one code exception, the hyperlink scanner, ported from
aerc with its MIT attribution in the source
(`src/lib/html/links.go`).

The client is the idea of merging mutt and notmuch. Mutt and
neomutt are the source of correctness of mail content: what the
client takes from them is the mail behavior, the style, the compose
dialog - the mutt-family surface.

| Project | What notmutt takes |
| --- | --- |
| [notmuch](https://notmuchmail.org) | the whole model: the client is a front-end, notmuch is the single source of truth (R1) |
| [neomutt](https://neomutt.org) | the mail behavior, the style, the compose dialog - the mutt-family UX the client mirrors |
| [afew](https://github.com/lazka/afew) | the filter engine shape: the per-message filter contract, per-account folder priorities, first-existing-folder-wins moves (R2) |
| [aerc](https://aerc-mail.org) | the worker action loop behind an async channel (R3/R4), go-message as the mail library, the per-context keybinding model, the crypto CLI-backend pattern (R10) |
| [matcha](https://github.com/floatpane/matcha) | the Lua plugin layer: one VM on the orchestrator, a lib-whitelist sandbox, deferred side effects (R8) |

## Documentation

Requirements and architecture are normative in AGENTS.md; the security
model lives in SECURITY.md. User documentation (features, installation,
usage, FAQ) is on the project site and in `docs/`.
