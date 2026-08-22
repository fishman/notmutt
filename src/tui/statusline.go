// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"notmutt/config"
	"notmutt/core"
)

// statusSegment is one composable cell of the status line: content,
// style, and a drop priority (powerline-go Segment, cut to notmutt).
// Lower priority drops earlier when the row exceeds the terminal width.
type statusSegment struct {
	content  string
	style    lipgloss.Style // zero value inherits the status style
	priority int
}

// statusData is the status line's input state, built from the view and progress state.
type statusData struct {
	view    string
	visible int
	prog    *core.Progress // nil = no job on
	on      bool
	legend  string // icon library: "icon name" pairs for the view's tags
	account string // the cursor message's account tag (R2), empty on none
	mime    string // the pager's rendered mime label, empty outside pager mode
	msg     string // the status line's last log entry, empty on none
	msgErr  bool   // styles the status message with the error style
}

// statusLine renders the status row at the default width.
func statusLine(st Styles, ui config.UI, d statusData) string {
	return statusLineWidth(st, ui, d, defaultStatusWidth)
}

// statusLineWidth composes the status row at a given width: the left
// group (view name, visible count) and the right group (the progress
// region, R15) on the shared status background. Fitting follows
// powerline-go's truncateRow: lowest-priority segments drop first -
// progress (0), then the count (5); the view name (10) always
// survives. The row always covers the full width (R11 slot
// reservation).
func statusLineWidth(st Styles, ui config.UI, d statusData, width int) string {
	left := []statusSegment{viewSegment(d.view, st), countSegment(d.visible, st)}
	if d.account != "" {
		left = append(left, accountSegment(d.account, st))
	}
	if d.mime != "" {
		left = append(left, mimeSegment(d.mime))
	}
	var right []statusSegment
	if d.on && d.prog != nil {
		right = append(right, progressSegment(ui, *d.prog, st))
	}
	// The status message (R4 send completions) is the reserved right
	// slot: rightmost, pre-fitted to the leftover width, truncated
	// wcwidth-aware (R11 slot reservation) - the row never shifts with
	// the message.
	if d.msg != "" {
		fixed := groupWidth(left)
		budget := width - fixed - groupWidth(right) - 3*lipgloss.Width(pillGap)
		if budget > 0 {
			right = append(right, msgSegment(d.msg, budget, d.msgErr, st))
		}
	}
	// The legend takes the leftover slot, pre-fitted to the leftover
	// width, truncated wcwidth-aware (R11 slot reservation); the drop
	// loop stays as the backstop when a future segment overruns. The
	// footprint is content + two inner gaps + the bar gap before it.
	if d.legend != "" {
		fixed := groupWidth(left)
		budget := width - fixed - groupWidth(right) - 3*lipgloss.Width(pillGap)
		if budget > 0 {
			left = append(left, legendSegment(d.legend, budget))
		}
	}
	for {
		w := groupWidth(left) + groupWidth(right)
		if width <= 0 || w <= width {
			break
		}
		dropFrom, dropIdx := pickLowest(left, right)
		if dropIdx < 0 {
			break // only the view name is left; it survives
		}
		if dropFrom == 0 {
			left = append(left[:dropIdx], left[dropIdx+1:]...)
		} else {
			right = append(right[:dropIdx], right[dropIdx+1:]...)
		}
	}
	row, rowWidth := composeGroup(left, st)
	if rightWidth := groupWidth(right); rightWidth > 0 {
		rr, _ := composeGroup(right, st)
		// lipgloss places the right group: right-aligned in the leftover
		// width, the gap padded in the status background (R11)
		row += st.Status.Width(width - rowWidth).Align(lipgloss.Right).Render(rr)
		return row
	}
	if pad := width - rowWidth; pad > 0 {
		row += st.Status.Render(strings.Repeat(" ", pad))
	}
	return row
}

// pickLowest finds the lowest-priority droppable segment across the
// left (0) and right (1) groups; priorities >= 10 never drop.
func pickLowest(left, right []statusSegment) (from, idx int) {
	from, idx = -1, -1
	lowest := 1 << 30
	consider := func(g int, segs []statusSegment) {
		for i, s := range segs {
			if s.priority >= 10 {
				continue
			}
			if s.priority < lowest {
				lowest, from, idx = s.priority, g, i
			}
		}
	}
	consider(0, left)
	consider(1, right)
	return from, idx
}

// groupWidth is a pill run's visible width: each segment's content plus its two inner gaps, and a bar gap between pills (lipgloss, SGR-aware).
func groupWidth(segs []statusSegment) int {
	if len(segs) == 0 {
		return 0
	}
	w := 0
	for _, s := range segs {
		w += lipgloss.Width(s.content) + 2*lipgloss.Width(pillGap)
	}
	return w + (len(segs)-1)*lipgloss.Width(pillGap)
}

