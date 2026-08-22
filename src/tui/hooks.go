// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"notmutt/compose"
	"notmutt/core"
)

// onApply is the apply seam (SetApplyHandler); a no-op default so the model works in tests.
var onApply = func() {}

func SetApplyHandler(fn func()) {
	onApply = fn
}

// onOpen is the open seam (SetOpenHandler): the open key hands the
// thread id and cursor message to the app, which loads the message and
// publishes ThreadLoaded. msgID is the opened message - the pager
// renders that message only, never the whole thread. preview=true is
// the preview fetch (no read-marking, the index mode stays); headers
// is the h key (full header block); width is the pager's terminal
// width (the html wrap caps at 120, narrower terminals reflow).
var onOpen = func(threadID, msgID string, preview, headers bool, width int) {}

func SetOpenHandler(fn func(string, string, bool, bool, int)) {
	onOpen = fn
}

// onToggleRender is the render-toggle seam (the v key in the pager,
// the source view ctrl+u, the link labels F): the app re-runs the open
// path with the requested view and publishes a fresh ThreadLoaded;
// a no-op default so the model works in tests. labelLinks (F) prefixes
// every link with its "[N]" label, the target list riding the reply.
var onToggleRender = func(threadID, msgID string, mode core.RenderMode, headers bool, width int, labelLinks bool) {}

func SetRenderHandler(fn func(string, string, core.RenderMode, bool, int, bool)) {
	onToggleRender = fn
}

// openLink is the link-open seam (the pager F key): the app runs the
// configured opener with the url as its final argv element (F4 - argv
// only, never shell-interpolated) and detaches; a no-op default.
var openLink = func(url string) {}

func SetOpenLinkHandler(fn func(string)) {
	openLink = fn
}

// onAttachmentView is the attachment-view seam (the v dialog's enter):
// the app re-opens the message, extracts the chosen attachment,
// renders it to pager lines and publishes AttachmentLoaded; a no-op
// default. msgID is the message the lines came from; ordinal the
// attachment's index in the parsed message.
var onAttachmentView = func(threadID, msgID string, ordinal int) {}

func SetAttachmentViewHandler(fn func(string, string, int)) {
	onAttachmentView = fn
}

// onAttachmentSave is the attachment-save seam (the s key in an
// attachment view): the app re-extracts the attachment and writes it
// to the path (0600, F5), publishing AttachmentSaved; a no-op default.
var onAttachmentSave = func(threadID, msgID string, ordinal int, path string) {}

func SetAttachmentSaveHandler(fn func(string, string, int, string)) {
	onAttachmentSave = fn
}

// onImageFetch is the image-fetch seam (the render-images remote mode): the app fetches the http(s) src and publishes ImageFetched; a no-op default.
var onImageFetch = func(url string) {}

func SetImageFetchHandler(fn func(string)) {
	onImageFetch = fn
}

// onSearch is the search-tab seam (the ctrl+f key): the app configures
// the fresh view (window budget, identity), registers it for hydration,
// and loads the raw notmuch query into it; a no-op default.
var onSearch = func(v *core.View) {}

func SetSearchHandler(fn func(*core.View)) {
	onSearch = fn
}

// onReply is the reply seam: the app builds the prefill (account detection, parsing) and publishes ComposeOpened; msg is nil for a blank compose.
var onReply = func(msg *core.Message, mode string) {}

func SetReplyHandler(fn func(*core.Message, string)) {
	onReply = fn
}

// onSend is the send seam: the app runs the send job (transport, fcc, tags) and publishes SendResult.
var onSend = func(st compose.State) {}

func SetSendHandler(fn func(compose.State)) {
	onSend = fn
}

// onDraft is the draft seam: the app writes the dialogue state into the account's draft folder (maildir new/ slot, same shape as the fcc) and reindexes; an error keeps the composition open.
var onDraft = func(st compose.State) error { return nil }

func SetDraftHandler(fn func(compose.State) error) {
	onDraft = fn
}

// onAddrRequest is the address-harvest request seam: the compose Tab completion fires it (lazy, debounced in the model); the app harvests the sender corpus and answers on the bus with AddressIndex.
var onAddrRequest = func() {}

func SetAddressRequestHandler(fn func()) {
	onAddrRequest = fn
}

// sigDir is the signatures root (spec section 9); the app resolves it from the config path, tests set it directly.
var sigDir string

func SetSignaturesDir(dir string) {
	sigDir = dir
}

// AttachCommand is one registered attach command (R8): the name the
// user types (after @) and the argv (F4 - argv only; the runner
// appends the chooser file path). Slice order IS the Tab preference:
// the Lua script that registers the choosers controls which one Tab
// runs by call order.
type AttachCommand struct {
	Name string
	Argv []string
}

// attachCommands is the attach-command registry seam (R8): the app wires it with SetAttachCommandSource; nil = no commands. Read-only after wiring.
var attachCommands = func() []AttachCommand { return nil }

func SetAttachCommandSource(fn func() []AttachCommand) {
	if fn != nil {
		attachCommands = fn
	}
}

// onCategorize is the categorize seam (the index c key): the app runs
// the attachment-category pass over the cursor thread's messages and
// publishes CategorizeResult; a no-op default.
var onCategorize = func(threadID string) {}

func SetCategorizeHandler(fn func(string)) {
	onCategorize = fn
}

// onLuaCommand is the :lua seam (R8): the app runs the chunk in a Lua
// VM (the current thread id as msg() context, empty when none) and
// publishes LuaResult; a no-op default.
var onLuaCommand = func(code, threadID string) {}

func SetLuaCommandHandler(fn func(code, threadID string)) {
	onLuaCommand = fn
}

// onLuaAction runs a plugin-registered action (R8): the app resolves the action in its Lua registry and publishes LuaResult.
var onLuaAction = func(action, threadID string) {}

func SetLuaActionHandler(fn func(action, threadID string)) {
	onLuaAction = fn
}

// pluginActions is the plugin action name registry seam: the app wires it with SetPluginActionSource; the binding validation and dispatch fallthrough consult it. Nil = no plugin actions.
var pluginActions = func() map[string]bool { return nil }

func SetPluginActionSource(fn func() map[string]bool) {
	if fn != nil {
		pluginActions = fn
	}
}

// pluginKeyBound answers the key dispatch fallback: a plugin bind_key for the key/area with no core binding (core wins, the plugin fills the rest - record 20 point 7).
var pluginKeyBound = func(key, area string) bool { return false }

func SetPluginKeyBoundSource(fn func(key, area string) bool) {
	if fn != nil {
		pluginKeyBound = fn
	}
}

// onLuaKey runs a plugin bind_key fn (the key dispatch fallback): the app resolves the binding in its Lua registry and publishes LuaResult.
var onLuaKey = func(key, area, threadID string) {}

func SetLuaKeyHandler(fn func(key, area, threadID string)) {
	onLuaKey = fn
}
