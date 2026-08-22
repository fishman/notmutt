// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua

// The Lua plugin layer (R8, decision record 20): plugin files from
// <configdir>/lua, each a gopher-lua VM sandboxed to the whitelisted
// libs, with body_render registered as a BodyRenderHook. The hook runs
// on the open job under the chain deadline via SetContext - a busy
// loop is killed by the deadline, never a UI freeze. Compiles only
// under the lua build tag (the R12 pattern): default builds carry
// lua_plugin_stub.go and no Lua runtime.

package app

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"

	"notmutt/config"
	"notmutt/core"
	"notmutt/i18n"
)

// pickersLib is the bundled Lua library (pickers.lua): the
// tool-specific chooser wrappers (picker_yazi, picker_ranger) over the
// core's picker_argv primitive. The core never hardcodes a client; the
// lib is DoFile'd into every action invocation before the plugin file
// (lua_action.go), so scripts call the wrappers like built-ins.
//
//go:embed lua/pickers.lua
var pickersLib string

// luaPlugin is one loaded plugin file: its VM and body_render function.
// A VM is not concurrency-safe, so every call serializes on mu
// (decision-record 20 one-VM-one-goroutine).
type luaPlugin struct {
	vm         *lua.LState
	mu         sync.Mutex
	bodyRender *lua.LFunction
	categorize *lua.LFunction
}

// loadLuaPlugins loads every *.lua file in dir (sorted, so the render
// chain order is deterministic) and registers each plugin's body_render
// as a BodyRenderHook. Network gates the sandbox http module per
// plugin ([lua.network.<name>], lua_http.go - deny by default). A file
// that fails to load is logged and skipped (load errors degrade, they
// never kill the client). A missing dir is a no-op - no plugins
// configured.
func loadLuaPlugins(dir string, network map[string]config.LuaNetwork) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".lua" {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	for _, name := range files {
		loadLuaPlugin(filepath.Join(dir, name), network)
	}
}

