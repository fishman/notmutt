// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"notmutt/config"
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

// TestLuaRegisterAttachCommand: register_attach_command during DoFile
// lands in the registry (R8).
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

// TestLuaBodyRenderTransforms: a plugin's body_render runs on the open
// job and its output rides ThreadLoaded to the TUI (decision record 20's
// adapter shape, this time through Lua).
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

	openThread(fw, bus, nil, "t1", "", false, core.RenderPlain, false, 0, false, nil, config.Crypto{}, false, "")

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

// TestLuaBodyRenderDeadlineFallsBack: a busy-looping body_render is
// killed by the chain deadline via SetContext, the open completes with
// the un-hooked render - the fallback holds per call, the plugin is not
// disabled.
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

	openThread(fw, bus, nil, "t1", "", false, core.RenderPlain, false, 0, false, nil, config.Crypto{}, false, "")

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

// TestLuaPluginLoadErrorSkips: a broken file logs and skips, the good
// plugin still registers.
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

	openThread(fw, bus, nil, "t1", "", false, core.RenderPlain, false, 0, false, nil, config.Crypto{}, false, "")
	select {
	case e := <-ch:
		if _, ok := e.(core.ThreadLoaded); !ok {
			t.Fatalf("expected ThreadLoaded, got %T", e)
		}
	case <-time.After(time.Second):
		t.Fatal("no ThreadLoaded")
	}
}

// TestLuaTranslateBinding (decision record 24): translate() is backed
// by the same embedded bundle as the UI, so its output rides the
// catalog lookup.
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

	openThread(fw, bus, nil, "t1", "", false, core.RenderPlain, false, 0, false, nil, config.Crypto{}, false, "")
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

// TestLuaSandboxNoOS: a plugin touching os fails to load (no os/io/debug
// in the whitelist - no filesystem, no exec) and is skipped without
// affecting the client.
func TestLuaSandboxNoOS(t *testing.T) {
	dir := pluginDir(t, map[string]string{"os.lua": "os.exit(1)\nfunction body_render(lines) return lines end"})
	saved := renderHooks
	defer restoreRenderHooks(saved, renderHookBudget)

	loadLuaPlugins(dir, nil)

	if len(renderHooks) != 0 {
		t.Fatalf("a plugin using os must not register, hooks=%d", len(renderHooks))
	}
}

// TestLuaCategorizeRegisters: a plugin declaring only categorize
// registers a hook (per-global registration); the handle fetches the
// attachment list via get_attachments, msg is the metadata-only
// projection, and the ordinal-keyed table is the return contract.
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

// TestLuaGetAttachmentsExt: the get_attachments row carries ext - the
// filename extension, lowercased, dot stripped (filepath.Ext) - so a
// plugin keys on the sender's naming, not the parser-reported mime. An
// uppercase or multi-dot name normalizes; a name with no extension is
// an empty ext. mime stays available alongside.
func TestLuaGetAttachmentsExt(t *testing.T) {
	saved := categorizeHooks
	defer func() { categorizeHooks = saved }()
	dir := pluginDir(t, map[string]string{"ext.lua": `
function categorize(handle, msg)
  local out = {}
  for i, att in ipairs(get_attachments(handle)) do
    out[i] = att.ext .. "/" .. att.mime
  end
  return out
end
`})
	loadLuaPlugins(dir, nil)
	if len(categorizeHooks) != 1 {
		t.Fatalf("categorize hooks = %d, want 1", len(categorizeHooks))
	}
	h := registerAttachments([]mail.Attachment{
		{Name: "invoice.PDF", MimeType: "application/octet-stream"},
		{Name: "a.b.docx", MimeType: "application/octet-stream"},
		{Name: "noext", MimeType: "text/plain"},
	})
	defer unregisterAttachments(h)
	cats, err := categorizeHooks[0](h, AttachMeta{From: "a@example.com", Subject: "s", Date: 1})
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]string{
		1: "pdf/application/octet-stream",
		2: "docx/application/octet-stream",
		3: "/text/plain",
	}
	for i := 1; i <= 3; i++ {
		if cats[i] != want[i] {
			t.Fatalf("attachment %d ext/mime = %q, want %q", i, cats[i], want[i])
		}
	}
}

// TestLuaReMatchCompileError: a bad pattern is false plus the error
// text (single-value use keeps working), never a panic.
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

// TestLuaDateStr: the YYYY/MM/DD token pattern formats a unix timestamp
// (the calendar lives in Go, not the plugin); the default pattern is
// YYYY/MM.
func TestLuaDateStr(t *testing.T) {
	saved := categorizeHooks
	defer func() { categorizeHooks = saved }()
	dir := pluginDir(t, map[string]string{"ds.lua": `
function categorize(handle, msg)
  local out = {}
  out[1] = date_str(msg.date, "YYYY/MM")
  out[2] = date_str(msg.date, "YYYY-MM")
  out[3] = date_str(msg.date)
  out[4] = date_str(msg.date, "MM/YYYY")
  return out
end
`})
	loadLuaPlugins(dir, nil)
	meta := AttachMeta{Date: 1720000000} // 2024-07-03T12:26:40Z
	cats, err := categorizeHooks[0]("", meta)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]string{1: "2024/07", 2: "2024-07", 3: "2024/07", 4: "07/2024"}
	for o, w := range want {
		if cats[o] != w {
			t.Fatalf("ordinal %d = %q, want %q", o, cats[o], w)
		}
	}
}

