-- icloud.lua - the icloud provider shape (seed, adjust to taste).
-- Match names are top-level folders only. Enable in [setup] templates.
return {
  name = "icloud",
  match = { "INBOX", "Sent Messages" },
  folders = {
    inbox = { "INBOX" },
    sent = { "Sent Messages" },
    deleted = { "Trash" },
    draft = { "Drafts" },
    archive = { "Archive" },
    spam = { "Junk" },
  },
}
