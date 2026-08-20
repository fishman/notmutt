// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua

// The network deny-by-default fence: these tests fail if a regression
// ever lets a plugin reach the network without an explicit
// [lua.network.<plugin>] allowlist. Every "no error log" assertion
// means the plugin file loaded cleanly - a plugin that was granted
// access it should not have (or denied access it needs) errors out and
// shows up in the captured log. The hit counters pin that a denied
// request never dials.

package app

import (
	"bytes"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"notmutt/config"
)

// loadPluginsCaptured runs the plugin loader with the logger captured:
// a plugin that fails to load (error() during DoFile) lands in the
// buffer, and the returned string is empty exactly when every plugin
// loaded cleanly.
func loadPluginsCaptured(t *testing.T, dir string, network map[string]config.LuaNetwork) string {
	t.Helper()
	var buf bytes.Buffer
	old := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(old)
	loadLuaPlugins(dir, network)
	return buf.String()
}

func TestPluginHTTPDeniedByDefault(t *testing.T) {
	dir := t.TempDir()
	// no [lua.network] section at all: the http global must be absent
	// and any attempt to call it must fail - both directions of the
	// gate, in the same plugin file
	writePlugin(t, dir, "plug.lua", `
if http ~= nil then error("http present without [lua.network]") end
-- the call is wrapped in a function: http.request as an argument
-- would index nil BEFORE pcall can guard it
local ok = pcall(function() http.request("GET", "http://127.0.0.1:1/", {}) end)
if ok then error("network allowed by default") end
`)
	if out := loadPluginsCaptured(t, dir, nil); out != "" {
		t.Fatalf("default build must deny network: %s", out)
	}
}

func TestPluginHTTPEmptySectionFailClosed(t *testing.T) {
	dir := t.TempDir()
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer srv.Close()
	// the section exists (http registered) but lists no targets: every
	// request must fail closed, before the dial. request returns
	// (nil, err) on denial - pcall would wrap a clean return, not a raise
	writePlugin(t, dir, "plug.lua", fmt.Sprintf(`
if http == nil then error("http missing with [lua.network] section") end
local resp, err = http.request("GET", %q, {})
if resp ~= nil or err == nil then error("empty targets allowed network") end
`, srv.URL))
	rules := map[string]config.LuaNetwork{"plug": {}}
	if out := loadPluginsCaptured(t, dir, rules); out != "" {
		t.Fatalf("empty [lua.network] must deny network: %s", out)
	}
	if hits != 0 {
		t.Fatalf("denied request dialed the server (%d hits)", hits)
	}
}

func TestPluginHTTPAllowed(t *testing.T) {
	dir := t.TempDir()
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "pong")
	}))
	defer srv.Close()
	// the positive control: with the allowlist in place the same
	// request succeeds - without it the fence tests above would pass
	// vacuously
	writePlugin(t, dir, "plug.lua", fmt.Sprintf(`
local resp, err = http.request("GET", %q, {})
if resp == nil then error("request failed: " .. tostring(err)) end
if resp.status ~= 200 then error("status " .. resp.status) end
if resp.body ~= "pong" then error("body " .. resp.body) end
if resp.headers["Content-Type"] ~= "text/plain" then error("missing header") end
`, srv.URL))
	rules := map[string]config.LuaNetwork{"plug": {Targets: []string{"127.0.0.1"}}}
	if out := loadPluginsCaptured(t, dir, rules); out != "" {
		t.Fatalf("allowlisted request must succeed: %s", out)
	}
	if hits != 1 {
		t.Fatalf("expected exactly 1 hit, got %d", hits)
	}
}

func TestPluginHTTPDeniedHostNeverDialed(t *testing.T) {
	dir := t.TempDir()
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer srv.Close()
	// the allowlist names a different host: the request to the test
	// server must fail before any dial
	writePlugin(t, dir, "plug.lua", fmt.Sprintf(`
local resp, err = http.request("GET", %q, {})
if resp ~= nil or err == nil then error("non-allowlisted host reached the server") end
`, srv.URL))
	rules := map[string]config.LuaNetwork{"plug": {Targets: []string{"example.com"}}}
	if out := loadPluginsCaptured(t, dir, rules); out != "" {
		t.Fatalf("denied host must fail cleanly: %s", out)
	}
	if hits != 0 {
		t.Fatalf("denied request dialed the server (%d hits)", hits)
	}
}

func TestPluginHTTPMethodGate(t *testing.T) {
	dir := t.TempDir()
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer srv.Close()
	// GET is allowlisted, POST is not: the POST fails before the dial,
	// the GET succeeds
	writePlugin(t, dir, "plug.lua", fmt.Sprintf(`
local resp, err = http.request("POST", %q, {})
if resp ~= nil or err == nil then error("POST must be denied") end
local resp, err = http.request("get", %q, {})
if resp == nil or resp.status ~= 200 then error("GET must be allowed: " .. tostring(err)) end
`, srv.URL, srv.URL))
	rules := map[string]config.LuaNetwork{"plug": {Targets: []string{"127.0.0.1"}, Methods: []string{"get"}}}
	if out := loadPluginsCaptured(t, dir, rules); out != "" {
		t.Fatalf("method gate failed: %s", out)
	}
	if hits != 1 {
		t.Fatalf("expected exactly 1 hit (GET), got %d", hits)
	}
}

