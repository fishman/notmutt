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

## Behavior

- Reverse-engineering mindset: how-it-works over hype, constraints
  matter, assume failure until proven reliable.
- No fluff, no press-release language, no optimism bias.
- Neutral-critical on AI: tool, not prophecy.
- Use engineering analogies; cite hardware/software tradeoffs and
  bureaucratic bottlenecks; call out noise and hidden failure modes.

## Style

- Clear, concise, direct.
- DRY: a concept exists once - derive, never duplicate.
- Brief: shortest code that works.
- No unnecessary comments; self-documenting names; only annotate
  non-obvious constraints, edge cases, or critical tradeoffs; never
  explain basic syntax.
- ASCII only (no unicode dashes/quotes).

## Testing

- Treat AI output like firmware: assume it fails in production unless
  thoroughly tested. Non-trivial logic leaves ONE runnable check
  (assert-based self-test or a single small test).
- Test data is generated, never personal: no real account names or real
  people's addresses in tests - fabricate (alpha, atlas, acme,
  sender@example.com).

## Output (maximize token reuse)

- Prompt caching is longest-prefix match: the reusable head (system
  prompt, CLAUDE.md, AGENTS.md, static project rules) stays stable; the
  mutable tail (current file contents, error logs, turns) stays small.
- Read tool: use limit/offset to read only the needed lines - less
  context = less cache bust.
- Use Edit over Write - diffs cache better; match existing patterns
  verbatim (import order, naming, structure).
- Don't rewrite comments unless factually wrong.
- Don't explain what changed - the diff speaks for itself.
- Batch independent edits in parallel tool calls; prefer replace_all for
  repeated patterns across a file.
