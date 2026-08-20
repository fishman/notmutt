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
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/yuin/gopher-lua"

	"notmutt/core"
	"notmutt/notmuch"
)

const (
	mcpDeadline           = 60 * time.Second // per-call VM budget; the SetContext kill
	mcpSearchDefaultLimit = 50
	mcpSearchMaxLimit     = 500
	mcpJSONMaxDepth       = 8 // a cyclic Lua table hits the depth cap, not a stack overflow
	mcpJSONMaxNodes       = 10000
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
type mcpToolSpec struct {
	name     string
	desc     string
	schema   []mcp.ToolOption
	chunk    string
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
}

// mcpTools builds the fixed tool registry for a worker: one
// ServerTool per spec, the handler closing over the worker. The
// tool-level description is the WithDescription option (Description is
// the property-level one); it prefixes the property schema options.
func mcpTools(worker workerAPI) []server.ServerTool {
	tools := make([]server.ServerTool, 0, len(mcpToolSpecs))
	for _, spec := range mcpToolSpecs {
		// the whole surface is read-only; annotate it so clients see it
		opts := append([]mcp.ToolOption{
			mcp.WithDescription(spec.desc),
			mcp.WithReadOnlyHintAnnotation(true),
			mcp.WithDestructiveHintAnnotation(false),
		}, spec.schema...)
		tools = append(tools, server.ServerTool{
			Tool:    mcp.NewTool(spec.name, opts...),
			Handler: mcpToolHandler(spec, worker),
		})
	}
	return tools
}

func newMCPServer(worker workerAPI) *server.MCPServer {
	s := server.NewMCPServer("notmutt", "0.1.0", server.WithToolCapabilities(false))
	s.AddTools(mcpTools(worker)...)
	return s
}

// mcpToolHandler is the uniform per-tool handler: validate the args,
// run the chunk in a fresh sandboxed VM, wrap the result. A script
// error is a tool-failure result the client sees, not a transport
// error.
func mcpToolHandler(spec mcpToolSpec, worker workerAPI) server.ToolHandlerFunc {
	return func(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args, err := spec.validate(req)
		if err != nil {
			return mcp.NewToolResultError(err.Error()), nil
		}
		out, err := mcpRunChunk(spec.chunk, args, worker)
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
func mcpRunChunk(chunk string, args map[string]any, worker workerAPI) (any, error) {
	vm, _, cancel, err := newSandboxVM(mcpDeadline)
	if err != nil {
		return nil, err
	}
	defer cancel()
	defer vm.Close()
	ctx := mcpCtxTable(vm, worker)
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

// mcpCtxTable builds the MCP ctx table: ONLY the three read bindings
// over the worker. No tag_add/tag_remove, attach_add, ai_chat,
// picker, prompt, translate, or mail_lines - the projection below is
// the privacy boundary, so a chunk can never widen the surface.
func mcpCtxTable(vm *lua.LState, worker workerAPI) *lua.LTable {
	ctx := vm.NewTable()
	ctx.RawSetString("thread_info", vm.NewFunction(func(L *lua.LState) int {
		tid := L.CheckString(1)
		rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActThread, ThreadID: tid})
		if err != nil || rpl.Err != nil {
			L.RaiseError("thread_info: %v %v", err, rpl.Err)
		}
		tbl := L.NewTable()
		tbl.RawSetString("thread_id", lua.LString(tid))
		tbl.RawSetString("count", lua.LNumber(len(rpl.Msgs)))
		msgs := L.NewTable()
		for _, m := range rpl.Msgs {
			msgs.Append(projectMessage(L, m))
		}
		tbl.RawSetString("messages", msgs)
		L.Push(tbl)
		return 1
	}))
	ctx.RawSetString("search", vm.NewFunction(func(L *lua.LState) int {
		q := L.CheckString(1)
		limit := int(L.OptNumber(2, mcpSearchDefaultLimit))
		if limit < 1 {
			limit = 1
		}
		if limit > mcpSearchMaxLimit {
			limit = mcpSearchMaxLimit
		}
		var rows []core.Message
		rpl, err := worker.Call(notmuch.Action{
			Kind:  notmuch.ActQuery,
			Query: q,
			Limit: limit,
			// the Emit closure only appends to an invocation-local slice
			// (the refresher.changed pattern); it runs on the worker
			// goroutine and never touches the Lua state
			Emit: func(chunk []core.Message) bool {
				rows = append(rows, chunk...)
				return true
			},
		})
		if err != nil || rpl.Err != nil {
			L.RaiseError("search: %v %v", err, rpl.Err)
		}
		tbl := L.NewTable()
		for _, m := range rows {
			tbl.Append(projectMessage(L, m))
		}
		L.Push(tbl)
		return 1
	}))
	ctx.RawSetString("count", vm.NewFunction(func(L *lua.LState) int {
		q := L.CheckString(1)
		rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActCount, Query: q})
		if err != nil || rpl.Err != nil {
			L.RaiseError("count: %v %v", err, rpl.Err)
		}
		L.Push(lua.LNumber(rpl.Count))
		return 1
	}))
	return ctx
}