func TestPluginHTTPRedirectHopChecked(t *testing.T) {
	dir := t.TempDir()
	var hitsB int
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hitsB++ }))
	defer srvB.Close()
	// srvB.URL is rewritten to a foreign hostname (net.Listen would
	// resolve "localhost" away). srvB stays dialable at 127.0.0.1, so a
	// broken hop check would dial it and bump hitsB - the fence pins
	// "refused", not merely "failed"
	_, port, err := net.SplitHostPort(strings.TrimPrefix(srvB.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	srvB.URL = "http://localhost:" + port
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srvB.URL, http.StatusFound)
	}))
	defer srvA.Close()
	// srvA is allowlisted; the redirect hop to localhost is a policy
	// violation - the hop must be refused, srvB never dialed
	writePlugin(t, dir, "plug.lua", fmt.Sprintf(`
local resp, err = http.request("GET", %q, {})
if resp ~= nil or err == nil then error("redirect to a foreign host must be denied") end
`, srvA.URL))
	rules := map[string]config.LuaNetwork{"plug": {Targets: []string{"127.0.0.1"}}}
	if out := loadPluginsCaptured(t, dir, rules); out != "" {
		t.Fatalf("redirect hop check failed: %s", out)
	}
	if hitsB != 0 {
		t.Fatalf("redirect hop dialed the foreign server (%d hits)", hitsB)
	}
}

func TestPluginHTTPBodyCap(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, luaHTTPMaxBody+1))
	}))
	defer srv.Close()
	writePlugin(t, dir, "plug.lua", fmt.Sprintf(`
local resp, err = http.request("GET", %q, {})
if resp ~= nil then error("oversized body must fail") end
if err == nil or not string.find(err, "too large") then error("wrong error: " .. tostring(err)) end
`, srv.URL))
	rules := map[string]config.LuaNetwork{"plug": {Targets: []string{"127.0.0.1"}}}
	if out := loadPluginsCaptured(t, dir, rules); out != "" {
		t.Fatalf("body cap failed: %s", out)
	}
}

func TestLuaNetworkHostAllowed(t *testing.T) {
	for _, c := range []struct {
		host    string
		targets []string
		want    bool
	}{
		{"api.hubspot.com", []string{"api.hubspot.com"}, true},
		{"API.HUBSPOT.COM", []string{"api.hubspot.com"}, true},
		{"api.hubspot.com", []string{"*.hubspot.com"}, true},
		{"x.api.hubspot.com", []string{"*.hubspot.com"}, true},
		{"hubspot.com", []string{"*.hubspot.com"}, false}, // *.suffix never matches the bare domain
		{"evilhubspot.com", []string{"*.hubspot.com"}, false},
		{"127.0.0.1.evil.example", []string{"127.0.0.1"}, false}, // substring is not a match
		{"example.com", []string{}, false},                       // empty targets deny everything
		{"anything", []string{"*."}, false},                      // a bare "*." prefix matches nothing
	} {
		if got := luaNetworkHostAllowed(c.host, c.targets); got != c.want {
			t.Errorf("luaNetworkHostAllowed(%q, %v) = %v, want %v", c.host, c.targets, got, c.want)
		}
	}
}

func TestLuaNetworkMethodAllowed(t *testing.T) {
	for _, c := range []struct {
		method  string
		methods []string
		want    bool
	}{
		{"GET", nil, true}, // empty list = any verb
		{"DELETE", nil, true},
		{"GET", []string{"get"}, true},
		{"post", []string{"POST"}, true},
		{"DELETE", []string{"GET", "POST"}, false},
	} {
		if got := luaNetworkMethodAllowed(c.method, c.methods); got != c.want {
			t.Errorf("luaNetworkMethodAllowed(%q, %v) = %v, want %v", c.method, c.methods, got, c.want)
		}
	}
}

func TestPluginKey(t *testing.T) {
	for _, c := range []struct{ path, want string }{
		{"hubspot.lua", "hubspot"},
		{"/home/u/.config/notmutt/lua/act.lua", "act"},
		{"noext", "noext"},
	} {
		if got := pluginKey(c.path); got != c.want {
			t.Errorf("pluginKey(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestLuaJSONModule(t *testing.T) {
	vm, _, cancel, err := newSandboxVM(actionDeadline)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	defer vm.Close()
	setPluginNet(vm, nil) // json always, http absent without a section
	if err := vm.DoString(`
local s, err = json.encode({a = 1, b = "x", c = {true}})
if s == nil then error("encode: " .. tostring(err)) end
if s ~= '{"a":1,"b":"x","c":[true]}' then error("encode mismatch: " .. s) end
local d, derr = json.decode(s)
if d == nil then error("decode: " .. tostring(derr)) end
if d.a ~= 1 or d.b ~= "x" or d.c[1] ~= true then error("decode mismatch") end
local bad, berr = json.decode("not json")
if bad ~= nil or berr == nil then error("malformed json must error") end
local t, terr = json.encode({})
if t == nil or terr ~= nil then error("empty table must encode to {} not error") end
`); err != nil {
		t.Fatalf("json module: %v", err)
	}
}
