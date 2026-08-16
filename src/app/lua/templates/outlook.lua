-- outlook.lua - the outlook provider shape (seed, adjust to taste).
-- Match names are top-level folders only. Enable in [setup] templates.
return {
  name = "outlook",
  match = { "INBOX", "Sent" },
  folders = {
    inbox = { "INBOX" },
    sent = { "Sent" },
    deleted = { "Trash", "Deleted Items" },
    draft = { "Drafts" },
    archive = { "Archives", "Archive" },
    spam = { "Spam", "Junk" },
    pending = { "Pending" },
  },
}
