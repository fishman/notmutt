// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build mcp && lua

package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
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
// default registry and served only when an account's capability grant
// enables it. Served, it lists a real message's attachments (name,
// mime, size - never bytes) with the mail-root join for relative paths.
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
	cfg.MCP.Accounts = map[string]config.MCPAccount{"gmail": {}}
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
	cfg.MCP.Accounts = map[string]config.MCPAccount{"bogus": {}}
	if _, err := resolveMCPScope(cfg); err == nil {
		t.Fatal("an unknown account in [mcp] accounts must error")
	}
	cfg.MCP.Accounts = map[string]config.MCPAccount{"atlas": {}}
	if _, err := resolveMCPScope(cfg); err == nil {
		t.Fatal("a readonly account in [mcp] accounts must error (no account tag = never matches)")
	}
	cfg.MCP.Accounts = map[string]config.MCPAccount{"gmail": {}}
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

// The capability tests below are LOCKED like TestMCPScopeEnforcement:
// they pin the [mcp] capability surface (attachments/bodies/tagging/
// apply), the class gate on the tag tool (soft vs folder verb vs the
// code-denied deleted-home), and the bodies projection. AGENTS.md
// forbids loosening or removing them without explicit user approval.

// capGrant is the [mcp] capability switches of one served surface.
type capGrant struct {
	att, bodies, tagging, apply bool
}

// capCfg builds a [mcp]-capability config: the gmail account space, the
// inbox soft-tag scope, and the folder tag group set explicitly so
// folder verbs (archive, deleted, ...) classify as such.
func capCfg(g capGrant) config.Config {
	return config.Config{
		Accounts: map[string]config.Account{"gmail": {}},
		MCP: config.MCP{
			Tags: []string{"inbox"},
			Accounts: map[string]config.MCPAccount{"gmail": {
				Attachments: config.MCPCap{Enabled: g.att},
				Bodies:      config.MCPBodies{Enabled: g.bodies},
				Tagging:     config.MCPCap{Enabled: g.tagging},
				Apply:       config.MCPCap{Enabled: g.apply},
			}},
		},
		TagGroups: map[string]core.TagGroup{"folder": {Tags: []string{"inbox", "archive", "deleted", "sent", "draft", "pending", "spam"}}},
	}
}

func toolNames(tools []server.ServerTool) []string {
	names := make([]string, 0, len(tools))
	for _, st := range tools {
		names = append(names, st.Tool.Name)
	}
	return names
}

// errText extracts a tool result's error text (the content the handler
// built via NewToolResultError; direct handler calls set no Text field).
func errText(res *mcp.CallToolResult) string {
	if res == nil || len(res.Content) == 0 {
		return ""
	}
	if t, ok := res.Content[0].(mcp.TextContent); ok {
		return t.Text
	}
	return ""
}

// TestMCPCapabilitySurface (LOCKED): the served surface is the metadata
// three plus exactly the granted capabilities - attachments, bodies,
// tag (when tagging OR apply grants a write class), mark_read (when
// tagging), and the apply folder-verb tool (when apply). A capability
// that is off is absent from tools/list, never a stubbed call.
func TestMCPCapabilitySurface(t *testing.T) {
	scopeOf := func(g capGrant) *mcpScope {
		c := capCfg(g)
		s, err := resolveMCPScope(&c)
		if err != nil {
			t.Fatalf("resolveMCPScope: %v", err)
		}
		return s
	}
	names := func(g capGrant) []string {
		return toolNames(mcpCfgTools(&fakeWorker{}, "", capCfg(g), scopeOf(g)))
	}
	if got := names(capGrant{}); !slices.Equal(got, []string{"thread_info", "search", "count"}) {
		t.Errorf("no capability: %v", got)
	}
	if got := names(capGrant{att: true}); !slices.Contains(got, "attachments") || len(got) != 4 {
		t.Errorf("attachments capability: %v", got)
	}
	if got := names(capGrant{bodies: true}); !slices.Contains(got, "thread_bodies") || len(got) != 4 {
		t.Errorf("bodies capability: %v", got)
	}
	if got := names(capGrant{tagging: true}); !slices.Contains(got, "tag") || !slices.Contains(got, "mark_read") || len(got) != 5 {
		t.Errorf("tagging capability: %v", got)
	}
	// apply alone serves tag (the general write tool) and the apply action
	if got := names(capGrant{apply: true}); !slices.Contains(got, "tag") || !slices.Contains(got, "apply") || len(got) != 5 {
		t.Errorf("apply capability: %v", got)
	}
}

