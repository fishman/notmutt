// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build mcp && lua

package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
	"github.com/mark3labs/mcp-go/server"
	"github.com/yuin/gopher-lua"

	"notmutt/config"
	"notmutt/core"
)

func mcpFixture() *fakeWorker {
	// alpha carries two messages (thread_info), beta one (search rows);
	// alpha-m1's Paths field pins the privacy projection - a path must
	// never cross into a tool result
	fw := &fakeWorker{}
	fw.setStubs([]core.Message{
		{ID: "m1", ThreadID: "alpha", Timestamp: 1755400000, Author: "sender@example.com",
			Subject: "quarterly report", Tags: []string{"inbox", "work"},
			References: []string{"<parent@example.com>"}, Paths: []string{"/home/alpha/mail/cur/1"}},
		{ID: "m3", ThreadID: "beta", Timestamp: 1755410000, Author: "atlas@example.com",
			Subject: "status update", Tags: []string{"inbox"}},
	})
	fw.setThreadMsgs(map[string][]core.Message{
		"alpha": {
			{ID: "m1", ThreadID: "alpha", Timestamp: 1755400000, Author: "sender@example.com",
				Subject: "quarterly report", Tags: []string{"inbox", "work"},
				References: []string{"<parent@example.com>"}, Paths: []string{"/home/alpha/mail/cur/1"}},
			{ID: "m2", ThreadID: "alpha", Timestamp: 1755403600, Author: "atlas@example.com",
				Subject: "Re: quarterly report", Tags: []string{"inbox"},
				References: []string{"<m1@example.com>"}},
		},
	})
	return fw
}

// callTool runs the named tool's handler directly (the registry handler
// is the seam; the stdio framing is exercised by TestMCPServerStdio).
func callTool(t *testing.T, tools []server.ServerTool, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	for _, st := range tools {
		if st.Tool.Name != name {
			continue
		}
		res, err := st.Handler(context.Background(), mcp.CallToolRequest{
			Params: mcp.CallToolParams{Name: name, Arguments: args},
		})
		if err != nil {
			t.Fatalf("%s: handler error: %v", name, err)
		}
		return res
	}
	t.Fatalf("tool %q not in the registry", name)
	return nil
}

// TestMCPToolExecution: thread_info carries per-message metadata, search
// one row per thread summary, count the thread total. Every result must
// pass the privacy pin - no paths, no bodies, no header text beyond the
// projected fields.
func TestMCPToolExecution(t *testing.T) {
	fw := mcpFixture()
	tools := mcpTools(fw, "", nil, nil)

	info := callTool(t, tools, "thread_info", map[string]any{"thread_id": "alpha"})
	infoJSON, _ := json.MarshalIndent(info.StructuredContent, "", "  ")
	got := string(infoJSON)
	for _, want := range []string{`"count": 2`, `"quarterly report"`, `"Re: quarterly report"`, `"atlas@example.com"`, "m1@example.com", `"work"`} {
		if !strings.Contains(got, want) {
			t.Errorf("thread_info result missing %s: %s", want, got)
		}
	}

	// every tool result is a record - the client reads results as such,
	// a bare array (search) or number (count) is malformed to it
	res := callTool(t, tools, "search", map[string]any{"query": "tag:inbox", "limit": 5})
	rowsObj, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("search must return a record, got %T", res.StructuredContent)
	}
	rowsJSON, _ := json.MarshalIndent(rowsObj["threads"], "", "  ")
	rows := string(rowsJSON)
	for _, want := range []string{`"quarterly report"`, `"status update"`, `"sender@example.com"`} {
		if !strings.Contains(rows, want) {
			t.Errorf("search result missing %s: %s", want, rows)
		}
	}

	cnt := callTool(t, tools, "count", map[string]any{"query": "*"})
	cntObj, ok := cnt.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("count must return a record, got %T", cnt.StructuredContent)
	}
	if n, _ := cntObj["count"].(float64); n != 2 {
		t.Errorf("count result = %v, want 2", cntObj["count"])
	}

	// the privacy pin: the Paths field and path values must never reach a
	// result (the projection is the boundary)
	for name, res := range map[string]*mcp.CallToolResult{
		"thread_info": info,
		"search":      res,
	} {
		b, _ := json.Marshal(res)
		s := string(b)
		if strings.Contains(s, "Paths") || strings.Contains(s, "/home/alpha") {
			t.Errorf("%s leaked a filesystem path: %s", name, s)
		}
	}

	// required args are enforced by the spec validation
	for _, bad := range []map[string]any{{}, {"thread_id": ""}} {
		r := callTool(t, tools, "thread_info", bad)
		if r.IsError != true {
			t.Errorf("thread_info with args %v must error, got %v", bad, r)
		}
	}
}

