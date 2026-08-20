// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"notmutt/core"
	"notmutt/i18n"
	"notmutt/mail"
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
	loadLuaPlugins(dir, nil)

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
	loadLuaPlugins(dir, nil)

	openThread(fw, bus, "t1", "", false, core.RenderPlain, false, 0, false, nil, nil)

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
	loadLuaPlugins(dir, nil)

	openThread(fw, bus, "t1", "", false, core.RenderPlain, false, 0, false, nil, nil)

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

	loadLuaPlugins(dir, nil)

	openThread(fw, bus, "t1", "", false, core.RenderPlain, false, 0, false, nil, nil)
	select {
	case e := <-ch:
		if _, ok := e.(core.ThreadLoaded); !ok {
			t.Fatalf("expected ThreadLoaded, got %T", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no ThreadLoaded")
	}
}

// TestLuaTranslateBinding pins the translate() global (decision record
// 24): the plugin binding is backed by the same embedded bundle as the
// UI, so its output rides the catalog lookup.
func TestLuaTranslateBinding(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	fw := &fakeTagWorker{fakeWorker: &fakeWorker{}}
	fw.setMsgs([]core.Message{{ID: "a", ThreadID: "t1"}})
	saved := renderHooks
	defer restoreRenderHooks(saved, renderHookBudget)
	i18n.SetLanguage("en")
	dir := pluginDir(t, map[string]string{"tr.lua": `
function body_render(lines)
  return { { text = translate("save attachment to: "), kind = 2, quoted = 0 } }
end
`})
	loadLuaPlugins(dir, nil)

	openThread(fw, bus, "t1", "", false, core.RenderPlain, false, 0, false, nil, nil)
	select {
	case e := <-ch:
		tl, ok := e.(core.ThreadLoaded)
		if !ok {
			t.Fatalf("expected ThreadLoaded, got %T", e)
		}
		last := tl.Lines[len(tl.Lines)-1]
		if last.Text != "save attachment to: " {
			t.Fatalf("translate must serve the catalog: %+v", last)
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

	loadLuaPlugins(dir, nil)

	if len(renderHooks) != 0 {
		t.Fatalf("a plugin using os must not register, hooks=%d", len(renderHooks))
	}
}

// TestLuaCategorizeRegisters pins the categorize adapter end to end: a
// plugin declaring only categorize registers a hook (the per-global
// registration), the handle fetches the message's attachment list via
// get_attachments, msg carries the metadata-only projection, and the
// ordinal-keyed table is the contract's return shape.
func TestLuaCategorizeRegisters(t *testing.T) {
	saved := categorizeHooks
	defer func() { categorizeHooks = saved }()
	dir := pluginDir(t, map[string]string{"cat.lua": `
function categorize(handle, msg)
  local out = {}
  for i, att in ipairs(get_attachments(handle)) do
    if att.mime == "application/pdf" and re_match("invoice|receipt", msg.subject) then
      out[i] = msg.from .. "/" .. string.format("%d", msg.date) .. "/" .. string.format("%d", att.size)
    end
  end
  return out
end
`})
	loadLuaPlugins(dir, nil)
	if len(categorizeHooks) != 1 {
		t.Fatalf("categorize hooks = %d, want 1", len(categorizeHooks))
	}
	h := registerAttachments([]mail.Attachment{
		{Name: "invoice.pdf", MimeType: "application/pdf", Size: 1234},
		{Name: "photo.png", MimeType: "image/png", Size: 5},
	})
	defer unregisterAttachments(h)
	meta := AttachMeta{From: "delta@example.com", Subject: "hotel invoice", Date: 1720000000}
	cats, err := categorizeHooks[0](h, meta)
	if err != nil {
		t.Fatal(err)
	}
	if len(cats) != 1 || cats[1] != "delta@example.com/1720000000/1234" {
		t.Fatalf("categories = %+v, want the pdf's ordinal-keyed category", cats)
	}
	// unknown handle: get_attachments raises, the hook surfaces the error
	if _, err := categorizeHooks[0]("att-999999", meta); err == nil || !strings.Contains(err.Error(), "unknown mail handle") {
		t.Fatalf("an unknown handle must error, got %v", err)
	}
}

// TestLuaReMatchCompileError pins the two-value contract: a bad
// pattern is false plus the error text (single-value use keeps
// working), never a panic.
func TestLuaReMatchCompileError(t *testing.T) {
	saved := categorizeHooks
	defer func() { categorizeHooks = saved }()
	dir := pluginDir(t, map[string]string{"re.lua": `
function categorize(handle, msg)
  local ok, err = re_match("(", msg.subject)
  if not ok and err then error("re_match: " .. err) end
  return nil
end
`})
	loadLuaPlugins(dir, nil)
	if _, err := categorizeHooks[0]("", AttachMeta{Subject: "x"}); err == nil || !strings.Contains(err.Error(), "re_match:") {
		t.Fatalf("a compile error must surface as false+err, got %v", err)
	}
}

// TestLuaCategorizeDeadline pins the kill switch: a busy-looping
// categorize is killed by the per-call budget, the VM is closed, and
// the disabled plugin fails fast on later calls.
func TestLuaCategorizeDeadline(t *testing.T) {
	saved := categorizeHooks
	defer func() { categorizeHooks = saved }()
	savedBudget := attachHookBudget
	attachHookBudget = 50 * time.Millisecond
	defer func() { attachHookBudget = savedBudget }()
	dir := pluginDir(t, map[string]string{"busy.lua": `
function categorize(handle, msg)
  while true do end
end
`})
	loadLuaPlugins(dir, nil)
	if _, err := categorizeHooks[0]("", AttachMeta{}); err == nil {
		t.Fatal("a busy loop must be killed by the budget")
	}
	if _, err := categorizeHooks[0]("", AttachMeta{}); err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatal("a killed plugin must fail fast")
	}
}
