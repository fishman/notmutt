-- Attachment categorization (manual): copy to <configdir>/lua, then
-- run `notmutt attachments --dry-run 'has:attachment'` to plan and
-- `notmutt attachments 'has:attachment'` to save.
--
-- Contract: categorize(handle, msg) fetches the message's attachment
-- list through the handle (get_attachments) and returns a table of
-- 1-based attachment ordinal to a relative path below the
-- [attachments] folder. A full path (category/filename) is used
-- verbatim - the plugin owns the structure and the filename. A bare
-- category (single segment) falls back to the config [attachments]
-- layout, so the client adds the date. Attachments without an entry
-- are skipped. msg carries from/subject/date only - no paths, no ids,
-- no content.

-- slug makes a name tidy: lowercase, non-word chars to dashes.
function slug(s)
  s = string.lower(s)
  s = s:gsub("[^%w.%-]", "-")
  s = s:gsub("%-+", "-")
  return s
end

-- Lookup table: sender regex, subject regex, category. First match
-- wins; patterns are RE2 (Lua string patterns have no alternation).
-- RE2 escapes with backslash: a literal dot is \., so the Lua literal
-- needs \\ (a single backslash is swallowed by the Lua parser).
local rules = {
  { from = "trip\\.com", subject = "^Flight Booking Confirmed:", category = "travel" },
  { from = "delta\\.com", subject = "boarding pass", category = "travel" },
  { from = "acme\\.com", subject = "invoice", category = "receipt" },
}

function categorize(handle, msg)
  local category
  for _, r in ipairs(rules) do
    local okFrom = re_match(r.from, msg.from)
    local okSubject, err = re_match(r.subject, msg.subject)
    if not okSubject and err then return nil end
    if okFrom and okSubject then
      category = r.category
      break
    end
  end
  if not category then return nil end

  local atts = get_attachments(handle)
  local out = {}
  for i, att in ipairs(atts) do
    if att.mime == "application/pdf" then
      out[i] = category .. "/" .. date_str(msg.date, "YYYY/MM") .. "/" .. slug(att.name)
    end
  end
  return out
end