// TestMCPRestrictedSurface: the MCP VM has only the read bindings - the
// write/interactive globals and mail_lines are absent, a chunk touching
// any of them fails, and the ctx table exposes exactly
// thread_info/search/count.
func TestMCPRestrictedSurface(t *testing.T) {
	fw := mcpFixture()
	// the chunks parse (return function(...)) and must fail at CALL time -
	// the write/interactive globals and mail_lines are absent from the
	// MCP sandbox
	for _, chunk := range []string{
		`return function(ctx, args) tag_add("work") end`,
		`return function(ctx, args) tag_remove("unread") end`,
		`return function(ctx, args) attach_add("/tmp/x") end`,
		`return function(ctx, args) return ctx.mail_lines() end`,
		`return function(ctx, args) ai_chat("p", {text = "x"}) end`,
		`return function(ctx, args) prompt("y") end`,
	} {
		if _, err := mcpRunChunk(chunk, nil, fw, "", nil); err == nil {
			t.Fatalf("chunk %q must fail in the MCP sandbox", chunk)
		}
	}

	vm, _, cancel, err := newSandboxVM(mcpDeadline)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	defer vm.Close()
	keys := map[string]bool{}
	metadataCtxTable(vm, fw, nil, nil).ForEach(func(k, _ lua.LValue) { keys[k.String()] = true })
	for _, want := range []string{"thread_info", "search", "count"} {
		if !keys[want] {
			t.Errorf("ctx table missing binding %s", want)
		}
	}
	if len(keys) != 3 {
		t.Errorf("ctx table exposes %d bindings, want exactly the 3 read ones", len(keys))
	}
}

// TestMCPAttachmentsGate: the attachments tool is absent from the
// default registry and served only when [mcp] allow names it. Served,
// it lists a real message's attachments (name, mime, size - never
// bytes) with the mail-root join for relative paths.
func TestMCPAttachmentsGate(t *testing.T) {
	root := t.TempDir()
	fixtureMail(t, root, "m1.eml", "hotel invoice", "Delta <delta@example.com>", "invoice.pdf", time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local))
	fw := &fakeWorker{}
	fw.setMsgs([]core.Message{{ID: "m1", Paths: []string{"m1.eml"}}})

	// default: not in the registry
	names := map[string]bool{}
	for _, st := range mcpTools(fw, root, nil, nil) {
		names[st.Tool.Name] = true
	}
	if names["attachments"] {
		t.Fatal("the attachments tool must be gated off by default")
	}

	// whitelisted: served, and the result is metadata only
	tools := mcpTools(fw, root, map[string]bool{"attachments": true}, nil)
	res := callTool(t, tools, "attachments", map[string]any{"id": "m1"})
	b, _ := json.Marshal(res)
	s := string(b)
	for _, want := range []string{`"invoice.pdf"`, `"application/pdf"`, `"photo.png"`, `"image/png"`} {
		if !strings.Contains(s, want) {
			t.Errorf("attachments result missing %s: %s", want, s)
		}
	}
	if strings.Contains(s, "fake pdf bytes") || strings.Contains(s, "fake png bytes") {
		t.Errorf("attachment bytes must never cross: %s", s)
	}

	// an unknown allow name is a startup error, never a silent drop
	if _, err := resolveMCPAllow([]string{"attachments"}); err != nil {
		t.Fatalf("attachments must be a known name: %v", err)
	}
	if _, err := resolveMCPAllow([]string{"bogus"}); err == nil {
		t.Fatal("an unknown allow name must error")
	}
}

