// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strconv"
	"strings"
)

// log.go: the session log. Every surfaced event (send results, lua
// results, job errors, lock timeouts) appends through logEntry - the
// single write path - and the status line always shows the last entry.
// The ~ overlay scrolls the ring like the help dialog.

// logLine is one session log entry: the text and whether it is an
// error (the error style renders it on the status line).
type logLine struct {
	text string
	err  bool
}

// logEntry is the single log write path: the entry appends to the
// ring (logCap caps it, the oldest drop first) and becomes the status
// line's last entry. Empty text is a no-op - a silent success logs
// nothing.
func (m *Model) logEntry(text string, err bool) {
	if text == "" {
		return
	}
	m.log = append(m.log, logLine{text: text, err: err})
	if n := len(m.log) - logCap; n > 0 {
		m.log = m.log[n:]
	}
	m.statusMsg = text
	m.statusMsgErr = err
}

// renderLog is the ~ overlay: the session log rows through a viewport
// - the same surface the help dialog uses. The pager scroll keys
// navigate it, any other keypress closes it (the check runs before
// dispatch, so the closing key never fires). The frame is exactly
// m.height lines, assembled like renderHelp.
func (m Model) renderLog() string {
	sig := strconv.Itoa(m.width) + "|" + strconv.Itoa(m.logView.height) + "|" + strconv.Itoa(m.logView.offset) + "|" + strconv.Itoa(m.styleVer)
	return m.logLayer.get(sig, m.logBuild)
}

func (m Model) logBuild() string {
	rows := m.logView.window()
	for len(rows) < m.logView.height {
		rows = append(rows, "")
	}
	body := make([]string, 0, len(rows)+3)
	body = append(body, m.tabBar())
	body = append(body, "log")
	body = append(body, rows...)
	body = append(body, m.logFooter())
	return strings.Join(body, "\n") + "\n" + m.statusLineWith(m.styles, m.ui)
}

// logRows renders the ring as viewport lines, oldest first, each
// truncated to the terminal width (R11 slot reservation).
func (m Model) logRows() []string {
	rows := make([]string, 0, len(m.log))
	for _, l := range m.log {
		rows = append(rows, truncCells(l.text, m.width))
	}
	return rows
}

// logFooter mirrors the help footer: the scroll keys and the close key
// derive from the pager binding data (the surface borrows the pager
// keys, R9 - the data answers).
func (m Model) logFooter() string {
	pm := m.bindings["pager"]
	var parts []string
	if s := scrollKeys(pm); len(s) > 0 {
		parts = append(parts, strings.Join(s, "/")+" scroll")
	}
	if q := keyFor(pm, "back"); q != "" {
		parts = append(parts, q+" closes")
	}
	return strings.Join(parts, "  ")
}
