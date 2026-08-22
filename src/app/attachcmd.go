// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"sort"
	"strings"
	"sync"

	"notmutt/config"
	"notmutt/tui"
)

// attachcmds is the attach-command registry (R8): config tables and Lua
// plugins register here; attachcmdsOrder preserves registration order -
// the Tab default-chooser preference (the Lua script's call order).
var (
	attachcmdsMu    sync.Mutex
	attachcmds      = map[string][]string{}
	attachcmdsOrder []string
)

func registerAttachCommand(name string, argv []string) {
	if strings.TrimSpace(name) == "" || len(argv) == 0 {
		return
	}
	attachcmdsMu.Lock()
	defer attachcmdsMu.Unlock()
	if _, ok := attachcmds[name]; !ok {
		attachcmdsOrder = append(attachcmdsOrder, name)
	}
	attachcmds[name] = append([]string(nil), argv...)
}

func loadConfigAttachCommands(cfg config.Config) {
	// sorted: the TOML table is unordered, registration must stay deterministic
	names := make([]string, 0, len(cfg.AttachCommands))
	for name := range cfg.AttachCommands {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		registerAttachCommand(name, cfg.AttachCommands[name])
	}
}

// attachCommandSnapshot returns a copied snapshot in registration order
// (the TUI seam contract); nil when none registered.
func attachCommandSnapshot() []tui.AttachCommand {
	attachcmdsMu.Lock()
	defer attachcmdsMu.Unlock()
	if len(attachcmdsOrder) == 0 {
		return nil
	}
	out := make([]tui.AttachCommand, 0, len(attachcmdsOrder))
	for _, name := range attachcmdsOrder {
		out = append(out, tui.AttachCommand{Name: name, Argv: append([]string(nil), attachcmds[name]...)})
	}
	return out
}
