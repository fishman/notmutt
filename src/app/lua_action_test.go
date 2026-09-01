// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua

package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"notmutt/config"
	"notmutt/core"
)

// writePlugin writes one plugin file into dir (the load path reads
// <configdir>/lua/*.lua sorted; unique action names keep the package
// registry clean across tests).
func writePlugin(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLuaActionRegistry(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "act.lua", `
register_action("act-1", function(ctx) end)
bind_key("x", "pager", "run act-1", function(ctx) end)
`)
	loadLuaPlugins(dir, nil)
	if !pluginActionNames()["act-1"] {
		t.Fatal("plugin action act-1 not registered")
	}
	if !luaKeyBound("x", "pager") {
		t.Fatal("plugin key x in pager not registered")
	}
}

func TestLuaActionRun(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "act.lua", `
register_action("act-print", function(ctx)
    print("hello", "plugin")
    attach_add("/tmp/alpha.txt")
end)
`)
	loadLuaPlugins(dir, nil)
	bus := core.NewBus()
	ch := bus.Subscribe()
	runLuaAction("act-print", "t1", bus, &config.Config{}, &fakeWorker{})
	var gotFiles []string
	var lr core.LuaResult
	for e := range ch {
		switch v := e.(type) {
		case core.AttachFiles:
			gotFiles = v.Paths
		case core.LuaResult:
			lr = v
			goto done
		}
	}
done:
	if lr.Err != nil {
		t.Fatal(lr.Err)
	}
	if lr.Output != "hello\tplugin\n" {
		t.Fatalf("print output = %q", lr.Output)
	}
	if len(gotFiles) != 1 || gotFiles[0] != "/tmp/alpha.txt" {
		t.Fatalf("attach effects = %v", gotFiles)
	}
}

func TestLuaActionTagStage(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "act.lua", `
register_action("act-tag", function(ctx)
    tag_add("work")
    tag_remove("unread")
end)
`)
	loadLuaPlugins(dir, nil)
	bus := core.NewBus()
	ch := bus.Subscribe()
	runLuaAction("act-tag", "t1", bus, &config.Config{}, &fakeWorker{})
	for e := range ch {
		if v, ok := e.(core.TagStaged); ok {
			if v.ThreadID != "t1" {
				t.Fatalf("TagStaged thread = %q", v.ThreadID)
			}
			if len(v.Ops) != 2 || v.Ops[0] != (core.TagOp{Tag: "work", Add: true}) || v.Ops[1] != (core.TagOp{Tag: "unread", Add: false}) {
				t.Fatalf("TagStaged ops = %v", v.Ops)
			}
			return
		}
		if _, ok := e.(core.LuaResult); ok {
			t.Fatal("no TagStaged before LuaResult")
		}
	}
}

func TestLuaActionMailLines(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "act.lua", `
register_action("act-lines", function(ctx)
    print(table.concat(ctx.mail_lines(), "|"))
end)
`)
	loadLuaPlugins(dir, nil)
	f := filepath.Join(t.TempDir(), "mail1.eml")
	fixture := "From: sender@example.com\nTo: alpha@example.com\nSubject: quarterly report\nMessage-ID: <m1@example.com>\nDate: Mon, 17 Aug 2026 10:00:00 +0000\nContent-Type: text/plain\n\nQ2 numbers attached.\n"
	if err := os.WriteFile(f, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	fw := &fakeWorker{}
	fw.setMsgs([]core.Message{{ID: "m1", ThreadID: "t1", Paths: []string{f}}})
	bus := core.NewBus()
	ch := bus.Subscribe()
	runLuaAction("act-lines", "t1", bus, &config.Config{}, fw)
	var lr core.LuaResult
	for e := range ch {
		if v, ok := e.(core.LuaResult); ok {
			lr = v
			break
		}
	}
	if lr.Err != nil {
		t.Fatal(lr.Err)
	}
	if !strings.Contains(lr.Output, "Q2 numbers attached.") {
		t.Fatalf("mail_lines output = %q", lr.Output)
	}
}

