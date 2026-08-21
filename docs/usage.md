---
layout: default
title: Usage
nav_order: 2
---

# Usage

## Keybindings

The binding map is declarative data (per context), vim by default,
emacs as a config choice (`[ui] keymap = "emacs"`). The help overlay
(`?`) derives from the binding map - rebinds update the hints
automatically. These are the vim defaults from `src/config/base.toml`;
every key is described there and rebindable.

### Index

| key | action |
| --- | --- |
| j / k | move the cursor |
| enter | open the thread under the cursor (marks it read) |
| H | open with the full raw headers (Received, DKIM-Signature, SPF) |
| P | preview in a popup without marking read |
| t / a / d / y / p | stage read/archive/delete/spam/pending |
| I | stage inbox (move back) |
| $ | apply staged tag ops |
| u | undo the staged ops on this message |
| r / R | reply / reply-all |
| f | forward |
| m | compose a new message |
| F | live-filter the index rows |
| / | search the index (enter commits, n repeats) |
| ctrl+f | search the whole database in a new tab (raw notmuch query) |
| g g / G | jump to first/last row |
| g i / u / a / p / s / S / d / D | jump to a view (inbox, unread, ...) |
| ctrl+d / ctrl+u | half-page scroll |
| pgdown / pgup | page scroll |
| h / l, left / right | scroll horizontally (rows longer than the window pan, never wrap) |
| C | collapse the cursor thread to its summary row (cursor-scoped) |
| ctrl+v | flatten every thread to one row, or expand the whole index back |
| [ / ] | previous / next tab |
| ? | help |
| = | check for new mail now |

`ctrl+f` opens a prompt taking a raw notmuch query - the whole
database, unlike `/` which filters the current rows. Enter opens the
results in a new tab named by the query; `[ / ]` cycle to it, `q`
closes it. The last query preloads for a repeat. `[ui]
search-open = "background"` runs the query without attaching: the tab
fills in the tab strip and the current surface stays.

### Threads

The index renders each thread as a tree, windowed to
`[index.thread] max-rows`: a thread bigger than the window shows its
chunk with a leading "-N more" ghost (rows hidden above) and a trailing
"+N more" ghost (rows hidden below). Walking to the window edge slides
it to the next chunk, so the whole thread is reachable row by row.
`[index.thread] sort` orders the flattened rows inside a thread:
`"desc"` (the default) reads newest-first like the index, `"asc"` the
notmuch-native oldest-first order.

`C` collapses the cursor thread to its summary row. The collapse is
cursor-scoped: moving the cursor off the thread expands it again, so a
thread never stays hidden after the cursor moved past. `ctrl+v`
flattens every thread to one row (or restores the full tree) and is
persistent - it survives cursor movement until toggled back.

At the bottom of the page, a single `j`/down step snaps the page to
the cursor thread's head: the thread window advances to its next chunk
and the page re-anchors at the beginning of the thread - 1, with the
leading "+N more" ghost on top when the window is cut. `pgdown`/`pgup`
flip the page plainly without re-anchoring - the cursor lands on the
new page's first (or last) row, in whatever thread it finds there.
Enter opens the cursor message only, never the whole thread.

### Pager

| key | action |
| --- | --- |
| j / k, space, ctrl+d, pgdown/pgup, g / G | scroll |
| alt+i | load remote images (and render embedded ones) - privacy gate |
| v | toggle the plain/html view |
| ctrl+u | toggle the html part's raw source view |
| F | easyjump link mode (type the [N] number to open) |
| H | toggle the full header block |
| h / l, left / right | scroll horizontally (long lines pan, never wrap) |
| enter | advance to the next thread |
| ctrl+f | search the whole database in a new tab (raw notmuch query) |
| q | back to the list |

Every pager opens with the header block - Date, From, To, Subject,
labels aligned - in the plain and html views alike; `h` replaces it
with the full raw header block.

The thread's tail marks by its position: the five most recent
messages render in one color, the most recent message from the other
side (not you) in a more prominent one - a long thread reads by its
tail. The marks tint the subject lines and thread indicators of the
marked messages in the index (they show when you return to the list,
wherever the marked messages sit - the opened message itself is never
tinted); the rest of the row and the pager text keep their own colors
- the quoted levels, signature, and subject always render in their
styles. Your identity is the message's
`sent` tag or a From matching an account `from` field.

### Compose

| key | action |
| --- | --- |
| j / k | move between attachment rows |
| t / s / f / c / b / r | edit To / Subject / From / Cc / Bcc / Reply-To |
| e | edit the body (or the field under the cursor) |
| a (or tab) | attach a file - plain path, `?` lists commands, `@name` runs one |
| d | detach the attachment under the cursor |
| A / C | choose the sender account / signature |
| S | cycle the security setting (none / sign / encrypt / sign+encrypt) |
| y | send |
| q | abort (confirm) |
| ctrl+d / ctrl+u / ctrl+f / ctrl+b | scroll the preview pane |

