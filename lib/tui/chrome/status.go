// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package chrome

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

// Segment is a pill whose runs are retained or dropped as one unit.
type Segment struct {
	Runs     []Run
	Priority int
}

// Status fits groups by priority and aligns the right group at the row edge.
func Status(width int, background string, left, right []Segment) []Run {
	if width <= 0 {
		return nil
	}
	l := append([]Segment(nil), left...)
	r := append([]Segment(nil), right...)
	for groupWidth(l)+groupWidth(r) > width {
		group, index := lowest(l, r)
		if index < 0 {
			break
		}
		if group == 0 {
			l = append(l[:index], l[index+1:]...)
		} else {
			r = append(r[:index], r[index+1:]...)
		}
	}
	out := make([]Run, 0, 2*len(l)+2*len(r)+3)
	out, used := appendGroup(out, l, background, width)
	rightWidth := groupWidth(r)
	if used+rightWidth <= width {
		if pad := width - used - rightWidth; pad > 0 {
			out = append(out, Run{Text: strings.Repeat(" ", pad), Style: background})
			used += pad
		}
	}
	var rightUsed int
	out, rightUsed = appendGroup(out, r, background, width-used)
	used += rightUsed
	if used < width {
		out = append(out, Run{Text: strings.Repeat(" ", width-used), Style: background})
	}
	return out
}

func lowest(left, right []Segment) (group, index int) {
	group, index = -1, -1
	priority := 10
	for side, segments := range [][]Segment{left, right} {
		for i, segment := range segments {
			if segment.Priority < priority {
				priority, group, index = segment.Priority, side, i
			}
		}
	}
	return group, index
}

func groupWidth(segments []Segment) int {
	if len(segments) == 0 {
		return 0
	}
	width := len(segments) - 1
	for _, segment := range segments {
		width += 2
		for _, run := range segment.Runs {
			width += runewidth.StringWidth(run.Text)
		}
	}
	return width
}

func appendGroup(out []Run, segments []Segment, background string, limit int) ([]Run, int) {
	used := 0
	add := func(text, style string) {
		if text == "" || used >= limit {
			return
		}
		text = clip(text, limit-used)
		if style == "" {
			style = background
		}
		out = append(out, Run{Text: text, Style: style})
		used += runewidth.StringWidth(text)
	}
	for i, segment := range segments {
		if i > 0 {
			add(" ", background)
		}
		style := background
		if len(segment.Runs) > 0 && segment.Runs[0].Style != "" {
			style = segment.Runs[0].Style
		}
		add(" ", style)
		for _, run := range segment.Runs {
			add(run.Text, run.Style)
		}
		add(" ", style)
	}
	return out, used
}
