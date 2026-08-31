# Account context

Files in this directory are per-account context notes. A file named
`<account>.md` is injected into an AI command's prompt (the
`account_context: true` frontmatter) when the command runs on a thread of
that account.

The note is the account owner's words about the account - who the usual
correspondents are, what the mailbox is for, or standing policy to remember
when drafting. It is sent to the configured AI provider with the command's
thread data, so write nothing confidential here.
