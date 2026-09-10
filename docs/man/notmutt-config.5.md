# NOTMUTT-CONFIG 5 notmutt

# NAME

notmutt-config - the notmutt TOML configuration

# SYNOPSIS

*$XDG_CONFIG_HOME/notmutt/*.toml*

# DESCRIPTION

**notmutt** reads every **.toml** file in its config directory and merges
them over the builtin base config, in filename order. Your files overlay
the defaults; they never replace them. Loading is strict: an unknown key
is a load error, and so is a value outside a documented set. A stale key
renames nothing and hides nothing - it fails at startup with the table
path that is wrong.

Tables are addressed by dotted path. An angle-bracketed segment
(**accounts.<name>**) is a key you choose. A table marked *array of
tables* takes the same table more than once. Scalars are TOML strings,
booleans, integers, and arrays; a **style** is the **{ fg, bg, attrs }**
table described under **THEMES**.

# TYPES

**string**
: A TOML string. Where a key names a set of values, any other value is a
load error.

**integer**
: A TOML integer. Sizes and widths are in terminal cells, not runes: a
double-width glyph occupies two.

**array**
: A TOML array. **array of strings** elements are literal strings;
**array of tables** elements are inline or repeated tables.

**binding**
: One keymap entry: a plain action string (**"cursor-down"**), a pair
(**["cursor-down", "Move the cursor down"]**), or a table
(**{ fun = "cursor-down", desc = "Move the cursor down", show = true }**).
The description travels with the binding - the help dialog and the keyhint
row both derive from it, there is no separate descriptions block. **show**
is opt-in and defaults to false: the help dialog lists every binding, the
keyhint row only the marked ones.

# TAGS

Folder tags are physical homes, soft tags are everything else. The seven
folder tags are mutually exclusive by declarative group, not by hand:

## [tag-groups]

**<name>**
: An exclusive group: **tags = ["inbox", "archive", "deleted", "sent",
"draft", "pending", "spam"]**. Applying any member removes the other
members present, so a message has exactly one home. Soft tags belong to
no group and coexist freely. Adding a group tag does not disturb the
existing rules.

## [tag-actions]

**tag-actions.***action*
: The tag an action name applies: **toggle-read = "unread"**,
**archive = "archive"**, **inbox = "inbox"**, **delete = "deleted"**,
**spam = "spam"**, **pending = "pending"**. The action names are what
keymap bindings and Lua call; the values are the notmuch tags.

# TOP LEVEL

**opener** (array of strings, default `["xdg-open"]`)
: The argv that opens a link from the pager, with the URL appended as the
last element. Arguments are tokenized at load; the URL is never
interpolated into a shell string.

**attach-commands** (table of arrays of strings)
: Per attachment category, the argv that handles a downloaded file. The
categories come from the Lua categorize hooks.

## [view.<name>]

**query** (string)
: The notmuch query the view runs.

**threads** (boolean, default `false`)
: Group the query's results into threads.

**flat** (boolean, default `false`)
: Render a threaded view's rows at one level: grouping stays, tree
glyphs go. The **z** key toggles it live.

# [ui]

**keymap** (string, default `"vim"`)
: The binding scheme: **"vim"** or **"emacs"**. The scheme selects a
binding map per context; per-key overrides go in **schemes.<scheme>.<context>**.

**language** (string, default `"auto"`)
: The interface language. **"auto"** resolves from **LANG** and
**LC_MESSAGES** at startup; a BCP 47 tag (**"de"**, **"en-US"**) pins one.

**glyph-set** (string, default `"ascii"`)
: The thread tree glyph set: **"ascii"** or **"utf-8"** (box-drawing).
Per-glyph keys under **glyphs** override the preset.

**search-open** (string, default `"active"`)
: How the search tab activates: **"active"** shows results immediately,
**"background"** runs the query while the current surface stays (the
**]** and **[** keys cycle to it).

## [ui.tags]

**max** (integer, default `2`)
: Tag cells shown per index row. Every cell is reserved whether or not a
tag fills it, so alignment never shifts between rows.

**attach** (string, default `"attachment"`)
: The tag marking a message with attachments - it renders in the row's
attachment slot.

**show-icons** (boolean, default `true`)
: Render a tag's icon instead of its name. The index row then shows only
tags with an entry here; tags without one are listed in the status bar
instead, so nothing is lost. Off renders names in the index row and
nothing in the status bar.

**icons** (table of strings)
: Tag name to display icon. The shipped set covers the client's own tags
(folder homes, unread/flagged/forwarded/signed, the attachment marker);
entries merge per key, so your own vocabulary layers on without dropping
them.

## [ui.glyphs]

Single-character display data. The tree glyphs must occupy two cells per
level - a wide glyph drifts the column math.

**staged** (string, default `"*"`)
: Marks a row with a pending staged operation.

**cursor** (string, default `"▌"`)
: The cursor marker.

**progress_fill**, **progress_empty** (strings, default `"#"`, `"-"`)
: The progress bar's filled and empty cells.

**border_tl**, **border_tr**, **border_bl**, **border_br**, **border_h**,
**border_v** (strings, default `"╭"`, `"╮"`, `"╰"`, `"╯"`, `"─"`, `"│"`)
: The box-drawing set.

**tree** (string, default `"+ "`)
: The thread root marker.

**tree_child** (string, default `"| "`)
: One indentation level.

**tree_branch** (string, default `"|-"`)
: A branch marker.

**tree_leaf** (string, default `` "`-" ``)
: A leaf in the default (newest-first) order.

**tree_leaf_desc** (string, default `"/-"`)
: A leaf in a bottom-up thread - the mirrored corner of **tree_leaf**.
Branch and child glyphs are shared by both orders.

# [index]

## [index.thread]

**max-rows** (integer, default `10`)
: A thread renders at most this many rows; deeper threads window, and the
window slides as the cursor moves through them. Zero disables the window
and renders every row.

**sort** (string, default `"desc"`)
: The message order inside a thread: **"desc"** (newest first) or
**"asc"** (oldest first, notmuch-native).

# [setup]

**templates** (array of strings, default `[]`)
: The contributed detection templates in **<configdir>/lua/templates**
that **notmutt setup** may load. Empty means the built-in set only: the
seeded examples stay inert until listed here.

# [lua]

**tags** (array of strings)
: The config-level tag list plugins reference. The ai-tags command
proposes only these.

## [lua.network.<plugin>]

One plugin's network gate, keyed by plugin file base name. The sandbox's
http module exists only when this table does, and it is deny-by-default:
a request must match a target *and* a path rule, redirect hops included.

**targets** (array of strings)
: Permitted hosts, exact (**"api.example.com"**) or suffix
(**"\*.example.com"**).

**paths** (array of strings)
: Permitted requests as **"METHOD /path"**: the verb is case-insensitive,
the path a prefix match. An empty list matches nothing.

# [accounts]

The account name is the section key. Its notmuch account tag is the
folder prefix - the **folder:/^<folder>//** pattern as data.

## [accounts.<name>]

**folder** (string, default: the account name)
: The account's folder space in the maildir. Unset and explicitly empty
are different: an empty folder is a load error.

**from** (string)
: The account's From address, used to match messages to the account.

**default_signature** (string)
: The signature file or text the composer seeds.

**folders** (table of strings)
: The detected hard-tag folder map, as written by **notmutt setup**.
The mover resolves move destinations against it.

**preset** (string)
: A built-in provider folder map: **"gmail"** or **"generic-imap"**. An
unknown name is a load error. Presets name the folder candidates per tag;
the move rule itself is universal.

**moves** (table of arrays of strings)
: Per-tag move candidates, overriding the preset: tried in order, first
existing wins, **"\*"** is a glob.

**readonly** (boolean, default `false`)
: The account is never classified: no folder tags, no account tag, no
header tags, no moves.

**return_inbox** (boolean, default `false`)
: Enable the trash return-to-inbox rule for this account.

**no_fcc** (boolean, default `false`)
: Skip the client's sent copy. Set it when the server already keeps one
(Gmail-family) and the mbsync copy is the sent record - an fcc would
duplicate the Message-ID record.

# SENDING

## [send]

One configurable transport, run as argv. The default reads the envelope
sender from the message's own From header, so a multi-account setup needs
no per-account transport.

**command** (string, default `"msmtp"`)
: The delivery program.

**args** (array of strings, default `["--read-envelope-from"]`)
: Its argv.

## [compose]

**wrap-width** (integer, default `72`)
: The line width generated draft bodies hard-wrap to. Zero means the
default (mutt's wrap, the RFC 3676 norm).

**forward** (string, default `"inline"`)
: The forward shape: **"inline"** quotes the original's text and carries
its attachments, **"attachment"** attaches the original whole as
message/rfc822, so its HTML and list headers survive.

## [schedule]

**dir** (string, default: *$XDG_DATA_HOME/notmutt/schedule*)
: The spool directory where composed messages wait for their delivery
time. It holds assembled message bytes, so it belongs in the data home,
never a cache or temporary directory.

**interval** (integer, default `60`)
: The due-mail check cadence in seconds. The check also runs at startup,
so a closed client catches up on resume.

# CLASSIFICATION

## [refresh]

**interval** (integer, default `20`)
: The periodic new-mail poll cadence in minutes: the poll runs
**notmuch new**, then the classification pass over what arrived, then
refreshes the view. Zero disables the automatic poll - the refresh key
still checks on demand.

## [filter]

**enabled** (boolean, default `true`)
: Run the post-new classification engine.

**dry-run** (boolean, default `true`)
: Resolve every target and write nothing, reporting what would change.
The first run against a real mailbox is always dry.

## [filter.header-rules]

An array of tables: content-based soft-tag rules. The engine evaluates
each rule's query against the new-mail delta and enforces the
not-already-applied guard itself, so re-runs touch only new mail.

**query** (string)
: The notmuch query the rule matches.

**add** (array of strings)
: The tags added when it matches.

## [notify]

The new-mail notification side effect. The payload carries counts and
sender/subject/time summaries, never bodies or message ids.

**backend** (string, default: auto-detected)
: **"command"** runs **command**, **"beeep"** uses the platform
notification backend. Empty auto-detects: platform when the session can
show notifications, command otherwise.

**command** (array of strings)
: The notification argv. **{count}** is the processed entry count,
**{subjects}** the aligned summary rows. No command and no platform
backend means notifications are off.

**priority** (array of strings)
: Sender or address patterns that sort first in the summary.

**tags** (array of strings, default `["inbox", "unread"]`)
: The notification scope: only mail carrying *every* tag here fires. The
default keeps a poll quiet when it only reclassified deleted, sent, or
archived mail. Empty means every classified message notifies.

**max** (integer, default `3`)
: Summary rows carried in the notification.

# ATTACHMENTS

## [attachments]

**folder** (string, default `"~/Downloads/Attachments"`)
: Where downloaded attachments land. A plugin returning a full relative
path owns its own structure and bypasses the layout below.

**layout** (string, default `"YYYY-MM"`)
: The date pattern for a bare-category return: **YYYY**, **MM**, and
**DD** tokens separated by **/** into directories. Empty adds no date
directory.

