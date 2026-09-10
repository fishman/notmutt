// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build mcp && lua

// The MCP server (Model Context Protocol, stdio): the `notmutt mcp`
// subcommand serves the Lua action layer to LLM clients. The metadata
// tools are a fixed registry of Lua chunks run in a fresh sandboxed VM;
// the MCP ctx table exposes ONLY read bindings (thread_info, search,
// count) - no tag ops, attach, ai_chat, picker, prompt, or mail_lines
// (the "not all of them" restriction). Content-adjacent and write tools
// are capability-gated ([mcp.attachments], [mcp.bodies],
// [mcp.tagging], [mcp.apply]) and implemented as Go handlers over the
// same scope checks - the projection in this file is the privacy
// boundary: metadata only unless a capability explicitly grants more,
// mail paths and raw headers never cross it.
//
// The data boundary is the [mcp] scope (resolveMCPScope): accounts
// names the folder spaces the server may see (folder prefix AND the
// account tag), tags the soft tags whose mail is reachable. Deny by
// default - empty lists serve nothing; every query is intersected
// with the scope (search/count), every id-addressed read is checked
// per message (thread_info rows, attachments, bodies). Tag writes and
// the folder apply are capability-gated and scope-checked per message;
// the destructive deleted-home tag is code-denied, never a config
// grant. The scope enforcement test is TestMCPScopeEnforcement - never
// loosened without explicit approval (AGENTS.md).
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	lua "github.com/yuin/gopher-lua"

	"notmutt/app/aicmd"
	"notmutt/config"
	"notmutt/core"
	"notmutt/mail"
	"notmutt/notmuch"
)

const (
	mcpDeadline = 60 * time.Second // per-call VM budget; the SetContext kill
	// mcpSearchDefaultLimit/mcpSearchMaxLimit live in lua_http.go with
	// metadataCtxTable, which both the MCP tools and network plugins use
)

// The chunks are one function expression each; the leading return makes
// the literal valid top-level Lua (a bare function literal is a syntax
// error at statement level). search and count wrap their scalar into a
// record so every tool result is a record: a client that reads tool
// results as records rejects a bare array (search) or number (count).
const (
	mcpThreadInfoChunk = `return function(ctx, args) return ctx.thread_info(args.thread_id) end`
	mcpSearchChunk     = `return function(ctx, args) return {threads = ctx.search(args.query, args.limit)} end`
	mcpCountChunk      = `return function(ctx, args) return {count = ctx.count(args.query)} end`
)

// mcpToolSpec is one registry entry: the MCP-facing name/description/
// schema, the Lua chunk that implements it, and the arg validation.
// A gated spec is served only when [mcp] allow names it - the default
// surface is the metadata-only three, and content-adjacent tools
// (attachments) must be whitelisted explicitly.
type mcpToolSpec struct {
	name     string
	desc     string
	schema   []mcp.ToolOption
	chunk    string
	gated    bool
	validate func(mcp.CallToolRequest) (map[string]any, error)
}

