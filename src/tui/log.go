// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
)

// log.go: the session log. Every surfaced event (send results, lua
// results, job errors, lock timeouts) appends through logEntry - the
// single write path - and the status line shows the last entry; the ~
// overlay scrolls the ring like the help dialog.

// logLine is one session log entry: the text, its wall-clock stamp, and whether it is an error (rendered in the error style).
type logLine struct {
	text string
	at   string
	err  bool
}

// logEntry is the single log write path: the entry appends to the
// ring (logCap caps it, oldest drops first) and becomes the status
// line's last entry. Empty text logs nothing (a silent success).
func (m *Model) logEntry(text string, err bool) {
	if text == "" {
		return
	}
	m.log = append(m.log, logLine{text: text, at: time.Now().Format("15:04:05"), err: err})
	if n := len(m.log) - logCap; n > 0 {
		m.log = m.log[n:]
	}
	if err {
		// an error persists for investigation: no auto-clear
		m.statusClearOn = false
	} else {
		m.statusAt = time.Now()
		m.statusClearOn = true
	}
	m.statusMsg = text
	m.statusMsgErr = err
}

// renderLog is the ~ overlay: the session log rows through a viewport
// - the same surface the help dialog uses. The pager scroll keys
// navigate it, any other keypress closes it (the check runs before
// dispatch, so the closing key never fires). The frame is exactly m.height lines.
func (m Model) renderLog() string {
	sig := strconv.Itoa(m.width) + "|" + strconv.Itoa(m.logView.height) + "|" + strconv.Itoa(m.logView.offset) + "|" + strconv.Itoa(m.styleVer) +
		"|" + strconv.Itoa(len(m.log)) + "|" + m.statusMsg
	return m.logLayer.get(sig, m.logBuild)
}

func (m Model) logBuild() string {
	rows := m.logView.window()
	for len(rows) < m.logView.height {
		rows = append(rows, "")
	}
	content := make([]string, 0, len(rows)+1)
	content = append(content, "session log: "+strconv.Itoa(len(m.log))+" entries")
	content = append(content, rows...)
	return m.frame(content, m.logFooter())
}

// logRows renders the ring as viewport lines, oldest first: the stamp in the log.stamp style, the text truncated to the leftover width (R11 - the stamp never shifts the row).
func (m Model) logRows() []string {
	rows := make([]string, 0, len(m.log))
	for _, l := range m.log {
		stamp := m.styles.LogStamp.Render(l.at + " ")
		rows = append(rows, stamp+truncCells(l.text, m.width-lipgloss.Width(stamp)))
	}
	return rows
}

// logFooter mirrors the help footer: the scroll and close keys derive from the pager binding data (the surface borrows the pager keys, R9 - the data answers).
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