// TestMCPBodiesBoundary (LOCKED): thread_bodies reads the cleaned body
// text of the thread's in-scope messages only - an out-of-scope row is
// dropped before any open, its text never crosses; the row pull is
// capped at [mcp.bodies] max_messages (newest first); the deny-all
// scope serves nothing.
func TestMCPBodiesBoundary(t *testing.T) {
	root := t.TempDir()
	write := func(subdir, name, subject string, ts time.Time) {
		dir := filepath.Join(root, subdir)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		fixtureMail(t, dir, name, subject, "sender@example.com", "", ts)
	}
	// thread gt: gm1+gm4 in scope (gmail+inbox under gmail/), gm2 and
	// gm3 out (sent/work tag - no allowed soft tag, even in the gmail
	// folder space); each row has a real file so a leak would carry its
	// subject
	write("gmail/Inbox/cur", "1", "inbox newest report", time.Date(2026, 8, 20, 11, 0, 0, 0, time.Local))
	write("gmail/Inbox/cur", "4", "older inbox note", time.Date(2026, 8, 20, 9, 0, 0, 0, time.Local))
	write("gmail/Sent/cur", "2", "sent secret", time.Date(2026, 8, 20, 10, 0, 0, 0, time.Local))
	write("gmail/Inbox/cur", "3", "work note", time.Date(2026, 8, 20, 8, 0, 0, 0, time.Local))
	fw := &fakeWorker{}
	fw.setThreadMsgs(map[string][]core.Message{
		"gt": {
			{ID: "gm1", ThreadID: "gt", Timestamp: 1755404000, Subject: "inbox newest report", Tags: []string{"gmail", "inbox"}, Paths: []string{"gmail/Inbox/cur/1"}},
			{ID: "gm4", ThreadID: "gt", Timestamp: 1755399000, Subject: "older inbox note", Tags: []string{"gmail", "inbox"}, Paths: []string{"gmail/Inbox/cur/4"}},
			{ID: "gm2", ThreadID: "gt", Timestamp: 1755400000, Subject: "sent secret", Tags: []string{"gmail", "sent"}, Paths: []string{"gmail/Sent/cur/2"}},
			{ID: "gm3", ThreadID: "gt", Timestamp: 1755403600, Subject: "work note", Tags: []string{"gmail", "work"}, Paths: []string{"gmail/Inbox/cur/3"}},
		},
	})
	// capCfg zeroes bodies; enable with the default (10) pull
	cfg := capCfg(capGrant{bodies: true})
	scope, _ := resolveMCPScope(&cfg)
	tools := mcpCfgTools(fw, root, cfg, scope)
	res := callTool(t, tools, "thread_bodies", map[string]any{"thread_id": "gt"})
	b, _ := json.Marshal(res.StructuredContent)
	s := string(b)
	for _, want := range []string{`"count":2`, `"inbox newest report"`, `"older inbox note"`, "body text"} {
		if !strings.Contains(s, want) {
			t.Errorf("thread_bodies missing %s: %s", want, s)
		}
	}
	for _, leak := range []string{"sent secret", "work note"} {
		if strings.Contains(s, leak) {
			t.Errorf("thread_bodies leaked an out-of-scope body (%s): %s", leak, s)
		}
	}

	// the pull cap: max_messages 1 serves the newest in-scope row only
	acct := cfg.MCP.Accounts["gmail"]
	acct.Bodies.MaxMessages = 1
	cfg.MCP.Accounts["gmail"] = acct
	tools = mcpCfgTools(fw, root, cfg, scope)
	res = callTool(t, tools, "thread_bodies", map[string]any{"thread_id": "gt"})
	b, _ = json.Marshal(res.StructuredContent)
	if !strings.Contains(string(b), `"count":1`) || strings.Contains(string(b), "older inbox note") {
		t.Errorf("max_messages cap not honored (newest only): %s", b)
	}

	// deny-all scope: no row, no text, but the tool is still gated on
	// the capability - the deny is a data fact, not a tool absence
	deny, _ := resolveMCPScope(&config.Config{})
	denyTools := mcpCfgTools(fw, root, cfg, deny)
	res = callTool(t, denyTools, "thread_bodies", map[string]any{"thread_id": "gt"})
	if b, _ := json.Marshal(res.StructuredContent); strings.Contains(string(b), "inbox newest report") {
		t.Errorf("deny-all thread_bodies leaked a row: %s", b)
	}
}