// mcpToolSpecs is the fixed tool surface. Built-in only: a tool is a
// named chunk over the read bindings, and the registry is the
// allowlist that keeps the server read-only by construction.
var mcpToolSpecs = []mcpToolSpec{
	{
		name: "thread_info",
		desc: "Per-message metadata for one thread: subject, author, timestamp, tags, references, message count. Thread metadata only - never message content.",
		schema: []mcp.ToolOption{
			mcp.WithString("thread_id", mcp.Required(), mcp.Description("The thread id (without the thread: prefix)")),
		},
		chunk: mcpThreadInfoChunk,
		validate: func(req mcp.CallToolRequest) (map[string]any, error) {
			tid := req.GetString("thread_id", "")
			if tid == "" {
				return nil, fmt.Errorf("thread_id is required")
			}
			return map[string]any{"thread_id": tid}, nil
		},
	},
	{
		name: "search",
		desc: "Thread summaries for a notmuch query: one row per thread with its subject, author, timestamp, and tags, returned as {threads: [...]}. Metadata only - never message content.",
		schema: []mcp.ToolOption{
			mcp.WithString("query", mcp.Required(), mcp.Description("A notmuch query, e.g. tag:inbox or from:alpha")),
			mcp.WithNumber("limit", mcp.Description("Max thread rows (1-500, default 50)")),
		},
		chunk: mcpSearchChunk,
		validate: func(req mcp.CallToolRequest) (map[string]any, error) {
			q := req.GetString("query", "")
			if q == "" {
				return nil, fmt.Errorf("query is required")
			}
			limit := req.GetInt("limit", mcpSearchDefaultLimit)
			if limit < 1 {
				limit = 1
			}
			if limit > mcpSearchMaxLimit {
				limit = mcpSearchMaxLimit
			}
			return map[string]any{"query": q, "limit": limit}, nil
		},
	},
	{
		name: "count",
		desc: "The thread count of a notmuch query, returned as {count: N}.",
		schema: []mcp.ToolOption{
			mcp.WithString("query", mcp.Required(), mcp.Description("A notmuch query")),
		},
		chunk: mcpCountChunk,
		validate: func(req mcp.CallToolRequest) (map[string]any, error) {
			q := req.GetString("query", "")
			if q == "" {
				return nil, fmt.Errorf("query is required")
			}
			return map[string]any{"query": q}, nil
		},
	},
	{
		name: "attachments",
		desc: "The attachment list of one message: name, mime, and size per attachment. Attachment metadata only - bytes never cross. Gated: served only when [mcp] allow names it.",
		schema: []mcp.ToolOption{
			mcp.WithString("id", mcp.Required(), mcp.Description("The message id (the id field of thread_info/search results)")),
		},
		chunk: mcpAttachmentsChunk,
		gated: true,
		validate: func(req mcp.CallToolRequest) (map[string]any, error) {
			id := req.GetString("id", "")
			if id == "" {
				return nil, fmt.Errorf("id is required")
			}
			return map[string]any{"id": id}, nil
		},
	},
}

// The attachments chunk calls the MCP-only ctx binding (registered in
// mcpRunChunk, never in the shared metadataCtxTable - network-enabled
// plugin VMs must not see it).
const mcpAttachmentsChunk = `return function(ctx, args) return ctx.attachments(args.id) end`

// mcpTools builds the tool registry for a worker and mail root: one
// ServerTool per spec, the handler closing over the worker. A gated
// spec is served only when allow names it (the [mcp] allow list); the
// scope is the data boundary every handler enforces.
func mcpTools(worker workerAPI, root string, allow map[string]bool, scope *mcpScope) []server.ServerTool {
	tools := make([]server.ServerTool, 0, len(mcpToolSpecs))
	for _, spec := range mcpToolSpecs {
		if spec.gated && !allow[spec.name] {
			continue
		}
		// the whole surface is read-only; annotate it so clients see it
		opts := append([]mcp.ToolOption{
			mcp.WithDescription(spec.desc),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
		}, spec.schema...)
		tools = append(tools, server.ServerTool{
			Tool:    mcp.NewTool(spec.name, opts...),
			Handler: mcpToolHandler(spec, worker, root, scope),
		})
	}
	return tools
}

// mcpToolHandler is the uniform per-tool handler: validate the args,
// run the chunk in a fresh sandboxed VM, wrap the result. A script
// error is a tool-failure result the client sees, not a transport
// error.
func mcpToolHandler(spec mcpToolSpec, worker workerAPI, root string, scope *mcpScope) server.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := spec.validate(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		out, err := mcpRunChunk(spec.chunk, args, worker, root, scope)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		text, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return nil, err
		}
		// the structured content is the machine-readable result; the
		// text fallback carries the same JSON so text-only clients see it
		return mcp.NewToolResultStructured(out, string(text)), nil
	}
}