func TestLuaActionAiChat(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Summary\"}}]}\n\ndata: {\"choices\":[{\"delta\":{\"content\":\" done\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()

	dir := t.TempDir()
	writePlugin(t, dir, "act.lua", `
register_action("act-ai", function(ctx)
    local out = ai_chat("local", { text = "summarize the mail" })
    print(out)
end)
`)
	loadLuaPlugins(dir, nil)
	cfg := &config.Config{
		AI: map[string]config.AIProvider{
			"local": {Type: "openai", Model: "qwen3:8b", BaseURL: srv.URL},
		},
		// ai_chat is the network egress: it requires the [lua.network] gate
		// (the data policy - content never enters a VM that cannot reach
		// the network)
		Lua: config.Lua{Network: map[string]config.LuaNetwork{
			"act": {Targets: []string{"127.0.0.1"}, Paths: []string{"POST /v1/*"}},
		}},
	}
	bus := core.NewBus()
	ch := bus.Subscribe()
	runLuaAction("act-ai", "t1", bus, cfg, &fakeWorker{})
	var started bool
	var chunks []string
	var lr core.LuaResult
	for e := range ch {
		switch v := e.(type) {
		case core.AiStarted:
			if v.ThreadID != "t1" {
				t.Fatalf("AiStarted thread = %q", v.ThreadID)
			}
			started = true
		case core.AiChunk:
			chunks = append(chunks, v.Text)
		case core.LuaResult:
			lr = v
			goto done
		}
	}
done:
	if !started {
		t.Fatal("no AiStarted event")
	}
	if strings.Join(chunks, "") != "Summary done" {
		t.Fatalf("chunks = %v", chunks)
	}
	if lr.Err != nil {
		t.Fatal(lr.Err)
	}
	if lr.Output != "Summary done\n" {
		t.Fatalf("print output = %q", lr.Output)
	}
}

func TestLuaActionPicker(t *testing.T) {
	dir := t.TempDir()
	writePlugin(t, dir, "act.lua", `
register_action("act-pick", function(ctx)
    local files = picker_argv({ "yazi", "--chooser-file" })
    print(files and files[1] or "none")
end)
`)
	loadLuaPlugins(dir, nil)
	bus := core.NewBus()
	ch := bus.Subscribe()
	done := make(chan core.LuaResult, 1)
	go runLuaAction("act-pick", "t1", bus, &config.Config{}, &fakeWorker{})
	// the runner publishes on the same bus; deliver the fake chooser's
	// selection for the request it must publish
	for e := range ch {
		if v, ok := e.(core.PickerRequest); ok {
			deliverPickerResult(core.PickerResult{ID: v.ID, Paths: []string{"/tmp/alpha.txt"}})
		}
		if v, ok := e.(core.LuaResult); ok {
			done <- v
			break
		}
	}
	select {
	case lr := <-done:
		if lr.Err != nil {
			t.Fatal(lr.Err)
		}
		if lr.Output != "/tmp/alpha.txt\n" {
			t.Fatalf("print output = %q", lr.Output)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("picker round trip did not complete")
	}
}

func TestRunLuaCommand(t *testing.T) {
	f := filepath.Join(t.TempDir(), "mail1.eml")
	fixture := "From: sender@example.com\nTo: alpha@example.com\nSubject: quarterly report\nMessage-ID: <m1@example.com>\nDate: Mon, 17 Aug 2026 10:00:00 +0000\nContent-Type: text/plain\n\nQ2 numbers attached.\n"
	if err := os.WriteFile(f, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	fw := &fakeWorker{}
	fw.setMsgs([]core.Message{{ID: "m1", ThreadID: "t1", Paths: []string{f}}})
	bus := core.NewBus()
	ch := bus.Subscribe()
	runLuaCommand(`lua print(ctx.thread_id .. "|" .. table.concat(ctx.mail_lines(), "|"))`, "t1", bus, &config.Config{}, fw)
	var lr core.LuaResult
	for e := range ch {
		if v, ok := e.(core.LuaResult); ok {
			lr = v
			break
		}
	}
	if lr.Err != nil {
		t.Fatal(lr.Err)
	}
	if !strings.Contains(lr.Output, "t1|") || !strings.Contains(lr.Output, "Q2 numbers attached.") {
		t.Fatalf(":lua output = %q", lr.Output)
	}

	// a non-lua command is an error, never silently dropped
	runLuaCommand("quit", "t1", bus, &config.Config{}, fw)
	for e := range ch {
		if v, ok := e.(core.LuaResult); ok {
			if v.Err == nil || !strings.Contains(v.Err.Error(), "unknown command") {
				t.Fatalf("unknown command must error: %+v", v)
			}
			break
		}
	}
}

func TestLuaCommandPrompt(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	done := make(chan core.LuaResult, 1)
	go runLuaCommand(`lua local a = prompt("Language:"); print(a or "none")`, "t1", bus, &config.Config{}, &fakeWorker{})
	// the runner publishes on the same bus; deliver the TUI's answer for
	// the request it must publish
	for e := range ch {
		if v, ok := e.(core.PromptRequest); ok {
			deliverPromptResult(core.PromptResult{ID: v.ID, Text: "english"})
		}
		if v, ok := e.(core.LuaResult); ok {
			done <- v
			break
		}
	}
	select {
	case lr := <-done:
		if lr.Err != nil {
			t.Fatal(lr.Err)
		}
		if lr.Output != "english\n" {
			t.Fatalf("prompt output = %q", lr.Output)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("prompt round trip did not complete")
	}
}
