package app

import (
	"strings"
	"sync"

	"notmutt/config"
)

// attachcmds is the attach-command registry (R8): config tables and Lua
// plugins both register here; the TUI reads a snapshot per invocation
// via SetAttachCommandSource.
var (
	attachcmdsMu sync.Mutex
	attachcmds   = map[string][]string{}
)

func registerAttachCommand(name string, argv []string) {
	if strings.TrimSpace(name) == "" || len(argv) == 0 {
		return
	}
	attachcmdsMu.Lock()
	attachcmds[name] = append([]string(nil), argv...)
	attachcmdsMu.Unlock()
}

func loadConfigAttachCommands(cfg config.Config) {
	for name, argv := range cfg.AttachCommands {
		registerAttachCommand(name, argv)
	}
}

// attachCommandSnapshot returns a copied snapshot sorted by name - the
// TUI seam contract (hooks.go); nil map when none are registered.
func attachCommandSnapshot() map[string][]string {
	attachcmdsMu.Lock()
	defer attachcmdsMu.Unlock()
	if len(attachcmds) == 0 {
		return nil
	}
	out := make(map[string][]string, len(attachcmds))
	for name, argv := range attachcmds {
		out[name] = append([]string(nil), argv...)
	}
	return out
}
