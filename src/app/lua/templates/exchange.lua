-- exchange.lua - the exchange provider shape (seed, adjust to taste).
-- Match names are top-level folders only. Enable in [setup] templates.
return {
  name = "exchange",
  match = { "INBOX", "Sent Items" },
  folders = {
    inbox = { "INBOX" },
    sent = { "Sent Items" },
    deleted = { "Trash", "Deleted Items" },
    draft = { "Drafts" },
    archive = { "Archives", "Archive" },
    spam = { "Spam", "Junk Email", "Junk" },
    pending = { "Pending" },
  },
}
