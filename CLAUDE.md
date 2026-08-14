# notmutt - Claude Code project rules

This project's requirements and architecture live in AGENTS.md (shared with
Pi and other tools). Read it first; it is normative.

@AGENTS.md

## Privacy (hard rule, overrides everything)

- Never submit mail content (bodies, headers, whole .eml/.mbox files) to
  the LLM.
- To read a subject or field from inside mail, extract it with a script
  first (pattern: `muttrc/bin/dedupe-mail`), pass only the extracted value.
- Include a file checksum (sha256, or faster md5/xxhash) when correlating
  or verifying the identity of a message.
- Config files (muttrc, afew configs, notmuch config) are not mail content
  and may be read freely; mail files are not.

## Commit trailer

Commits follow the Conventional Commits style: `type(scope): subject`, brief
lowercase imperative subject. Spec/doc commits carry an
`AI-assisted: deepseek` trailer (the session model is deepseek); code
commits carry no AI-assisted trailer - the human bears responsibility for
code.
