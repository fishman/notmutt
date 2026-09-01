# Per-account AI context and prompts

Each account gets a folder here, named after the account (`<account>/`). Two
kinds of files live in it:

- `default.md` - the account's default context. It is injected into every AI
  command run on a thread of that account (the `account_context: true`
  frontmatter), after the global `context/default.md` so it reads as the
  override on top of the shared style.
- any other `*.md` - that account's prompts. They show in the picker above
  the default `prompts/` files, only when the open thread belongs to the
  account.

The default context is the account owner's words about the account - who the
usual correspondents are, what the mailbox is for, or standing policy to
remember when drafting. It is sent to the configured AI provider with the
command's thread data, so write nothing confidential here.