// mcpRunChunk runs one tool chunk in a fresh sandboxed VM: the
// read-only ctx table, the args table, the mcpDeadline kill. The
// chunk receives (ctx, args) and its first return value is the tool
// result. Mirrors runLuaCommand's LoadString + SetGlobal + push +
// PCall (lua_action.go).
func mcpRunChunk(chunk string, args map[string]any, worker workerAPI, root string, scope *mcpScope) (any, error) {
	vm, _, cancel, err := newSandboxVM(mcpDeadline)
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer vm.Close()
	ctx := metadataCtxTable(vm, worker, scope, nil)
	// the attachments binding is the MCP-only surface extension: it
	// lists one message's attachments by id - name/mime/size, never
	// bytes, and the in-scope check gates the file read (a message
	// outside the allowed folder space and tags is refused, not parsed).
	ctx.RawSetString("attachments", vm.NewFunction(func(L *lua.LState) int {
		id := L.CheckString(1)
		rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActSnapshots, Paths: []string{id}})
		if err != nil || rpl.Err != nil {
			L.RaiseError("attachments: %v %v", err, rpl.Err)
		}
		if len(rpl.Msgs) == 0 {
			L.RaiseError("attachments: no such message %q", id)
		}
		if !scope.inScope(rpl.Msgs[0]) {
			L.RaiseError("attachments: message %q is not in the allowed mcp scope", id)
		}
		tbl := L.NewTable()
		for _, p := range rpl.Msgs[0].Paths {
			msg, err := mail.ParseMessage(absMailPath(root, p))
			if err != nil {
				L.RaiseError("attachments: %v", err)
			}
			for _, a := range msg.Attachments {
				row := L.NewTable()
				row.RawSetString("name", lua.LString(a.Name))
				row.RawSetString("mime", lua.LString(a.MimeType))
				row.RawSetString("size", lua.LNumber(a.Size))
				tbl.Append(row)
			}
		}
		L.Push(tbl)
		return 1
	}))
	vm.SetGlobal("ctx", ctx)
	vm.SetGlobal("args", luaValue(vm, args, 0))
	fn, err := vm.LoadString(chunk)
	if err != nil {
		return nil, err
	}
	// the chunk is `return function(ctx, args) ... end`, so one call
	// yields the tool function and a second call runs it with (ctx, args)
	vm.Push(fn)
	if err := vm.PCall(0, 1, nil); err != nil {
		return nil, err
	}
	vm.Push(vm.Get(-1))
	vm.Push(ctx)
	vm.Push(vm.GetGlobal("args"))
	if err := vm.PCall(2, 1, nil); err != nil {
		return nil, err
	}
	nodes := 0
	out, err := luaToJSON(vm, vm.Get(-1), 0, &nodes)
	vm.Pop(1)
	return out, err
}

// mcpDeletedHome is the deleted-home folder tag. An ADD of it is
// code-denied for MCP regardless of [mcp.apply]: deletion is the one
// destructive move an agent never performs, and a config edit must not
// enable it. "deleted" is the canonical home in this client (the mover
// sends it to the Trash folders); a renamed home tag is out of the
// deny's scope.
const mcpDeletedHome = "deleted"

// mcpBodiesTool is the thread_bodies capability tool ([mcp.bodies]):
// the cleaned body text (aicmd's shared cleaner) of the thread's
// in-scope messages, newest first, per-message aicmd.BodyCap and at
// most cfg.MCP.Bodies.PullCap() rows. The in-scope gate runs before
// any file open - an out-of-scope message is dropped, never read. No
// headers cross: body text only, id/subject tie each row to the
// metadata thread_info already serves.
func mcpBodiesTool(worker workerAPI, root string, cfg config.Config, scope *mcpScope) server.ServerTool {
	opts := []mcp.ToolOption{
		mcp.WithDescription("The readable text of a thread's messages (quoted/signature/html markup dropped, an html-only body rendered to text and tables), newest first, capped. No headers, no attachments. Gated: served only when [mcp.bodies] is enabled; an out-of-scope message is never read."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("thread_id", mcp.Required(), mcp.Description("The thread id (without the thread: prefix)")),
	}
	return server.ServerTool{
		Tool: mcp.NewTool("thread_bodies", opts...),
		Handler: func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			tid := req.GetString("thread_id", "")
			if tid == "" {
				return mcp.NewToolResultError("thread_id is required"), nil
			}
			rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActThread, ThreadID: tid})
			if err != nil || rpl.Err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("thread_bodies: %v %v", err, rpl.Err)), nil
			}
			var rows []core.Message
			for _, m := range rpl.Msgs {
				if scope.inScope(m) {
					rows = append(rows, m)
				}
			}
			// newest first: the pull serves the tail the agent is answering
			sort.Slice(rows, func(i, j int) bool { return rows[i].Timestamp > rows[j].Timestamp })
			capN := cfg.MCP.Bodies.PullCap()
			if len(rows) > capN {
				rows = rows[:capN]
			}
			msgs := make([]map[string]any, 0, len(rows))
			for _, m := range rows {
				// the cleaner reads m.Paths[0]; notmuch reports absolute
				// filenames, absMailPath passes them through (a relative
				// path would be joined for the fake-worker tests)
				p := m.Paths[0]
				mm := m
				mm.Paths = []string{absMailPath(root, p)}
				msgs = append(msgs, map[string]any{
					"id":      m.ID,
					"subject": m.Subject,
					"body":    aicmd.BodyText(mm, aicmd.BodyCap),
				})
			}
			out := map[string]any{"count": len(msgs), "messages": msgs}
			text, _ := json.MarshalIndent(out, "", "  ")
			return mcp.NewToolResultStructured(out, string(text)), nil
		},
	}
}

