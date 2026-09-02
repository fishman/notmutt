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
| s | list scheduled mail (e reopens one for editing) |
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
| P | schedule the message |
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
# one (argv only; a chooser file path is appended as the last element).
# The attach prompt's tab prefers the Lua attach-choose action (see
# docs/lua-picker.md); these are the config-command fallback.
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

# the scheduled-mail spool and check cadence: where composed messages
# wait and how often the client checks for due mail (seconds). The
# check also runs at startup, so a closed client catches up on resume.
[schedule]
# dir = "/path/to/spool"   # default: ~/.local/share/notmutt/schedule
interval = 60
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

### Status line

The status row leads with a working indicator: a fixed glyph at rest,
an animated spinner while background work is in flight (a sync, a
refresh, a filter job, a send, the AI stream). Both occupy one cell -
the row never shifts between idle and busy.

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

### Scheduled send

`P` in the compose view schedules the message instead of sending it:
the prompt takes the delivery time, the dialogue closes, and the mail
waits in a spool until the client delivers it. Accepted forms - the
exact grammar first, then the natural-language engine for every
locale:

```
tomorrow             # tomorrow 09:00
tomorrow 14:30
today 14:30          # the next occurrence (today if still ahead)
2026-08-23 09:00     # absolute
in 90m / in 2h / in 3d
2026-08-23T09:00:00+02:00   # RFC 3339
next monday          # natural language, any locale: English, Persian
فردا ساعت ۱۰:۳۰       # (فارسی), Arabic (بعد أسبوع), Chinese (明天下午三点)
```

The spool lives in the XDG data home (`~/.local/share/notmutt/
schedule`, overridable with `[schedule] dir`), files 0600 (F5). The
message assembles at delivery - the wire `Date` and `Message-ID` are
the send instant, never the schedule time - and attachments read from
their paths like a live send. Delivery is checked at startup (a
closed client catches up on resume) and every `[schedule] interval`
seconds (default 60); an offline machine or a failed transport keeps
the mail pending for the next check, and concurrent instances
serialize through a spool lock, so a mail is never delivered twice
(delivery is at-least-once: a crash between transport and removal can
double).

`s` lists the pending mail (send time + subject); `e` on an entry
unschedules it and reopens the compose dialogue with the stored
state, ready to edit and re-schedule or send.

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
with a deadline: no os/io/debug, no filesystem. The full function
reference is the [Lua library](lua.html) page. Every plugin gets a
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

### Pre-poll sync hook

A plugin may declare `refresh(ctx)`: the client calls it before the
normal refresh (the `=` key or the automatic `[refresh]` interval)
and awaits it. It returns the argv to run first - an external sync,
so the poll indexes fresh mail - or nil for a plain refresh. F4: the
argv is used verbatim, never shell-interpolated.

`ctx` carries `view` (the current view name), `account`, and
`account_name`: on an account view the account tag and its config key
(the mbsync channel); on inbox and named folders all are absent. The
argv runs as a cancellable background job - the task view (`T`) can
cancel it - and only when it completes (or is cancelled) does the
normal refresh run.

```lua
-- refresh.lua: on an account view run mbsync first, then refresh
function refresh(ctx)
  if ctx.account then
    log("sync " .. ctx.account_name)
    return { "mbsync", ctx.account_name }
  end
end
```

### Attachment categorization

Attachment downloading is manual and local: a Lua plugin declares a
`categorize(handle, msg)` function, and the headless command saves the
categorized attachments. Nothing runs on new mail - you invoke it.
The library helpers it draws on (`get_attachments`, `re_match`,
`date_str`) are on the [Lua library](lua.html) page.

The plugin receives an opaque mail `handle` plus the metadata-only
projection: `msg` carries `from`, `subject`, `date` (unix seconds), and
`domain` - the lowercase From-address domain (subdomains kept), computed
in Go from the raw `from` header, never parsed in the plugin - never
paths, ids, or content. The attachment list is fetched from
the handle with the library command `get_attachments(handle)`, which
returns the message's attachments as `{name, ext, mime, size, ordinal}`
tables (`ext` is the filename extension without the dot, lowercased -
the sender's naming, immune to parser mime quirks; the ordinal is the
1-based position in the message). The
plugin never opens files - the client parsed the list.

Return a table of attachment ordinal to a relative path below the
download root: a full path (`"travel/flights/london.pdf"`) is used
verbatim - the plugin owns the structure and the filename; a bare
category (`"travel"`) falls back to the config `layout`, so the client
adds the date. Attachments without an entry are skipped, and `nil`
skips the whole message.