# CRYPTO

## [crypto]

The in-process S/MIME verifier. The gpg subprocess is reserved for PGP -
a separate backend, never S/MIME.

**ca-file** (string, default `""`)
: A PEM bundle pinning mail trust to specific roots. Empty trusts the
system CA pool - the mainstream posture, bounded by the emailProtection
extended-key-usage gate and the signer-identity match.

**use-system-pool** (boolean, default `true`)
: With an empty **ca-file**, whether the system CA pool is trusted. Set
to false to fail closed: no system pool, no verification.

# MCP

## [mcp]

The Model Context Protocol server's data boundary. It is deny-by-default:
an empty **tags** list or no granted account serves nothing.

**tags** (array of strings)
: The reachable soft tags. A message must carry at least one of them
(the union of this list and the owning account's own list).

## [mcp.accounts.<name>]

One granted account. The table's presence *is* the grant: it covers the
account's folder prefix and its account tag. Absent, the account is
invisible. A granted account with no capability table below is
metadata-only - search, count, and thread metadata.

**tags** (array of strings)
: Soft tags reachable through this account, unioned with the **[mcp]**
list. The account tag itself is part of the grant, not this pool.

**attachments.enabled** (boolean, default `false`)
: Serve attachment metadata (names, sizes, MIME types).

**bodies.enabled** (boolean, default `false`)
: Serve cleaned body text. No headers.

**bodies.max_messages** (integer, default `10`)
: How many of this account's messages one thread pull may attach.

**tagging.enabled** (boolean, default `false`)
: Write soft tags.

**apply.enabled** (boolean, default `false`)
: Drive non-destructive folder verbs (archive, pending, and so on)
through the folder-move apply. The destructive **deleted** home is
code-denied and cannot be granted.

# AI

The AI surface is a second boundary, independent of **[mcp]**: an
account grants the fields a command may send, and a command declares
what it wants. The intersection leaves the machine.

## [ai.<name>]

One named provider.

**type** (string)
: The wire protocol and vendor default endpoint: **"anthropic"**,
**"openai"**, **"deepseek"**, or **"openrouter"**. An unknown type is a
load error.

**model** (string)
: The model name the provider expects.

**max-tokens** (integer, default `1024`)
: The response budget. Anthropic's protocol requires it.

**base-url** (string, default: the type's endpoint)
: Overrides the vendor endpoint, so any protocol-compatible server
works (a local ollama, a proxy).

**timeout** (integer, default `180`)
: The streaming budget in seconds.

**pass_cmd** (array of strings)
: The argv that prints the API key on stdout. Tokenized at load, never a
shell string; the key is fetched per request, held only for that
request, and never logged. Empty means no authorization header.

## [ai-data.<account>]

**data** (array of strings)
: The thread fields this account permits toward an LLM:
**participants**, **subjects**, **dates**, **count**, **bodies**,
**last_body**, **structure**. Deny-by-default: no entry grants nothing,
and **"*"** as an account name or a field grants everything. This is the
privacy boundary, and it is why an AI command cannot ask for more than
the account allows.

# CRM

## [crm]

**provider** (string, default `""`)
: The active CRM engine. Empty is dormant; **"hubspot"** is the one
vendored today.

## [crm.hubspot]

**ai** (string, default: the first configured **[ai]** entry)
: The provider the workflow's analysis, research, and draft commands run
on.

**token_cmd** (array of strings)
: The argv that prints the portal token on stdout. Tokenized at load,
never a config literal.

**marker_property** (string, default `"notmutt_followed_up"`)
: The contact property recording that a follow-up was sent.

**created_after** (string)
: An RFC 3339 instant; only contacts created after it are considered.
Empty means every unprocessed contact.

**account** (string)
: The mail account whose context file and **[ai-data]** grant the draft
leg uses. Empty resolves from the thread.

# DISPLAY

## [pager]

**default-views** (table of strings)
: Sender domain to view name, resolved when a thread opens. Unmapped
domains get the plain default view.

**image-protocol** (string, default `"sixel"`)
: The terminal image protocol: **"sixel"** or **"kitty"**.

**allow-tracking-images** (boolean, default `false`)
: Permit remote images that are tracking pixels. Off by default: a 1x1
image is blocked.

## [html]

**dark-mode** (string, default `"auto"`)
: How HTML mail colors map onto the theme background: **"auto"** follows
the resolved theme variant, **"on"** and **"off"** override.

## [export]

**paper** (string, default `"a4"`)
: The pager export's PDF page size: **"a4"** or **"letter"**.

# THEMES

## [palette]

Named colors, so a style can say **"base0B"** instead of a hex literal.
Each key is a color name with a **"#rrggbb"** value, or a table of
per-variant overrides:

    [palette]
    red = "#e06c75"
    [palette.light]
    red = "#c0392b"

Resolution order: a style's own hex, then the variant override, then the
base palette. Truecolor only, no 256-color fallback.

## [theme]

**theme.default** (string, default `"dark"`)
: The variant in use at startup - **"dark"** or **"light"**, and the
**[html] dark-mode = "auto"** setting follows it.

**theme.***variant*.***style***
: A style table **{ fg, bg, attrs }** for one variant. **fg** and **bg**
take a palette name or a raw **"#rrggbb"**; **attrs** is a subset of
**"bold"**, **"italic"**, **"underline"**, **"reverse"**. Every field
inherits from **normal** when unset, and a named style merges field by
field over the builtin - naming one style keeps the variant's others.

The style identifiers mirror mutt's color objects: **normal**,
**indicator**, **status**, **progress**, **error**, **tabbar** (with
**active**), **compose.label**, **compose.divider**, **queue.header**,
**index.number**, **index.date**, **index.author**, **index.subject**,
**index.flags**, **index.staged**, **index.ghost**, **index.tree**,
**index.collapsed**, **index.tag**, **pager.header**,
**pager.hdrdefault**, **pager.header-colors** (a rotating list of
styles), **pager.quoted0** through **pager.quoted5**,
**pager.signature**, **pager.attachment**, **pager.recent**, and
**pager.other-side**.

**index.tag** is mixed: its own **fg**, **bg**, and **attrs** are the
default tag-glyph style, and any other key names a per-tag override.

## [schemes]

The keymap: a scheme name, a context, and a key.

    [schemes.vim.index]
    "j" = "cursor-down"
    "K" = { fun = "thread-prev", desc = "Previous thread", show = true }

**schemes.***scheme*.***context***.***key***
: A binding (see **TYPES**). The contexts are **index**, **pager**,
**compose**, and **fuzzy**. The builtin **vim** and **emacs** schemes
carry descriptions for every action; naming a key overrides that
binding and leaves the rest of the scheme intact. An action bound
without a description loses its help text but keeps working.

# FILES

*$XDG_CONFIG_HOME/notmutt/*.toml*
: The config directory, overridable with **NOTMUTT_CONFIG**.

# SEE ALSO

**notmutt**(1), **notmuch-config**(1), **notmuch-search-terms**(7),
**msmtp**(1)