// mcpTagTool is the tag capability tool ([mcp.tagging] for soft tags,
// [mcp.apply] for folder verbs): add/remove tags on the messages of one
// thread (or one message), in-scope members only. Every op routes
// through the shared apply path (execApply) - soft ops land as plain
// ActTag, a folder verb (a tag that is a folder-group member, e.g.
// archive or pending) resolves and executes its physical move first.
// The deleted-home tag can never be added. Capabilities are checked
// per class before anything is touched: a soft-tag op needs tagging, a
// folder verb needs apply, +deleted is code-denied - an unpermitted op
// refuses the whole call.
func mcpTagTool(worker workerAPI, root string, cfg config.Config, scope *mcpScope) server.ServerTool {
	opts := []mcp.ToolOption{
		mcp.WithDescription("Add or remove tags on one thread's in-scope messages (or one message). Soft tags need [mcp.tagging]; folder verbs (archive, pending, inbox, ...) need [mcp.apply] and move the mail. The deleted-home tag is code-denied. Only in-scope messages are touched."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("thread_id", mcp.Description("The thread id (without the thread: prefix); exactly one of thread_id or id")),
		mcp.WithString("id", mcp.Description("A message id (the id field of thread_info/search results); exactly one of thread_id or id")),
		mcp.WithArray("add", mcp.WithStringItems(), mcp.Description("Tags to add")),
		mcp.WithArray("remove", mcp.WithStringItems(), mcp.Description("Tags to remove")),
	}
	return server.ServerTool{
		Tool: mcp.NewTool("tag", opts...),
		Handler: func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			threadID := req.GetString("thread_id", "")
			msgID := req.GetString("id", "")
			if (threadID == "") == (msgID == "") {
				return mcp.NewToolResultError("exactly one of thread_id or id is required"), nil
			}
			var ops []core.TagOp
			for _, t := range req.GetStringSlice("add", nil) {
				ops = append(ops, core.TagOp{Tag: t, Add: true})
			}
			for _, t := range req.GetStringSlice("remove", nil) {
				ops = append(ops, core.TagOp{Tag: t})
			}
			if len(ops) == 0 {
				return mcp.NewToolResultError("at least one add or remove is required"), nil
			}
			for _, op := range ops {
				if op.Tag == "" || strings.ContainsAny(op.Tag, " \t()\"") {
					return mcp.NewToolResultError(fmt.Sprintf("invalid tag %q", op.Tag)), nil
				}
			}
			groups := cfg.TagGroupList()
			// capability gate per class, before any message is touched
			for _, op := range ops {
				folder := folderTag(op.Tag, groups)
				if op.Add && op.Tag == mcpDeletedHome {
					return mcp.NewToolResultError("tag: the deleted-home tag is code-denied for mcp"), nil
				}
				if folder {
					if !cfg.MCP.Apply.Enabled {
						return mcp.NewToolResultError(fmt.Sprintf("tag: %q is a folder verb; enable [mcp.apply] to move mail", op.Tag)), nil
					}
					continue
				}
				if !cfg.MCP.Tagging.Enabled {
					return mcp.NewToolResultError(fmt.Sprintf("tag: %q is a soft tag; enable [mcp.tagging] to write it", op.Tag)), nil
				}
			}
			// resolve and apply per in-scope message id (never a t:thread
			// ActTag, which would sweep out-of-scope members)
			applied, err := mcpApply(worker, root, cfg, scope, groups, threadID, msgID, ops, "tag")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			out := map[string]any{"applied": applied}
			text, _ := json.MarshalIndent(out, "", "  ")
			return mcp.NewToolResultStructured(out, string(text)), nil
		},
	}
}

