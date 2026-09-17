# PGP/MIME through the system gpg

## Status

Design approved 2026-09-17. This is the v1 crypto gate: PGP/MIME signing,
encryption, decryption, and verification use the user's `gpg` binary. The
provider never handles passphrases or private-key bytes; gpg-agent invokes its
configured external pinentry.

## Critical limitation: protected headers unsupported

PGP/MIME encrypts the MIME entity only. The outer `From`, `To`, `Cc`,
`Subject`, and mail-routing headers remain readable to mail servers, notmuch,
and anyone with access to the encrypted message. `Subject` confidentiality is
not provided.

Do not use this implementation where subject confidentiality is required.
Protected-header support requires an RFC 3156-compatible inner header entity,
explicit display/index precedence, and a test corpus before it can be enabled.
Until then, all encrypted compose output must retain the visible outer headers
and the pager must report them as outer metadata.

## Context

Compose currently stores a `Security` selection but delivers the unmodified
assembled RFC 5322 message. The read path renders the encrypted or signed
container as ordinary MIME and has only an S/MIME verdict seam. PGP must use a
process because agent, smartcard, and pinentry behavior are gpg's trust
boundary. It must not introduce a shell or send mail data to logs.

## 1. Boundary

`src/lib/crypto` gains a PGP provider built on argv-only `exec.Command`:

- Every command includes `--batch --no-tty --status-fd=2`; stdout is payload
  and stderr is parsed solely as machine-readable status records.
- Commands do not use `--pinentry-mode loopback`, a passphrase fd, a shell, or
  a temporary GPG home. The caller's normal gpg-agent and pinentry are used.
- `Sign`, `Encrypt`, `Decrypt`, and `Verify` return bytes plus a typed status.
  The parser recognizes `GOODSIG`, `VALIDSIG`, `BADSIG`, `EXPSIG`,
  `EXPKEYSIG`, `REVKEYSIG`, `ERRSIG`, `DECRYPTION_OKAY`, `DECRYPTION_FAILED`,
  `NO_PUBKEY`, and `FAILURE`; unknown records are ignored. An unsuccessful
  gpg exit without a decisive status is an error whose diagnostic is bounded
  and never logged by the provider.
- `SecretKeys` executes `gpg --batch --with-colons --list-secret-keys` and
  returns usable `uid` entries, percent-decoded into display name/address plus
  a fingerprint key id. It is the selector's data source.

## 2. PGP/MIME wire format

The transform owns the outer MIME envelope; `compose.Assemble` remains the one
place that builds the clear RFC 5322 message.

- Signing canonicalizes client-produced line endings to CRLF and signs the
  resulting entity. Verification passes the exact first multipart bytes to
  gpg: header order, folding, casing, and the delimiter-adjacent line ending
  remain unchanged.
- The reader decodes base64 and quoted-printable encrypted payloads before
  invoking gpg. It also accepts the Microsoft Exchange malformed envelope:
  `multipart/mixed` with an empty `text/plain` part followed by the PGP/MIME
  control and encrypted parts.
- PGP input, stdout, and status buffers are capped at 32 MiB. A candidate is
  identified from the initial 64 KiB before the full bounded read; non-PGP
  messages retain the existing renderer path.

- Encryption first produces the signed form when both flags are selected. It
  encrypts the full inner content entity and emits `multipart/encrypted;
  protocol="application/pgp-encrypted"`: a `Version: 1` control part and an
  armored `application/octet-stream` part. The original transport headers
  remain visible outside the envelope.
- Encryption recipients are the deduplicated To, Cc, and Bcc envelope
  addresses. When signing-and-encrypting, the From address is also a recipient
  so the sender can decrypt the stored FCC copy. `SecurityEncrypt` does not
  silently sign; `SecuritySignEncrypt` signs then encrypts.
- Bcc is stripped only after the crypto transform, immediately before the
  transport. The FCC retains Bcc, as today.

## 3. Send path and selector

`sendJob` does `Assemble -> PGP transform -> deliverSend`. Transform failure
publishes a failed `SendResult`, keeps the dialogue open, and never invokes the
transport, FCC, index, or reply marker. The transform runs inside the existing
send goroutine.

The compose `security` action selects a signing key with this precedence:

1. `[accounts.<name>] pgp-key` initializes a composition for that account and
   supplies `--local-user` whenever signing is enabled.
2. An account without `pgp-key` opens the asynchronous `pgp-key` fuzzy
   selector when signing is enabled. Its selected fingerprint belongs to that
   dialogue only.

Changing account updates the dialogue with that account's configured key. A
key picker request is issued only when the newly selected account has no key.
Encryption recipients are the compose addresses, not selector data.

## 4. Read path

`openThread` calls a PGP/MIME decoder before `mail.RenderThread`, on the
existing background open goroutine. It detects only valid PGP/MIME containers:

- `multipart/encrypted` requires the PGP/MIME control part and an encrypted
  payload; it decrypts, then recursively handles an encapsulated signed form.
- `multipart/signed` verifies the exact first MIME part bytes against the
  signature part. The message remains renderable on failed verification; the
  verdict is a warning.
- The decoder replaces only the content entity while retaining the outer
  transport headers, then gives the resulting generated bytes to the existing
  mail renderer. It never overwrites a mailbox file.

`core.ThreadLoaded` carries a generic `PGPStatus`; the pager prepends an
honest `[PGP]` banner. Crypto validity, signer identity, and encryption are
separate. Unreadable or malformed PGP/MIME returns a visible read error; plain
mail has no PGP status and follows the existing path.

## 5. Tests and security

Tests use a generated temporary `GNUPGHOME` and fabricated addresses. They
exercise the actual gpg binary: sign/verify, encrypt/decrypt, combined
sign-encrypt/decrypt-verify, a bad signature, PGP/MIME parsing, send transform
before transport, and pager verdict rendering. No fixture is personal mail.
All process calls are argv-only. Error paths must not log payloads, headers,
filenames, or passphrases. Generated outputs and temporary files are 0600;
temporary directories are 0700.
