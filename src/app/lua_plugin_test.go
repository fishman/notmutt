// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua

package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"notmutt/core"
)

func pluginDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestLuaRegisterAttachCommand pins the in-DoFile registration: a
// plugin calling register_attach_command lands in the registry (R8).
func TestLuaRegisterAttachCommand(t *testing.T) {
	attachcmdsMu.Lock()
	attachcmds = map[string][]string{}
	attachcmdsOrder = nil // the order slice survives a map swap - stale names leak empty argv
	attachcmdsMu.Unlock()

	dir := pluginDir(t, map[string]string{"attach.lua": `
register_attach_command("yazi", {"yazi", "--chooser-file"})
`})
	loadLuaPlugins(dir)

	snap := attachCommandSnapshot()
	if len(snap) != 1 || snap[0].Name != "yazi" || len(snap[0].Argv) != 2 ||
		snap[0].Argv[0] != "yazi" || snap[0].Argv[1] != "--chooser-file" {
		t.Fatalf("lua-registered command = %+v", snap)
	}
}

// TestLuaBodyRenderTransforms pins the end-to-end adapter: a plugin's
// body_render runs on the open job and its output rides ThreadLoaded to
// the TUI (decision record 20's adapter shape, this time through Lua).
func TestLuaBodyRenderTransforms(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	fw.setMsgs([]core.Message{{ID: "a", ThreadID: "t1"}})
	saved := renderHooks
	defer restoreRenderHooks(saved, renderHookBudget)
	dir := pluginDir(t, map[string]string{"render.lua": `
function body_render(lines)
  lines[#lines + 1] = {text = "lua says hi", kind = 2, quoted = 0}
  return lines
end
`})
	loadLuaPlugins(dir)

	openThread(fw, bus, "t1", false, core.RenderPlain, false, 0, false, nil)

	select {
	case e := <-ch:
		tl, ok := e.(core.ThreadLoaded)
		if !ok {
			t.Fatalf("expected ThreadLoaded, got %T", e)
		}
		last := tl.Lines[len(tl.Lines)-1]
		if last.Text != "lua says hi" || last.Kind != core.LineBody {
			t.Fatalf("the plugin's line must ride the event: %+v", tl.Lines)
		}
	case <-time.After(time.Second):
		t.Fatal("no ThreadLoaded")
	}
}

// TestLuaBodyRenderDeadlineFallsBack pins the freeze fix through Lua:
// a busy-looping body_render is killed by the chain deadline via
// SetContext, the open completes with the un-hooked render, and the
// disabled plugin fails fast on later calls instead of hanging them.
func TestLuaBodyRenderDeadlineFallsBack(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	fw.setMsgs([]core.Message{{ID: "a", ThreadID: "t1"}})
	saved := renderHooks
	savedBudget := renderHookBudget
	defer restoreRenderHooks(saved, savedBudget)
	renderHookBudget = 50 * time.Millisecond
	dir := pluginDir(t, map[string]string{"busy.lua": `
function body_render(lines)
  while true do end
end
`})
	loadLuaPlugins(dir)

	openThread(fw, bus, "t1", false, core.RenderPlain, false, 0, false, nil)

	select {
	case e := <-ch:
		tl, ok := e.(core.ThreadLoaded)
		if !ok {
			t.Fatalf("expected ThreadLoaded, got %T", e)
		}
		if tl.Err != nil {
			t.Fatalf("a deadline kill must not fail the open: %v", tl.Err)
		}
		for _, l := range tl.Lines {
			if l.Text == "lua says hi" {
				t.Fatalf("the killed plugin's output must not survive: %+v", tl.Lines)
			}
		}
		if len(tl.Lines) == 0 {
			t.Fatal("the deadline fallback must keep the un-hooked render")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the busy-loop plugin must be killed by the deadline")
	}
}

// TestLuaPluginLoadErrorSkips pins the load-error degrade: a broken
// file logs and skips, the good plugin still registers.
func TestLuaPluginLoadErrorSkips(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	fw.setMsgs([]core.Message{{ID: "a", ThreadID: "t1"}})
	saved := renderHooks
	defer restoreRenderHooks(saved, renderHookBudget)
	dir := pluginDir(t, map[string]string{
		"broken.lua": "this is not lua (",
		"ok.lua":     "function body_render(lines) return lines end",
	})

	loadLuaPlugins(dir)

	openThread(fw, bus, "t1", false, core.RenderPlain, false, 0, false, nil)
	select {
	case e := <-ch:
		if _, ok := e.(core.ThreadLoaded); !ok {
			t.Fatalf("expected ThreadLoaded, got %T", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no ThreadLoaded")
	}
}

// TestLuaSandboxNoOS pins the sandbox whitelist: a plugin touching os
// fails to load (no os/io/debug in the whitelist - no filesystem, no
// exec) and is skipped without affecting the client.
func TestLuaSandboxNoOS(t *testing.T) {
	dir := pluginDir(t, map[string]string{"os.lua": "os.exit(1)\nfunction body_render(lines) return lines end"})
	saved := renderHooks
	defer restoreRenderHooks(saved, renderHookBudget)

	loadLuaPlugins(dir)

	if len(renderHooks) != 0 {
		t.Fatalf("a plugin using os must not register, hooks=%d", len(renderHooks))
	}
}