// mcpApply resolves the op target - one message id or the thread's
// in-scope messages - and applies ops per in-scope message id through
// the shared apply path (execApply). Per-message, never a t:thread
// ActTag: an out-of-scope thread member is never swept. Returns the
// applied message ids; verb names the error text.
func mcpApply(worker workerAPI, root string, cfg config.Config, scope *mcpScope, groups []core.TagGroup, threadID, msgID string, ops []core.TagOp, verb string) ([]string, error) {
	var targets []core.Message
	if msgID != "" {
		rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActSnapshots, Paths: []string{msgID}})
		if err != nil || rpl.Err != nil {
			return nil, fmt.Errorf("%s: %v %v", verb, err, rpl.Err)
		}
		if len(rpl.Msgs) == 0 {
			return nil, fmt.Errorf("%s: no such message %q", verb, msgID)
		}
		if !scope.inScope(rpl.Msgs[0]) {
			return nil, fmt.Errorf("%s: message %q is not in the allowed mcp scope", verb, msgID)
		}
		targets = rpl.Msgs
	} else {
		rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActThread, ThreadID: threadID})
		if err != nil || rpl.Err != nil {
			return nil, fmt.Errorf("%s: %v %v", verb, err, rpl.Err)
		}
		for _, m := range rpl.Msgs {
			if scope.inScope(m) {
				targets = append(targets, m)
			}
		}
		if len(targets) == 0 {
			return nil, fmt.Errorf("%s: no message of thread %q is in the allowed mcp scope", verb, threadID)
		}
	}
	// no views and no bus: the MCP server has no live surface, so the
	// shared seam lands the write and skips the row reconcile
	env := applyEnv{worker: worker, cfg: cfg, root: root, groups: groups}
	var applied []string
	for _, m := range targets {
		if !scope.inScope(m) {
			continue
		}
		_, resolved := core.ResolveOps(m.Tags, ops, groups)
		if len(resolved) == 0 {
			continue // net no-op: tags already at the target state
		}
		if err := env.execApply(m.ID, resolved); err != nil {
			return nil, fmt.Errorf("%s %s: %v", verb, m.ID, err)
		}
		applied = append(applied, m.ID)
	}
	return applied, nil
}

// mcpMarkReadTool is the mark_read action ([mcp.tagging]): removes the
// unread soft tag on one thread's in-scope messages (or one message) so
// a later folder apply archives without carrying the unread flag.
func mcpMarkReadTool(worker workerAPI, root string, cfg config.Config, scope *mcpScope) server.ServerTool {
	opts := []mcp.ToolOption{
		mcp.WithDescription("Mark one thread's in-scope messages (or one message) as read: removes the unread tag. A soft-tag write, needs [mcp.tagging]; out-of-scope messages are never touched. Archive separately (add the archive folder tag) once read."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("thread_id", mcp.Description("The thread id (without the thread: prefix); exactly one of thread_id or id")),
		mcp.WithString("id", mcp.Description("A message id (the id field of thread_info/search results); exactly one of thread_id or id")),
	}
	return server.ServerTool{
		Tool: mcp.NewTool("mark_read", opts...),
		Handler: func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			threadID := req.GetString("thread_id", "")
			msgID := req.GetString("id", "")
			if (threadID == "") == (msgID == "") {
				return mcp.NewToolResultError("exactly one of thread_id or id is required"), nil
			}
			if !cfg.MCP.Tagging.Enabled {
				return mcp.NewToolResultError("mark_read is a soft-tag write; enable [mcp.tagging]"), nil
			}
			applied, err := mcpApply(worker, root, cfg, scope, cfg.TagGroupList(), threadID, msgID, []core.TagOp{{Tag: "unread"}}, "mark_read")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			out := map[string]any{"applied": applied}
			text, _ := json.MarshalIndent(out, "", "  ")
			return mcp.NewToolResultStructured(out, string(text)), nil
		},
	}
}