// TestMCPGrantIsExplicit (LOCKED): the boundary can only be widened by
// naming an account under [mcp.accounts] AND enabling a capability
// under it. An [accounts.<name>] the [mcp] section does not name stays
// invisible; a named account with no capability table serves metadata
// only; a capability table without enabled grants nothing. AGENTS.md
// forbids loosening or removing this without explicit user approval -
// it is the regression guard against a config change quietly handing
// the agent a whole account or a whole capability.
func TestMCPGrantIsExplicit(t *testing.T) {
	fw := &fakeWorker{}
	gm := core.Message{ID: "gm1", ThreadID: "gt", Timestamp: 1755400000,
		Subject: "gmail inbox report", Tags: []string{"gmail", "inbox"}, Paths: []string{"gmail/Inbox/cur/1"}}
	om := core.Message{ID: "om1", ThreadID: "ot", Timestamp: 1755401000,
		Subject: "outlook inbox mail", Tags: []string{"outlook", "inbox"}, Paths: []string{"outlook/Inbox/cur/1"}}
	fw.setStubs([]core.Message{gm, om})
	fw.setThreadMsgs(map[string][]core.Message{"gt": {gm}, "ot": {om}})

	// both accounts exist for the TUI; neither is granted to mcp
	cfg := config.Config{Accounts: map[string]config.Account{"gmail": {}, "outlook": {}}}
	cfg.MCP.Tags = []string{"inbox"}
	run := func(c config.Config) []server.ServerTool {
		t.Helper()
		scope, err := resolveMCPScope(&c)
		if err != nil {
			t.Fatalf("resolveMCPScope: %v", err)
		}
		return mcpCfgTools(fw, "", c, scope)
	}
	only := func(label string, tools []server.ServerTool, want ...string) {
		t.Helper()
		if got := toolNames(tools); !slices.Equal(got, want) {
			t.Fatalf("%s: served tools = %v, want %v", label, got, want)
		}
	}
	meta := []string{"thread_info", "search", "count"}
	has := func(tools []server.ServerTool, name string, args map[string]any) bool {
		t.Helper()
		res := callTool(t, tools, name, args)
		b, _ := json.Marshal(res)
		return strings.Contains(string(b), "inbox")
	}

	// no [mcp.accounts] table: nothing is granted, the metadata three are
	// all a client sees, and they answer nothing
	only("no account granted", run(cfg), meta...)
	scope, _ := resolveMCPScope(&cfg)
	if scope.allowed() {
		t.Fatal("configured accounts without [mcp.accounts] tables must not open the scope")
	}
	if has(run(cfg), "thread_info", map[string]any{"thread_id": "gt"}) {
		t.Error("an ungranted account's mail reached thread_info")
	}

	// granting one account opens exactly that folder space, and nothing
	// more until a capability says so
	cfg.MCP.Accounts = map[string]config.MCPAccount{"gmail": {}}
	only("gmail granted, no capabilities", run(cfg), meta...)
	if !has(run(cfg), "thread_info", map[string]any{"thread_id": "gt"}) {
		t.Error("the granted account's mail must reach thread_info")
	}
	if has(run(cfg), "thread_info", map[string]any{"thread_id": "ot"}) {
		t.Error("an account the [mcp] section never named must stay invisible")
	}
	callTool(t, run(cfg), "search", map[string]any{"query": "tag:inbox"})
	if q, _ := fw.lastQuery.Load().(string); strings.Contains(q, "tag:outlook") || strings.Contains(q, "outlook/") {
		t.Errorf("the scope query named an ungranted account: %q", q)
	}

	// a capability table without enabled grants nothing
	cfg.MCP.Accounts["gmail"] = config.MCPAccount{Bodies: config.MCPBodies{MaxMessages: 3}}
	only("capability table without enabled", run(cfg), meta...)

	// enabled: true is what serves it - one capability at a time
	cfg.MCP.Accounts["gmail"] = config.MCPAccount{Bodies: config.MCPBodies{Enabled: true}}
	only("bodies enabled", run(cfg), append(meta, "thread_bodies")...)
	cfg.MCP.Accounts["gmail"] = config.MCPAccount{}
	only("back to no capabilities", run(cfg), meta...)
}

