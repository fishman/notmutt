package tui

import (
	"sort"
	"strconv"
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

// activeBindings resolves the current context's map, falling back to
// the index map for modes without bindings.
func (m Model) activeBindings() map[string]string {
	if km := m.bindings[m.mode]; km != nil {
		return km
	}
	return m.bindings["index"]
}

// keyhint is the context keyhint row, extended while a chain prefix
// is armed: pressing "g" lists "g g cursor-top" and "g r
// reply-all", so the user sees what the prefix can become (R9 - the
// binding data answers, no hardcoded list). The layer caches the row
// per (mode, armed prefix, width) - a cursor move repaints it without
// rebuilding.
func (m Model) keyhint() string {
	sig := m.mode + "|" + m.pendingPrefix + "|" + strconv.Itoa(m.width) + "|" + strconv.Itoa(m.styleVer)
	return m.hintLayer.get(sig, m.keyhintBuild)
}

func (m Model) keyhintBuild() string {
	km := m.activeBindings()
	if m.pendingPrefix == "" {
		return keyhintRow(km, m.width)
	}
	continuations := map[string]string{}
	for k, a := range km {
		if k != m.pendingPrefix && strings.HasPrefix(k, m.pendingPrefix) {
			continuations[k] = a
		}
	}
	return keyhintRow(continuations, m.width)
}

// renderHelp is the ? overlay: the active context's bindings as
// "key action" rows (single keys and chains, sorted) with a close
// hint. Any keypress closes it - the help check runs before
// dispatch, so the closing key never fires. The frame is always
// exactly m.height lines, assembled like renderCompose (R11 slot
// reservation).
func (m Model) renderHelp() string {
	sig := m.mode + "|" + strconv.Itoa(m.width) + "|" + strconv.Itoa(m.height) + "|" + strconv.Itoa(m.styleVer)
	return m.helpLayer.get(sig, m.helpBuild)
}

func (m Model) helpBuild() string {
	km := m.activeBindings()
	rows := m.height - 2
	if rows < 1 {
		rows = 1
	}
	title := "help: " + m.mode + " bindings"
	body := make([]string, 0, rows)
	body = append(body, title)
	keys := make([]string, 0, len(km))
	for k := range km {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		body = append(body, keyhintRow(map[string]string{k: km[k]}, m.width))
	}
	if len(body) > rows {
		body = body[:rows]
	}
	for len(body) < rows {
		body = append(body, "")
	}
	body = append(body, "? or any key closes")
	return strings.Join(body, "\n") + "\n" + m.statusLineWith(m.styles, m.ui)
}
