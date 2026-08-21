# notmutt - Claude Code project rules

This project's requirements and architecture live in AGENTS.md (shared with
Pi and other tools). Read it first; it is normative.

@AGENTS.md

## Prompt caching

- CLAUDE.md + AGENTS.md load once at session start; mid-session edits
  neither apply nor cost a cache miss. State missing rules in the
  conversation; file doc changes at session end.
- Cache busts on: model/effort switches, MCP/plugin toggles, tool deny
  rules, /compact, CC upgrade. Not on: file edits, skills, subagents.
- Every response is re-read at cached rate on later turns - terse wins.

## Privacy (hard rule, overrides everything)

- Never submit mail content (bodies, headers, whole .eml/.mbox files) to
  the LLM.
- To read a subject or field from inside mail, extract it with a script
  first (pattern: `references/muttrc/bin/dedupe-mail`), pass only the extracted value.
- Include a file checksum (sha256, or faster md5/xxhash) when correlating
  or verifying the identity of a message.
- Config files (muttrc, afew configs, notmuch config) are not mail content
  and may be read freely; mail files are not.
