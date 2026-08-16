-- icloud.lua - an iCloud Mail IMAP layout (seed shape).
return {
  name = "icloud",
  required = {
    inbox = { "INBOX" },
    sent = { "Sent Messages" },
    deleted = { "Trash" },
  },
  optional = {
    archive = { "Archive" },
    draft = { "Drafts" },
    spam = { "Junk" },
  },
}
