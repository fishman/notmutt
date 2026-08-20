// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build mcp && lua

package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/mcptest"
	"github.com/mark3labs/mcp-go/server"
	"github.com/yuin/gopher-lua"

	"notmutt/core"
)

func mcpFixture() *fakeWorker {
	// alpha carries two messages (thread_info), beta one (search rows);
	// the Paths field on alpha-m1 pins the privacy projection - a path
	// must never cross into a tool result
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

// callTool runs the named tool's handler directly with the given args
// (the registry handler is the seam; the stdio framing is exercised by
// TestMCPServerStdio).
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

// TestMCPToolExecution pins the three tools against the fake worker:
// thread_info carries the per-message metadata, search returns one row
// per thread summary, count the thread total. Every result must also
// pass the privacy pin - no paths, no bodies, no header text beyond the
// projected fields.
func TestMCPToolExecution(t *testing.T) {
	fw := mcpFixture()
	tools := mcpTools(fw, "", nil)

	info := callTool(t, tools, "thread_info", map[string]any{"thread_id": "alpha"})
	infoJSON, _ := json.MarshalIndent(info.StructuredContent, "", "  ")
	got := string(infoJSON)
	for _, want := range []string{`"count": 2`, `"quarterly report"`, `"Re: quarterly report"`, `"atlas@example.com"`, "m1@example.com", `"work"`} {
		if !strings.Contains(got, want) {
			t.Errorf("thread_info result missing %s: %s", want, got)
		}
	}

	res := callTool(t, tools, "search", map[string]any{"query": "tag:inbox", "limit": 5})
	rowsJSON, _ := json.MarshalIndent(res.StructuredContent, "", "  ")
	rows := string(rowsJSON)
	for _, want := range []string{`"quarterly report"`, `"status update"`, `"sender@example.com"`} {
		if !strings.Contains(rows, want) {
			t.Errorf("search result missing %s: %s", want, rows)
		}
	}

	cnt := callTool(t, tools, "count", map[string]any{"query": "*"})
	if cnt.StructuredContent != float64(2) {
		t.Errorf("count result = %v, want 2", cnt.StructuredContent)
	}

	// the privacy pin: the Paths field and the path value set on the
	// fixture messages must never reach a result (the projection is the
	// boundary)
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

// TestMCPRestrictedSurface pins the "not all of them" restriction: the
// MCP VM has only the read bindings, so the write/interactive globals
// and mail_lines are absent - a chunk touching any of them fails, and
// the ctx table exposes exactly thread_info/search/count.
func TestMCPRestrictedSurface(t *testing.T) {
	fw := mcpFixture()
	// the chunks parse (return function(...)) and must fail at CALL
	// time: the write/interactive globals and mail_lines are absent
	// from the MCP sandbox
	for _, chunk := range []string{
		`return function(ctx, args) tag_add("work") end`,
		`return function(ctx, args) tag_remove("unread") end`,
		`return function(ctx, args) attach_add("/tmp/x") end`,
		`return function(ctx, args) return ctx.mail_lines() end`,
		`return function(ctx, args) ai_chat("p", {text = "x"}) end`,
		`return function(ctx, args) prompt("y") end`,
	} {
		if _, err := mcpRunChunk(chunk, nil, fw, ""); err == nil {
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
	metadataCtxTable(vm, fw).ForEach(func(k, _ lua.LValue) { keys[k.String()] = true })
	for _, want := range []string{"thread_info", "search", "count"} {
		if !keys[want] {
			t.Errorf("ctx table missing binding %s", want)
		}
	}
	if len(keys) != 3 {
		t.Errorf("ctx table exposes %d bindings, want exactly the 3 read ones", len(keys))
	}
}

// TestMCPAttachmentsGate pins the whitelist gate: the attachments tool
// is absent from the default registry and served only when [mcp] allow
// names it. Served, it lists a real message's attachments (name, mime,
// size - never bytes) with the mail-root join for relative paths.
func TestMCPAttachmentsGate(t *testing.T) {
	root := t.TempDir()
	fixtureMail(t, root, "m1.eml", "hotel invoice", "Delta <delta@example.com>", "invoice.pdf", time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local))
	fw := &fakeWorker{}
	fw.setMsgs([]core.Message{{ID: "m1", Paths: []string{"m1.eml"}}})

	// default: not in the registry
	names := map[string]bool{}
	for _, st := range mcpTools(fw, root, nil) {
		names[st.Tool.Name] = true
	}
	if names["attachments"] {
		t.Fatal("the attachments tool must be gated off by default")
	}

	// whitelisted: served, and the result is metadata only
	tools := mcpTools(fw, root, map[string]bool{"attachments": true})
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

// TestMCPServerStdio drives the full MCP round trip through the
// mcp-go stdio server (io.Pipe, real JSON-RPC framing): initialize,
// tools/list, and a tools/call over the client.
func TestMCPServerStdio(t *testing.T) {
	fw := mcpFixture()
	s, err := mcptest.NewServer(t, mcpTools(fw, "", nil)...)
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
