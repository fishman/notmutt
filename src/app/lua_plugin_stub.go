// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build !lua

package app

import (
	"fmt"

	"notmutt/config"
	"notmutt/core"
)

// loadLuaPlugins is a no-op in default builds: the Lua layer and its
// gopher-lua dependency exist only under the lua build tag (the R12
// build-gating pattern), so default binaries carry no Lua runtime and
// the render boundary runs Go hooks only.
func loadLuaPlugins(dir string, network map[string]config.LuaNetwork) {}

// pluginActionNames is empty in default builds: no plugin registry, so
// a binding naming a plugin action is rejected by validateBindings and
// the dispatch fallthrough never fires.
func pluginActionNames() map[string]bool { return nil }

// luaKeyBound is false in default builds: the plugin-key dispatch
// fallback never fires.
func luaKeyBound(key, area string) bool { return false }

// runLuaAction and runLuaBind are no-ops in default builds: the seams
// are wired either way, the Lua registry is empty under the tag, so the
// calls are unreachable in practice.
func runLuaAction(action, threadID string, bus *core.Bus, cfg *config.Config, worker workerAPI) {}

func runLuaBind(key, area, threadID string, bus *core.Bus, cfg *config.Config, worker workerAPI) {}

// deliverPickerResult is a no-op in default builds: no Lua action can
// block on a picker, so no waiter exists to resume.
func deliverPickerResult(e core.PickerResult) {}

// runLuaCommand answers the :lua command line in default builds: no
// Lua runtime, so the command reports the missing tag instead of
// silently dropping.
func runLuaCommand(command, threadID string, bus *core.Bus, cfg *config.Config, worker workerAPI) {
	bus.Publish(core.LuaResult{Err: fmt.Errorf("lua: not built in (compile with -tags lua)")})
}

// deliverPromptResult is a no-op in default builds: no Lua action can
// block on a prompt, so no waiter exists to resume.
func deliverPromptResult(e core.PromptResult) {}