Patterns are RE2 (Lua string patterns have no alternation). RE2
escapes with backslash, so a literal dot is `\.` and the Lua literal
needs `\\` - a single backslash is swallowed by the Lua parser. The
filename transform below is plain Lua string work on the returned
value - the client writes whatever path comes back, segment by
segment:

```lua
-- slug: a name made path-safe - lowercase, non-word chars to dashes.
function slug(s)
  s = string.lower(s)
  s = s:gsub("[^%w.%-]", "-")   -- non word/dot/dash chars to a dash
  s = s:gsub("%-+", "-")
  return s
end

-- Rules tune the destination, never gate it: a message that matches
-- no rule keeps the default category below, so every pdf/docx lands
-- somewhere.
local default_category = "other"
local rules = {
  { from = "trip\\.com", subject = "^Flight Booking Confirmed:", category = "travel" },
  { from = "delta\\.com", subject = "boarding pass", category = "travel" },
  { from = "acme\\.com", subject = "invoice", category = "receipt" },
}

function categorize(handle, msg)
  local category = default_category
  for _, r in ipairs(rules) do
    local okFrom = re_match(r.from, msg.from)
    local okSubject, err = re_match(r.subject, msg.subject)
    if not okSubject and err then return nil end
    if okFrom and okSubject then
      category = r.category
      break
    end
  end

  -- msg.domain is the sender's address domain, lowercased (subdomains
  -- kept), computed in Go - the path's sender segment. A From header
  -- with no parseable domain is skipped.
  if msg.domain == "" then return nil end

  local out = {}
  for i, att in ipairs(get_attachments(handle)) do
    -- ext is the filename extension, lowercased and dot-less - match
    -- the sender's naming (pdf/docx here), not parser-reported mime
    if att.ext == "pdf" or att.ext == "docx" then
      out[i] = category .. "/" .. msg.domain .. "/" .. date_str(msg.date, "YYYY/MM") .. "/" .. slug(att.name)
    end
  end
  return out
end
```

`re_match(pattern, str)` is the regex helper: Go's RE2 syntax (Lua
string patterns have no alternation), returning `match, err` - a bad
pattern is `false` plus the error text, never a raise.
`date_str(sec, pattern)` formats a unix timestamp by the same
`YYYY`/`MM`/`DD` token pattern as the config `layout` (default
`YYYY/MM`) - the calendar lives in the client, not the plugin. The
file above ships as `config/examples/lua/categorize.lua` - copy it to
`<configdir>/lua`. The attachments it filters on come from
`get_attachments(handle)` (`{name, ext, mime, size, ordinal}`, see the
[Lua library](lua.html)); an extension check covers senders whose
parser-reported mime is empty or wrong, while `mime` stays available
for the ones whose extension lies.

The command:

```sh
./notmutt attachments --dry-run 'has:attachment'   # report the plan, write nothing
./notmutt attachments 'has:attachment'             # save, no dry-run (query defaults to *)
```

The plugin's path lands below the download root from the config
(default `~/Downloads/Attachments`); a bare category gets its date
from the config layout - `YYYY`, `MM`, `DD` tokens, `/`-separated into
directories, empty = no date:

```toml
[attachments]
folder = "~/Downloads/Attachments"
layout = "YYYY-MM"   # date pattern for bare-category returns; "" = none
```

A multi-segment plugin path bypasses the layout entirely - the
structure it returns is the structure written.

Idempotent: an existing target is skipped (`skip <path> (exists)`),
never overwritten - re-runs are safe. Filenames and categories are
sanitized as single path segments (separators become `_`, control
runes dropped), so no name can traverse the tree. Files are 0600,
directories 0700.

### S/MIME verification

S/MIME verification runs in-process (R10): the client extracts the CMS
from a signed message - detached `multipart/signed` with an
`application/pkcs7-signature` part, or attached `application/pkcs7-mime`
signed-data - and verifies it against its trust roots with the
emailProtection EKU gate enforced. Opening a signed message shows the
verdict banner in the pager: the signer, the validity, and whether the
signature's certificate was checked for revocation. A valid signature
from a cert that does not match the From header renders as a warning,
never green - crypto validity and identity are separate results.

The roots come from the config: a pinned PEM bundle when `ca-file` is
set, else the system CA pool when `use-system-pool = true` (the
default), and `use-system-pool = false` with no `ca-file` fails closed:

```toml
[crypto]
# ca-file = "/path/to/roots.pem"   # pin mail trust to specific roots
# use-system-pool = true           # default: trust the system CA pool
```

The headless command exposes the same verifier to scripts (the pager
path and the CLI share one implementation):