## Configuration

Config files are TOML and unmarshal 1:1 into typed structs. Your file
overlays the built-in defaults in `src/config/base.toml`; unknown
keys are load errors.

```toml
[ui]
keymap = "vim"        # or "emacs"
search-open = "active"  # ctrl+f: "active" attaches the new tab, "background" runs the query behind the current surface

[pager]
# terminal image protocol: sixel by default (most terminals support
# it), kitty opt-in. Fetched remote images decode and paint on the
# alt+i key only.
image-protocol = "sixel"
# lift the 1x1 tracking-pixel block on fetched remote images
allow-tracking-images = false
# per-domain part preference for the open key:
# "html" opens that sender's mail in the html view
default-views = { "example.com" = "html" }

[view.inbox]
query = "tag:inbox"
threads = true

# exclusive folder tag group (R2): applying any member removes the
# others present, inbox included
[tag-groups.folder]
tags = ["inbox", "archive", "deleted", "sent", "draft", "pending", "spam"]

# the link opener (pager F key): argv only, the url is the last element
opener = ["xdg-open"]

# attach commands for the compose prompt: '?' lists them, '@name' runs
# one (argv only; a chooser file path is appended as the last element)
[attach-commands]
yazi = ["yazi", "--chooser-file"]

# tag actions: which tag a key stages
[tag-actions]
"toggle-read" = "unread"
"archive" = "archive"

# the new-mail poll cadence in minutes (mbsync syncs in minutes, never
# seconds; 0 disables the automatic poll - the refresh key still works)
[refresh]
interval = 20
```

### Themes

Truecolor baseline; styles reference palette names or raw hex. A
theme states only what differs from `normal`; light/dark variants
live in one file, switching re-renders live. Index row coloring is
tag-driven: `[index.tag.<name>]` styles per tag, composing with the
base row style. The subject tint keys: `[theme.dark.pager]`
`recent = { fg = ... }` (the recent-5 tint) and
`other-side = { fg = ..., attrs = ["bold"] }` (the prominent
other-side tint) - the tint paints the marked message's subject run
and its tree indicator, the fixed slots keep their colors.

### Staged operations

`t` / `a` / `d` / `y` / `p` stage a tag op - the row renders the
staged state immediately, notmuch is untouched. `$` applies the
buffer (one batch per message); `u` discards the cursor message's
staged ops. Staged state is session-local and lost on exit - apply
before you quit.

A folder tag whose physical move cannot resolve is refused before the
tag lands - the error names the fix (the account folder space must
cover the message, the tag needs move candidates, readonly accounts
never move) and the entry stays staged. Move skips land in the `~`
log, never silently.

### Filters

The classification pipeline (folder tags, header rules, physical
moves) runs on every poll - the `=` key, the automatic interval, or
the headless `notmutt poll`. During the current testing period
every filter op is dry-run by default: the run computes and reports
the full diff (tag changes, moves), writes nothing, and never
touches your mailbox. When you trust the rules, flip the config:

```toml
[filter]
enabled = true
dry-run = false
```

`notmutt poll --apply` runs one live pass on demand, overriding the
dry-run config for that run only and leaving the file untouched -
the review flow: read a dry-run report, then apply.

### Lua plugins

Plugins are files in `<configdir>/lua`, loaded into a sandboxed VM
with a deadline: no os/io/debug, no filesystem. Every plugin gets a
`json` global (encode/decode, depth- and size-capped); the `http`
global exists only when the plugin has a network section - network
is deny-by-default:

```toml
[lua.network.hubspot]
targets = ["*.hubspot.com"]        # exact host or *.suffix, hostname-matched
paths = ["GET /crm/v3/objects/contacts*"]  # verb + path, one rule unit
```

`http.request(method, url, opts)` returns `{status, headers, body}`
or `nil, err`; `opts` may carry `headers` and `body`. Every request
(redirect hops included) must match the targets AND one `paths`
rule: a verb without its path is meaningless, so each entry is
`"METHOD /path"` - the verb case-insensitive, the path exact or a
trailing-`*` prefix (`GET /a*` allows `/a`, `/ab`, `/a/b/c`).
Malformed entries are load errors. The response body is capped at
256 KiB (a REST page, not a transfer), and the VM deadline aborts
in-flight requests.

The data policy is part of the gate: a plugin with a network section
never sees mail content. Its ctx is the metadata surface only
(`thread_id`, `thread_info`, `search`, `count` - the same projection
the MCP server exposes); `mail_lines` (the full thread plain text) is
absent, so a body cannot cross the allowlist. A plugin without a
network section keeps the full ctx.

