package tui

import (
	"notmutt/compose"
	"notmutt/core"
)

// onApply is the apply seam: the app wires it with SetApplyHandler; the
// default is a no-op so the model works in tests.
var onApply = func() {}

func SetApplyHandler(fn func()) {
	onApply = fn
}

// onOpen is the open seam: the app wires it with SetOpenHandler (the
// open key hands the thread id to the app, which loads the thread's
// messages and publishes ThreadLoaded); the default is a no-op so the
// model works in tests. preview=true is the preview fetch - the app
// skips the read-marking, the model keeps the index mode.
var onOpen = func(threadID string, preview bool) {}

func SetOpenHandler(fn func(string, bool)) {
	onOpen = fn
}

// onReply is the reply seam: the app builds the prefill (account
// detection, parsing) and publishes ComposeOpened; msg is nil for a
// blank compose.
var onReply = func(msg *core.Message, mode string) {}

func SetReplyHandler(fn func(*core.Message, string)) {
	onReply = fn
}

// onSend is the send seam: the app runs the send job (transport, fcc,
// tags) and publishes SendResult.
var onSend = func(st compose.State) {}

func SetSendHandler(fn func(compose.State)) {
	onSend = fn
}

// sigDir is the signatures root (spec section 9); the app resolves it
// from the config path, the tests set it directly.
var sigDir string

func SetSignaturesDir(dir string) {
	sigDir = dir
}

// attachCommands is the attach-command registry seam (R8): the app
// wires it with SetAttachCommandSource; nil map = no commands.
// Read-only after wiring.
var attachCommands = func() map[string][]string { return nil }

func SetAttachCommandSource(fn func() map[string][]string) {
	if fn != nil {
		attachCommands = fn
	}
}

// onLuaCommand is the :lua seam (R8): the app runs the chunk in a
// Lua VM (the current thread id as msg() context, empty when none)
// and publishes LuaResult; the default is a no-op so the model works
// in tests.
var onLuaCommand = func(code, threadID string) {}

func SetLuaCommandHandler(fn func(code, threadID string)) {
	onLuaCommand = fn
}

// onLuaAction runs a plugin-registered action (R8): the app resolves
// the action in its Lua registry and publishes LuaResult.
var onLuaAction = func(action, threadID string) {}

func SetLuaActionHandler(fn func(action, threadID string)) {
	onLuaAction = fn
}

// pluginActions is the plugin action name registry seam: the app
// wires it with SetPluginActionSource; the binding validation and the
// dispatch fallthrough consult it. Nil = no plugin actions.
var pluginActions = func() map[string]bool { return nil }

func SetPluginActionSource(fn func() map[string]bool) {
	if fn != nil {
		pluginActions = fn
	}
}
