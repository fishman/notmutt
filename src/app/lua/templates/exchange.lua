-- exchange.lua - the exchange provider shape (seed, adjust to taste).
-- Match names are top-level folders only. Enable in [setup] templates.
return {
  name = "exchange",
  match = { "INBOX", "Sent Items" },
  folders = {
    inbox = { "INBOX" },
    sent = { "Sent Items" },
    deleted = { "Deleted Items" },
    draft = { "Drafts" },
    archive = { "Archive" },
    spam = { "Junk Email", "Junk" },
  },
}
