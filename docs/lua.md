---
layout: default
title: Lua library
nav_order: 11
---

# Lua library

Plugins and `:lua` chunks run in a sandboxed VM (no os/io/debug, no
filesystem) with a deadline kill. The library surface is the full set
of globals the VM provides, split by where they exist:

- **load-time globals** - available in every plugin file while it
  loads, and in the hook functions it declares (`body_render`,
  `categorize`, `refresh`), which run on the same loaded VM:
  `re_match`, `get_attachments`, `date_str`, `json`, `http` (gated),
  `log`, `register_attach_command`, `register_action`, `bind_key`,
  `translate`.
- **invocation globals** - available in every action body and `:lua`
  chunk: `ctx`, `attach_add`, `tag_add`, `tag_remove`, `picker_argv`,
  `prompt`, `ai_chat`, `translate`, `print`, plus `json` (and `http`
  when gated). The two sets overlap but neither is a subset of the
  other: `re_match`/`get_attachments`/`date_str` are load-time only
  (their data does not exist at invocation), and the client-effect
  bindings are invocation only.

The [usage](usage.html) page covers how plugins load, the network gate,
and the categorization contract; this page is the function reference.

## Load-time globals

Every plugin file sees these while it loads.

### re_match(pattern, str)

RE2 regex helper - Lua string patterns have no alternation, so Go's
regexp syntax is exposed instead. Returns two values: `match, err`. A
bad pattern is `false` plus the error text, never a raise; the common
single-value use `if re_match(...)` keeps working.

```lua
local ok, err = re_match("trip\\.com", msg.from)  -- literal dot: \\.
if not ok and err then return nil end              -- a pattern error is fatal
```

RE2 escapes with backslash, so a literal dot is `\.` and the Lua
literal needs `\\` - a single backslash is swallowed by the Lua parser.

### get_attachments(handle)

Fetches the attachment list of the message the save pass is
categorizing. The `handle` is the opaque string passed to
`categorize(handle, msg)`; the plugin never opens files - this returns
what the client already parsed. Returns a table of `{name, mime,
size, ordinal}` rows; `ordinal` is the 1-based position in the message
(the key the categorize return table uses). An unknown handle raises.

```lua
for i, att in ipairs(get_attachments(handle)) do
  if att.mime == "application/pdf" then out[i] = category .. "/" .. slug(att.name) end
end
```

### date_str(sec, pattern)

Formats a unix timestamp by the same `YYYY`/`MM`/`DD` token pattern as
the `[attachments] layout` config - the calendar lives in the client,
not the plugin. Literal text passes through; the default pattern is
`YYYY/MM`.

```lua
date_str(msg.date, "YYYY/MM")      -- "2026/08"
date_str(msg.date)                 -- "2026/08" (default)
date_str(msg.date, "YYYY-MM")      -- "2026-08"
date_str(msg.date, "MM/YYYY")      -- "08/2026"
```

### json

`json.encode(v)` and `json.decode(s)`, depth- and size-capped (a
cyclic Lua table hits the depth cap, a fan-out table cannot build a
JSON bomb). `encode` returns the string, or `nil, err`; `decode`
returns the value, or `nil, err`.

```lua
local ok, err = json.decode(json.encode({a = 1}))
```

### http

The REST binding - exists only when the plugin has a `[lua.network.<name>]`
section (deny-by-default). `http.request(method, url, opts)` returns
`{status, headers, body}` or `nil, err`; `opts` may carry `headers`
and `body`. Every request (redirect hops included) must match the
configured targets AND one `"METHOD /path"` rule; the body is capped
at 256 KiB; the VM deadline aborts in-flight requests. A
network-enabled plugin never sees mail content (its ctx is the
metadata surface only, see below).

```lua
local res, err = http.request("GET", "https://api.hubspot.com/crm/v3/objects/contacts", {
  headers = { authorization = "Bearer " .. token },
})
if not res then error("api: " .. err) end
local data = json.decode(res.body)
```

### register_attach_command(name, argv)

Adds an attach command to the compose prompt (`?` lists it, `@name`
runs it). Runs during load, the reverse of the read-after pattern - the
plugin file calls it directly.

```lua
register_attach_command("yazi", {"yazi", "--chooser-file"})
```

### register_action(name, fn)

