# Licensing

notmutt ships as **two release binaries with different licenses**, because
the two backends sit on opposite sides of a GPL link boundary.

## The two variants

| binary | build | backend | license |
| --- | --- | --- | --- |
| `notmutt` | default | cgo binding of libnotmuch | **GPL-3.0** |
| `notmutt-cli` | `make build-cli` | notmuch CLI as a subprocess | **Apache-2.0** |

`notmutt` (cgo) statically links libnotmuch through the vendored
`github.com/fishman/go.notmuch` binding. That makes the binary a GPL-3.0
combined work: the whole program, notmutt's own code included, is
distributed under GPL-3.0. The repo's own source stays Apache-2.0 (per-file
SPDX headers, root `LICENSE`), but it cannot be distributed as Apache-2.0
once linked with libnotmuch.

`notmutt-cli` drives the notmuch CLI as a separate subprocess - no linking,
no combined work. It depends on a GPL `notmuch` *program* at runtime but does
not incorporate it, so the binary itself is Apache-2.0.

## Build tags

The two backends are build-exclusive (`//go:build !cli` on the cgo binding,
`//go:build cli` on the CLI backend). A single binary cannot compile both:
the cgo build would make the CLI binary GPL too. `make build` and
`make build-cli` produce the two variants; the release workflow packages both
as deb, rpm, and Arch packages, each labeled with its real license.

## Why not one binary

GPL-3.0 propagates to the linked whole. There is no MIT/Apache binding of
libnotmuch - the library itself is GPL-3.0 - so any cgo client is GPL-3.0.
The only way to keep notmutt's own code Apache-2.0 is to not link notmuch at
all, which is the CLI variant. This is the same posture as the whole notmuch
client ecosystem (aerc, notmuch-emacs), all GPL.

## Obligations

- The GPL binary must ship the GPL-3.0 text and offer corresponding source
  (the vendored binding + this repo satisfy source availability).
- Package metadata names the true license per variant (`GPL-3.0` on
  `notmutt`, `Apache-2.0` on `notmutt-cli`).
- Never distribute the cgo binary as Apache-2.0-only.
