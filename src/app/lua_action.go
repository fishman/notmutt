// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build lua

// The Lua action layer (R8, decision record 20): a plugin script IS a
// program. register_action(name, fn) and bind_key(key, area, desc, fn)
// register callables; the attach prompt's Tab, a core binding naming a
// plugin action, or the plugin-key dispatch fallback invoke them. Every
// invocation re-runs the plugin file on a FRESH VM - a seconds-long
// ai_chat call must never contend with the persistent body_render VMs
// on their mutex, and the SetContext deadline kills runaway scripts
// (the R3 async discipline: a plugin never blocks the UI). Client
// effects (attach_add, picker_*, ai_chat) ride the bus; the runner
// drains the queue after the call.

package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	lua "github.com/yuin/gopher-lua"

	"notmutt/app/ai"
	"notmutt/config"
	"notmutt/core"
	"notmutt/i18n"
	"notmutt/mail"
	"notmutt/notmuch"
)

// actionDeadline bounds one action run: the script's budget covers a
// picker round trip and an AI stream (the 180s provider default) with
// headroom. A script that outlives it is killed by SetContext; a
// parked Go call (picker wait, stream read) aborts on its context.
const actionDeadline = 5 * time.Minute

// luaBind is one bind_key registration: the plugin file whose fn the
// key invokes, and the description for the keyhint merge.
type luaBind struct {
	file string
	desc string
}

var (
	regMu   sync.Mutex
	actions = map[string]string{}  // action name -> plugin file
	binds   = map[string]luaBind{} // key\x00area -> bind
)

// pluginActionNames is the seam source (SetPluginActionSource wiring):
// the binding validation and the dispatch fallthrough consult it.
func pluginActionNames() map[string]bool {
	regMu.Lock()
	defer regMu.Unlock()
	out := make(map[string]bool, len(actions))
	for n := range actions {
		out[n] = true
	}
	return out
}

// luaKeyBound answers the dispatch fallback: a plugin bind_key for the
// key/area with no core binding (record 20 point 7: core wins, the
// plugin fills the rest).
func luaKeyBound(key, area string) bool {
	regMu.Lock()
	defer regMu.Unlock()
	_, ok := binds[key+"\x00"+area]
	return ok
}

// registerAction records a load-time register_action call: the registry
// maps name to the plugin file, and an invocation re-runs that file in
// a fresh VM (a plugin file edited since load is what the call sees).
func registerAction(name, path string) {
	regMu.Lock()
	defer regMu.Unlock()
	actions[name] = path
}

func registerBind(key, area, desc, path string) {
	regMu.Lock()
	defer regMu.Unlock()
	binds[key+"\x00"+area] = luaBind{file: path, desc: desc}
}

// actionCtx is one invocation's client interface: the bus, the config
// (AI providers), the worker (mail_lines), the thread, and the queues
// the API globals fill.
type actionCtx struct {
	bus     *core.Bus
	cfg     *config.Config
	worker  workerAPI
	tid     string
	ctx     context.Context // the action deadline (SetContext kill + select)
	print   strings.Builder
	attach  []string
	tags    []core.TagOp // tag_add/tag_remove staging (R14: script stages, APPLY flushes)
	once    sync.Once
	lines   []core.Line
	lineErr error
}

// drain publishes the invocation's queued effects after the action
// returns: attach paths and the staged tag ops. The script never
// touches notmuch itself - the R14 apply boundary holds from Lua too.
func (ac *actionCtx) drain() {
	if len(ac.attach) > 0 {
		ac.bus.Publish(core.AttachFiles{Paths: ac.attach})
	}
	if len(ac.tags) > 0 {
		ac.bus.Publish(core.TagStaged{ThreadID: ac.tid, Ops: ac.tags})
	}
}

