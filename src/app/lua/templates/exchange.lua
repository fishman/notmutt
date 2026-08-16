-- exchange.lua - an Exchange Online IMAP layout (seed shape).
return {
  name = "exchange",
  required = {
    inbox = { "INBOX" },
    sent = { "Sent Items" },
    deleted = { "Deleted Items" },
  },
  optional = {
    archive = { "Archive" },
    draft = { "Drafts" },
    spam = { "Junk Email", "Junk" },
  },
}
