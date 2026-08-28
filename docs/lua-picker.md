# Lua picker

The attach prompt (`a` / `tab` in compose) attaches files through a
chooser. The picker is **not hardcoded** in the client: the core exposes
one primitive, `picker_argv`, and the client-specific wrapper lives in
your own plugin (here `~/.config/notmutt/lua/chooser.lua`).

## picker_argv(argv)

Runs a chooser (the attach-command exec path) and blocks the VM on the
TUI's reply, under the action deadline. Returns the selection as a table
of paths. `argv` is appended with a chooser-file path by the client (F4
- argv only, never a shell string); the tool writes each selected path
to that file, one per line.

## The attach-choose action

`tab` in the attach prompt runs the `attach-choose` action when one is
registered, then closes the prompt on success. Define it in your plugin:

```lua
-- ~/.config/notmutt/lua/chooser.lua
local function picker_yazi()
	return picker_argv({ "yazi", "--chooser-file" })
end

register_action("attach-choose", function(ctx)
	local files = picker_yazi()
	if not files or #files == 0 then
		return
	end
	for _, p in ipairs(files) do
		attach_add(p)
	end
end)
```

The wrapper is yours: name it what you like and pick any argv. `tab`
falls back to a registered attach command (below), else the built-in
directory picker.

## Alternatives

- **Config command**: `[attach-commands]` in TOML, or the Lua
  `register_attach_command(name, argv)` (both feed the `?` / `@name`
  path in the prompt). A chooser command gets the chooser-file path
  appended as its last argv element:

  ```toml
  [attach-commands]
  yazi = ["yazi", "--chooser-file"]
  ```