Registers a named action (`:name`). `fn` runs in a fresh invocation VM
on every call, with the invocation globals below. The name must be
non-empty; a duplicate just overwrites.

```lua
register_action("triage", function(ctx)
  for _, line in ipairs(ctx.mail_lines()) do print(line) end
end)
```

### bind_key(context, key, action, fn)

Binds a key in a binding context; the action name is the keybinding's
`fun`. `fn` receives the same invocation context as `register_action`.

```lua
bind_key("index", "g t", "triage", function(ctx) ... end)
```

### translate(id)

The i18n lookup - the same embedded catalog the client UI uses,
selected by the `[ui] language` setting, never plugin config.

```lua
print(translate("save attachment to: "))
```

### log(msg, err)

Appends to the session log - the same surface the `~` overlay and the
status line show, shared with the client's own events. `log(msg)` is a
normal entry, `log(msg, true)` an error line. Available in every
plugin-declared hook (`body_render`, `categorize`, `refresh`); `:lua`
chunks and action bodies have `print` instead, which captures into the
run's output rather than the log. Never log mail content (F6) - the
text is the plugin's own data, not message bodies.

```lua
function refresh(ctx)
  log("sync " .. ctx.account_name)
end
```

## Invocation globals

Available in action bodies and `:lua` chunks, on a fresh VM per call
(the plugin file re-runs, so an edit since load is what the call
sees). `:lua` chunks and action invocations are user-typed or
plugin-authored code, never mail content. The `json` and gated `http`
modules above exist here too; `register_action` and `bind_key` re-run
scoped to the call (a plugin may register from inside an action).

### ctx

The invocation context table.

- `ctx.thread_id` - the id of the thread the action ran on.
- `ctx.mail_lines()` - the full thread plain text as a table of
  strings. **Absent on a network-enabled plugin's invocation** - the
  data policy replaces it with the metadata surface, so a body cannot
  cross the network allowlist.

A network-enabled plugin sees the metadata surface instead:

- `ctx.thread_info(thread_id)` - one thread's messages as
  `{thread_id, count, messages = {id, thread_id, timestamp, author,
  subject, tags, references}}`.
- `ctx.search(query, limit)` - message rows for a notmuch query
  (same row shape; `limit` 1-500, default 50).
- `ctx.count(query)` - the message count of a query.

The surface is filtered per message by the account's `[ai-data]` grant
(`docs/usage.md` "Per-account data grants", deny by default): a message
whose account grants `participants`/`subjects`/`dates` projects its
`author`/`subject`/`timestamp`, and `thread_info`'s `count` appears only
when the newest message's grant allows it. The structural identity
(`id`, `thread_id`, `tags`, `references`) always renders.

### attach_add(path)

Adds an attachment to the compose dialogue's attachment list.

### tag_add(tag) / tag_remove(tag)

Stage a tag op into the current folder's R14 buffer - the same buffer
the `t`/`a`/`d` keys fill. The script classifies, the APPLY key
flushes; Lua never writes notmuch directly.

### picker_argv(argv)

Runs a chooser (the attach-command exec path) and blocks the VM on the
TUI's reply, under the action deadline. Returns the selection as a
table of paths. This is the only picker primitive; the client-specific
wrapper is your plugin (see `lua-picker.md`).

### prompt(...)

Opens the native text dialogue and blocks for its reply: committed
text returns as the string, an esc cancel returns nil. The wait is
deadline-bounded - a never-answered prompt cannot wedge the plugin.

### ai_chat(name, opts)

Streams one completion to a configured `[ai.<name>]` provider. `opts`
carries `model`, `system`, and `text` (required); the streamed deltas
publish as `AiChunk` (the pager-inline summary) and the full text
returns. On failure it raises.

`ai_chat` is network egress, so it exists **only on a plugin with a
`[lua.network.<name>]` section** - a no-network plugin or a `:lua` chunk
calling it gets "attempt to call a nil value". A network plugin's ctx is
already the metadata surface (no `mail_lines`), so a body can never
reach the provider this way.

### print(...)

Captures into the run's output (rides `LuaResult`, shown to the user) -
never a log line.

## Pickers

No picker is bundled: the core exposes only `picker_argv`, and the
chooser wrapper lives in your plugin. See `lua-picker.md` for the
`attach-choose` action pattern and a `chooser.lua` example.
