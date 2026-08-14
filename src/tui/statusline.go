package tui

import (
	"fmt"
	"strings"

	"github.com/mattn/go-runewidth"

	"notmutt/core"
)

const progressWidth = 40

// statusLine is the bottom row: view name + visible count on the left,
// the async progress bar right-aligned in a fixed-width region (R15).
// Completion (Done == Total) clears the bar; labels are job-kind
// derived, never mail content (F6). The whole line renders through the
// status style; the bar's filled cells carry the progress style, the
// empty cells the base style. The `progress` style identifier and the
// filled-cell glyph (default "#") are hardcoded defaults until the
// theming milestone.
func (m Model) statusLine(st Styles) string {
	left := fmt.Sprintf("%s %d", m.view.Name, len(m.rows))
	if !m.progressOn {
		return st.Status.Render(left)
	}
	label := fmt.Sprintf("%s %d/%d", m.progress.Job, m.progress.Done, m.progress.Total)
	fill := progressWidth - runewidth.StringWidth(label) - 1
	if fill < 0 {
		fill = 0
	}
	bar := styleBar(progressBar(m.progress, fill), st)
	right := label + " " + bar
	if pad := m.width - runewidth.StringWidth(left) - progressWidth; pad > 0 {
		return st.Status.Render(left + strings.Repeat(" ", pad) + right)
	}
	return st.Status.Render(left)
}

func progressBar(p core.Progress, cells int) string {
	if cells < 0 {
		return ""
	}
	fill := 0
	if p.Total > 0 && p.Done < p.Total {
		fill = int(float64(p.Done) * float64(cells) / float64(p.Total))
	}
	return strings.Repeat("#", fill) + strings.Repeat("-", cells-fill)
}

// styleBar applies the progress style to the filled cells and the base
// style to the empty cells of a bar string.
func styleBar(bar string, st Styles) string {
	if i := strings.IndexByte(bar, '-'); i >= 0 {
		return st.Progress.Render(bar[:i]) + st.Normal.Render(bar[i:])
	}
	return st.Progress.Render(bar)
}
