// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/fishman/notmutt/lib/tui/chrome"

	"notmutt/config"
	"notmutt/core"
	"notmutt/i18n"
)

// statusSegment keeps its resolved style for existing callers and a shared style ID.
type statusSegment struct {
	content  string
	style    lipgloss.Style
	id       string
	priority int
	runs     []chrome.Run
}

// statusData is the status line's input state, built from the view and progress state.
type statusData struct {
	view      string
	visible   int
	prog      *core.Progress // nil = no job on
	on        bool
	legend    string // icon library: "icon name" pairs for the view's tags
	account   string // the cursor message's account tag (R2), empty on none
	mime      string // the pager's rendered mime label, empty outside pager mode
	edited    bool   // the cursor message has staged tag ops (R14), index or pager
	flags     string // the staged flag letters (flagChars of the staged display tags), empty on none
	msg       string // the status line's last log entry, empty on none
	msgErr    bool   // styles the status message with the error style
	spin      bool   // background work in flight (the status spinner)
	spinFrame int    // the spinner's current frame index
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
	left := []statusSegment{spinnerSegment(d.spin, d.spinFrame, st), viewSegment(d.view, st), countSegment(d.visible, st)}
	if d.account != "" {
		left = append(left, accountSegment(d.account, st))
	}
	if d.edited {
		left = append(left, editedSegment(ui.Glyphs.Staged+d.flags, st))
	}
	if d.mime != "" {
		left = append(left, mimeSegment(d.mime))
	}
	var right []statusSegment
	if d.on && d.prog != nil {
		right = append(right, progressSegment(ui, *d.prog, st))
	}
	if d.msg != "" {
		budget := width - chrome.Width(chromeSegments(left)) - chrome.Width(chromeSegments(right)) - 3*lipgloss.Width(pillGap)
		if budget > 0 {
			right = append(right, msgSegment(d.msg, budget, d.msgErr, st))
		}
	}
	if d.legend != "" {
		budget := width - chrome.Width(chromeSegments(left)) - chrome.Width(chromeSegments(right)) - 3*lipgloss.Width(pillGap)
		if budget > 0 {
			left = append(left, legendSegment(d.legend, budget))
		}
	}
	return renderChrome(chrome.Status(width, "status", chromeSegments(left), chromeSegments(right)), st, true)
}

func chromeSegments(segments []statusSegment) []chrome.Segment {
	out := make([]chrome.Segment, 0, len(segments))
	for _, segment := range segments {
		runs := segment.runs
		if len(runs) == 0 {
			runs = []chrome.Run{{Text: segment.content, Style: segment.id}}
		}
		out = append(out, chrome.Segment{Runs: runs, Priority: segment.priority})
	}
	return out
}

func renderChrome(runs []chrome.Run, st Styles, groupPills bool) string {
	var row, span strings.Builder
	styleID := ""
	paint := func() {
		if span.Len() == 0 {
			return
		}
		style := st.Status
		switch styleID {
		case "tabbar":
			style = st.Tabbar
		case "tabbar.active":
			style = st.TabActive
		case "status.view":
			style = st.View
		case "status.count":
			style = st.Count
		case "status.account":
			style = st.Account
		case "index.staged":
			style = st.Index.Staged
		case "progress":
			style = st.Progress
		case "error":
			style = st.Error
		case "normal":
			style = st.Normal
		}
		row.WriteString(style.Render(span.String()))
		span.Reset()
	}
	for _, run := range runs {
		if run.Text == "" {
			continue
		}
		if run.Style != styleID || !groupPills {
			paint()
			styleID = run.Style
		}
		span.WriteString(run.Text)
		if !groupPills {
			paint()
		}
	}
	paint()
	return row.String()
}

const pillGap = " "

// progressBar builds the fill and empty glyph runs for done/total at
// the given cell budget. The glyphs are config data (R11), so the bar
// comes back as two runs styled separately. Done at or past Total (a
// job outgrowing a stale count) renders FULL - the kind protocol, not
// the ratio, clears the bar.
func progressBar(ui config.UI, p core.Progress, cells int) (string, string) {
	if cells < 0 {
		return "", ""
	}
	fill := 0
	if p.Total > 0 {
		fill = int(float64(p.Done) * float64(cells) / float64(p.Total))
	}
	if fill > cells {
		fill = cells
	}
	return strings.Repeat(ui.Glyphs.ProgressFill, fill), strings.Repeat(ui.Glyphs.ProgressEmpty, cells-fill)
}

// tabBar renders the tab strip: the mail surface tab and every open
// dialogue, the active one highlighted. Trailing tabs drop to fit the
// width, the active tab always survives (it trades places with the
// dropped tail); the row pads to the full width (R11).
func (m Model) tabBar() string {
	return renderChrome(chrome.Tabs(m.tabNames(), m.tabIdx, m.width, "tabbar", "tabbar.active"), m.styles, false)
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
	for _, s := range m.singletons {
		names = append(names, capName(core.SanitizeControls(s.name)))
	}
	if m.summary != nil {
		names = append(names, capName(i18n.T("summary")))
	}
	return names
}