// TestMCPPerAccountGrants pins the per-account grant model: the served
// tool list is the union of the configured accounts' grants (a tool one
// account enables is served for the whole server), and each call is
// then admitted per message by the OWNING account's own grant - so
// bodies, tagging, and the folder-verb apply each follow the message's
// account, not a global switch.
func TestMCPPerAccountGrants(t *testing.T) {
	ftw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	gm := core.Message{ID: "gm1", ThreadID: "mt", Timestamp: 1755400000,
		Subject: "gmail inbox report", Tags: []string{"gmail", "inbox"}, Paths: []string{"gmail/Inbox/cur/1"}}
	om := core.Message{ID: "om1", ThreadID: "mt", Timestamp: 1755401000,
		Subject: "outlook inbox mail", Tags: []string{"outlook", "inbox"}, Paths: []string{"outlook/Inbox/cur/1"}}
	cfg := config.Config{
		Accounts: map[string]config.Account{"gmail": {}, "outlook": {}},
		MCP: config.MCP{
			Tags: []string{"inbox"},
			Accounts: map[string]config.MCPAccount{
				"gmail":   {Bodies: config.MCPBodies{Enabled: true}, Tagging: config.MCPCap{Enabled: true}},
				"outlook": {Apply: config.MCPCap{Enabled: true}},
			},
		},
		TagGroups: map[string]core.TagGroup{"folder": {Tags: []string{"inbox", "archive", "deleted", "sent", "draft", "pending", "spam"}}},
	}
	scope, err := resolveMCPScope(&cfg)
	if err != nil {
		t.Fatalf("resolveMCPScope: %v", err)
	}
	tools := mcpCfgTools(ftw, "", cfg, scope)
	names := toolNames(tools)
	for _, want := range []string{"thread_bodies", "mark_read", "apply"} {
		if !slices.Contains(names, want) {
			t.Errorf("%s not served: the union of the account grants enables it (got %v)", want, names)
		}
	}

	// bodies are gmail's grant alone: the outlook message in the same
	// thread never renders
	ftw.setThreadMsgs(map[string][]core.Message{"mt": {gm, om}})
	res := callTool(t, tools, "thread_bodies", map[string]any{"thread_id": "mt"})
	b, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(b), "gmail inbox report") {
		t.Errorf("gmail body missing (its account grants bodies): %s", b)
	}
	if strings.Contains(string(b), "outlook inbox mail") {
		t.Errorf("outlook body leaked (its account grants no bodies): %s", b)
	}

	// mark_read writes a soft tag: gmail's grant admits its own message,
	// outlook's lack refuses its own
	ftw.setMsgs([]core.Message{gm})
	if res := callTool(t, tools, "mark_read", map[string]any{"id": "gm1"}); res.IsError {
		t.Errorf("gmail mark_read refused (its account grants tagging): %v", res)
	}
	ftw.setMsgs([]core.Message{om})
	res = callTool(t, tools, "mark_read", map[string]any{"id": "om1"})
	if !res.IsError || !strings.Contains(errText(res), "tagging") {
		t.Errorf("outlook mark_read must be refused per account: %v", res)
	}

	// a folder verb is outlook's grant alone: it clears gmail's gate on
	// outlook mail and refuses gmail's own
	ftw.setMsgs([]core.Message{gm})
	res = callTool(t, tools, "tag", map[string]any{"id": "gm1", "add": []string{"archive"}})
	if !res.IsError || !strings.Contains(errText(res), ".apply") {
		t.Errorf("gmail folder verb must be refused per account: %v", res)
	}
	ftw.setMsgs([]core.Message{om})
	res = callTool(t, tools, "tag", map[string]any{"id": "om1", "add": []string{"archive"}})
	if res.IsError && strings.Contains(errText(res), ".apply") {
		t.Errorf("outlook folder verb refused on the capability, not the move: %v", res)
	}
}