// mailLines loads the thread's F1-clean plain lines for the script
// (ctx.mail_lines(), lazy - a script that never reads them pays
// nothing). The load never publishes ThreadLoaded: the open path's
// publish would fight the summary swap or re-render the index pager.
// width 120 wraps like the html cap - the app does not know the UI's
// width, and a narrow terminal must not mangle the text.
func (ac *actionCtx) mailLines() ([]core.Line, error) {
	ac.once.Do(func() {
		rpl, err := ac.worker.Call(notmuch.Action{Kind: notmuch.ActThread, ThreadID: ac.tid})
		if err != nil {
			ac.lineErr = err
			return
		}
		if rpl.Err != nil {
			ac.lineErr = rpl.Err
			return
		}
		ac.lines, _, _, ac.lineErr = mail.RenderThread(rpl.Msgs, core.RenderPlain, false, 120, false)
	})
	return ac.lines, ac.lineErr
}

// runLuaAction runs a registered action (the seam handler wraps it in
// a goroutine): the registry resolves the plugin file, the invocation
// runs it, the attach effects drain, and LuaResult always publishes -
// the TUI surfaces the outcome even when the call fails.
func runLuaAction(action, threadID string, bus *core.Bus, cfg *config.Config, worker workerAPI) {
	regMu.Lock()
	path, ok := actions[action]
	regMu.Unlock()
	if !ok {
		bus.Publish(core.LuaResult{Err: fmt.Errorf("lua: no such action %q", action)})
		return
	}
	ac := &actionCtx{bus: bus, cfg: cfg, worker: worker, tid: threadID}
	output, err := ac.run(path, func(reg map[string]*lua.LFunction) *lua.LFunction {
		return reg[action]
	})
	ac.drain()
	bus.Publish(core.LuaResult{Output: output, Err: err})
}

// runLuaBind invokes a bind_key fn (the dispatch fallback): the same
// machinery as runLuaAction, keyed by key + area.
func runLuaBind(key, area, threadID string, bus *core.Bus, cfg *config.Config, worker workerAPI) {
	regMu.Lock()
	b, ok := binds[key+"\x00"+area]
	regMu.Unlock()
	if !ok {
		bus.Publish(core.LuaResult{Err: fmt.Errorf("lua: no binding %q in %s", key, area)})
		return
	}
	ac := &actionCtx{bus: bus, cfg: cfg, worker: worker, tid: threadID}
	output, err := ac.run(b.file, func(reg map[string]*lua.LFunction) *lua.LFunction {
		return reg["k\x00"+key+"\x00"+area]
	})
	ac.drain()
	bus.Publish(core.LuaResult{Output: output, Err: err})
}

// run executes one plugin file on a fresh VM: DoFile re-runs the
// registrations into the invocation-scoped map, then the registered fn
// is called with the ctx table. The VM is sandboxed (the shared lib
// whitelist) and dies with the deadline.
func (ac *actionCtx) run(path string, lookup func(map[string]*lua.LFunction) *lua.LFunction) (string, error) {
	vm, reg, cancel, err := ac.newVM()
	if err != nil {
		return "", err
	}
	defer cancel()
	defer vm.Close()
	if err := vm.DoFile(path); err != nil {
		return "", err
	}
	fn := lookup(reg)
	if fn == nil {
		return "", fmt.Errorf("lua: %s: no such function registered", path)
	}
	vm.Push(fn)
	vm.Push(ac.ctxTable(vm))
	if err := vm.PCall(1, 0, nil); err != nil {
		return "", err
	}
	return ac.print.String(), nil
}

