// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build mcp && lua

// The MCP server (Model Context Protocol, stdio): the `notmutt mcp`
// subcommand serves the Lua action layer to LLM clients. The tools are
// a fixed registry of Lua chunks run in a fresh sandboxed VM; the MCP
// ctx table exposes ONLY read bindings (thread_info, search, count) -
// no tag ops, attach, ai_chat, picker, prompt, or mail_lines (the
// "not all of them" restriction). The projection in this file is the
// privacy boundary: metadata only (subject, author, timestamp, tags,
// thread id, message count, references) - mail content, paths, and raw
// headers never cross it.
//
// The data boundary is the [mcp] scope (resolveMCPScope): accounts
// names the folder spaces the server may see (folder prefix AND the
// account tag), tags the soft tags whose mail is reachable. Deny by
// default - empty lists serve nothing; every query is intersected
// with the scope (search/count), every id-addressed read is checked
// per message (thread_info rows, attachments). The scope enforcement
// test is TestMCPScopeEnforcement - never loosened without explicit
// approval (AGENTS.md).
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	lua "github.com/yuin/gopher-lua"

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
// error at statement level).
const (
	mcpThreadInfoChunk = `return function(ctx, args) return ctx.thread_info(args.thread_id) end`
	mcpSearchChunk     = `return function(ctx, args) return ctx.search(args.query, args.limit) end`
	mcpCountChunk      = `return function(ctx, args) return ctx.count(args.query) end`
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
		desc: "Thread summaries for a notmuch query: one row per thread with its subject, author, timestamp, and tags. Metadata only - never message content.",
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
		desc: "The thread count of a notmuch query.",
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

func newMCPServer(worker workerAPI, root string, allow map[string]bool, scope *mcpScope) *server.MCPServer {
	s := server.NewMCPServer("notmutt", "0.1.0", server.WithToolCapabilities(false))
	s.AddTools(mcpTools(worker, root, allow, scope)...)
	return s
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
	// the attachments binding is the MCP-only surface extension,
	// registered here and never in metadataCtxTable: a network-enabled
	// plugin VM (which shares that table) must not see it. It lists
	// one message's attachments by id - name/mime/size, never bytes.
	// The in-scope check gates the file read: a message outside the
	// allowed folder space and tags is refused, not parsed.
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

// serveMCP runs the MCP stdio server: the [mcp] allow list decides
// which gated tools are served, the scope which mail the server may
// see, the read-only worker opens the DB, and ServeStdio owns
// stdin/stdout until the client closes the pipe. Nothing else may
// write stdout.
func serveMCP() error {
	cfg, err := config.Load(configDir())
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	allow, err := resolveMCPAllow(cfg.MCP.Allow)
	if err != nil {
		return err
	}
	scope, err := resolveMCPScope(&cfg)
	if err != nil {
		return err
	}
	root, err := mailRoot()
	if err != nil {
		return fmt.Errorf("mcp: mail root: %w", err)
	}
	bus := core.NewBus()
	worker := notmuch.NewWorker(bus, notmuch.New(), lockBudget)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go worker.Start(ctx)
	if rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActOpen, Query: ""}); err != nil || rpl.Err != nil {
		return fmt.Errorf("notmuch open: %v %v", err, rpl.Err)
	}
	if err := server.ServeStdio(newMCPServer(worker, root, allow, scope)); err != nil {
		return fmt.Errorf("mcp: %w", err)
	}
	return nil
}
