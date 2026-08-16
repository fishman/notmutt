-- gmail.lua - the gmail provider shape (reference template).
-- The match names are TOP-LEVEL folders: a top-level [Gmail] dir says
-- gmail, even when the sync skipped some of its subfolders. Copy to
-- <configdir>/lua/templates/ and enable it in [setup] templates to
-- override. Mirrors setup.Templates in setup.go - the sync test pins
-- them equal.
return {
  name = "gmail",
  match = { "INBOX", "[Gmail]" },
  folders = {
    inbox = { "INBOX" },
    draft = { "[Gmail]/Drafts" },
    sent = { "[Gmail]/Sent Mail" },
    spam = { "[Gmail]/Spam" },
    deleted = { "[Gmail]/Trash" },
    archive = { "Archives", "Archive" },
    pending = { "Pending" },
  },
}
