# PGP crypto implementation plan

**Goal:** Implement the v1 PGP gate: gpg-backed PGP/MIME sign, encrypt,
decrypt, and verify, integrated with async compose send and pager open paths.

**Architecture:** `lib/crypto` owns gpg argv/status parsing and PGP/MIME
transforms. `compose` carries the selected secret key through existing state
round trips. The app invokes transformations in existing background jobs; core
carries the verdict and TUI renders it. Clear message assembly and transport
remain independent.

**Constraints:** Only generated test mail and a temporary `GNUPGHOME`; no
shell, no loopback pinentry, no sensitive logging. GPG status is on stderr
because `--status-fd=2`; human diagnostics are never treated as status.

## Tasks

1. Add `lib/crypto` PGP status types, argv-only command runner, robust status
   parser, key-list parser, and tests against temporary fake gpg output.
2. Add PGP/MIME encode/decode around the gpg provider: detached signatures,
   multipart encryption, CRLF canonicalization, and nested sign+encrypt.
3. Extend compose state and its event/core mapping with the optional PGP signing
   key. Add a `pgp-key` selector hook and TUI fuzzy chooser backed by async
   gpg key discovery.
4. Transform assembled data in `sendJob` before `deliverSend`; preserve current
   FCC/Bcc ordering and reject failed crypto before transport.
5. Decode/verify PGP/MIME during `openThread`, add the generic bus verdict, and
   render an honest pager banner.
6. Add generated-GPG integration tests covering sign/verify, encrypt/decrypt,
   sign+encrypt, invalid signatures, send rejection before transport, and pager
   status. Run the affected package tests and `make test TAGS=""`.

## Acceptance criteria

- The client asks gpg-agent/pinentry, never collects a secret itself.
- Sign, encrypt, decrypt, and verify produce interoperable RFC 3156 MIME.
- Signed/encrypted mail renders decrypted content and a non-forgeable verdict.
- A failed crypto transformation does not invoke the send transport or create
  an FCC copy.
- Key selection lists gpg secret-key identities and persists its key choice in
  the open dialogue.
