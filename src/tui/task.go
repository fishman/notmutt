// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"slices"
	"strconv"
	"strings"

	"notmutt/core"
)

// task.go: the task view - the running background jobs (the refresh
// hook's sync) as rows; x cancels the cursor task. The active set comes
// from TaskChanged events; the app's task loop owns the cancel.

// taskIDs returns the active task ids in registration order (the app
// sequences them, so id order is start order).
func taskIDs(m map[string]core.TaskChanged) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
}

// taskRows renders the active tasks, cursor-marked. ponytail: reuses
// normal/indicator, no new theme objects.
func (m Model) taskRows() []string {
	ids := taskIDs(m.tasks)
	if len(ids) == 0 {
		return []string{m.styles.Normal.Render("(no running tasks)")}
	}
	rows := make([]string, 0, len(ids))
	for i, id := range ids {
		t := m.tasks[id]
		st := m.styles.Normal
		if i == m.taskCursor {
			st = m.styles.Indicator
		}
		rows = append(rows, st.Render(t.Label))
	}
	return rows
}

// taskFooter mirrors the log footer: the scroll keys and the cancel/close keys.
func (m Model) taskFooter() string {
	pm := m.bindings["pager"]
	var parts []string
	if s := scrollKeys(pm); len(s) > 0 {
		parts = append(parts, strings.Join(s, "/")+" scroll")
	}
	parts = append(parts, "x cancels")
	if q := keyFor(pm, "back"); q != "" {
		parts = append(parts, q+" closes")
	}
	return strings.Join(parts, "  ")
}

// renderTasks is the ~ style overlay surface: the active tasks through
// a viewport (the same widget the log overlay uses).
func (m Model) renderTasks() string {
	sig := strconv.Itoa(m.width) + "|" + strconv.Itoa(m.taskView.height) + "|" + strconv.Itoa(m.taskView.offset) + "|" + strconv.Itoa(m.taskCursor) + "|" + strconv.Itoa(m.styleVer) + "|" + strconv.Itoa(len(m.tasks))
	return m.taskLayer.get(sig, m.taskBuild)
}

func (m Model) taskBuild() string {
	rows := m.taskView.window()
	for len(rows) < m.taskView.height {
		rows = append(rows, "")
	}
	body := make([]string, 0, len(rows)+3)
	body = append(body, m.tabBar())
	body = append(body, "tasks")
	body = append(body, rows...)
	body = append(body, m.taskFooter())
	return strings.Join(body, "\n") + "\n" + m.statusLineWith(m.styles, m.ui)
}
