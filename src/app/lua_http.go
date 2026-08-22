// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua

// The sandbox network boundary: plugins call REST APIs through a Go
// binding over net/http, so vendor API code lives as plugin files in
// a future plugins repo, never in the client. Deny-by-default twice
// over:
//   - the http global exists on a plugin VM only when the plugin has a
//     [lua.network.<plugin>] section, and every request (redirect hops
//     included) must match the configured hosts AND one "METHOD /path"
//     rule;
//   - a network-enabled plugin VM never sees mail content: metadataCtxTable
//     (the MCP projection) replaces the action ctx's mail_lines, so a
//     body cannot cross the allowlist.
// The VM deadline propagates into in-flight requests (the SetContext
// kill); the body cap bounds a runaway response.

package app

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"

	"notmutt/config"
	"notmutt/core"
	"notmutt/notmuch"
)

const (
	// luaHTTPMaxBody caps one response body (256 KiB: a REST page, not
	// a transfer); a bigger payload fails instead of ballooning memory.
	luaHTTPMaxBody = 1 << 18
	// luaHTTPTimeout is the per-request backstop. The plugin VM deadline
	// (actionDeadline) is the usual killer; this catches the load-time VM,
	// which has no deadline.
	luaHTTPTimeout = 30 * time.Second
	// the shared search caps (metadataCtxTable; MCP spec references
	// them too - the mcp build compiles this file)
	mcpSearchDefaultLimit = 50
	mcpSearchMaxLimit     = 500
)

// pluginKey maps a plugin file path to its config identity: the file
// base name without extension ([lua.network.<name>]).
func pluginKey(path string) string {
	return strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
}

// networkFor resolves a plugin's network gate; nil = no section =
// deny-by-default (http never registered).
func networkFor(network map[string]config.LuaNetwork, path string) *config.LuaNetwork {
	if r, ok := network[pluginKey(path)]; ok {
		return &r
	}
	return nil
}

// setPluginNet registers the json and http globals on a plugin VM:
// json always, http only when the plugin has a network section.
func setPluginNet(vm *lua.LState, rules *config.LuaNetwork) {
	vm.SetGlobal("json", luaJSONModule(vm))
	if rules != nil {
		vm.SetGlobal("http", luaHTTPModule(vm, rules))
	}
}

// luaHTTPModule builds the http table for one plugin's rules:
// request(method, url, opts) -> {status, headers, body} or nil, err.
// opts: {headers = {k = v}, body = string}. Method, host, and path
// (the "METHOD /path" rules) are checked before any dial; redirect
// hops re-check all three.
func luaHTTPModule(vm *lua.LState, rules *config.LuaNetwork) *lua.LTable {
	tbl := vm.NewTable()
	tbl.RawSetString("request", vm.NewFunction(func(L *lua.LState) int {
		fail := func(msg string) int {
			L.Push(lua.LNil)
			L.Push(lua.LString("http: " + msg))
			return 2
		}
		method := strings.ToUpper(L.CheckString(1))
		rawURL := L.CheckString(2)
		u, err := url.Parse(rawURL)
		if err != nil || u.Hostname() == "" {
			return fail("invalid url")
		}
		if !luaNetworkHostAllowed(u.Hostname(), rules.Targets) {
			return fail("host " + u.Hostname() + " not allowed for this plugin")
		}
		// the path is the endpoint: a verb means nothing without it, so
		// the two are checked as one rule unit
		if !luaNetworkEndpointAllowed(method, u.Path, rules.Paths) {
			return fail(method + " " + u.Path + " not allowed for this plugin")
		}
		var body io.Reader
		opts := L.OptTable(3, nil)
		if opts != nil {
			if b := opts.RawGetString("body"); b != lua.LNil {
				body = strings.NewReader(b.String())
			}
		}
		// the VM deadline (SetContext) propagates into the request, so
		// the sandbox kill aborts an in-flight call too
		ctx := L.Context()
		if ctx == nil {
			ctx = context.Background()
		}
		req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
		if err != nil {
			return fail(err.Error())
		}
		if opts != nil {
			if h := opts.RawGetString("headers"); h != lua.LNil {
				if ht, ok := h.(*lua.LTable); ok {
					ht.ForEach(func(k, v lua.LValue) {
						req.Header.Set(k.String(), v.String())
					})
				}
			}
		}
		client := &http.Client{
			Timeout: luaHTTPTimeout,
			// a redirect hop outside the allowlist (host or endpoint) is
			// a policy violation - headers must not travel there either
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if !luaNetworkHostAllowed(req.URL.Hostname(), rules.Targets) {
					return fmt.Errorf("redirect to %s not allowed for this plugin", req.URL.Hostname())
				}
				if !luaNetworkEndpointAllowed(req.Method, req.URL.Path, rules.Paths) {
					return fmt.Errorf("redirect to %s %s not allowed for this plugin", req.Method, req.URL.Path)
				}
				return nil
			},
		}
		resp, err := client.Do(req)
		if err != nil {
			return fail(err.Error())
		}
		defer resp.Body.Close()
		bodyB, err := io.ReadAll(io.LimitReader(resp.Body, luaHTTPMaxBody+1))
		if err != nil {
			return fail(err.Error())
		}
		if len(bodyB) > luaHTTPMaxBody {
			return fail("response too large")
		}
		res := L.NewTable()
		res.RawSetString("status", lua.LNumber(resp.StatusCode))
		headers := L.NewTable()
		for k, vs := range resp.Header {
			headers.RawSetString(k, lua.LString(strings.Join(vs, ", ")))
		}
		res.RawSetString("headers", headers)
		res.RawSetString("body", lua.LString(bodyB))
		L.Push(res)
		return 1
	}))
	return tbl
}

