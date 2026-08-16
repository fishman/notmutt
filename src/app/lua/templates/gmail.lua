-- gmail.lua - the gmail provider shape (reference template).
-- Copy to <configdir>/lua/templates/ to override; the lua build loads
-- every *.lua there. Mirrors setup.Templates in setup.go - the sync
-- test pins them equal.
return {
  name = "gmail",
  required = {
    inbox = { "INBOX" },
    draft = { "[Gmail]/Drafts" },
    sent = { "[Gmail]/Sent Mail" },
    spam = { "[Gmail]/Spam" },
    deleted = { "[Gmail]/Trash" },
  },
  optional = {
    archive = { "Archives", "Archive" },
    pending = { "Pending" },
  },
}