func loadLuaPlugin(path string, network map[string]config.LuaNetwork) {
	vm := lua.NewState(lua.Options{SkipOpenLibs: true})
	if err := openSandboxLibs(vm, path); err != nil {
		log.Printf("lua plugin %s: %v", path, err)
		vm.Close()
		return
	}
	// the sandbox json/http modules: http only when the plugin has a
	// network section (the deny-by-default gate)
	setPluginNet(vm, networkFor(network, path))
	// register_attach_command runs DURING DoFile: a plugin file calls it
	// to add a command to the attach-command registry (R8).
	vm.SetGlobal("register_attach_command", vm.NewFunction(func(L *lua.LState) int {
		name := L.CheckString(1)
		var argv []string
		for i := 1; ; i++ {
			v := L.CheckTable(2).RawGetInt(i)
			if v == lua.LNil {
				break
			}
			argv = append(argv, v.String())
		}
		registerAttachCommand(name, argv)
		return 0
	}))
	// register_action and bind_key run during DoFile too: name/key ->
	// THIS plugin file; an invocation re-runs the file in a fresh VM
	// before calling the registered fn (lua_action.go)
	vm.SetGlobal("register_action", vm.NewFunction(func(L *lua.LState) int {
		registerAction(L.CheckString(1), path)
		L.CheckFunction(2) // type-check the fn; the invocation re-registers the callable
		return 0
	}))
	vm.SetGlobal("bind_key", vm.NewFunction(func(L *lua.LState) int {
		registerBind(L.CheckString(1), L.CheckString(2), L.CheckString(3), path)
		L.CheckFunction(4)
		return 0
	}))
	// translate runs the session language lookup (decision record 24):
	// the same embedded bundle as the client UI, selected by the [ui]
	// language - never plugin config.
	vm.SetGlobal("translate", vm.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LString(i18n.T(L.CheckString(1))))
		return 1
	}))
	// re_match is the regex helper (Go regexp syntax - Lua string
	// patterns have no alternation): match(bool), err(string or nil). A
	// compile error is false plus the error text, so the common
	// single-value usage keeps working.
	vm.SetGlobal("re_match", vm.NewFunction(func(L *lua.LState) int {
		re, err := regexp.Compile(L.CheckString(1))
		if err != nil {
			L.Push(lua.LFalse)
			L.Push(lua.LString(err.Error()))
			return 2
		}
		L.Push(lua.LBool(re.MatchString(L.CheckString(2))))
		L.Push(lua.LNil)
		return 2
	}))
	// get_attachments is the mail-handle fetch (the categorize
	// contract): returns name/mime/size/ordinal per attachment for the
	// handle the save pass passed to categorize. The plugin never opens
	// files - the list is what the client parsed.
	vm.SetGlobal("get_attachments", vm.NewFunction(func(L *lua.LState) int {
		atts, ok := attachmentsForHandle(L.CheckString(1))
		if !ok {
			L.RaiseError("get_attachments: unknown mail handle")
		}
		tbl := L.NewTable()
		for i, a := range atts {
			row := L.NewTable()
			row.RawSetString("name", lua.LString(a.Name))
			row.RawSetString("mime", lua.LString(a.MimeType))
			row.RawSetString("size", lua.LNumber(a.Size))
			row.RawSetString("ordinal", lua.LNumber(i+1))
			tbl.Append(row)
		}
		L.Push(tbl)
		return 1
	}))
	// date_str formats a unix timestamp by the YYYY/MM/DD token pattern
	// (the same tokens as the [attachments] layout): date_str(msg.date,
	// "YYYY/MM") -> "2026/08". Literal text passes through; the default
	// pattern is "YYYY/MM". The calendar lives here, not in the plugin.
	vm.SetGlobal("date_str", vm.NewFunction(func(L *lua.LState) int {
		pattern := L.OptString(2, "YYYY/MM")
		s := time.Unix(int64(L.CheckNumber(1)), 0).UTC().Format(dateLayout(pattern))
		L.Push(lua.LString(s))
		return 1
	}))
	if err := vm.DoFile(path); err != nil {
		log.Printf("lua plugin %s: %v", path, err)
		vm.Close()
		return
	}
	// hooks register per declared global: a plugin may carry only
	// body_render, only categorize, or both - each registers
	// independently
	p := &luaPlugin{vm: vm}
	if fn := vm.GetGlobal("body_render"); fn != lua.LNil {
		lf, ok := fn.(*lua.LFunction)
		if !ok {
			log.Printf("lua plugin %s: body_render must be a function", path)
		} else {
			p.bodyRender = lf
			RegisterBodyRenderHook(func(ctx context.Context, lines []core.Line) ([]core.Line, error) {
				return p.renderBody(ctx, lines)
			})
		}
	}
	if fn := vm.GetGlobal("categorize"); fn != lua.LNil {
		lf, ok := fn.(*lua.LFunction)
		if !ok {
			log.Printf("lua plugin %s: categorize must be a function", path)
		} else {
			p.categorize = lf
			RegisterCategorizeHook(func(handle string, m AttachMeta) (map[int]string, error) {
				return p.categorizeMessage(handle, m)
			})
		}
	}
	if p.bodyRender == nil && p.categorize == nil {
		// a hook-less plugin (http helpers, actions, attach commands)
		// loaded its side effects during DoFile; nothing to register
		vm.Close()
	}
}

// openSandboxLibs opens the whitelisted libs (decision record 20 point
// 3): no os/io/debug - no filesystem, no exec. Package opens first so
// require works for plugin-local files. Shared by the load-time and
// per-invocation VMs (the action layer, lua_action.go).
func openSandboxLibs(vm *lua.LState, path string) error {
	for _, pair := range []struct {
		name string
		fn   lua.LGFunction
	}{
		{lua.LoadLibName, lua.OpenPackage},
		{lua.BaseLibName, lua.OpenBase},
		{lua.TabLibName, lua.OpenTable},
		{lua.StringLibName, lua.OpenString},
		{lua.MathLibName, lua.OpenMath},
	} {
		if err := vm.CallByParam(lua.P{Fn: vm.NewFunction(pair.fn), NRet: 0, Protect: true}, lua.LString(pair.name)); err != nil {
			return fmt.Errorf("open lib %s: %w", pair.name, err)
		}
	}
	return nil
}