// mcpApplyTool is the apply action ([mcp.apply]): performs one folder
// verb - a member of an exclusive folder tag group (archive, pending,
// inbox, ...) - on one thread's in-scope messages or one message. The
// physical move resolves before the tag lands; group exclusivity drops
// the previous home tag; soft tags (unread included) ride through; the
// deleted-home tag is code-denied.
func mcpApplyTool(worker workerAPI, root string, cfg config.Config, scope *mcpScope) server.ServerTool {
	opts := []mcp.ToolOption{
		mcp.WithDescription("Move one thread's in-scope messages (or one message) to a folder home by applying a folder verb - a member of an exclusive folder tag group (archive, pending, inbox, ...). Needs [mcp.apply]; the physical move resolves before the tag lands and the previous home tag is dropped; soft tags (unread included) ride through; the deleted-home tag is code-denied. Mark read separately via mark_read."),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithString("thread_id", mcp.Description("The thread id (without the thread: prefix); exactly one of thread_id or id")),
		mcp.WithString("id", mcp.Description("A message id (the id field of thread_info/search results); exactly one of thread_id or id")),
		mcp.WithString("action", mcp.Required(), mcp.Description("The folder tag to move to (a member of the exclusive folder group)")),
	}
	return server.ServerTool{
		Tool: mcp.NewTool("apply", opts...),
		Handler: func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			action := req.GetString("action", "")
			threadID := req.GetString("thread_id", "")
			msgID := req.GetString("id", "")
			if (threadID == "") == (msgID == "") {
				return mcp.NewToolResultError("exactly one of thread_id or id is required"), nil
			}
			if !cfg.MCP.Apply.Enabled {
				return mcp.NewToolResultError("apply needs [mcp.apply]; it is not enabled"), nil
			}
			groups := cfg.TagGroupList()
			if action == mcpDeletedHome {
				return mcp.NewToolResultError("apply: the deleted-home tag is code-denied for mcp"), nil
			}
			if action == "" || strings.ContainsAny(action, " \t()\"") || !folderTag(action, groups) {
				return mcp.NewToolResultError(fmt.Sprintf("apply: %q is not a folder verb (a member of the exclusive folder tags)", action)), nil
			}
			applied, err := mcpApply(worker, root, cfg, scope, groups, threadID, msgID, []core.TagOp{{Tag: action, Add: true}}, "apply")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			out := map[string]any{"applied": applied}
			text, _ := json.MarshalIndent(out, "", "  ")
			return mcp.NewToolResultStructured(out, string(text)), nil
		},
	}
}

// folderTag reports whether tag is a folder-group member: any exclusive
// group's member is a folder tag (R2) - touching one is a folder verb,
// the class that moves mail. The hard-tag list comes from the config's
// tag groups, never a copy kept by the mcp server.
func folderTag(tag string, groups []core.TagGroup) bool {
	for _, g := range groups {
		if groupMember(g, tag) {
			return true
		}
	}
	return false
}

// resolveMCPAllow maps the config's [mcp] allow names to the served
// set; an unknown name is an error - a typo fails loudly instead of
// silently serving fewer tools.
func resolveMCPAllow(allow []string) (map[string]bool, error) {
	known := map[string]bool{}
	for _, spec := range mcpToolSpecs {
		known[spec.name] = true
	}
	out := map[string]bool{}
	for _, name := range allow {
		if !known[name] {
			return nil, fmt.Errorf("mcp: unknown allowed tool %q", name)
		}
		out[name] = true
	}
	return out, nil
}