// TestMCPTagCapabilityGate (LOCKED): the tag tool's class gate runs
// before any message is touched - a soft tag needs [mcp.tagging], a
// folder verb needs [mcp.apply] and then routes through the shared
// apply path (execApply), and the deleted-home tag can never be added
// regardless of the grant. An unresolvable folder move refuses the
// whole call: nothing is tagged when the move cannot land.
func TestMCPTagCapabilityGate(t *testing.T) {
	ftw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	scopeMsg := core.Message{ID: "gm1", ThreadID: "gt", Tags: []string{"gmail", "inbox"}, Paths: []string{"gmail/Inbox/cur/1"}}
	outMsg := core.Message{ID: "om1", ThreadID: "ot", Tags: []string{"outlook", "inbox"}, Paths: []string{"outlook/Inbox/cur/4"}}
	run := func(cfg config.Config) []server.ServerTool {
		scope, err := resolveMCPScope(&cfg)
		if err != nil {
			t.Fatal(err)
		}
		return mcpCfgTools(ftw, "", cfg, scope)
	}
	// expectCalls tracks the calls that may have legitimately landed so
	// far: each refusal below must not grow the count beyond it
	expectCalls := 0
	noWrites := func(label string) {
		t.Helper()
		if n := len(ftw.tagCallsSnapshot()); n != expectCalls {
			t.Fatalf("%s wrote %d tags (count went %d -> %d), want no growth", label, n-expectCalls, expectCalls, n)
		}
	}

	// soft tag with tagging on: in-scope message tagged through the
	// apply path (a plain ActTag, id-addressed)
	ftw.setMsgs([]core.Message{scopeMsg})
	tools := run(capCfg(capGrant{tagging: true}))
	res := callTool(t, tools, "tag", map[string]any{"id": "gm1", "add": []string{"work"}})
	if res.IsError {
		t.Fatalf("soft tag refused: %v", res)
	}
	calls := ftw.tagCallsSnapshot()
	if len(calls) != 1 || !strings.Contains(calls[0].query, `id:"gm1"`) || len(calls[0].tagOps) != 1 || !calls[0].tagOps[0].Add || calls[0].tagOps[0].Tag != "work" {
		t.Errorf("soft tag did not land as one id-addressed ActTag: %+v", calls)
	}
	expectCalls = len(calls) // this one landed; the refusals below must add none

	// class gates: soft without tagging, folder verb without apply,
	// deleted-home regardless of apply
	ftw.setMsgs([]core.Message{scopeMsg})
	tools = run(capCfg(capGrant{apply: true})) // tagging off
	res = callTool(t, tools, "tag", map[string]any{"id": "gm1", "add": []string{"work"}})
	if !res.IsError {
		t.Errorf("soft tag without [mcp.tagging] must be refused: %v", res)
	}
	noWrites("soft-without-tagging")
	ftw.setMsgs([]core.Message{scopeMsg})
	tools = run(capCfg(capGrant{tagging: true})) // apply off
	res = callTool(t, tools, "tag", map[string]any{"id": "gm1", "add": []string{"archive"}})
	if !res.IsError {
		t.Errorf("folder verb without [mcp.apply] must be refused: %v", res)
	}
	noWrites("folder-without-apply")
	ftw.setMsgs([]core.Message{scopeMsg})
	tools = run(capCfg(capGrant{apply: true}))
	res = callTool(t, tools, "tag", map[string]any{"id": "gm1", "add": []string{"deleted"}})
	if !res.IsError || !strings.Contains(errText(res), "deleted-home") {
		t.Errorf("the deleted-home tag must be code-denied even with apply on: %v", res)
	}
	noWrites("deleted-home")

	// folder verb with apply on routes through the apply path: the move
	// cannot resolve (no candidates in the bare gmail account), so the
	// call fails and nothing is tagged - the write never bypasses the
	// move
	ftw.setMsgs([]core.Message{scopeMsg})
	tools = run(capCfg(capGrant{tagging: true, apply: true}))
	res = callTool(t, tools, "tag", map[string]any{"id": "gm1", "add": []string{"archive"}})
	if !res.IsError || !strings.Contains(errText(res), "no move candidates") {
		t.Errorf("an unresolvable folder move must refuse through the apply path: %v", res)
	}
	noWrites("unresolvable-folder-move")

	// out-of-scope id: refused before any write
	ftw.setMsgs([]core.Message{outMsg})
	tools = run(capCfg(capGrant{tagging: true, apply: true}))
	res = callTool(t, tools, "tag", map[string]any{"id": "om1", "add": []string{"work"}})
	if !res.IsError || !strings.Contains(errText(res), "not in the allowed mcp scope") {
		t.Errorf("out-of-scope message must be refused: %v", res)
	}
	noWrites("out-of-scope")
}

