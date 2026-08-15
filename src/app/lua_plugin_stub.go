//go:build !lua

package app

// loadLuaPlugins is a no-op in default builds: the Lua layer and its
// gopher-lua dependency exist only under the lua build tag (the R12
// build-gating pattern), so default binaries carry no Lua runtime and
// the render boundary runs Go hooks only.
func loadLuaPlugins(dir string) {}
