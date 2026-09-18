# Markdown mail implementation plan

**Goal:** Send Markdown compose bodies as compatible text and HTML alternatives.

**Architecture:** `compose` owns Markdown detection and assembly. A small
renderer turns the signed body into alternatives; existing attachment and PGP
layers receive its final MIME entity unchanged.

## Tasks

1. Add pinned, vendored Goldmark and Chroma dependencies.
2. Add a renderer that produces readable plain text and styled, sanitized HTML
   from Markdown with one fixed safe configuration.
3. Change assembly to emit `multipart/alternative` for Markdown and nest it
   inside existing `multipart/mixed` when attachments exist.
4. Replace Markdown-source assembly expectations with MIME shape and generated
   content tests. Preserve existing plain compose and attachment contracts.
5. Run both test matrices.

## Acceptance

- Markdown source never appears as a message body part.
- Markdown output has text/plain then text/html alternatives.
- Generated HTML escapes authored raw HTML and uses no remote resources.
- Attachment order and PGP transform placement remain unchanged.