// TestMCPMarkRead pins the mark_read action ([mcp.tagging]): it drops
// the unread soft tag on the in-scope message through the shared apply
// path; an already-read message is a net no-op (no write); an
// out-of-scope message is refused.
func TestMCPMarkRead(t *testing.T) {
	ftw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	inScope := core.Message{ID: "gm1", ThreadID: "gt", Tags: []string{"gmail", "inbox", "unread"}, Paths: []string{"gmail/Inbox/cur/1"}}
	read := core.Message{ID: "gm2", ThreadID: "gt", Tags: []string{"gmail", "inbox"}, Paths: []string{"gmail/Inbox/cur/2"}}
	out := core.Message{ID: "om1", ThreadID: "ot", Tags: []string{"outlook", "inbox", "unread"}, Paths: []string{"outlook/Inbox/cur/1"}}
	run := func(cfg config.Config) []server.ServerTool {
		scope, err := resolveMCPScope(&cfg)
		if err != nil {
			t.Fatal(err)
		}
		return mcpCfgTools(ftw, "", cfg, scope)
	}
	before := func() int { return len(ftw.tagCallsSnapshot()) }

	// tagging on: one id-addressed -unread ActTag lands
	ftw.setMsgs([]core.Message{inScope})
	tools := run(capCfg(capGrant{tagging: true}))
	res := callTool(t, tools, "mark_read", map[string]any{"id": "gm1"})
	if res.IsError {
		t.Fatalf("mark_read refused: %v", res)
	}
	calls := ftw.tagCallsSnapshot()
	if len(calls) != 1 || !strings.Contains(calls[0].query, `id:"gm1"`) || len(calls[0].tagOps) != 1 || calls[0].tagOps[0].Add || calls[0].tagOps[0].Tag != "unread" {
		t.Errorf("mark_read did not land as one id-addressed -unread ActTag: %+v", calls)
	}

	// already read: net no-op, no write
	n := before()
	ftw.setMsgs([]core.Message{read})
	tools = run(capCfg(capGrant{tagging: true}))
	res = callTool(t, tools, "mark_read", map[string]any{"id": "gm2"})
	if res.IsError {
		t.Fatalf("already-read mark_read refused: %v", res)
	}
	if got := before(); got != n {
		t.Errorf("already-read mark_read wrote %d tags (was %d)", got-n, n)
	}

	// out-of-scope id refused before any write
	n = before()
	ftw.setMsgs([]core.Message{out})
	tools = run(capCfg(capGrant{tagging: true}))
	res = callTool(t, tools, "mark_read", map[string]any{"id": "om1"})
	if !res.IsError || !strings.Contains(errText(res), "not in the allowed mcp scope") {
		t.Errorf("out-of-scope mark_read must be refused: %v", res)
	}
	if got := before(); got != n {
		t.Errorf("out-of-scope mark_read wrote %d tags (was %d)", got-n, n)
	}
}

