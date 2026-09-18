# Markdown compose with text and HTML delivery

## Status

Design approved 2026-09-18. Compose remains plain Markdown text. Markdown
content sends as `multipart/alternative`: generated `text/plain` first and
sanitized `text/html` second. Attachments wrap that alternative entity in
`multipart/mixed`.

## Scope

Markdown detection remains `compose.ContentTypeOf`. Plain compose content
retains the current single `text/plain` shape. Markdown content has exactly
two generated alternatives; the Markdown source itself is not sent.

Goldmark renders CommonMark with GFM extensions: tables, strikethrough,
linkification, task lists, and typographer disabled. Generated HTML is a UTF-8
fragment; mail clients supply document-level styling. Goldmark's unsafe HTML
option remains disabled, so authored HTML is escaped. Chroma highlights fenced
code blocks using the `dracula` style and `monokai` fallback lexer. No external
styles, images, scripts, tracking, or network references are emitted.

## MIME shape

- No attachments: `multipart/alternative` with `text/plain; charset=utf-8`
  followed by `text/html; charset=utf-8`.
- Attachments: `multipart/mixed`; its first part is the complete
  `multipart/alternative` entity, followed by attachment parts in their
  existing order.
- Both alternatives use quoted-printable. Existing Bcc stripping occurs only
  after assembly and therefore applies identically to both formats.
- PGP signs or encrypts the complete assembled MIME entity. No separate crypto
  path is introduced.

## Plain text

The plain alternative derives from the Markdown AST, not the HTML. Headings,
lists, links, block quotes, code blocks, and tables retain readable Markdown
semantics. Its source is the authored Markdown plus the attached signature;
no CSS or HTML escapes reach text-only recipients.

## Tests

Generated fixtures cover a Markdown body with a heading, link, list, table,
and fenced Go block. Tests assert alternative order, source-free HTML,
escaped authored HTML, deterministic code styling, readable text, and the
mixed-over-alternative shape with an attachment.