```sh
./notmutt smime-verify signed.eml            # system pool
./notmutt smime-verify signed.eml ca.pem     # pinned bundle
```

Exit 0 = valid signature or an unsigned message, 1 = invalid.
`scripts/smime-compare.sh` runs both `openssl smime -verify` and
`notmutt smime-verify` on the same .eml and reports agreement. One
asymmetry by design: openssl bare checks the signature without the
signer's chain, notmutt checks signature AND chain - an untrusted
signer can pass openssl and fail notmutt.

## Notifications

New-mail notifications are the filter job's side effect: the backend
resolves once at startup (auto-detected by default - the platform
backend when the session can show notifications, else `notify-send`),
and a poll that classified mail fires it. The payload is the count
plus a subject summary, never bodies or ids.

By default only **new unread inbox mail** notifies. The `tags` list is
the scope: a message must carry every tag in it to fire. A poll that
only reclassified deleted, sent, or archive mail stays quiet. Empty
`tags` notifies on every classified message.

```toml
[notify]
tags = ["inbox", "unread"]   # the scope; empty = notify on everything
# backend = "beeep"          # "beeep" | "command"; empty = auto-detect
# command = ["notify-send", "notmutt", "{count} new mail"]  # {count}, {subjects}
# priority = ["urgent"]      # tags whose mail leads the summary
# max = 3                    # summary rows cap
```

The default `tags` lives in `src/config/base.toml` like the rest of
the builtin config - your file overrides it wholesale.

## MCP server

An optional Model Context Protocol server lets LLM clients query the
mail index. It is disabled by default: the `mcp` subcommand exists in
every build, but only the `mcp` + `lua` build tag combination carries
the server; any other build answers with a not-built-in error.

```sh
make build TAGS="lua mcp"
./notmutt mcp
```

Any MCP stdio client can speak to it; the server runs JSON-RPC on
stdin/stdout, so nothing else may write to stdout while it runs.
`tools/list` exposes three read-only tools:

- `thread_info(thread_id)` - per-message metadata of one thread
  (subject, author, timestamp, tags, references, message count)
- `search(query, limit)` - thread summaries for a notmuch query, one
  row per thread (subject, author, timestamp, tags); `limit` 1-500,
  default 50
- `count(query)` - the thread count of a query

Every tool runs as a fixed Lua chunk in a fresh sandboxed VM with a
60s per-call deadline; the tool set is an allowlist, so a client can
never reach anything beyond it.

### Connecting Claude Code