// TestMCPApplyFolderVerb pins the apply action ([mcp.apply]): it takes
// one folder verb (a member of the exclusive folder group), never a soft
// tag; the deleted-home tag is code-denied; a folder verb routes through
// the shared apply path so an unresolvable move refuses the whole call.
func TestMCPApplyFolderVerb(t *testing.T) {
	ftw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	scopeMsg := core.Message{ID: "gm1", ThreadID: "gt", Tags: []string{"gmail", "inbox", "unread"}, Paths: []string{"gmail/Inbox/cur/1"}}
	run := func(cfg config.Config) []server.ServerTool {
		scope, err := resolveMCPScope(&cfg)
		if err != nil {
			t.Fatal(err)
		}
		return mcpCfgTools(ftw, "", cfg, scope)
	}
	n := func() int { return len(ftw.tagCallsSnapshot()) }

	// a soft tag is not a folder verb
	ftw.setMsgs([]core.Message{scopeMsg})
	tools := run(capCfg(capGrant{apply: true}))
	res := callTool(t, tools, "apply", map[string]any{"id": "gm1", "action": "work"})
	if !res.IsError || !strings.Contains(errText(res), "not a folder verb") {
		t.Errorf("soft action through apply must be refused: %v", res)
	}
	if got := n(); got != 0 {
		t.Errorf("soft action wrote %d tags", got)
	}

	// deleted-home is code-denied even with apply on
	ftw.setMsgs([]core.Message{scopeMsg})
	tools = run(capCfg(capGrant{apply: true}))
	res = callTool(t, tools, "apply", map[string]any{"id": "gm1", "action": "deleted"})
	if !res.IsError || !strings.Contains(errText(res), "code-denied") {
		t.Errorf("deleted action must be code-denied: %v", res)
	}
	if got := n(); got != 0 {
		t.Errorf("deleted action wrote %d tags", got)
	}

	// a folder verb routes through the apply path: the bare gmail account
	// has no move candidates, so the move resolves before the tag and the
	// whole call refuses - nothing tagged, unread never touched
	ftw.setMsgs([]core.Message{scopeMsg})
	tools = run(capCfg(capGrant{apply: true}))
	res = callTool(t, tools, "apply", map[string]any{"id": "gm1", "action": "archive"})
	if !res.IsError || !strings.Contains(errText(res), "no move candidates") {
		t.Errorf("unresolvable folder move must refuse the call: %v", res)
	}
	if got := n(); got != 0 {
		t.Errorf("unresolvable apply wrote %d tags", got)
	}
}

// TestMCPInScopeAbsolutePaths (regression, 2026-09-09): notmuch reports
// message filenames absolute (go.notmuch documents Filenames as
// absolute; show --format=json emits the stored path), so the scope's
// folder pin has to match the root-relative form the engine normalizes
// to - serveMCP wires scope.root = mailRoot. Before the fix every live
// message failed inScope and thread_info/thread_bodies returned
// {"count":0} for threads search showed in scope.
func TestMCPInScopeAbsolutePaths(t *testing.T) {
	cfg := capCfg(capGrant{})
	root := "/home/u/Mail"
	scope, err := resolveMCPScope(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	scope.root = root
	in := core.Message{Tags: []string{"gmail", "inbox"}, Paths: []string{root + "/gmail/Inbox/cur/1"}}
	if !scope.inScope(in) {
		t.Error("message under the granted account folder must be in scope")
	}
	out := core.Message{Tags: []string{"gmail", "inbox"}, Paths: []string{root + "/outlook/Inbox/cur/1"}}
	if scope.inScope(out) {
		t.Error("message outside the granted account folder must be out of scope")
	}
	// without the root the absolute path must not false-positive into scope
	bare, _ := resolveMCPScope(&cfg)
	if bare.inScope(in) {
		t.Error("an un-rooted scope must not admit an absolute path")
	}
}