// TestLuaCategorizeDeadline: a busy-looping categorize is killed by the
// per-call budget every call - bounded, never a session disable. The VM
// survives the kill, so the plugin keeps retrying instead of dying.
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
	for i := 0; i < 2; i++ {
		if _, err := categorizeHooks[0]("", AttachMeta{}); err == nil || strings.Contains(err.Error(), "not loaded") {
			t.Fatalf("call %d: a busy loop must be killed per-call, got %v", i, err)
		}
	}
}

// TestLuaCategorizeRecovers: a deadline kill must not disable the plugin -
// one slow call fails, the next call on the same VM succeeds.
func TestLuaCategorizeRecovers(t *testing.T) {
	saved := categorizeHooks
	defer func() { categorizeHooks = saved }()
	savedBudget := attachHookBudget
	attachHookBudget = 50 * time.Millisecond
	defer func() { attachHookBudget = savedBudget }()
	dir := pluginDir(t, map[string]string{"slow.lua": `
local slow = true
function categorize(handle, msg)
  if slow then
    slow = false
    while true do end
  end
  return {"inbox"}
end
`})
	loadLuaPlugins(dir, nil)
	if _, err := categorizeHooks[0]("", AttachMeta{}); err == nil {
		t.Fatal("the slow first call must be killed by the budget")
	}
	cats, err := categorizeHooks[0]("", AttachMeta{})
	if err != nil {
		t.Fatalf("the second call must succeed after the kill: %v", err)
	}
	if cats[1] != "inbox" {
		t.Fatalf("recovered categorize = %v, want {1: inbox}", cats)
	}
}

// TestLuaLog: a plugin's log() publishes a LuaLog event - the session
// log the TUI shows; the error flag rides along.
func TestLuaLog(t *testing.T) {
	bus := core.NewBus()
	saved := luaLogBus
	luaLogBus = bus
	defer func() { luaLogBus = saved }()
	ch := bus.Subscribe()
	hookSaved := renderHooks
	defer restoreRenderHooks(hookSaved, renderHookBudget)
	dir := pluginDir(t, map[string]string{"log.lua": `
function body_render(lines)
  log("indexed " .. #lines .. " lines")
  log("a failure", true)
  return lines
end
`})
	loadLuaPlugins(dir, nil)
	if _, err := renderHooks[0](context.Background(), []core.Line{{Text: "x", Kind: core.LineBody}}); err != nil {
		t.Fatal(err)
	}
	var got []core.LuaLog
	for len(got) < 2 {
		select {
		case e := <-ch:
			if le, ok := e.(core.LuaLog); ok {
				got = append(got, le)
			}
		case <-time.After(time.Second):
			t.Fatal("no LuaLog events")
		}
	}
	if got[0].Text != "indexed 1 lines" || got[0].Err {
		t.Fatalf("first log = %+v", got[0])
	}
	if got[1].Text != "a failure" || !got[1].Err {
		t.Fatalf("second log = %+v", got[1])
	}
}

// TestLuaRefreshRegisters: a plugin's refresh(ctx) registers a
// RefreshHook; the ctx carries the active view and, for an account view,
// the account tag and config name. The return is the pre-poll argv.
func TestLuaRefreshRegisters(t *testing.T) {
	saved := refreshHooks
	defer func() { refreshHooks = saved }()
	dir := pluginDir(t, map[string]string{"r.lua": `
function refresh(ctx)
  if ctx.account then
    return {"mbsync", ctx.account_name}
  end
end
`})
	loadLuaPlugins(dir, nil)
	if len(refreshHooks) != 1 {
		t.Fatalf("refresh hooks = %d, want 1", len(refreshHooks))
	}
	cfg := config.Default()
	// the gmail placeholder: the account view returns the mbsync argv
	argv, err := refreshHooks[0](context.Background(), refreshCtxFor(cfg, "gmail"))
	if err != nil {
		t.Fatal(err)
	}
	if len(argv) != 2 || argv[0] != "mbsync" || argv[1] != "gmail" {
		t.Fatalf("account view argv = %v", argv)
	}
	// a plain view carries no account: the hook returns nil - a plain poll
	argv, err = refreshHooks[0](context.Background(), refreshCtxFor(cfg, "inbox"))
	if err != nil || argv != nil {
		t.Fatalf("plain view argv = %v, err = %v (want nil, nil)", argv, err)
	}
}

// TestLuaRefreshDeadline: a busy-looping refresh is killed by the hook
// budget every call - bounded, never a session disable. The VM survives
// the kill, so the next poll retries instead of dying.
func TestLuaRefreshDeadline(t *testing.T) {
	saved := refreshHooks
	defer func() { refreshHooks = saved }()
	savedBudget := refreshHookBudget
	refreshHookBudget = 50 * time.Millisecond
	defer func() { refreshHookBudget = savedBudget }()
	dir := pluginDir(t, map[string]string{"busy.lua": `
function refresh(ctx)
  while true do end
end
`})
	loadLuaPlugins(dir, nil)
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		_, err := refreshHooks[0](ctx, RefreshCtx{})
		cancel()
		if err == nil || strings.Contains(err.Error(), "not loaded") {
			t.Fatalf("call %d: a busy loop must be killed per-call, got %v", i, err)
		}
	}
}