// projectMessage converts one message to its metadata-only Lua table:
// subject, author, timestamp, tags, references, id, thread_id. NEVER
// Paths or Atts - mail content and filesystem paths stay out of the
// LLM surface (the privacy rule).
func projectMessage(L *lua.LState, m core.Message) *lua.LTable {
	t := L.NewTable()
	t.RawSetString("id", lua.LString(m.ID))
	t.RawSetString("thread_id", lua.LString(m.ThreadID))
	t.RawSetString("timestamp", lua.LNumber(m.Timestamp))
	t.RawSetString("author", lua.LString(m.Author))
	t.RawSetString("subject", lua.LString(m.Subject))
	tags := L.NewTable()
	for _, tag := range m.Tags {
		tags.Append(lua.LString(tag))
	}
	t.RawSetString("tags", tags)
	refs := L.NewTable()
	for _, r := range m.References {
		refs.Append(lua.LString(r))
	}
	t.RawSetString("references", refs)
	return t
}

// luaValue converts a JSON-decoded arg value to a Lua value
// (recursive, depth-capped); the result is the args table for the
// chunk.
func luaValue(vm *lua.LState, v any, depth int) lua.LValue {
	switch x := v.(type) {
	case nil:
		return lua.LNil
	case string:
		return lua.LString(x)
	case bool:
		return lua.LBool(x)
	case float64:
		return lua.LNumber(x)
	case []any:
		if depth >= mcpJSONMaxDepth {
			return lua.LNil
		}
		t := vm.NewTable()
		for _, e := range x {
			t.Append(luaValue(vm, e, depth+1))
		}
		return t
	case map[string]any:
		if depth >= mcpJSONMaxDepth {
			return lua.LNil
		}
		t := vm.NewTable()
		for k, val := range x {
			t.RawSetString(k, luaValue(vm, val, depth+1))
		}
		return t
	}
	return lua.LNil
}

// luaToJSON converts a Lua value to a JSON-able Go value: array
// tables -> []any, map tables -> map[string]any, scalars pass
// through, nil -> nil. Depth- and fan-out-capped (the node counter is
// per call, so concurrent tool calls never share state) - a runaway
// chunk cannot build a JSON bomb; an overrun fails the call.
func luaToJSON(vm *lua.LState, v lua.LValue, depth int, nodes *int) (any, error) {
	if depth >= mcpJSONMaxDepth {
		return nil, fmt.Errorf("lua result too deep (depth %d)", depth)
	}
	switch x := v.(type) {
	case *lua.LTable:
		if x.Len() > 0 && pureArray(x) {
			out := make([]any, 0, x.Len())
			for i := 1; i <= x.Len(); i++ {
				if *nodes++; *nodes > mcpJSONMaxNodes {
					return nil, fmt.Errorf("lua result too large")
				}
				e, err := luaToJSON(vm, x.RawGetInt(i), depth+1, nodes)
				if err != nil {
					return nil, err
				}
				out = append(out, e)
			}
			return out, nil
		}
		out := map[string]any{}
		var convErr error
		x.ForEach(func(k, val lua.LValue) {
			if convErr != nil {
				return
			}
			if *nodes++; *nodes > mcpJSONMaxNodes {
				convErr = fmt.Errorf("lua result too large")
				return
			}
			e, err := luaToJSON(vm, val, depth+1, nodes)
			if err != nil {
				convErr = err
				return
			}
			out[k.String()] = e
		})
		return out, convErr
	case lua.LString:
		return string(x), nil
	case lua.LNumber:
		return float64(x), nil
	case lua.LBool:
		return bool(x), nil
	}
	return nil, nil
}

// pureArray reports whether the table's keys are exactly 1..n (a pure
// array); an empty table is a map by default.
func pureArray(t *lua.LTable) bool {
	n := t.Len()
	valid := true
	t.ForEach(func(k, _ lua.LValue) {
		ki, ok := k.(lua.LNumber)
		if !ok || float64(ki) < 1 || float64(ki) > float64(n) {
			valid = false
		}
	})
	return valid
}

// serveMCP runs the MCP stdio server: the read-only worker (ActOpen
// resolves the DB path via `notmuch config get database.path`, the
// same empty-query resolution), then ServeStdio owns stdin/stdout
// until the client closes the pipe. Nothing else may write stdout.
func serveMCP() error {
	bus := core.NewBus()
	worker := notmuch.NewWorker(bus, notmuch.New(), lockBudget)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go worker.Start(ctx)
	if rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActOpen, Query: ""}); err != nil || rpl.Err != nil {
		return fmt.Errorf("notmuch open: %v %v", err, rpl.Err)
	}
	if err := server.ServeStdio(newMCPServer(worker)); err != nil {
		return fmt.Errorf("mcp: %w", err)
	}
	return nil
}