Register the built binary as a stdio server. The `--scope` flag picks
where the registration lives: `user` makes it available in every
project on this machine, `project` writes a `.mcp.json` into the repo
(shared with the team; Claude Code shows such servers "pending
approval" until each developer approves them), and the default `local`
ties it to this checkout only. Use an absolute path to the binary -
Claude Code spawns it from its own working directory.

```sh
make build TAGS="lua mcp"
claude mcp add -s user notmutt -- /home/you/src/notmutt/notmutt mcp
claude mcp get notmutt   # spawns the server and health-checks it
```

The tools appear at session start, so begin a new session (or run
`/mcp` to reconnect) and `thread_info`, `search`, and `count` show up
as ordinary tools. The server loads the same config the TUI does, so
the `[mcp]` data boundary below holds no matter which client started
it. Launch it from an environment that reaches that config dir - with
none reachable the scope is empty and every tool serves nothing.

The surface is read-only by construction and every tool result is a
record: `search` returns `{threads: [...]}`, `count` returns
`{count: N}`. Queries are intersected with the scope before they reach
notmuch, so `tag:inbox` means "in-scope threads tagged inbox", and no
tool reads message bodies - ask only about the metadata the tools
return.

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

## AI commands

The `A` key (index and pager) opens a picker of user-authored AI
commands: markdown prompt files in `<configdir>/ai/`. On first run the
client seeds two prompts - "Thread next steps" (a summary) and "Draft
reply" (a compose draft) - plus an `accounts/` directory for
per-account notes and a `context/` directory for the default style
note. The files are yours: edit or replace them, the seed never
overwrites.

### The prompt format

A file is a strict frontmatter block plus a markdown body. The
frontmatter declares the data the command may access - the allowlist
is enforced structurally, a field you do not declare never reaches the
model.

```yaml
---
name: Thread next steps
description: Summarize the next actionable steps for this thread
action: view          # view = summary pager, compose = draft a reply
data: [participants, subjects, count, last_body]
account_context: true # optional: inject the account note
---
You are an executive assistant reading a mail thread. Below is the
thread data: participants, subjects, count, and the latest message.
Summarize the current state and list the concrete next steps.
```

- `name` and `description` fill the picker; `name` is what the
  selection runs.
- `action` is `view` (stream into the summary, which opens as its own
  tab the client switches to - the message stays intact underneath) or
  `compose` (draft into a new compose dialogue - recipient and subject
  prefilled, you review before sending).
- `data` is the scope: `participants`, `subjects`, `dates`, `count`,
  `bodies`, `last_body`, `structure`. Sender metadata is bare
  addresses only; `bodies`/`last_body` strip quoted lines, signatures,
  and html, and cap at 4000 chars per message (20000 total).
- `provider` (optional) names an `[ai]` provider; empty uses the first
  configured. An unknown key, a missing `name`/`description`/`action`/
  `data`, or a data field outside the allowlist fails the load - a
  broken file stops the picker, never silently drops the command.

### The provider

Commands run against an `[ai]` provider (the Lua `ai_chat` backend):
type, model, and the key's `pass_cmd` argv. Nothing runs until one is
configured - the picker says so.

`type` selects both the wire protocol and its default URL - four
vendors are built in, and `base-url` overrides the URL for any of them,
so any protocol-compatible endpoint works (a local ollama, a proxy, a
vendor's alternate-compat URL).

| type | protocol | default URL |
| --- | --- | --- |
| `anthropic` | Anthropic `/v1/messages` | `https://api.anthropic.com/v1` |
| `openai` | OpenAI `/v1/chat/completions` | `https://api.openai.com/v1` |
| `deepseek` | OpenAI (compatible) | `https://api.deepseek.com/v1` |
| `openrouter` | OpenAI (compatible) | `https://openrouter.ai/api/v1` |

```toml
[ai.default]
type = "anthropic"
model = "claude-sonnet-5"
pass_cmd = ["pass", "show", "llm/anthropic"]   # argv only, F4
```

A 401 almost always means the request went to the wrong host - the
`type` must match the key's vendor, or point `base-url` at it:

```toml
[ai.deepseek]
type = "deepseek"
model = "deepseek-v4-flash"
pass_cmd = ["pass", "show", "llm/deepseek"]

[ai.openrouter]
type = "openrouter"
model = "anthropic/claude-sonnet-5"
pass_cmd = ["pass", "show", "llm/openrouter"]

# any OpenAI-compatible endpoint, custom URL
[ai.local]
type = "openai"
model = "qwen3:8b"
base-url = "http://localhost:11434/v1"
```

The key's `pass_cmd` argv prints the key on stdout (`pass`, `gpg -d`,
any command). Empty `pass_cmd` sends no auth header - fine for a
keyless local endpoint, a guaranteed 401 against a hosted vendor.

### The privacy carve-out

The hard rule "never submit mail content to an LLM" is exactly as hard
here - this feature is its one controlled exception. The context
builder is the only path mail takes toward a provider; it runs only on
an explicit picker invocation with a provider configured, enforces the
frontmatter allowlist, and never logs or caches the prompt or the
output. Attachments never appear; body text is cleaned (quoted lines,
signatures, html) and capped; the output is control-stripped before it
renders in the pager or a draft.

### Per-account data grants

Each account additionally gates what commands may send: the
`[ai-data.<account>]` section grants a data-field allowlist,
deny-by-default. A command's `data` runs through the account's grant
(BuildContext, the chokepoint above) - a declared field the account
does not grant renders no section. Independent of `[mcp]`: an account
can be MCP-denied and AI-allowed, or the reverse.

```toml
[ai-data.gmail]
data = ["participants", "subjects", "count"]

[ai-data."*"]          # fallback for every unlisted account
data = ["count"]
```

`data = ["*"]` grants every field. Precedence: the explicit account
wins over the `"*"` account; no entry at all denies. The known fields
are the command `data` set (`participants`, `subjects`, `dates`,
`count`, `bodies`, `last_body`, `structure`); anything else is a load
error.

The default is deny. Migrating existing config: add

```toml
[ai-data."*"]
data = ["*"]
```

to restore the pre-grant behavior (every field on every account). An AI
command on an account with no grant refuses with a clear error instead
of running.

### Per-account context

`<configdir>/ai/accounts/<account>.md` holds a note about that account
(usual correspondents, standing policy). A command whose frontmatter
sets `account_context: true` injects the note into its prompt when run
on that account's thread. The note is sent to the provider with the
thread data - keep it non-confidential.

### Default context

`<configdir>/ai/context/default.md` is the default style note: seeded
with a brief-style instruction, it is injected into every command's
prompt regardless of `account_context`. Switch the AI's speaking style
by editing this file - the change applies from the next command run.
The note is sent to the provider with every prompt, so keep it
non-confidential.
