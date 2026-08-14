package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"notmutt/config"
	"notmutt/core"
)

// statusSegment is one composable cell of the status line: content,
// style, and a drop priority (powerline-go Segment, cut to notmutt).
// The lower the priority, the earlier the segment drops when the row
// exceeds the terminal width.
type statusSegment struct {
	content  string
	style    lipgloss.Style // zero value inherits the status style
	priority int
}

// statusData is the status line's input state; the model builds it
// from the view and progress state.
type statusData struct {
	view    string
	visible int
	prog    *core.Progress // nil = no job on
	on      bool
	legend  string // icon library: "icon name" pairs for the view's tags
	account string // the cursor message's account tag (R2), empty on none
}

// statusLine renders the status row at the default width.
func statusLine(st Styles, ui config.UI, d statusData) string {
	return statusLineWidth(st, ui, d, defaultStatusWidth)
}

// statusLineWidth composes the status row at a given width: the left
// group (view name, visible count - future segments append the same
// way) and the right group (the progress region, R15) on the shared
// status background. Width fitting follows powerline-go's truncateRow:
// when the composed row exceeds the width, the lowest-priority
// segments drop first - progress region (0), then the visible count
// (5); the view name (10) always survives. The row always covers the
// full width: the right group right-aligns, trailing gaps pad with
// the status background (R11 slot reservation).
func statusLineWidth(st Styles, ui config.UI, d statusData, width int) string {
	left := []statusSegment{viewSegment(d.view), countSegment(d.visible)}
	if d.account != "" {
		left = append(left, accountSegment(d.account))
	}
	var right []statusSegment
	if d.on && d.prog != nil {
		right = append(right, progressSegment(ui, *d.prog, st))
	}
	sep := ui.Glyphs.StatuslineSeparator
	if d.legend != "" {
		// The legend is pre-fitted to the row: whatever width the fixed
		// segments (view, count) and the right group leave, truncated
		// wcwidth-aware - the status row never shifts with its content
		// (R11 slot reservation). The drop loop below stays as the
		// backstop when a future segment overruns.
		fixed := groupWidth(left, sep)
		budget := width - fixed - groupWidth(right, sep) - runewidth.StringWidth(sep)
		if budget > 0 {
			left = append(left, legendSegment(d.legend, budget))
		}
	}
	for {
		w := groupWidth(left, sep) + groupWidth(right, sep)
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
	row, rowWidth := composeGroup(left, sep, st)
	if rightWidth := groupWidth(right, sep); rightWidth > 0 {
		rr, _ := composeGroup(right, sep, st)
		if pad := width - rowWidth - rightWidth; pad > 0 {
			row += st.Status.Render(strings.Repeat(" ", pad))
		}
		row += rr
		return row
	}
	if pad := width - rowWidth; pad > 0 {
		row += st.Status.Render(strings.Repeat(" ", pad))
	}
	return row
}

// pickLowest finds the lowest-priority droppable segment across the
// left (0) and right (1) groups; priorities >= 10 (the view name)
// never drop.
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

// groupWidth is the visible width of a joined segment run.
func groupWidth(segs []statusSegment, sep string) int {
	if len(segs) == 0 {
		return 0
	}
	w := 0
	for _, s := range segs {
		w += runewidth.StringWidth(stripANSI(s.content))
	}
	return w + (len(segs)-1)*runewidth.StringWidth(sep)
}

// composeGroup renders a run of segments joined by separator seams.
func composeGroup(segs []statusSegment, sep string, st Styles) (string, int) {
	if len(segs) == 0 {
		return "", 0
	}
	var b strings.Builder
	prev := segmentStyle(segs[0], st)
	b.WriteString(prev.Render(segs[0].content))
	for _, s := range segs[1:] {
		cur := segmentStyle(s, st)
		b.WriteString(seam(prev, cur, sep))
		b.WriteString(cur.Render(s.content))
		prev = cur
	}
	return b.String(), runewidth.StringWidth(stripANSI(b.String()))
}

// segmentStyle resolves a segment's zero style to the status style.
func segmentStyle(s statusSegment, st Styles) lipgloss.Style {
	if s.style.GetForeground() == (lipgloss.NoColor{}) && s.style.GetBackground() == (lipgloss.NoColor{}) {
		return st.Status
	}
	return s.style
}

// seam renders the separator between two adjacent segments: fg = the
// previous segment's bg on the next segment's bg (the powerline
// chevron); equal bgs render the separator in the previous segment's
// fg instead - a plain "|" on the shared status background.
func seam(prev, next lipgloss.Style, sep string) string {
	s := next
	if prev.GetBackground() != next.GetBackground() {
		s = s.Foreground(prev.GetBackground())
	} else if fg := prev.GetForeground(); fg != (lipgloss.NoColor{}) {
		s = s.Foreground(fg)
	}
	return s.Render(sep)
}

// progressBar builds the fill and empty glyph runs for the job's
// done/total at the given cell budget. The glyphs are config data
// (R11), so the bar comes back as two runs and the caller styles each
// separately - no glyph is hardcoded and multi-byte glyphs split
// correctly. Empty runs for a clamped or zero-total job.
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

// styleBar applies the progress style to the fill run and the base
// style to the empty run.
func styleBar(fill, empty string, st Styles) string {
	if empty == "" {
		return st.Progress.Render(fill)
	}
	return st.Progress.Render(fill) + st.Normal.Render(empty)
}
