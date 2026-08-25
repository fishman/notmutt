// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

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
// statusSegment; statusline.go composes them by drop priority. Adding
// a status part is a new builder here, never a change to the composition.

// viewSegment is the view name - priority 10, always survives the width fit.
func viewSegment(name string, st Styles) statusSegment {
	return statusSegment{content: name, style: st.View, priority: 10}
}

// countSegment is the visible thread count.
func countSegment(visible int, st Styles) statusSegment {
	return statusSegment{content: strconv.Itoa(visible), style: st.Count, priority: 5}
}

// editedSegment marks a cursor message with staged tag ops (R14): the
// staged glyph plus the staged flag letters (R14), right after the view
// name. Priority 7 - survives the count and account, never outlives the
// view name.
func editedSegment(glyph string, st Styles) statusSegment {
	return statusSegment{content: glyph, style: st.Index.Staged, priority: 7}
}

// accountSegment is the cursor message's account (R2): the account tag
// rendered as its own colored pill. Priority 6 - survives the count's
// drop, never outlives the view name. Empty when the message has no
// account tag; the segment does not compose then.
func accountSegment(name string, st Styles) statusSegment {
	return statusSegment{content: name, style: st.Account, priority: 6}
}

// mimeSegment labels what the pager renders (text/plain or text/html,
// resolved against the message's actual parts - the ctrl+u and
// toggle-render surface). The zero style inherits the status
// background. Priority 5, like the count: drops with it on overrun.
func mimeSegment(mime string) statusSegment {
	return statusSegment{content: mime, priority: 5}
}

// legendSegment is the icon library: the pre-fitted "icon name" pairs
// for the view's tags. budget is the cells it may occupy; the content
// truncates wcwidth-aware so the row never shifts (R11). Priority 3:
// drops before the count on overrun.
func legendSegment(legend string, budget int) statusSegment {
	return statusSegment{content: truncCells(legend, budget), priority: 3}
}

// msgSegment is the status line's last log entry (the logEntry
// surface) on the reserved right slot. Pre-fitted to the leftover
// width; err styles it with the error style. Priority 0 - drops with
// the progress region first on overrun.
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
	fill := max(0, progressWidth-lipgloss.Width(label)-1)
	fillBar, emptyBar := progressBar(ui, p, fill)
	bar := styleBar(fillBar, emptyBar, st)
	return statusSegment{content: label + " " + bar, style: st.Status, priority: 0}
}

// accountTag is the message's account: core owns the one definition (the compose dialogue's detection chain uses it too).
func accountTag(tags []string, set map[string]bool) string {
	return core.AccountTag(tags, set)
}

// iconLegend builds the status-bar icon library for the selected
// message: the cursor row's tagged icons render as "icon name" pairs
// in row order. Account tags never appear - the account owns the
// accountSegment, one surface per fact. Empty when icons are off or
// the row has no mapped tags.
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