// scopeFixture is the [mcp] boundary fixture: a gmail account (allowed)
// plus the readonly atlas account (rejected as allowed), messages across
// both folder spaces, and a thread mixing in- and out-of-scope messages.
func scopeFixture() *fakeWorker {
	fw := &fakeWorker{}
	fw.setStubs([]core.Message{
		{ID: "gm1", ThreadID: "gt", Timestamp: 1755400000, Author: "sender@example.com",
			Subject: "gmail inbox report", Tags: []string{"gmail", "inbox"}, Paths: []string{"gmail/Inbox/cur/1"}},
		{ID: "om1", ThreadID: "ot", Timestamp: 1755405000, Author: "gamma@example.com",
			Subject: "outlook inbox mail", Tags: []string{"outlook", "inbox"}, Paths: []string{"outlook/Inbox/cur/4"}},
	})
	fw.setThreadMsgs(map[string][]core.Message{
		"gt": {
			{ID: "gm1", ThreadID: "gt", Timestamp: 1755400000, Author: "sender@example.com",
				Subject: "gmail inbox report", Tags: []string{"gmail", "inbox"}, Paths: []string{"gmail/Inbox/cur/1"}},
			{ID: "gm2", ThreadID: "gt", Timestamp: 1755403600, Author: "alpha@example.com",
				Subject: "gmail sent note", Tags: []string{"gmail", "sent"}, Paths: []string{"gmail/Sent/cur/2"}},
			{ID: "gm3", ThreadID: "gt", Timestamp: 1755404000, Author: "beta@example.com",
				Subject: "gmail work mail", Tags: []string{"gmail", "work"}, Paths: []string{"gmail/Inbox/cur/3"}},
		},
	})
	return fw
}

func scopeConfig() *config.Config {
	return &config.Config{Accounts: map[string]config.Account{
		"gmail":   {},
		"outlook": {},
		"atlas":   {ReadOnly: true},
	}}
}

