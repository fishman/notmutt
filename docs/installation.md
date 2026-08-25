---
layout: default
title: Installation
nav_order: 4
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
make build TAGS="cli"
```

## Build

```sh
git clone git@github.com:fishman/notmutt.git
cd notmutt
make build
```

The Makefile default carries the Lua runtime (R8); `TAGS` overrides
(`make build TAGS="cli"` for the CLI backend, `make build TAGS=""` for
a plain Go build). `make test` runs the suite under the same tags. The
binary lands at `./notmutt`. Run it from anywhere; your configuration
is read from `~/.config/notmutt/config.toml`.

## Packages

The release workflow builds three packages from a `vMAJOR.MINOR.PATCH`
tag (`packaging/`): a deb and an rpm via nfpm, and an Arch package via
makepkg. All three wrap the same Go binary; they split only on the link
model.

### deb and rpm

The deb and rpm embed libnotmuch **statically**: the release workflow
builds notmuch 0.40 and links its archives into the binary. Static is
forced, not chosen - the cgo binding calls `notmuch_threads_status`,
which needs libnotmuch >= 0.40, and the default distro libnotmuch is
older (Ubuntu ships 0.37/0.38). Embedding 0.40 statically is the only
way the binary's own libnotmuch calls compile and run there.

The packages still `Depends: notmuch`, because the binary shells out to
the `notmuch` CLI for `database.path` (see `packaging/notmutt.yaml`).
That runtime `notmuch` comes from the distro - on stock Ubuntu still
0.37/0.38, older than the 0.40 the client targets.

**The deb and rpm are untested.** CI builds and packages them but never
runs them on a real system, and no stock distro ships libnotmuch 0.40,
so the runtime dependency chain cannot be satisfied there. They are
buildable artifacts, not validated installs. Use the Arch package, or
build from source against your distro's libnotmuch, if you need
something you can run.

### Arch

The Arch package is the usable path. Its PKGBUILD builds against the
distro's current libnotmuch (`go build -tags lua`, shared linking) -
Arch's rolling notmuch is new enough to satisfy the 0.40 requirement -
and the runtime `notmuch` CLI matches. The release workflow also pushes
it to the AUR; see `packaging/notmutt/PKGBUILD`.

## Configuration

The client ships with its defaults as data: `src/config/base.toml` is
the reference - every default (keybindings, themes, views, tag
groups, pager settings) is visible there. Your user file overlays it;
nothing in your file needs to restate a default. Start with an empty
file if you want to discover the defaults, or copy `base.toml` and
edit.

Configuration is strict by design: unknown keys are load errors, not
silently ignored typos.

## Transport: mbsync and msmtp

notmutt is a front-end: it never talks IMAP or SMTP itself (see the
non-goals). Delivery in is mbsync (or vdirsyncer), delivery out is
msmtp (or any mutt-compatible sendmail). The client reads the
maildirs mbsync writes and calls the send command with the message
on stdin. The reference setup ships in this repository -
`references/.mbsyncrc` and `references/.msmtprc` are the live
working shape: copy them, edit, and use them as the template for
every additional account.

### Receive: mbsync

One account is three blocks: an IMAPStore (the remote side), a
MaildirStore (the local maildir), and a Channel wiring them
together:

```text
IMAPStore your.email@gmail.com-remote
PipelineDepth 3
Host imap.gmail.com
Port 993
User your.email@gmail.com
PassCmd "oama access your.email@gmail.com"
AuthMechs XOAUTH2
TLSType IMAPS
CertificateFile /etc/ssl/certs/ca-certificates.crt

MaildirStore your.email@gmail.com-local
Subfolders Verbatim
Path /home/you/Mail/gmail/
Inbox /home/you/Mail/gmail/INBOX

Channel your.email@gmail.com
Expunge Both
Far :your.email@gmail.com-remote:
Near :your.email@gmail.com-local:
Patterns INBOX "[Gmail]/Drafts" "[Gmail]/Sent Mail" "[Gmail]/Spam" "[Gmail]/Trash" Archives Pending
Create Both
SyncState *
MaxMessages 0
ExpireUnread no
```

The points that matter to notmutt:

- The MaildirStore `Path` is the account root. notmutt's setup
  detection walks these roots, so every account needs its own
  directory (`~/Mail/<account>/`) and its own store/channel block
  trio, named by the account.
- `Subfolders Verbatim` keeps the provider's real folder names
  (`[Gmail]/Drafts`, ...) as subdirectories. Setup's provider
  detection and the client's folder rules match against exactly
  those names - do not rename them away.
- `PassCmd` runs a command that prints the credential on stdout;
  nothing secret sits in the config. The reference uses `oama` (an
  OAuth token helper) with `AuthMechs XOAUTH2` - the Gmail shape.
  App passwords work the same way: `PassCmd "echo ..."` is the
  mechanism, oama is just the reference's command.
- `Patterns` lists the folders to mirror. Everything the client's
  tag pipeline classifies (see the mail concept) must be mirrored;
  the Gmail special folders are the bracketed names above.

### Index: notmuch

notmuch is the single source of truth (R1): it owns the database,
the client only reads and tags. Its config (`~/.notmuch-config`)
tells it where the mail lives and how to treat new mail; the
reference lives at `references/muttrc/toolconfig/notmuch-config`.
The parts that matter to the chain:

```ini
[database]
path=Mail

[new]
tags=unread;inbox;
ignore=.mbsyncstate;.uidvalidity;.cache;

[maildir]
synchronize_flags=true
```

- `path` is the mail root, relative to `$HOME` - the directory that
  holds every account's Maildir (`~/Mail` in this document's
  example, matching the mbsync store paths above).
- `[new] tags` is the initial set `notmuch new` stamps on delivery:
  the `unread;inbox;` the client's inbox view and the unread flag
  rely on. The client's views are tag queries - change the stamp
  and the views move with it.
- `ignore` is the mbsync glue: mbsync drops `.mbsyncstate`,
  `.uidvalidity` and its cache files into every maildir, and
  without the ignore list notmuch would index those dotfiles as
  mail.
- `synchronize_flags` keeps the maildir flags and the notmuch tags
  in sync - the physical side of the tag model. The `[user]`
  identity block feeds notmuch's own addressing (its reply tools);
  the client's sender addresses come from `accounts.toml`.

Index once, before the client ever runs:

```sh
notmuch new
```

### Send: msmtp

msmtp is a mutt-compatible sendmail: it reads the message on stdin,
authenticates, and hands it to the server. The reference
`references/.msmtprc` shows the shape:

```text
defaults
auth on
tls on
logfile ~/.msmtp.log

account gmail
host smtp.gmail.com
from your.email@gmail.com
user your.email@gmail.com
port 587
auth oauthbearer
passwordeval "oama access your.email@gmail.com"
tls_trust_file /etc/ssl/certs/ca-certificates.crt
```

The contract with notmutt: the client runs the configured `[send]
command` (default `msmtp`) with the envelope recipients as argv and
the assembled message on stdin, and msmtp picks the account from
the message's From header (its `--read-envelope-from` default).
That is per-account sending: choose the sender
in the compose dialogue (`A`), the From header follows, msmtp
routes by it. One account block per address, `from` and `user` matching
the account's From address.

Like mbsync, the credential is a command, never plaintext:
`passwordeval` runs it and reads stdout (oama again for the Gmail
OAuth case). `tls_trust_file` is your distribution's CA bundle.

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