// luaNetworkHostAllowed matches an exact host or a "*.suffix" entry.
// The match is on the parsed hostname only - never the raw URL string,
// so userinfo/encoded tricks cannot fake an allowlisted host.
func luaNetworkHostAllowed(host string, targets []string) bool {
	host = strings.ToLower(host)
	for _, t := range targets {
		t = strings.ToLower(t)
		if strings.HasPrefix(t, "*.") {
			if strings.HasSuffix(host, "."+strings.TrimPrefix(t, "*.")) {
				return true
			}
			continue
		}
		if host == t {
			return true
		}
	}
	return false
}

// luaNetworkEndpointAllowed matches a "METHOD /path" rule: verb
// case-insensitive, path exact or trailing-* prefix ("GET /a*" matches
// /a, /ab, /a/b/c). The path comes from the parsed URL (u.Path), so
// query strings never take part. A malformed rule simply never matches
// (config load rejects it).
func luaNetworkEndpointAllowed(method, path string, paths []string) bool {
	if path == "" {
		path = "/" // url.Parse leaves a host-only URL pathless; the server sees "/"
	}
	method = strings.ToUpper(method)
	for _, rule := range paths {
		m, glob, ok := strings.Cut(rule, " ")
		if !ok || method != strings.ToUpper(m) {
			continue
		}
		if prefix, ok := strings.CutSuffix(glob, "*"); ok {
			if strings.HasPrefix(path, prefix) {
				return true
			}
			continue
		}
		if path == glob {
			return true
		}
	}
	return false
}

// mcpScope is the MCP server's data boundary: the account folder
// spaces it may see (each = folder prefix AND the account's tag) and
// the soft tags whose mail is reachable. Deny by default: an empty
// accounts or tags list allows nothing, and the nil scope (network
// plugin VMs, which carry their own config gates) is unscoped.
type mcpScope struct {
	folders []string // account folder prefixes; the account tag is the folder name
	tags    []string // allowed soft tags; a message must carry one
	query   string   // the scope as a notmuch term, for query intersection
}

// allowed reports whether the scope admits anything at all: both lists
// must be non-empty - each is an explicit grant. The nil scope
// (plugin VMs) is unscoped; deny-all short-circuits only a non-nil
// scope.
func (s *mcpScope) allowed() bool {
	return len(s.folders) > 0 && len(s.tags) > 0
}

// and intersects a user query with the scope: the user query alone
// may match any folder and any tag, the scope term pins both. A nil
// scope passes the query through untouched.
func (s *mcpScope) and(q string) string {
	if s == nil {
		return q
	}
	return "(" + q + ") AND " + s.query
}

// inScope is the per-message check for the id-addressed tools
// (thread_info rows, attachments): the message must live under an
// allowed account's folder space, carry that account's tag, and
// carry at least one allowed soft tag - the folder pin alone could
// be spoofed by a moved message, the tag alone by a retag. A nil
// scope (plugin VMs) admits everything.
func (s *mcpScope) inScope(m core.Message) bool {
	if s == nil {
		return true
	}
	if !s.allowed() {
		return false
	}
	has := make(map[string]bool, len(m.Tags))
	for _, t := range m.Tags {
		has[t] = true
	}
	under := false
	for _, f := range s.folders {
		if has[f] && underFolder(m.Paths, f) {
			under = true
			break
		}
	}
	if !under {
		return false
	}
	for _, t := range s.tags {
		if has[t] {
			return true
		}
	}
	return false
}

func underFolder(paths []string, folder string) bool {
	for _, p := range paths {
		if strings.HasPrefix(p, folder+"/") {
			return true
		}
	}
	return false
}

// metadataCtxTable builds the metadata-only ctx table over the worker:
// thread_info, search, count - nothing that projects mail content (no
// mail_lines, no ai_chat). The MCP server (mcp.go) and network-enabled
// plugin VMs share this surface: what may cross the network is exactly
// what this table can see. The scope is the MCP boundary - the
// id-addressed bindings per-message check it, the query bindings
// intersect the user query with it.
func metadataCtxTable(vm *lua.LState, worker workerAPI, scope *mcpScope) *lua.LTable {
	ctx := vm.NewTable()
	ctx.RawSetString("thread_info", vm.NewFunction(func(L *lua.LState) int {
		tid := L.CheckString(1)
		rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActThread, ThreadID: tid})
		if err != nil || rpl.Err != nil {
			L.RaiseError("thread_info: %v %v", err, rpl.Err)
		}
		// only in-scope messages cross into the projection - an
		// out-of-scope tail cannot ride a visible thread id
		var rows []core.Message
		for _, m := range rpl.Msgs {
			if scope.inScope(m) {
				rows = append(rows, m)
			}
		}
		tbl := L.NewTable()
		tbl.RawSetString("thread_id", lua.LString(tid))
		tbl.RawSetString("count", lua.LNumber(len(rows)))
		msgs := L.NewTable()
		for _, m := range rows {
			msgs.Append(projectMessage(L, m))
		}
		tbl.RawSetString("messages", msgs)
		L.Push(tbl)
		return 1
	}))
	ctx.RawSetString("search", vm.NewFunction(func(L *lua.LState) int {
		q := L.CheckString(1)
		if scope != nil && !scope.allowed() {
			L.Push(L.NewTable())
			return 1
		}
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
			Query: scope.and(q),
			Limit: limit,
			// the Emit closure appends to an invocation-local slice (the
			// refresher.changed pattern); it runs on the worker goroutine,
			// never touching the Lua state
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
		if scope != nil && !scope.allowed() {
			L.Push(lua.LNumber(0))
			return 1
		}
		rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActCount, Query: scope.and(q)})
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
// network surface (the privacy rule).
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
