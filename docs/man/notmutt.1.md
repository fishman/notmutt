# NOTMUTT 1 notmutt

# NAME

notmutt - asynchronous terminal mail client on notmuch

# SYNOPSIS

**notmutt** [*command*] [*options*]

**notmutt setup**

**notmutt poll** [**--apply**] [**--from** *N* **--to** *N*]

**notmutt attachments** [**--dry-run**] [*query*]

**notmutt mcp**

**notmutt smime-verify** *file* [*ca-file*]

**notmutt lua** [**-t** *thread-id*] [*chunk*]

# DESCRIPTION

**notmutt** is an asynchronous terminal mail client. Notmuch is the single
source of truth: views, filters, and searches are notmuch queries, and the
client keeps no database of its own.

Tags are the logical model, folders the physical storage. A message's
*folder* is a tag like any other, and the seven folder tags form an
exclusive group (see **tag-groups** in
**notmutt-config**(5)): a message has exactly one home. Physical moves
exist only for sync-tool compatibility and are resolved per account by
folder priority.

With no command, **notmutt** starts the TUI. Every key is bound by the
active keymap scheme (**vim** by default, **emacs** available); the built-in
help dialog (**?**) lists the full binding set for the current context with
its description. **docs/usage.md** in the source tree is the longer guide.

# COMMANDS

**setup**
: Detect accounts under the notmuch **database.path** mail root and write
them to **accounts.toml**. Detection is destructive to nothing: it reads
folder names and existing notmuch tags. Contributed detection templates
gated by **[setup] templates** join the built-in set.

**poll**
: Run one classification pass headlessly and exit: **notmuch new**, then
the filter engine over the new delta. Intended for a timer or a mail
hook, and for driving a running client, which picks up the revision
change. **--apply** overrides the **[filter] dry-run** setting for this
run only; the config file is untouched. **--from** *N* **--to** *N*
replays a fixed lastmod window instead of the live delta (both are
required together) - this makes a run reproducible for comparison.

**attachments**
: Categorize and download the attachments of every message matching
*query* (default **\***) into the **[attachments]** folder, headlessly.
Requires a Lua plugin declaring a categorize hook; a build without one
saves nothing and says so. **--dry-run** reports the plan and writes
nothing.

**mcp**
: Serve the Model Context Protocol over stdin/stdout, for an LLM agent
to query the mailbox. See **MCP** below; the data boundary is the
**[mcp]** config section, and it is deny-by-default.

**smime-verify**
: Verify an S/MIME signed message in *file* and print the verdict, then
exit non-zero when verification fails. *ca-file* overrides the
**[crypto] ca-file** trust roots for this run. The check is in-process
(no openssl, no gpgsm) and reports two independent results: whether the
signature is cryptographically valid, and whether the signer's
certificate matches the message's From/Sender header.

**lua**
: Relay a Lua *chunk* to a live **notmutt** session over its per-user
unix socket and print the reply. **-t** *thread-id* gives the chunk a
cursor context (mail lines, tag staging). The chunk is read from
standard input when omitted. Only the process that owns the socket
runs the chunk, so client and server must resolve the same runtime
directory.

# MCP

**notmutt mcp** speaks the Model Context Protocol on stdio. What the
server may see is a config-time decision, not a query-time one, and it is
deny-by-default:

- **Accounts are granted by naming them** in **[mcp.accounts.NAME]**. The
  table's presence is the grant; it covers the account's folder prefix
  and its account tag. Absent, the account is invisible.
- **Tags** (**[mcp] tags** and each account's own) name the reachable
  soft tags. Mail must carry one of them to be visible at all.
- **Capabilities are per account**, each enabled explicitly:
  **attachments** (attachment metadata), **bodies** (cleaned body text,
  with its own pull cap), **tagging** (soft-tag writes), **apply**
  (non-destructive folder verbs: archive, pending, and so on).

An empty **[mcp]** section serves nothing, and a granted account with no
capability table is metadata-only. Out-of-scope message ids are refused
before any file is opened, and the **deleted** home is code-denied - it
cannot be configured into the boundary. Reading mail through this server
is the one path mail content takes toward an LLM; it exists only because
a human configured the grant and an agent asked for specific messages.

# AI AND CRM

The **AI** commands (**[ai]** entries) analyse a selected thread through a
configured provider. They are gated by a second, independent boundary:
**[ai-data.NAME]** lists the fields an account permits (participants,
subjects, dates, count, bodies, last_body, structure), deny-by-default
with no entry. A command declares the fields it wants; the intersection
is what leaves the machine. API keys come from the argv in **type**'s
**pass_cmd** and are held only for the request that uses them.

The **CRM** workflow (**[crm]**) runs a follow-up pass over a provider's
contacts using the configured **AI** entry. It is dormant with an empty
**provider**. The HubSpot token is likewise an argv, never a literal in
the config.

# FILES

*$XDG_CONFIG_HOME/notmutt/*.toml*
: The config directory, overridable with **NOTMUTT_CONFIG**. Every
**.toml** file in it is loaded, merged over the builtin base. See
**notmutt-config**(5).

*$XDG_CACHE_HOME/notmutt/notmutt.log*
: Diagnostics. Message bodies, headers, and passphrases are never
written here.

*$XDG_CACHE_HOME/notmutt/mime-cache.db*
: The derived MIME metadata cache (attachment presence, structure,
sizes), keyed by path, size, and mtime. Deleting it costs one re-parse
pass, nothing else.

*$XDG_DATA_HOME/notmutt/schedule/*
: The scheduled-send spool. Composed bytes wait here until their
delivery time; the client catches up at startup.

*$XDG_RUNTIME_DIR/notmutt/ipc.sock*
: The Lua IPC socket, or the state home when **XDG_RUNTIME_DIR** is
unset.

# ENVIRONMENT

**NOTMUTT_CONFIG**
: Config directory override. Resolved from the environment only, and
deliberately not applied to the IPC socket path, so a client cannot be
split from its server.

**XDG_CONFIG_HOME**, **XDG_DATA_HOME**, **XDG_CACHE_HOME**,
**XDG_STATE_HOME**, **XDG_RUNTIME_DIR**
: Standard base directories, as described in **FILES**.

**LANG**, **LC_MESSAGES**
: Interface language, when **[ui] language** is **auto**.

# SEE ALSO

**notmutt-config**(5), **notmuch**(1), **notmuch-config**(1),
**msmtp**(1)
