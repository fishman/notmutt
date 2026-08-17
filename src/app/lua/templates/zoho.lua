-- zoho.lua - the zoho provider shape (reference template).
-- Snoozed is zoho's system folder discriminator: an account carrying
-- it is zoho. Without it a flat zoho account falls to outlook (same
-- shape, no no_fcc - set the flag by hand). zoho stores sent copies
-- server-side, so the account is generated no_fcc. Copy to
-- <configdir>/lua/templates/ and enable it in [setup] templates to
-- override. Mirrors setup.Templates in setup.go - the sync test pins
-- them equal.
return {
  name = "zoho",
  match = { "INBOX", "Sent", "Snoozed" },
  -- zoho stores sent copies server-side; the client writes no fcc copy
  no_fcc = true,
  folders = {
    inbox = { "INBOX" },
    sent = { "Sent" },
    deleted = { "Trash" },
    draft = { "Drafts" },
    spam = { "Spam", "Junk" },
    archive = { "Archive", "Archives" },
    pending = { "Pending" },
  },
}
