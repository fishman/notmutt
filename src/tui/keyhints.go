package tui

import (
	"sort"
	"strings"
)

// keyhintRow renders the active context's bindings as "key action"
// pairs, sorted by key and truncated to the terminal width (R11 slot
// reservation: the row never shifts with content). Labels are the
// action names - config data, never hardcoded.
func keyhintRow(km map[string]string, w int) string {
	keys := make([]string, 0, len(km))
	for k := range km {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		parts = append(parts, k+" "+km[k])
	}
	line := strings.Join(parts, "  ")
	if w > 0 {
		return truncCells(line, w)
	}
	return line
}
