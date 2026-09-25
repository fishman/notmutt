// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

// Package chrome lays out terminal bars without owning their state or renderer.
package chrome

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

// Run is text annotated with a consumer-defined theme style ID.
type Run struct{ Text, Style string }

// Tabs renders a full-width tab strip, retaining the active tab when labels overflow.
func Tabs(labels []string, active, width int, barStyle, activeStyle string) []Run {
	if width <= 0 {
		return nil
	}
	if active < 0 || active >= len(labels) {
		active = 0
	}
	visible := labels
	for len(visible) > 1 && tabsWidth(visible) > width {
		last := len(visible) - 1
		if last == active {
			visible = append(append([]string(nil), visible[:last-1]...), visible[last:]...)
			active = len(visible) - 1
		} else {
			visible = visible[:last]
		}
	}
	runs := make([]Run, 0, len(visible)*2+1)
	used := 0
	for i, label := range visible {
		if i > 0 {
			runs = append(runs, Run{Text: " ", Style: barStyle})
			used++
		}
		style := barStyle
		if i == active {
			style = activeStyle
		}
		text := " " + label + " "
		text = clip(text, width-used)
		runs = append(runs, Run{Text: text, Style: style})
		used += runewidth.StringWidth(text)
	}
	if used < width {
		runs = append(runs, Run{Text: strings.Repeat(" ", width-used), Style: barStyle})
	}
	return runs
}

func tabsWidth(labels []string) int {
	width := len(labels) - 1
	for _, label := range labels {
		width += runewidth.StringWidth(label) + 2
	}
	return width
}

func clip(text string, width int) string {
	if width <= 0 {
		return ""
	}
	cells := 0
	for i, r := range text {
		cells += runewidth.RuneWidth(r)
		if cells > width {
			return text[:i]
		}
	}
	return text
}
