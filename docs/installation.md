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

## Terminal requirements

- Truecolor (`COLORTERM=truecolor`); the R11 baseline is truecolor,
  no 256-color mapping
- Images: a sixel-capable terminal (foot, mlterm, xterm with
  `-ti 340`, ...) outside tmux - tmux does not pass image protocols
  through. Kitty graphics is opt-in via `[pager] image-protocol`
- The client detects the terminal image protocol from the environment;
  nothing to configure for text-only use
