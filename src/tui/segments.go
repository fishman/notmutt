package tui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"

	"notmutt/config"
	"notmutt/core"
)

// segments.go: the status line's composable parts (the powerline-go
// Segment model, cut to notmutt - R15). Each builder returns a
// statusSegment; statusline.go composes and fits them by drop
// priority. Adding a status part is a new builder here, never a change
// to the composition.

// viewSegment is the view name - priority 10, the one segment that
// always survives the width fit.
func viewSegment(name string, st Styles) statusSegment {
	return statusSegment{content: name, style: st.View, priority: 10}
}

// countSegment is the visible thread count.
func countSegment(visible int, st Styles) statusSegment {
	return statusSegment{content: strconv.Itoa(visible), style: st.Count, priority: 5}
}

// accountSegment is the cursor message's account (R2): the account tag
// the message carries, rendered as its own colored pill. Priority 6 -
// it survives the count's drop but never outlives the view name. Empty
// when the message has no account tag; the segment simply does not
// compose then.
func accountSegment(name string, st Styles) statusSegment {
	return statusSegment{content: name, style: st.Account, priority: 6}
}

// legendSegment is the icon library: the pre-fitted "icon name" pairs
// for the view's tags. budget is the cells it may occupy; the content
// truncates wcwidth-aware so the row never shifts with its content
// (R11 slot reservation). Priority 3: drops before the count when the
// row overruns.
func legendSegment(legend string, budget int) statusSegment {
	return statusSegment{content: truncCells(legend, budget), priority: 3}
}

// msgSegment is the status line's last log entry (the send result,
// the lua result, a job error - the logEntry surface) on the status
// line's reserved right slot. Pre-fitted to the leftover width; err
// styles it with the error style. Priority 0 - it drops with the
// progress region first when the row overruns.
func msgSegment(msg string, budget int, err bool, st Styles) statusSegment {
	s := statusSegment{content: truncCells(msg, budget), priority: 0}
	if err {
		s.style = st.Error
	}
	return s
}

// progressSegment is the job progress region (R15).
func progressSegment(ui config.UI, p core.Progress, st Styles) statusSegment {
	label := fmt.Sprintf("%s %d/%d", p.Job, p.Done, p.Total)
	fill := progressWidth - lipgloss.Width(label) - 1
	if fill < 0 {
		fill = 0
	}
	fillBar, emptyBar := progressBar(ui, p, fill)
	bar := styleBar(fillBar, emptyBar, st)
	return statusSegment{content: label + " " + bar, style: st.Status, priority: 0}
}

// accountTag is the message's account: core owns the one definition
// (the compose dialogue's detection chain uses it too).
func accountTag(tags []string, set map[string]bool) string {
	return core.AccountTag(tags, set)
}

// iconLegend builds the status-bar icon library for the currently
// selected message: the tags of the cursor row that have icons render
// as "icon name" pairs, in the row's tag order. Account tags never
// appear - the account owns the accountSegment, one surface per fact.
// Empty when icons are off or the row has no mapped tags - the legend
// mirrors the row, so its meaning is always on screen.
func iconLegend(tags []string, t config.UITags, accounts map[string]bool) string {
	if !t.ShowIcons || len(t.Icons) == 0 {
		return ""
	}
	var b strings.Builder
	n := 0
	for _, tag := range tags {
		if accounts[tag] {
			continue
		}
		icon, ok := t.Icons[tag]
		if !ok {
			continue
		}
		if n > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(icon)
		b.WriteByte(' ')
		b.WriteString(tag)
		n++
	}
	return b.String()
}
