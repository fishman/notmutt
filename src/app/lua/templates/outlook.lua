-- outlook.lua - an Outlook.com IMAP layout (seed shape).
return {
  name = "outlook",
  required = {
    inbox = { "INBOX" },
    sent = { "Sent" },
    deleted = { "Deleted Items" },
  },
  optional = {
    archive = { "Archive" },
    draft = { "Drafts" },
    spam = { "Junk" },
  },
}