// TestMCPScopeEnforcement is the LOCKED correctness test for the MCP
// data boundary ([mcp] accounts + tags): the resolver's deny-by-default
// and validation errors; the query intersection on search/count; the
// per-message projection gate on thread_info; the file-read gate on
// attachments. AGENTS.md forbids loosening or removing it without
// explicit user approval - it is the enforcement proof of the boundary.
func TestMCPScopeEnforcement(t *testing.T) {
	cfg := scopeConfig()

	// the resolver: empty lists deny everything (the default posture),
	// unknown and readonly accounts error, a query-breaking tag errors;
	// the granted scope carries the folder space AND the account tag AND
	// the soft tags as one intersection
	if _, err := resolveMCPScope(cfg); err != nil {
		t.Fatalf("empty scope config must be legal (deny-all), got %v", err)
	}
	cfg.MCP.Accounts = []string{"gmail"}
	if s, err := resolveMCPScope(cfg); err != nil {
		t.Fatalf("accounts without tags must be legal (deny-all): %v", err)
	} else if s.allowed() {
		t.Fatal("accounts without tags must not allow anything")
	}
	cfg.MCP.Tags = []string{"inbox"}
	s, err := resolveMCPScope(cfg)
	if err != nil {
		t.Fatalf("resolveMCPScope: %v", err)
	}
	wantQuery := "((folder:/^gmail\\// AND tag:gmail)) AND (tag:inbox)"
	if s.query != wantQuery {
		t.Errorf("scope query = %q, want %q", s.query, wantQuery)
	}
	cfg.MCP.Accounts = []string{"bogus"}
	if _, err := resolveMCPScope(cfg); err == nil {
		t.Fatal("an unknown account in [mcp] accounts must error")
	}
	cfg.MCP.Accounts = []string{"atlas"}
	if _, err := resolveMCPScope(cfg); err == nil {
		t.Fatal("a readonly account in [mcp] accounts must error (no account tag = never matches)")
	}
	cfg.MCP.Accounts = []string{"gmail"}
	cfg.MCP.Tags = []string{"inbox) or tag:spam"}
	if _, err := resolveMCPScope(cfg); err == nil {
		t.Fatal("a query-breaking tag must error at config resolution, not at query time")
	}
	cfg.MCP.Tags = []string{"inbox"}

	fw := scopeFixture()
	tools := mcpTools(fw, "", nil, s)

	// search and count: the user query may match any folder and any tag -
	// the binding must intersect it with the scope before it reaches the
	// worker
	callTool(t, tools, "search", map[string]any{"query": "tag:inbox", "limit": 5})
	if q, _ := fw.lastQuery.Load().(string); !strings.Contains(q, wantQuery) || !strings.HasPrefix(q, "(tag:inbox) AND ") {
		t.Errorf("search query not intersected with the scope: %q", q)
	}
	callTool(t, tools, "count", map[string]any{"query": "*"})
	if q, _ := fw.countQ.Load().(string); !strings.Contains(q, wantQuery) || !strings.HasPrefix(q, "(*) AND ") {
		t.Errorf("count query not intersected with the scope: %q", q)
	}

	// thread_info: the fetch returns the whole thread - the projection
	// must drop every out-of-scope message, so a tail cannot ride a
	// visible thread id
	info := callTool(t, tools, "thread_info", map[string]any{"thread_id": "gt"})
	got, _ := json.Marshal(info.StructuredContent)
	for _, want := range []string{`"count":1`, `"gmail inbox report"`} {
		if !strings.Contains(string(got), want) {
			t.Errorf("thread_info missing in-scope row %s: %s", want, got)
		}
	}
	for _, leak := range []string{`"gmail sent note"`, `"gmail work mail"`} {
		if strings.Contains(string(got), leak) {
			t.Errorf("thread_info leaked an out-of-scope message (%s): %s", leak, got)
		}
	}

	// attachments: the in-scope file is read, the out-of-scope id is
	// refused before any file open
	root := t.TempDir()
	maildir := filepath.Join(root, "gmail", "Inbox", "cur")
	if err := os.MkdirAll(maildir, 0o700); err != nil {
		t.Fatal(err)
	}
	fixtureMail(t, maildir, "1", "hotel invoice", "Delta <delta@example.com>", "invoice.pdf", time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local))
	fw.setMsgs([]core.Message{{ID: "gm1", Tags: []string{"gmail", "inbox"}, Paths: []string{"gmail/Inbox/cur/1"}}})
	at := mcpTools(fw, root, map[string]bool{"attachments": true}, s)
	res := callTool(t, at, "attachments", map[string]any{"id": "gm1"})
	if res.IsError {
		t.Errorf("in-scope attachments refused: %v", res)
	}
	fw.setMsgs([]core.Message{{ID: "om1", Tags: []string{"outlook", "inbox"}, Paths: []string{"outlook/Inbox/cur/4"}}})
	res = callTool(t, at, "attachments", map[string]any{"id": "om1"})
	if !res.IsError {
		t.Errorf("out-of-scope attachments must be refused: %v", res)
	}
	if b, _ := json.Marshal(res); !strings.Contains(string(b), "not in the allowed mcp scope") {
		t.Errorf("out-of-scope attachments refusal must name the boundary: %s", b)
	}

	// deny-all: the default scope serves nothing - no query reaches the
	// worker, no thread row or attachment file crosses
	deny, _ := resolveMCPScope(&config.Config{})
	denyTools := mcpTools(fw, root, map[string]bool{"attachments": true}, deny)
	denyRes := callTool(t, denyTools, "search", map[string]any{"query": "tag:inbox"})
	if b, _ := json.Marshal(denyRes.StructuredContent); strings.Contains(string(b), "report") {
		t.Errorf("deny-all search must not leak a row: %v", denyRes.StructuredContent)
	}
	if fw.queries.Load() != 1 {
		t.Errorf("deny-all search reached the worker (queries=%d, want the 1 scoped one)", fw.queries.Load())
	}
	cnt := callTool(t, denyTools, "count", map[string]any{"query": "*"})
	if cntObj, _ := cnt.StructuredContent.(map[string]any); cntObj["count"] != float64(0) {
		t.Errorf("deny-all count = %v, want {count: 0}", cnt.StructuredContent)
	}
	info = callTool(t, denyTools, "thread_info", map[string]any{"thread_id": "gt"})
	if got, _ := json.Marshal(info.StructuredContent); !strings.Contains(string(got), `"count":0`) {
		t.Errorf("deny-all thread_info must project nothing: %s", got)
	}
	res = callTool(t, denyTools, "attachments", map[string]any{"id": "gm1"})
	if !res.IsError {
		t.Errorf("deny-all attachments must be refused: %v", res)
	}
}

// TestMCPServerStdio drives the full MCP round trip through the mcp-go
// stdio server (io.Pipe, real JSON-RPC framing): initialize, tools/list,
// and a tools/call.
func TestMCPServerStdio(t *testing.T) {
	fw := mcpFixture()
	s, err := mcptest.NewServer(t, mcpTools(fw, "", nil, nil)...)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	cl := s.Client()

	list, err := cl.ListTools(t.Context(), mcp.ListToolsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Tools) != 3 {
		t.Fatalf("tools/list returned %d tools, want 3", len(list.Tools))
	}
	seen := map[string]bool{}
	for _, tool := range list.Tools {
		seen[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("tool %s has no description", tool.Name)
		}
	}
	for _, want := range []string{"thread_info", "search", "count"} {
		if !seen[want] {
			t.Errorf("tools/list missing %s", want)
		}
	}

	res, err := cl.CallTool(t.Context(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "search", Arguments: map[string]any{"query": "tag:inbox", "limit": 5}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("search call errored: %v", res)
	}
	b, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(b), `"quarterly report"`) {
		t.Errorf("search round trip missing the expected row: %s", b)
	}
}