const pillGap = " "

// composeGroup renders a run of segments as pills: each segment is a colored block with inner padding, separated by whitespace on the bar - never connected.
func composeGroup(segs []statusSegment, st Styles) (string, int) {
	if len(segs) == 0 {
		return "", 0
	}
	var b strings.Builder
	for i, s := range segs {
		if i > 0 {
			b.WriteString(st.Status.Render(pillGap))
		}
		cur := segmentStyle(s, st)
		b.WriteString(cur.Render(pillGap + s.content + pillGap))
	}
	return b.String(), lipgloss.Width(b.String())
}

// segmentStyle resolves a segment's zero style to the status style.
func segmentStyle(s statusSegment, st Styles) lipgloss.Style {
	if s.style.GetForeground() == (lipgloss.NoColor{}) && s.style.GetBackground() == (lipgloss.NoColor{}) {
		return st.Status
	}
	return s.style
}

// progressBar builds the fill and empty glyph runs for done/total at
// the given cell budget. The glyphs are config data (R11), so the bar
// comes back as two runs styled separately. Empty runs for a clamped
// or zero-total job.
func progressBar(ui config.UI, p core.Progress, cells int) (string, string) {
	if cells < 0 {
		return "", ""
	}
	fill := 0
	if p.Total > 0 && p.Done < p.Total {
		fill = int(float64(p.Done) * float64(cells) / float64(p.Total))
	}
	return strings.Repeat(ui.Glyphs.ProgressFill, fill), strings.Repeat(ui.Glyphs.ProgressEmpty, cells-fill)
}

// styleBar applies the progress style to the fill run and the base style to the empty run.
func styleBar(fill, empty string, st Styles) string {
	if empty == "" {
		return st.Progress.Render(fill)
	}
	return st.Progress.Render(fill) + st.Normal.Render(empty)
}

// tabBar renders the tab strip: the mail surface tab and every open
// dialogue, the active one highlighted. Trailing tabs drop to fit the
// width, the active tab always survives (it trades places with the
// dropped tail); the row pads to the full width (R11).
func (m Model) tabBar() string {
	if m.width <= 0 {
		return ""
	}
	names := m.tabNames()
	active := m.tabIdx
	if active >= len(names) {
		active = 0
	}
	for len(names) > 1 && tabStripWidth(names, active, m.styles) > m.width {
		last := len(names) - 1
		if last == active {
			// the tail is the active tab: drop the one before it so the active keeps the tail slot
			names = append(names[:last-1], names[last:]...)
			active = len(names) - 1
		} else {
			names = names[:last]
		}
	}
	var b strings.Builder
	for i, n := range names {
		s := m.styles.Tabbar
		if i == active {
			s = m.styles.TabActive
		}
		if i > 0 {
			b.WriteString(m.styles.Tabbar.Render(pillGap))
		}
		b.WriteString(s.Render(pillGap + n + pillGap))
	}
	row := b.String()
	if pad := m.width - lipgloss.Width(row); pad > 0 {
		row += m.styles.Tabbar.Render(strings.Repeat(" ", pad))
	}
	return row
}

// tabNames is the strip's labels in session order: the mail surface
// first (the view name), then every open dialogue (the subject, or
// "compose" when none - mail-derived, F1 sanitize applies). Names cap
// at a third of the strip so one subject cannot crowd the rest.
func (m Model) tabNames() []string {
	capName := func(n string) string {
		if c := m.width / 3; c > 0 && lipgloss.Width(n) > c {
			n = truncCells(n, c)
		}
		return n
	}
	names := make([]string, 0, len(m.tabs)+len(m.searchTabs)+1)
	names = append(names, capName(m.view.Name))
	for _, st := range m.tabs {
		n := st.Subject
		if n == "" {
			n = "compose"
		}
		names = append(names, capName(core.SanitizeControls(n)))
	}
	for _, v := range m.searchTabs {
		names = append(names, capName(core.SanitizeControls(v.ViewName())))
	}
	return names
}

// tabStripWidth is the pill run's visible width (groupWidth for the tab strip, gaps included).
func tabStripWidth(names []string, active int, st Styles) int {
	w := 0
	for i, n := range names {
		s := st.Tabbar
		if i == active {
			s = st.TabActive
		}
		w += lipgloss.Width(s.Render(pillGap + n + pillGap))
		if i > 0 {
			w += lipgloss.Width(pillGap)
		}
	}
	return w
}
