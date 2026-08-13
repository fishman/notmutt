# notmutt - security analysis

Threat model for the design in AGENTS.md / DESIGN.md. This is a living
document: every design change re-runs this analysis. The default posture
is "assume failure until proven reliable": every trust boundary here is
fuzzed or tested, not trusted.

## Assets

| asset | confidentiality | integrity |
|---|---|---|
| mail content (bodies, headers, attachments) | yes | yes |
| notmuch database + tags | - | yes (tags are the logical model) |
| drafts / fcc / postpone files | yes | - |
| gpg keys + passphrase | via gpg-agent (out of client scope) | - |
| the user's system (terminal, shell, files) | - | yes (mail must never execute) |

## Trust boundaries

1. **Mail content -> parser** (attacker-controlled input). Fuzz target #1.
   Already in CI standard (AGENTS.md R7 CI section). Non-negotiable.
2. **Mail content + maildir filenames -> exec'd processes** (mailcap
   viewers, HTML renderers, editor, hooks). Filenames are attacker-
   influenced: the remote server chooses them. Fuzz target #2.
3. **Compose fields -> MIME -> sendmail**. Header injection boundary.
4. **Config (user-owned) -> command strings** (hooks, mailcap, viewers,
   sendmail). User-owned = not a confidentiality boundary, but the TOML
   store must never execute anything at parse time.
5. **Terminal -> rendered content**. Escape sequence injection.
6. **External tools** (notmuch, gpg, openssl, mbsync, afew): invoked via
   argv, never shell. Each is a trust boundary with an invocation
   contract, not a library.
7. **notmuch DB**: shared mutable state. Lock waits are capped
   (lock_timeout=10, muttrc precedent) - ops error instead of hanging
   behind another process's lock.
8. **Same-user processes**: out of scope - a same-user attacker can
   ptrace the client or read the keyboard regardless. (Linux yama
   ptrace_scope mitigates; not relied on.)

## Findings

### HIGH

- **F1. Terminal escape injection (design gap - must fix).** Mail content
  rendered in the pager can carry CSI/OSC sequences: cursor attacks,
  OSC 52 clipboard exfiltration, terminal-emulator vulnerabilities.
  Notmutt renders mail content with BubbleTea styling - the styling is
  generated, but CONTENT must pass through a control-char sanitizer
  (strip ESC and C0 control chars except \n \t; block OSC entirely)
  before it reaches the terminal. Same for HTML renderers' output.
- **F2. mailcap dispatch (design gap - must fix).** Attachment viewers
  run commands with attacker-influenced filenames. Rule: mailcap
  commands are TOKENIZED (shlex-style) at config load; the filename is
  passed as argv, never interpolated into a shell string. A filename
  containing `$(...)` or backticks must be harmless. neomutt's own
  mailcap history is the cautionary tale.
- **F3. Reply header injection (verify against go-message).** Replying
  to crafted mail quotes original headers (subject, addresses). CRLF in
  quoted headers splits the reply into a second message or smuggles
  headers. go-message's mail package encodes headers - VERIFY it
  handles CRLF in all quoted positions; add a boundary test that
  fuzzes reply-construction with hostile input headers.
- **F4. No-shell rule (design gap - must fix, migration rule).** The
  muttrc pipeline is shell scripts (post-new, untag-reversal) operating
  on maildir filenames. The client internalizes this pipeline in Go:
  every external invocation (notmuch, mbsync, gpg, openssl, afew) is
  exec with argv. No interpolation of mail data into shell strings,
  ever. If external scripts must run (user hooks), they receive mail
  paths as argv, not as shell text.

### MEDIUM

- **F5. Job output and temp files.** Send/output temp files may contain
  mail content: 0600 perms, temp dir 0700. Output ring in memory is
  fine; nothing may be world-readable.
- **F6. Log redaction.** Debug logs must never contain message bodies,
  headers, or passphrases. Rule: loggers take structured fields;
  content never enters the log path. This is a permanent review rule,
  not a one-time fix.
- **F7. Draft/fcc plaintext at rest.** Accepted risk (mail is plaintext
  in the maildir anyway). Requirement: 0600 on all written mail files.
- **F8. In-process OpenPGP provider (crypto/pgp, optional).** Key
  material in process, no agent, no zeroization possible in Go.
  Documented optional backend; never default; gpg stays the trust
  boundary. If shipped, it must be gated behind explicit config.
- **F9. Key server trust (gpg --recv-keys).** TOFU/trust-on-first-use
  by design; prefer WKD. Document in crypto section; no client-side
  trust policy beyond gpg's own.
- **F10. cgo surface.** M1's notmuch decision (CLI-per-query vs cgo):
  cgo adds unsafe/FFI surface and crashes take down the client.
  Security bias: CLI-per-query unless the benchmark forces cgo; if
  cgo, the bindings are a reviewed, minimal, fuzz-covered module.

### LOW / accepted noise

- Same-user attacks (ptrace, keyboard): out of model, noted above.
- Mail bombing / DB growth: performance, not security.
- Notmuch DB tampering by same-user process: same-user is game over;
  tag integrity relies on the user's own filesystem perms.
- Telemetry: none. The client sends nothing; the address cache is
  local. Keep it that way - this is a feature, not an accident.

## Fuzzing targets (CI, from AGENTS.md R7)

1. mail parser boundary (go-message usage, not the library itself)
2. mailcap tokenizer + dispatch (filename hostile-input corpus)
3. reply-construction with hostile headers (F3)
4. filter rule query builder (TOML rules -> notmuch argv)
5. pager content sanitizer (escape-sequence corpus)

## Agent rules (mirrors AGENTS.md)

- argv exec only, no shell interpolation of mail data (F4)
- no mail content or passphrases in logs (F6)
- 0600/0700 on everything written (F5, F7)
- AI-assisted code gets the same review bar as human code; the fuzz
  targets above are the acceptance gate for parser-adjacent code
- any design change touching a trust boundary re-runs this analysis
