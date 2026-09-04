---
layout: default
title: Features
nav_order: 3
---

# Features

## Async everything (R3, R4)

All notmuch reads and updates happen asynchronously. Thread views load
without blocking the UI; a refresh diffs new messages into the visible
threads instead of rebuilding the list. Background sync, filtering and
tag pipelines run as jobs on the event bus, and their progress renders
as a bar above the status line while you keep navigating.

Composition is tabbed and its state lives outside the UI: a filter run
can retag and re-render the mailbox while you keep typing in the
compose tab. Sends run as background jobs with captured output kept
for review, and a failed send re-opens the dialogue with the message
intact - pause and restart, never start over.

## Scheduled send

`P` in the compose view stores the message for a future delivery
instead of sending it. The spool (XDG data home, 0600) survives restarts
and offline stretches: delivery is checked at startup - a closed client
catches up - and on a cadence, a failed transport keeps the mail
pending, and concurrent instances serialize through a spool lock. The
message assembles at delivery, so the wire Date is the send instant.
Natural-language times work in English, Persian, Arabic and Chinese
(`s` lists pending mail, `e` reopens one for editing).

## Staged tag operations (R14)

UI tag operations (read/unread, archive, delete, flag) never write to
notmuch at keypress time. They stage into a per-session buffer; the
view renders the staged state immediately, notmuch sees it only when
you apply (`$`). The buffer is the undo mechanism: `u` discards the
staged ops before apply - a pure buffer drop, free of database
traffic. Staged rows render visually distinct with a configurable
glyph, and survive view switches and refreshes.

## Exclusive tag groups (R2)

Folder tags are a declarative exclusive group: one message has exactly
one home. Applying any member (inbox, archive, deleted, sent, draft,
pending, spam) removes the others present - automatically, with no
hand-maintained `-tag` chains in your rules. Soft tags (work,
conference, receipt) are not in any group: unlimited, coexisting,
never moved.

The classification pipeline - folder rules, header rules, and
per-account physical moves - all run inside the client. Per-account
folder priorities resolve move destinations by existence: candidates
tried in order, first existing folder wins, globs allowed. Rules carry
NOT guards so re-runs touch only new mail.

## Index cache (R1, R13)

notmuch stays the single source of truth; the client's index is a
materialized bbolt mirror of the overview query, revision-keyed and
invalidated by notmuch's lastmod. Startup re-syncs O(changed); a full
walk happens only on cache miss or revision mismatch. Reads are never
served stale - a read that finds the cache stale re-syncs from
notmuch.

## Terminal images

Inline images render as placeholders until the load-remote-images key
(alt+i) - a privacy gate, and an explicit one. Images-off keeps today's
alt markers exactly; the layout is image-blind. The key turns images
on and RE-LAYS-OUT the page at real geometry: the worker sizes each
image (`image.DecodeConfig`, dimensions only) before the px layout
runs, so text flows around real boxes instead of painting over the
markers' reserved rows. Embedded images (cid:/data:) size from their
bytes on the toggle; remote (http) images fetch, the measured px ride a
second (refine) render, and the image seats once it lands. The bytes
never leave the TUI - only dimensions cross to the worker; pixels
decode in the terminal only. The toggle keeps the reader's scroll
position. Protocol selection is config data: sixel by default, kitty
opt-in, both detected from the environment. Remote srcs fetch on the
same key, size-capped and time-bounded, and 1x1 tracking pixels drop
unless `allow-tracking-images = true`. Images paint after the frame
flush, so pixels never race the text.

An image that owns its line (a chart or photo sitting alone in its
row) fills the text column - an email that sizes a figure for a ~600px
browser column is not left at half the width of a wide terminal - and
seats at its block's text-align (left, center, or right), flush when
the block does not align it, capped at its natural pixels and the
100-cell paint cap. Small images render at natural size; an image
sharing its row with text keeps its authored width and flow position.

## HTML rendering

HTML mail renders inline - parsed and laid out in Go, never a browser.
Block flow, inline runs, column-aligned tables; layout budgeted
(wraps at 120 columns, caps at 5000 lines) so a hostile document
cannot balloon the thread. Easyjump link mode (`F`) labels every link
with a number; type the number to open it.

Dark mode (`[html] dark-mode = auto|on|off`, auto follows the theme)
maps the mail's colors onto the theme's background instead of rendering
as a white box: light-declared backgrounds reflect onto the theme bg by
an isometry (white lands exactly on the theme bg), text colors invert
their lightness keeping their hue - a blue link stays blue - with a
contrast guard that walks toward white until it reads as well as it did
on white. A mail that already declares dark stays dark, and unstyled
mail just uses the theme's own background and text.

## Theming (R11)

Truecolor baseline with palette indirection: styles reference named
palette entries or raw hex, inherit from a base style, and the
light/dark variant switch is a config-store notification - the UI
re-renders live with zero reload. Index rows are fixed-slot templates
(sizes in terminal cells, not runes), tag-driven coloring with
configurable glyphs, and the same slot discipline holds everywhere:
alignment never shifts per row.

## Security posture

- argv-only execution - mail content, filenames and queries are never
  interpolated into shell strings
- rendered mail content is control-character sanitized before it
  reaches the terminal
- S/MIME verification in-process (pkcs7 + stdlib x509, emailProtection
  EKU enforced, system roots or a pinned `[crypto] ca-file`); secret-
  holding crypto (PGP) stays a system-tool seam - `gpg`, passphrases
  through gpg-agent only - as a send-path follow-on
- no message bodies or headers are ever logged
- 0600 files, 0700 directories for everything written
- the mail parser boundary is fuzz-exercised

## Up next

The sign/encrypt engine (R10) is the biggest gap: the compose security
field cycles the mode, but no signer is wired into the send job yet.
PGP signs through the system gpg agent; S/MIME signs and verifies
in-process (pkcs7 + stdlib x509, emailProtection EKU enforced). Both
sit between MIME assembly and fcc, with decrypt/verify as an async job
on the read path.

- **Markdown compose** - write the body in markdown and send as
  multipart/alternative with an HTML part; code blocks render with
  syntax highlighting. Same assemble-stage slot as the signer.