// runLuaCommand runs a :lua chunk (the command line, R8): the chunk IS
// the program - compile it and call it with the same ctx table as an
// action. The prefix dispatch is the seam for future builtin
// ex-commands; anything not "lua ..." errors.
func runLuaCommand(command, threadID string, bus *core.Bus, cfg *config.Config, worker workerAPI) {
	code, ok := strings.CutPrefix(command, "lua ")
	if !ok {
		bus.Publish(core.LuaResult{Err: fmt.Errorf("unknown command: %q", command)})
		return
	}
	ac := &actionCtx{bus: bus, cfg: cfg, worker: worker, tid: threadID}
	vm, _, cancel, err := ac.newVM()
	if err != nil {
		bus.Publish(core.LuaResult{Err: err})
		return
	}
	defer cancel()
	defer vm.Close()
	var runErr error
	if fn, err := vm.LoadString(code); err != nil {
		runErr = err
	} else {
		// the chunk is a bare statement list, not function(ctx): the ctx
		// table is a global it reads by name (and its first arg too)
		ctx := ac.ctxTable(vm)
		vm.SetGlobal("ctx", ctx)
		vm.Push(fn)
		vm.Push(ctx)
		if err := vm.PCall(1, 0, nil); err != nil {
			runErr = err
		}
	}
	ac.drain()
	bus.Publish(core.LuaResult{Output: ac.print.String(), Err: runErr})
}

// newSandboxVM builds one fresh sandboxed VM: the whitelisted libs
// (openSandboxLibs) and the deadline kill (SetContext). Shared by the
// action layer and the MCP server (mcp.go) - the invocation-VM start
// is one concept. The caller owns cancel and closes the VM.
func newSandboxVM(deadline time.Duration) (*lua.LState, context.Context, func(), error) {
	vm := lua.NewState(lua.Options{SkipOpenLibs: true})
	if err := openSandboxLibs(vm, "<sandbox>"); err != nil {
		vm.Close()
		return nil, nil, nil, err
	}
	dctx, cancel := context.WithTimeout(context.Background(), deadline)
	vm.SetContext(dctx)
	return vm, dctx, cancel, nil
}

// newVM builds one invocation's VM: the sandboxed libs, the action
// deadline (the SetContext kill), the API globals (the shared R8
// surface - actions and :lua chunks see the same functions), and the
// bundled picker library. The caller owns the returned cancel.
func (ac *actionCtx) newVM() (*lua.LState, map[string]*lua.LFunction, func(), error) {
	vm, dctx, cancel, err := newSandboxVM(actionDeadline)
	if err != nil {
		return nil, nil, nil, err
	}
	ac.ctx = dctx
	// the invocation-scoped registry: what THIS run of the file
	// registered, not the load-time index.
	reg := map[string]*lua.LFunction{}
	vm.SetGlobal("register_action", vm.NewFunction(func(L *lua.LState) int {
		name := L.CheckString(1)
		if name == "" {
			L.RaiseError("register_action: empty name")
		}
		reg[name] = L.CheckFunction(2)
		return 0
	}))
	vm.SetGlobal("bind_key", vm.NewFunction(func(L *lua.LState) int {
		reg["k\x00"+L.CheckString(1)+"\x00"+L.CheckString(2)] = L.CheckFunction(4)
		return 0
	}))
	vm.SetGlobal("attach_add", vm.NewFunction(func(L *lua.LState) int {
		ac.attach = append(ac.attach, L.CheckString(1))
		return 0
	}))
	// tag_add/tag_remove stage ops into the current folder's buffer
	// (R14): the script classifies, the APPLY key flushes. Lua never
	// writes notmuch directly (the user constraint; the UI keypress
	// path is the only write).
	vm.SetGlobal("tag_add", vm.NewFunction(func(L *lua.LState) int {
		ac.tags = append(ac.tags, core.TagOp{Tag: L.CheckString(1), Add: true})
		return 0
	}))
	vm.SetGlobal("tag_remove", vm.NewFunction(func(L *lua.LState) int {
		ac.tags = append(ac.tags, core.TagOp{Tag: L.CheckString(1), Add: false})
		return 0
	}))
	vm.SetGlobal("picker_argv", vm.NewFunction(func(L *lua.LState) int {
		var argv []string
		for i := 1; ; i++ {
			v := L.CheckTable(1).RawGetInt(i)
			if v == lua.LNil {
				break
			}
			argv = append(argv, v.String())
		}
		if len(argv) == 0 {
			L.RaiseError("picker_argv: empty argv")
		}
		return ac.picker(L, argv)
	}))
	vm.SetGlobal("prompt", vm.NewFunction(func(L *lua.LState) int { return ac.promptDialog(L) }))
	vm.SetGlobal("ai_chat", vm.NewFunction(func(L *lua.LState) int { return ac.aiChat(L) }))
	vm.SetGlobal("translate", vm.NewFunction(func(L *lua.LState) int {
		L.Push(lua.LString(i18n.T(L.CheckString(1))))
		return 1
	}))
	// print captures into the run's output (rides LuaResult; never a
	// log line - F6, the plugin's own data anyway)
	vm.SetGlobal("print", vm.NewFunction(func(L *lua.LState) int {
		n := L.GetTop()
		parts := make([]string, 0, n)
		for i := 1; i <= n; i++ {
			parts = append(parts, L.Get(i).String())
		}
		ac.print.WriteString(strings.Join(parts, "\t") + "\n")
		return 0
	}))
	// the bundled library runs first: picker_yazi/picker_ranger (and a
	// plugin's overrides) land in the VM globals before the plugin file
	if err := vm.DoString(pickersLib); err != nil {
		vm.Close()
		cancel()
		return nil, nil, nil, err
	}
	return vm, reg, cancel, nil
}

