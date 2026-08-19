---
layout: default
title: Usage
nav_order: 2
---

# Usage

## Keybindings

The binding map is declarative data (per context), vim by default,
emacs as a config choice (`[ui] keymap = "emacs"`). The help overlay
(`?`) derives from the binding map - rebinds update the hints
automatically. These are the vim defaults from `src/config/base.toml`;
every key is described there and rebindable.

### Index

| key | action |
| --- | --- |
| j / k | move the cursor |
| enter | open the thread under the cursor (marks it read) |
| h | open with the full raw headers (Received, DKIM-Signature, SPF) |
| P | preview in a popup without marking read |
| t / a / d / y / p | stage read/archive/delete/spam/pending |
| I | stage inbox (move back) |
| $ | apply staged tag ops |
| u | undo the staged ops on this message |
| r / R | reply / reply-all |
| f | forward |
| m | compose a new message |
| F | live-filter the index rows |
| / | search the index (enter commits, n repeats) |
| g g / G | jump to first/last row |
| g i / u / a / p / s / S / d / D | jump to a view (inbox, unread, ...) |
| ctrl+d / ctrl+u | half-page scroll |
| pgdown / pgup | page scroll |
| [ / ] | previous / next tab |
| ? | help |
| = | check for new mail now |

### Pager

| key | action |
| --- | --- |
| j / k, space, ctrl+d, pgdown/pgup, g / G | scroll |
| alt+i | load remote images (and render embedded ones) - privacy gate |
| v | toggle the plain/html view |
| ctrl+u | show the html part's raw source |
| F | easyjump link mode (type the [N] number to open) |
| h | toggle the full header block |
| enter | advance to the next thread |
| q | back to the list |

### Compose

| key | action |
| --- | --- |
| j / k | move between attachment rows |
| t / s / f / c / b / r | edit To / Subject / From / Cc / Bcc / Reply-To |
| e | edit the body (or the field under the cursor) |
| a (or tab) | attach a file - plain path, `?` lists commands, `@name` runs one |
| d | detach the attachment under the cursor |
| A / C | choose the sender account / signature |
| S | cycle the security setting (none / sign / encrypt / sign+encrypt) |
| y | send |
| q | abort (confirm) |
| ctrl+d / ctrl+u / ctrl+f / ctrl+b | scroll the preview pane |

## Configuration

Config files are TOML and unmarshal 1:1 into typed structs. Your file
overlays the built-in defaults in `src/config/base.toml`; unknown
keys are load errors.

```toml
[ui]
keymap = "vim"        # or "emacs"

[pager]
# terminal image protocol: sixel by default (most terminals support
# it), kitty opt-in. Fetched remote images decode and paint on the
# alt+i key only.
image-protocol = "sixel"
# lift the 1x1 tracking-pixel block on fetched remote images
allow-tracking-images = false
# per-domain part preference for the open key:
# "html" opens that sender's mail in the html view
default-views = { "example.com" = "html" }

[view.inbox]
query = "tag:inbox"
threads = true

# exclusive folder tag group (R2): applying any member removes the
# others present, inbox included
[tag-groups.folder]
tags = ["inbox", "archive", "deleted", "sent", "draft", "pending", "spam"]

# the link opener (pager F key): argv only, the url is the last element
opener = ["xdg-open"]

# attach commands for the compose prompt: '?' lists them, '@name' runs
# one (argv only; a chooser file path is appended as the last element)
[attach-commands]
yazi = ["yazi", "--chooser-file"]

# tag actions: which tag a key stages
[tag-actions]
"toggle-read" = "unread"
"archive" = "archive"
```

### Themes

Truecolor baseline; styles reference palette names or raw hex. A
theme states only what differs from `normal`; light/dark variants
live in one file, switching re-renders live. Index row coloring is
tag-driven: `[index.tag.<name>]` styles per tag, composing with the
base row style.

### Staged operations

`t` / `a` / `d` / `y` / `p` stage a tag op - the row renders the
staged state immediately, notmuch is untouched. `$` applies the
buffer (one batch per message); `u` discards the cursor message's
staged ops. Staged state is session-local and lost on exit - apply
before you quit.

### Filters

The classification pipeline (folder tags, header rules, physical
moves) runs on every poll - the `=` key, the automatic interval, or
the headless `notmutt poll`. During the current testing period
every filter op is dry-run by default: the run computes and reports
the full diff (tag changes, moves), writes nothing, and never
touches your mailbox. When you trust the rules, flip the config:

```toml
[filter]
enabled = true
dry-run = false
```

`notmutt poll --apply` runs one live pass on demand, overriding the
dry-run config for that run only and leaving the file untouched -
the review flow: read a dry-run report, then apply.
