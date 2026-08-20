// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua

// The sandbox http module: plugins call REST APIs through a Go binding
// over net/http, so vendor API code (HubSpot and friends) lives as
// plugin files in a future plugins repo, never in the client. Network
// is deny-by-default: the http global exists on a plugin VM only when
// the plugin has a [lua.network.<plugin>] section, and every request
// (redirect hops included) must match the configured hosts. The VM
// deadline propagates into in-flight requests (the SetContext kill);
// the body cap bounds a runaway response.

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
)

const (
	// luaHTTPMaxBody caps one response body: a plugin fetching a huge
	// payload fails instead of ballooning memory.
	luaHTTPMaxBody = 1 << 20
	// luaHTTPTimeout is the per-request backstop. The plugin VM
	// deadline (actionDeadline) is the usual killer; this catches the
	// load-time VM, which has no deadline.
	luaHTTPTimeout = 30 * time.Second
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
// opts: {headers = {k = v}, body = string}. Method and host are
// checked before any dial; redirect hops re-check the host.
func luaHTTPModule(vm *lua.LState, rules *config.LuaNetwork) *lua.LTable {
	tbl := vm.NewTable()
	tbl.RawSetString("request", vm.NewFunction(func(L *lua.LState) int {
		fail := func(msg string) int {
			L.Push(lua.LNil)
			L.Push(lua.LString("http: " + msg))
			return 2
		}
		method := strings.ToUpper(L.CheckString(1))
		if !luaNetworkMethodAllowed(method, rules.Methods) {
			return fail("method " + method + " not allowed for this plugin")
		}
		rawURL := L.CheckString(2)
		u, err := url.Parse(rawURL)
		if err != nil || u.Hostname() == "" {
			return fail("invalid url")
		}
		if !luaNetworkHostAllowed(u.Hostname(), rules.Targets) {
			return fail("host " + u.Hostname() + " not allowed for this plugin")
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
			// a redirect hop to a host outside the allowlist is a
			// policy violation (headers must not travel there either)
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if !luaNetworkHostAllowed(req.URL.Hostname(), rules.Targets) {
					return fmt.Errorf("redirect to %s not allowed for this plugin", req.URL.Hostname())
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

// luaNetworkMethodAllowed: an empty method list allows every verb.
func luaNetworkMethodAllowed(method string, methods []string) bool {
	if len(methods) == 0 {
		return true
	}
	method = strings.ToUpper(method)
	for _, m := range methods {
		if method == strings.ToUpper(m) {
			return true
		}
	}
	return false
}