### Attachment categorization

Attachment downloading is manual and local: a Lua plugin declares a
`categorize(handle, msg)` function, and the headless command saves the
categorized attachments. Nothing runs on new mail - you invoke it.

The plugin receives an opaque mail `handle` plus the metadata-only
projection: `msg` carries `from`, `subject`, and `date` (unix seconds)
- never paths, ids, or content. The attachment list is fetched from
the handle with the library command `get_attachments(handle)`, which
returns the message's attachments as `{name, mime, size, ordinal}`
tables (the ordinal is the 1-based position in the message). The
plugin never opens files - the client parsed the list.

Return a table of attachment ordinal to category string ("travel",
"receipt", ...); attachments without an entry are skipped, and `nil`
skips the whole message:

Patterns are RE2 (Lua string patterns have no alternation). RE2
escapes with backslash, so a literal dot is `\.` and the Lua literal
needs `\\` - a single backslash is swallowed by the Lua parser:

```lua
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
    if okFrom and okSubject then category = r.category break end
  end
  if not category then return nil end

  local out = {}
  for i, att in ipairs(get_attachments(handle)) do
    if att.mime == "application/pdf" then out[i] = category end
  end
  return out
end
```

`re_match(pattern, str)` is the regex helper: Go's RE2 syntax (Lua
string patterns have no alternation), returning `match, err` - a bad
pattern is `false` plus the error text, never a raise. The file above
ships as `config/examples/lua/categorize.lua` - copy it to
`<configdir>/lua`.

The command:

```sh
./notmutt attachments --dry-run 'has:attachment'   # report the plan, write nothing
./notmutt attachments 'has:attachment'             # save, no dry-run (query defaults to *)
```

Files land in `<folder>/<YYYY-MM from the message date>/<category>/<filename>`,
with the download root from the config (default `~/Downloads/Attachments`):

```toml
[attachments]
folder = "~/Downloads/Attachments"
```

Idempotent: an existing target is skipped (`skip <path> (exists)`),
never overwritten - re-runs are safe. Filenames and categories are
sanitized as single path segments (separators become `_`, control
runes dropped), so no name can traverse the tree. Files are 0600,
directories 0700.

## MCP server

An optional Model Context Protocol server lets LLM clients query the
mail index. It is disabled by default: the `mcp` subcommand exists in
every build, but only the `mcp` + `lua` build tag combination carries
the server; any other build answers with a not-built-in error.

```sh
make build TAGS="lua mcp"
./notmutt mcp
```

Register it with Claude Code (`claude mcp add`) or any MCP stdio
client; the server speaks JSON-RPC on stdin/stdout, so nothing else
may write to stdout while it runs. `tools/list` exposes three read-only
tools:

- `thread_info(thread_id)` - per-message metadata of one thread
  (subject, author, timestamp, tags, references, message count)
- `search(query, limit)` - thread summaries for a notmuch query, one
  row per thread (subject, author, timestamp, tags); `limit` 1-500,
  default 50
- `count(query)` - the thread count of a query

Every tool runs as a fixed Lua chunk in a fresh sandboxed VM with a
60s per-call deadline; the tool set is an allowlist, so a client can
never reach anything beyond it.

Beyond the metadata-only defaults, content-adjacent tools are gated:
they are served only when `[mcp] allow` names them. The one such tool
is `attachments(id)` - the attachment list of one message (name, mime,
size per attachment; bytes never cross). Whitelist it explicitly:

```toml
[mcp]
allow = ["attachments"]
```

An unknown name in `allow` is a startup error - a typo fails loudly
instead of silently serving fewer tools.

## The data boundary: accounts and tags

The server's world is explicit: `[mcp] accounts` names the account
folder spaces it may see, `[mcp] tags` the soft tags whose mail is
reachable. Deny by default - an empty `accounts` or `tags` list
serves nothing.

```toml
[mcp]
allow = ["attachments"]
accounts = ["gmail"]        # folder space AND the account tag
tags = ["inbox", "sent"]    # a message must carry one of these
```

Each allowed account grants its folder prefix AND its account tag
(`folder:/^gmail\// AND tag:gmail`, subfolders included). Every tool
enforces the scope: `search` and `count` intersect the query with the
scope before it reaches notmuch, `thread_info` projects only in-scope
messages of the thread, and the gated `attachments` tool refuses any
message outside the scope before its file is opened. An unknown
account name, a read-only account (its mail carries no account tag,
so the scope could never match), or a tag that would break the query
are startup errors - never a silent partial grant.

The privacy rule (never submit mail content to an LLM) is a hard
boundary of the server: results carry thread metadata only. Bodies,
raw maildir paths, and headers are never projected, and no tool reads
them; the gated `attachments` tool projects attachment metadata only,
never bytes.