// renderBody runs the plugin's body_render under the chain context.
// SetContext is the kill switch: a deadline aborts the VM mid-loop, the
// hook fails, and the chain falls back to the un-hooked render. A
// deadline kill leaves the VM unusable, so the plugin is disabled
// (dropped VM, every later call fails fast) - a wedged plugin degrades
// to the fallback, it never takes the open down.
func (p *luaPlugin) renderBody(ctx context.Context, lines []core.Line) ([]core.Line, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.vm == nil {
		return nil, fmt.Errorf("lua plugin disabled after a deadline kill")
	}
	p.vm.SetContext(ctx)
	out, err := p.callBodyRender(lines)
	if err != nil && ctx.Err() != nil {
		p.vm.Close()
		p.vm = nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// callBodyRender converts the lines to a Lua table, calls the plugin's
// body_render, and converts the returned table back. The output is
// plugin-authored, user-installed code - the same trust as config, not
// mail content (F1's sanitize boundary already ran). A malformed return
// (non-table, non-string rows) is a hook failure - the chain falls
// back.
func (p *luaPlugin) callBodyRender(lines []core.Line) ([]core.Line, error) {
	arg := p.vm.NewTable()
	for _, l := range lines {
		row := p.vm.NewTable()
		row.RawSetString("text", lua.LString(l.Text))
		row.RawSetString("kind", lua.LNumber(l.Kind))
		row.RawSetString("quoted", lua.LNumber(l.Quoted))
		arg.Append(row)
	}
	p.vm.Push(p.bodyRender)
	p.vm.Push(arg)
	if err := p.vm.PCall(1, 1, nil); err != nil {
		return nil, err
	}
	ret := p.vm.Get(-1)
	p.vm.Pop(1)
	tbl, ok := ret.(*lua.LTable)
	if !ok {
		return nil, fmt.Errorf("body_render must return a table, got %s", ret.Type().String())
	}
	var out []core.Line
	for i := 1; ; i++ {
		row := tbl.RawGetInt(i)
		if row == lua.LNil {
			break
		}
		rt, ok := row.(*lua.LTable)
		if !ok {
			return nil, fmt.Errorf("body_render row %d must be a table", i)
		}
		text, ok := rt.RawGetString("text").(lua.LString)
		if !ok {
			return nil, fmt.Errorf("body_render row %d: text must be a string", i)
		}
		out = append(out, core.Line{
			Text:   string(text),
			Kind:   core.LineKind(lua.LVAsNumber(rt.RawGetString("kind"))),
			Quoted: int(lua.LVAsNumber(rt.RawGetString("quoted"))),
		})
	}
	return out, nil
}

// attachHookBudget bounds one categorize call - the same kill-switch
// role as the render chain's deadline: a busy-looping plugin falls
// back, it never blocks the save pass.
var attachHookBudget = time.Second

// categorizeMessage runs the plugin's categorize(handle, msg) under a
// per-call deadline: handle fetches the attachment list via
// get_attachments, msg carries the metadata projection (from, subject,
// date). The return is a table of 1-based attachment ordinal to
// category, or nil to skip the message. A deadline kill closes the VM
// and disables the plugin (the render path's fail-fast).
func (p *luaPlugin) categorizeMessage(handle string, m AttachMeta) (map[int]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.vm == nil {
		return nil, fmt.Errorf("lua plugin disabled after a deadline kill")
	}
	ctx, cancel := context.WithTimeout(context.Background(), attachHookBudget)
	defer cancel()
	p.vm.SetContext(ctx)
	msg := p.vm.NewTable()
	msg.RawSetString("from", lua.LString(m.From))
	msg.RawSetString("subject", lua.LString(m.Subject))
	msg.RawSetString("date", lua.LNumber(m.Date))
	p.vm.Push(p.categorize)
	p.vm.Push(lua.LString(handle))
	p.vm.Push(msg)
	err := p.vm.PCall(2, 1, nil)
	if err != nil && ctx.Err() != nil {
		p.vm.Close()
		p.vm = nil
	}
	if err != nil {
		return nil, err
	}
	ret := p.vm.Get(-1)
	p.vm.Pop(1)
	switch v := ret.(type) {
	case *lua.LNilType:
		return nil, nil
	case *lua.LTable:
		out := map[int]string{}
		var verr error
		v.ForEach(func(k, val lua.LValue) {
			if verr != nil {
				return
			}
			o, ok := k.(lua.LNumber)
			if !ok {
				verr = fmt.Errorf("categorize keys must be attachment ordinals, got %s", k.Type().String())
				return
			}
			s, ok := val.(lua.LString)
			if !ok {
				verr = fmt.Errorf("categorize values must be relative path strings, got %s", val.Type().String())
				return
			}
			out[int(o)] = string(s)
		})
		return out, verr
	}
	return nil, fmt.Errorf("categorize must return a table or nil, got %s", ret.Type().String())
}