// ctxTable is the invocation context the chunk or action receives: the
// thread id and the lazy full-thread plain text (mail_lines).
func (ac *actionCtx) ctxTable(vm *lua.LState) *lua.LTable {
	ctx := vm.NewTable()
	ctx.RawSetString("thread_id", lua.LString(ac.tid))
	ctx.RawSetString("mail_lines", vm.NewFunction(func(L *lua.LState) int {
		lines, err := ac.mailLines()
		if err != nil {
			L.RaiseError("%v", err)
		}
		tbl := L.NewTable()
		for _, l := range lines {
			tbl.Append(lua.LString(l.Text))
		}
		L.Push(tbl)
		return 1
	}))
	return ctx
}

// picker blocks the VM on the picker round trip: the request rides the
// bus to the TUI (the attach-command exec path), the TUI publishes the
// selection back, the waiter resolves. The wait selects on the action
// deadline - a stalled chooser cannot wedge the plugin past its budget.
// The tool wrappers (picker_yazi, picker_ranger) live in the bundled
// Lua library (pickers.lua) - the core only exposes the argv primitive.
func (ac *actionCtx) picker(L *lua.LState, argv []string) int {
	id := fmt.Sprintf("%d", time.Now().UnixNano())
	ch := make(chan pickerReply, 1)
	pickersMu.Lock()
	pickers[id] = ch
	pickersMu.Unlock()
	defer func() {
		pickersMu.Lock()
		delete(pickers, id)
		pickersMu.Unlock()
	}()
	ac.bus.Publish(core.PickerRequest{ID: id, Argv: argv})
	select {
	case r := <-ch:
		if r.err != nil {
			L.RaiseError("%v", r.err)
		}
		tbl := L.NewTable()
		for _, p := range r.paths {
			tbl.Append(lua.LString(p))
		}
		L.Push(tbl)
		return 1
	case <-ac.ctx.Done():
		L.RaiseError("picker timed out")
	}
	return 0
}

