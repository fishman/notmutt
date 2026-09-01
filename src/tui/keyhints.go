// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// keyhintRow renders the active context's bindings as "key action"
// pairs, sorted by key and truncated to the terminal width (R11 slot
// reservation). Labels are the action names - config data, never hardcoded.
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

// bindingCtx is the context the bindings answer for: the preview
// popup borrows the pager surface (its scroll keys ARE the pager
// keys) - both the keyhint and the help derive the same way (R9).
func (m Model) bindingCtx() string {
	if m.preview {
		return "pager"
	}
	return m.mode
}

// activeBindings resolves the current context's map, falling back to the index map for modes without bindings.
func (m Model) activeBindings() map[string]string {
	if km := m.bindings[m.bindingCtx()]; km != nil {
		return km
	}
	return m.bindings["index"]
}

// keyhint is the context keyhint row, extended while a chain prefix
// is armed: pressing "g" lists "g g cursor-top" and "g r reply-all"
// (R9 - the binding data answers, no hardcoded list). The layer
// caches the row per (mode, armed prefix, width).
func (m Model) keyhint() string {
	sig := m.mode + "|" + strconv.FormatBool(m.preview) + "|" + m.pendingPrefix + "|" + strconv.Itoa(m.width) + "|" + strconv.Itoa(m.styleVer)
	return m.hintLayer.get(sig, m.keyhintBuild)
}

func (m Model) keyhintBuild() string {
	km := m.visibleBindings()
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

// visibleBindings is the active context's map restricted to its shown
// keys: visibility is opt-in (the show flag), the generic bindings
// (paging, navigation) stay out of the keyhint, the help dialog shows
// every binding (R9). A store without a Shown set (tests, pre-config)
// shows everything.
func (m Model) visibleBindings() map[string]string {
	km := m.activeBindings()
	if m.st == nil {
		return km
	}
	shown := m.st.Config().Shown[m.bindingCtx()]
	if len(shown) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(km))
	for k, a := range km {
		if shown[k] {
			out[k] = a
		}
	}
	return out
}

// keyFor finds the alphabetically-first key bound to an action (single
// keys and chains both count) - the dispatch table's inverse; map
// iteration never drives output.
func keyFor(km map[string]string, action string) string {
	keys := make([]string, 0, len(km))
	for k := range km {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if km[k] == action {
			return k
		}
	}
	return ""
}

// scrollKeys returns the pager map's scroll-up/down keys sorted - the shared hint derivation for the preview popup and help dialog.
func scrollKeys(pm map[string]string) []string {
	keys := make([]string, 0, len(pm))
	for k := range pm {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	scroll := make([]string, 0, 2)
	for _, k := range keys {
		if a := pm[k]; a == "scroll-up" || a == "scroll-down" {
			scroll = append(scroll, k)
		}
	}
	return scroll
}

// previewHint derives the popup's hint row from the binding data: the
// pager's scroll keys, the index's open key, the pager's back action (R9).
func (m Model) previewHint() string {
	pm := m.bindings["pager"]
	var parts []string
	if s := scrollKeys(pm); len(s) > 0 {
		parts = append(parts, strings.Join(s, "/")+" scroll")
	}
	if o := keyFor(m.bindings["index"], "open"); o != "" {
		parts = append(parts, o+" open")
	}
	if q := keyFor(pm, "back"); q != "" {
		parts = append(parts, q+" close")
	}
	return strings.Join(parts, "  ")
}

// renderHelp is the ? overlay: the active context's binding rows
// (helpRows) through a viewport - the pager widget, the same scroll
// surface the mail thread uses. The pager scroll keys navigate it,
// any other keypress closes it (the check runs before dispatch, so
// the closing key never fires). The frame is always exactly m.height lines (R11).
func (m Model) renderHelp() string {
	sig := m.mode + "|" + strconv.Itoa(m.width) + "|" + strconv.Itoa(m.helpView.height) + "|" + strconv.Itoa(m.helpView.offset) + "|" + strconv.Itoa(m.styleVer)
	return m.helpLayer.get(sig, m.helpBuild)
}

func (m Model) helpBuild() string {
	rows := m.helpView.window()
	for len(rows) < m.helpView.height {
		rows = append(rows, "")
	}
	content := make([]string, 0, len(rows)+1)
	content = append(content, "help: "+m.mode+" bindings")
	content = append(content, rows...)
	return m.frame(content, m.helpFooter())
}

// helpRows is the active context's binding rows, three neomutt help
// columns (neomutt/help.c): key, function, description, each
// left-aligned at the data's widest entry, two spaces apart.
func (m Model) helpRows() []string {
	km := m.activeBindings()
	descs := m.descriptions()
	keys := make([]string, 0, len(km))
	for k := range km {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	wk, wf := 0, 0
	for _, k := range keys {
		if len(k) > wk {
			wk = len(k)
		}
		if a := km[k]; len(a) > wf {
			wf = len(a)
		}
	}
	rows := make([]string, 0, len(keys))
	for _, k := range keys {
		row := fmt.Sprintf("%-*s  %-*s  %s", wk, k, wf, km[k], actionDesc(descs, m.tagActions, km[k]))
		rows = append(rows, truncCells(row, m.width))
	}
	return rows
}

// helpFooter derives the dialog's hint row from the pager binding data: the scroll, close, and help keys (R9 - the data answers).
func (m Model) helpFooter() string {
	pm := m.bindings["pager"]
	var parts []string
	if s := scrollKeys(pm); len(s) > 0 {
		parts = append(parts, strings.Join(s, "/")+" scroll")
	}
	if q := keyFor(pm, "back"); q != "" {
		parts = append(parts, q+" closes")
	}
	if q := keyFor(pm, "help"); q != "" {
		parts = append(parts, q+" help")
	}
	return strings.Join(parts, "  ")
}

// descriptions is the help vocabulary from the config store (nil without one - the description falls back to the action).
func (m Model) descriptions() map[string]string {
	if m.st == nil {
		return nil
	}
	return m.st.Config().Descriptions
}

// actionDesc is a bound action's help description: the config's one-line text, or a tag action's derived line (the tag IS the data).
func actionDesc(descs, tagActions map[string]string, action string) string {
	if d, ok := descs[action]; ok {
		return d
	}
	if tag, ok := tagActions[action]; ok {
		return "apply tag " + tag
	}
	return action
}
