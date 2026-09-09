// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

// Package table renders fixed-column text tables for terminal rows. A table's
// geometry - its bounded columns and the separator drawn between them - is one
// Layout shared by every row of a surface; column widths are laid out once per
// frame so rows align. Each column is bounded by a floor and a cap, and the
// width left over is handed to the columns in order (the tview expansion model
// - references/tview table.go - with the floors tview lacks). Output is plain
// lines: alignment is this package's job, styling and the cell buffer stay
// with the caller.
package table

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

// Col is one bounded column: at least Floor cells wide, at most Cap. A column
// with Cap == Floor is fixed - it never grows and never gives width back.
type Col struct{ Floor, Cap int }

// Layout is a table's fixed geometry: its bounded columns and the separator
// glyph drawn in each gutter. Build a Layout once (the separator from the
// caller's theme, with whatever padding belongs around it) and let every row
// of the surface read it - columns and separator never change row to row.
type Layout struct {
	Cols []Col
	Sep  string
}

// Sizes lays a Layout's columns out over a row of width cells. The separator's
// cell width is measured, so any glyph that renders in one width budgets
// identically and padding is just part of the separator string. tail reports
// whether a free-form cell follows the table (its gutter is reserved, so the
// bounded columns align whether the tail is there or not). Every column starts
// at its Floor; the width left over goes to the columns in order, each
// stopping at its Cap. A width under the floors shrinks the flexible columns
// from the end so the row still fits; fixed columns keep their width.
func (l *Layout) Sizes(width int, tail bool) []int {
	seams := len(l.Cols) - 1
	if tail {
		seams++
	}
	budget := width - runewidth.StringWidth(l.Sep)*seams
	sizes := make([]int, len(l.Cols))
	sum := 0
	for i, c := range l.Cols {
		sizes[i] = c.Floor
		sum += c.Floor
	}
	if budget < sum {
		// over the floors: take back from the flexible columns, end first
		for i := len(l.Cols) - 1; i >= 0 && sum > budget; i-- {
			if l.Cols[i].Cap == l.Cols[i].Floor {
				continue
			}
			d := sum - budget
			if d > sizes[i] {
				d = sizes[i]
			}
			sizes[i] -= d
			sum -= d
		}
	} else {
		// surplus to the columns in order, each up to its cap
		for left, i := budget-sum, 0; i < len(l.Cols) && left > 0; i++ {
			if room := l.Cols[i].Cap - sizes[i]; room > 0 {
				if room > left {
					room = left
				}
				sizes[i] += room
				left -= room
			}
		}
	}
	return sizes
}

// Line renders one table row: cells[i] is padded into (or truncated to) its
// column and the columns join on the layout's separator (the same string Sizes
// measured). A column with no cell stays blank but reserved, so the edges
// hold. Cells beyond the bounded columns trail the table free-form - a status
// or detail tail that runs to the row's end, never padded.
func (l *Layout) Line(cells []string, sizes []int) string {
	var b strings.Builder
	for i, w := range sizes {
		if i > 0 {
			b.WriteString(l.Sep)
		}
		var s string
		if i < len(cells) {
			s = cells[i]
		}
		b.WriteString(pad(s, w))
	}
	for i := len(sizes); i < len(cells); i++ {
		if i > 0 {
			b.WriteString(l.Sep)
		}
		b.WriteString(cells[i])
	}
	return b.String()
}

// pad left-aligns s into width cells: wider text truncates and shorter text
// pads with spaces so a column's edge never shifts. Cell math (wcwidth), not
// runes - a double-width rune that would straddle the cut is dropped whole.
func pad(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) >= width {
		var b strings.Builder
		cells := 0
		for _, r := range s {
			cw := runewidth.RuneWidth(r)
			if cells+cw > width {
				break
			}
			b.WriteRune(r)
			cells += cw
		}
		return b.String()
	}
	return s + strings.Repeat(" ", width-runewidth.StringWidth(s))
}