// resolveMCPScope maps the config's [mcp] accounts and tags to the
// server's data boundary: each account grants its folder space AND
// its account tag (folder:/^<name>\// AND tag:<name>), each tag the
// soft-tag membership. An unknown account name is an error (a typo
// fails loudly), a read-only account is an error (its mail carries no
// account tag, so the scope would silently match nothing), and a tag
// that could break out of the query term is an error. Empty accounts
// or tags yield a deny-all scope - the default posture serves
// nothing.
func resolveMCPScope(cfg *config.Config) (*mcpScope, error) {
	if len(cfg.MCP.Accounts) == 0 || len(cfg.MCP.Tags) == 0 {
		return &mcpScope{}, nil
	}
	scope := &mcpScope{}
	for _, name := range cfg.MCP.Accounts {
		a, ok := cfg.Accounts[name]
		if !ok {
			return nil, fmt.Errorf("mcp: unknown account %q in accounts", name)
		}
		if a.ReadOnly {
			return nil, fmt.Errorf("mcp: account %q is readonly - it carries no account tag, the scope would never match", name)
		}
		scope.folders = append(scope.folders, a.Tag(name))
	}
	for _, t := range cfg.MCP.Tags {
		if t == "" || strings.ContainsAny(t, " \t()\"") {
			return nil, fmt.Errorf("mcp: invalid allowed tag %q", t)
		}
		scope.tags = append(scope.tags, t)
	}
	// the account tag IS the folder name (Account.Tag), so the regex
	// is QuoteMeta'd but the tag term stays literal - the tag side
	// pins the identity exactly
	acct := make([]string, len(scope.folders))
	for i, f := range scope.folders {
		acct[i] = fmt.Sprintf("(folder:/^%s\\// AND tag:%s)", regexp.QuoteMeta(f), f)
	}
	tags := make([]string, len(scope.tags))
	for i, t := range scope.tags {
		tags[i] = "tag:" + t
	}
	scope.query = "(" + strings.Join(acct, " OR ") + ") AND (" + strings.Join(tags, " OR ") + ")"
	return scope, nil
}

// mcpCfgTools builds the served tool surface from the [mcp] capability
// tables (the serveMCP path): the metadata three always, attachments
// when [mcp.attachments], thread_bodies when [mcp.bodies], the tag tool
// when [mcp.tagging] or [mcp.apply] grants a write class, mark_read
// when [mcp.tagging], and the apply folder-verb tool when [mcp.apply].
// The capability tools are Go handlers (scope-checked per message, the
// deleted-home deny code-level) appended over mcpTools' chunk registry.
func mcpCfgTools(worker workerAPI, root string, cfg config.Config, scope *mcpScope) []server.ServerTool {
	allow := map[string]bool{}
	if cfg.MCP.Attachments.Enabled {
		allow["attachments"] = true
	}
	tools := mcpTools(worker, root, allow, scope)
	if cfg.MCP.Bodies.Enabled {
		tools = append(tools, mcpBodiesTool(worker, root, cfg, scope))
	}
	if cfg.MCP.Tagging.Enabled || cfg.MCP.Apply.Enabled {
		tools = append(tools, mcpTagTool(worker, root, cfg, scope))
	}
	if cfg.MCP.Tagging.Enabled {
		tools = append(tools, mcpMarkReadTool(worker, root, cfg, scope))
	}
	if cfg.MCP.Apply.Enabled {
		tools = append(tools, mcpApplyTool(worker, root, cfg, scope))
	}
	return tools
}

func newMCPCfgServer(worker workerAPI, root string, cfg config.Config, scope *mcpScope) *server.MCPServer {
	s := server.NewMCPServer("notmutt", "0.1.0", server.WithToolCapabilities(false))
	s.AddTools(mcpCfgTools(worker, root, cfg, scope)...)
	return s
}

// serveMCP runs the MCP stdio server: the [mcp] capability tables
// decide which gated tools are served, the scope which mail the server
// may see, the read-only worker opens the DB, and ServeStdio owns
// stdin/stdout until the client closes the pipe. Nothing else may
// write stdout.
func serveMCP() error {
	cfg, err := config.Load(configDir())
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	scope, err := resolveMCPScope(&cfg)
	if err != nil {
		return err
	}
	root, err := mailRoot()
	if err != nil {
		return fmt.Errorf("mcp: mail root: %w", err)
	}
	// notmuch reports absolute filenames; the scope's folder pin matches
	// the root-relative form, so it needs the same root the worker opened
	scope.root = root
	bus := core.NewBus()
	worker := notmuch.NewWorker(bus, notmuch.New(), lockBudget)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go worker.Start(ctx)
	if rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActOpen, Query: ""}); err != nil || rpl.Err != nil {
		return fmt.Errorf("notmuch open: %v %v", err, rpl.Err)
	}
	if err := server.ServeStdio(newMCPCfgServer(worker, root, cfg, scope)); err != nil {
		return fmt.Errorf("mcp: %w", err)
	}
	return nil
}