// aiChat streams one completion to a configured provider: the streamed
// deltas publish as AiChunk (the pager-inline summary), the full text
// returns to the script. AiResult publishes on the way out - success,
// failure, or a deadline kill (the deferred publish survives the VM
// unwinding) - so the summary view always resolves.
func (ac *actionCtx) aiChat(L *lua.LState) int {
	name := L.CheckString(1)
	p, ok := ac.cfg.AI[name]
	if !ok {
		L.RaiseError("ai: no provider %q configured", name)
	}
	opts := L.OptTable(2, L.NewTable())
	model := opts.RawGetString("model")
	if model == lua.LNil {
		model = lua.LString(p.Model)
	}
	system := opts.RawGetString("system")
	if system == lua.LNil {
		system = lua.LString("")
	}
	text := opts.RawGetString("text")
	if text == lua.LNil {
		L.RaiseError("ai_chat: opts.text required")
	}
	jobID := fmt.Sprintf("%d", time.Now().UnixNano())
	ac.bus.Publish(core.AiStarted{JobID: jobID, ThreadID: ac.tid})
	var chatErr error
	defer func() { ac.bus.Publish(core.AiResult{JobID: jobID, Err: chatErr}) }()
	out, err := ai.Chat(ac.ctx, p, model.String(), system.String(), text.String(), func(d string) {
		ac.bus.Publish(core.AiChunk{JobID: jobID, Text: d})
	})
	chatErr = err
	if err != nil {
		L.RaiseError("%v", err)
	}
	L.Push(lua.LString(out))
	return 1
}

type pickerReply struct {
	paths []string
	err   error
}

// pickers is the blocked-VM waiter registry: picker calls register
// their id, the Run() bus subscriber delivers the TUI's reply here.
var (
	pickersMu sync.Mutex
	pickers   = map[string]chan pickerReply{}
)

// deliverPickerResult resolves one blocked picker call. The buffered
// channel keeps a reply whose waiter died (deadline kill) from
// blocking the deliverer; the map entry is gone either way.
func deliverPickerResult(e core.PickerResult) {
	pickersMu.Lock()
	ch, ok := pickers[e.ID]
	delete(pickers, e.ID)
	pickersMu.Unlock()
	if ok {
		ch <- pickerReply{paths: e.Paths, err: e.Err}
	}
}

// promptDialog blocks the VM on the prompt round trip: the request
// rides the bus to the TUI (the native text dialogue), the answer (or
// the esc cancel) publishes back, the waiter resolves - committed text
// returns as the string, a cancel as Lua nil. The wait selects on the
// action deadline - a never-answered prompt cannot wedge the plugin
// past its budget.
func (ac *actionCtx) promptDialog(L *lua.LState) int {
	id := fmt.Sprintf("%d", time.Now().UnixNano())
	ch := make(chan promptReply, 1)
	promptsMu.Lock()
	prompts[id] = ch
	promptsMu.Unlock()
	defer func() {
		promptsMu.Lock()
		delete(prompts, id)
		promptsMu.Unlock()
	}()
	prefill := ""
	if v := L.Get(2); v != lua.LNil {
		prefill = L.CheckString(2)
	}
	ac.bus.Publish(core.PromptRequest{ID: id, Label: L.CheckString(1), Prefill: prefill})
	select {
	case r := <-ch:
		if r.canceled {
			L.Push(lua.LNil)
		} else {
			L.Push(lua.LString(r.text))
		}
		return 1
	case <-ac.ctx.Done():
		L.RaiseError("prompt timed out")
	}
	return 0
}

type promptReply struct {
	text     string
	canceled bool
}

// prompts is the blocked-VM waiter registry for prompt() calls: the
// mirror of pickers, resolved by the app's bus subscriber.
var (
	promptsMu sync.Mutex
	prompts   = map[string]chan promptReply{}
)

// deliverPromptResult resolves one blocked prompt call. The buffered
// channel keeps a reply whose waiter died (deadline kill) from
// blocking the deliverer; the map entry is gone either way.
func deliverPromptResult(e core.PromptResult) {
	promptsMu.Lock()
	ch, ok := prompts[e.ID]
	delete(prompts, e.ID)
	promptsMu.Unlock()
	if ok {
		ch <- promptReply{text: e.Text, canceled: e.Canceled}
	}
}
