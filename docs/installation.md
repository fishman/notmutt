---
layout: default
title: Installation
---

# Installation

## Dependencies

- Go 1.26 or newer
- libnotmuch (the default build links the cgo binding against
  `libnotmuch.so` - install the `notmuch` runtime and dev package of
  your distribution)
- a running notmuch setup (`notmuch new` with your maildirs indexed -
  notmutt is a front-end, notmuch owns the database)

No libnotmuch available, or you prefer not to link it? The CLI backend
is one build tag away - the same binary code, the same interface:

```sh
cd src
go build -tags cli -o ../notmutt .
```

## Build

```sh
git clone git@github.com:fishman/notmutt.git
cd notmutt/src
go build -o ../notmutt .
```

The binary lands at `./notmutt`. Run it from anywhere; your
configuration is read from `~/.config/notmutt/config.toml`.

## Configuration

The client ships with its defaults as data: `src/config/base.toml` is
the reference - every default (keybindings, themes, views, tag
groups, pager settings) is visible there. Your user file overlays it;
nothing in your file needs to restate a default. Start with an empty
file if you want to discover the defaults, or copy `base.toml` and
edit.

Configuration is strict by design: unknown keys are load errors, not
silently ignored typos.

## Accounts

Run `notmutt setup` once after indexing your mail. It walks the
notmuch mail root, detects each account from its folder structure,
and writes `~/.config/notmutt/accounts.toml` (0600) with one
`[accounts.<name>]` entry per match:

```sh
$ ./notmutt setup
setup: accounts: gmail (gmail), acme (outlook)
setup: no template match: atlas
setup: wrote /home/you/.config/notmutt/accounts.toml
```

Detection is template-driven. Each built-in provider shape (gmail,
exchange, icloud, zoho, outlook) names the folders that must exist at
the account root - gmail is gated by a top-level `[Gmail]`, exchange
by `Sent Items`, zoho by `Snoozed` - then maps the hard tags (inbox,
sent, draft, spam, deleted, archive, pending) to that provider's real
folder names: `[Gmail]/Drafts`, `Sent Items`, `Archives` vs
`Archive`. Candidates are priority-ordered, first existing folder
wins, and a flat layout falls back to the provider names. Only
directory names are read, never mail content.

`setup` is detection output, not a complete account: `from` and
`default_signature` are yours to fill in. The `[accounts.<name>]`
table then carries the full surface:

| key | meaning |
| --- | --- |
| `from` | sender address used to prefill the compose dialogue |
| `default_signature` | signature file name in the account's signatures dir |
| `folder` | the account's folder prefix (the account tag); derived by setup |
| `folders` | the hard-tag -> folder map; derived by setup |
| `no_fcc` | skip the sent copy (the provider stores it server-side); setup sets it for gmail/zoho |
| `readonly` | never classify, never move, never tag this account's mail |
| `return_inbox` | trash returns to inbox instead of staying deleted |
| `preset` | provider preset name for the default move rules |
| `moves` | per-tag move overrides (tag -> folder candidates) |

Accounts drive the compose dialogue: `A` picks the sender account,
reply mode resolves it from the message's account tag, and the Fcc
path derives from the account's sent-folder map at send time. The
generated `accounts.toml` merges with the rest of your config
automatically - edit it freely, re-run `notmutt setup` to regenerate
(a `from` you typed by hand is overwritten only if you delete the
file; regeneration writes the whole file).

Provider-specific setup notes and template shapes (Gmail, Outlook,
iCloud, Zoho, Exchange, and the flat layouts) are welcome - if your
provider's folder names do not match, open an issue with its folder
list, or contribute a template: drop a `lua/templates/<provider>.lua`
in the config dir, enable it via `[setup] templates`, and send a PR
mirroring it in `src/setup/setup.go`. A maildir fixture with the
provider's folder names (no mail inside) is all a template test
needs.

## Terminal requirements

- Truecolor (`COLORTERM=truecolor`); the R11 baseline is truecolor,
  no 256-color mapping
- Images: a sixel-capable terminal (foot, mlterm, xterm with
  `-ti 340`, ...) outside tmux - tmux does not pass image protocols
  through. Kitty graphics is opt-in via `[pager] image-protocol`
- The client detects the terminal image protocol from the environment;
  nothing to configure for text-only use
