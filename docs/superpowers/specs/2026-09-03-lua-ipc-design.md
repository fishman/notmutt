# Lua IPC channel - design spec

`notmutt lua '<chunk>'` hands a Lua chunk to a LIVE notmutt session over a
same-user unix socket; the session runs it on a fresh R8 sandbox VM (the
`:lua` machinery) and returns the print output or the error to the calling
process. Roadmap Tier 2 item 6: the IPC seam was designed when Lua shipped
but the channel itself was never built.

## 1. Goal and acceptance

The channel, only. An external process drives the running client with no TUI
round trip: query the session (counts, searches, thread metadata), read a
named thread's lines, stage tag ops into the R14 buffer for the user to
review and apply. Roadmap names the consumers: the notification-activate
action and external scripting. Click-to-activate wiring in notify.go is NOT
part of this spec - `[notify] command` can already call `notmutt lua`; the
`-t` thread field exists so a future activator can name its message.

Acceptance (scripted tests; item 5 manual):

1. A live session serves `$XDG_RUNTIME_DIR/notmutt/ipc.sock` (fallback
   `$XDG_STATE_HOME/notmutt/`). A second session probes and refuses the busy
   socket instead of stealing it; a stale socket file (a crashed session) is
   removed and re-listened. The socket file is gone when the session exits.
2. `notmutt lua 'print(1 + 1)'` against a live session prints `2` on stdout
   and exits 0. An erroring chunk exits non-zero with the message on stderr.
3. The chunk runs in the R8 sandbox: same lib whitelist
   (`openSandboxLibs`, no os/io/debug), same SetContext deadline kill
   (actionDeadline), a fresh VM per invocation - a chunk cannot touch the
   persistent plugin VMs or another invocation's state.
4. ctx surface: without `-t`, `ctx.search`, `ctx.count`, `ctx.thread_info`
   work (the read-only metadata surface, unconstrained - the session's own
   scope). With `-t THREAD`, `ctx.thread_id` and `ctx.mail_lines()` resolve
   that thread and `tag_add`/`tag_remove` stage ops for it (drain ->
   TagStaged -> the staged buffer, APPLY flushes).
5. Manual: with the TUI open on a real mailbox, `notmutt lua 'print(
   ctx.count("tag:inbox"))'` prints a count; no client running reports
   "no live notmutt client"; a non-owner cannot reach the socket (0700 dir).

## 2. Transport and framing

One connection, one request/response. Request `{thread_id, chunk}` and reply
`{output, err}` are single JSON objects; both sides read until EOF, so no
length prefixing. `chunk` caps at 1 MiB (a `LimitReader` rejection, never an
OOM); the reply is unbounded like `:lua`'s in-memory print capture. The
client dials with a 2s timeout and reads with a 6m deadline (the server's
actionDeadline is 5m - a literal here because the deadline constant is
lua-gated and the client compiles without Lua).

- Socket path is resolved from environment only (never under the config
  dir), so `NOTMUTT_CONFIG` cannot split client and server. Dir is 0700.
- Single-instance: on startup the server probes an existing socket with a
  500ms dial; a live peer means another session owns it (log + skip); a
  failed dial means stale - remove and re-listen. The cleanup goroutine
  closes the listener and removes the file on `ctx.Done()`.
- Same-user is two layers: the 0700 runtime dir is the filesystem boundary
  (no other user traverses XDG_RUNTIME_DIR, 0700 by spec); a Linux
  `SO_PEERCRED` check is defense-in-depth (the vendored x/sys/unix has
  `GetsockoptUcred` on linux only). Other unix builds rely on the dir perms.

## 3. The chunk runner

The runner is the `:lua` command's, extracted so both share one skeleton.
Today `runLuaCommand` (src/app/lua_action.go:215) cuts the `"lua "` prefix,
builds `actionCtx{bus, cfg, worker, tid}`, `ac.newVM(false)` (sandboxed
libs + actionDeadline), `LoadString`, binds `ctx` as a global AND first
arg, `PCall`, `ac.drain()` (publishes AttachFiles/TagStaged), and publishes
LuaResult. The refactor lifts the load/call/drain body into
`runLuaChunk(code, tid, bus, cfg, worker, ctx) (string, error)` where `ctx`
is a builder `func(*actionCtx, *lua.LState) *lua.LTable`. `:lua` keeps its
behavior exactly (ctx = `ctxTable(vm, false)`, then LuaResult). IPC passes a
wider ctx (below) and returns over the socket instead of publishing.

## 4. ctx surface for an IPC chunk

`ctxTable(vm, false)` (thread_id + lazy mail_lines) merged with
`metadataCtxTable(vm, worker, nil, nil)` (search/count/thread_info,
metadata-only projections - the MCP/network surface, unconstrained by a nil
scope). The merge is the existing ctxTable(net) pattern (lua_action.go:
358-360). Key sets are disjoint (ctxTable owns thread_id/mail_lines;
metadataCtxTable owns search/count/thread_info), so no collisions.

The thread id rides the request, optional. Without one: `thread_id` is the
empty string, metadata queries work, `mail_lines()` raises (no thread) and
staged tag ops drain as an empty-thread TagStaged - a script bug (a render
no-op, never a crash); the spec says pass `-t`. The surface is the same
trust level as the user typing `:lua` in their own session: same-uid caller
over a peer-checked socket already owns the notmuch DB, and no path here
leads to an LLM (the privacy rule's aicmd exception is untouched).

## 5. Build gating

- `src/app/lua_ipc.go` - the client and shared framing (socket path,
  structs, cap, `allowPeer`, `luaSend`, `luaOnce`). NO build tag: the
  client relays bytes and needs no Lua. A `!lua` build's `notmutt lua`
  against a `lua` live session works; against a `!lua` session (which never
  listens) it reports no live client - correct in every build.
- `src/app/lua_ipc_server.go` - the listener + handler. `//go:build lua`:
  it runs a VM. The `!lua` wiring stub (`wireLuaIPC`) no-ops, appended to
  lua_plugin_stub.go with the other stubs.
- `src/app/lua_ipc_peer_linux.go` / `lua_ipc_peer_other.go` - the peer
  check, build-split (`linux` does SO_PEERCRED; the rest defer to the dir).

The client is unconditional; `allowPeer(peer uint32) bool` compares against
`os.Getuid()` and is unit-testable without a socket.

## 6. Non-goals

- Headless `notmutt lua` (own bus/worker) when no client runs. MCP's
  `serveMCP`/`mcpRunChunk` already is the headless shape; this channel is
  the live-session one the roadmap names. A no-client run is a clean error.
- Notification-activate wiring, reply streaming, socket reply caps.
- A darwin/bare-`unix` SO_PEERCRED path (unverifiable on this toolchain;
  the dir perms hold). No new dependencies.
