//go:build !lua

package app

import "notmutt/setup"

// builtinTemplates and luaTemplates are no-ops in default builds: the
// Lua layer and its gopher-lua dependency exist only under the lua
// build tag (the R12 build-gating pattern), so default binaries carry
// no Lua runtime and setup detects with the Go fallback in
// setup.Templates only.
func builtinTemplates() []setup.Template {
	return nil
}

func luaTemplates(dir string, active []string) []setup.Template {
	return nil
}
