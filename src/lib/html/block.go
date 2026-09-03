// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

// Row is one emitted content row of block flow: a filled text line, the
// 2px hr rule, or a marker-only row for an item that emitted no content
// row (empty li), positioned in px. Rows are pure vertical flow (no
// floats), so a flat ordered stream is lossless for stage 2.
type Row struct {
	Gap     int         // collapsed px of margin above this row's content edge
	X       int         // absolute px left edge of the content box
	W       int         // content-box px width (wrap/align budget)
	Box     *Box        // the block that owns the row (style/theme)
	Line    LineBox     // filled content line (unused when HR or marker-only)
	HR      bool        // this row is the 2px hr rule
	Markers []RowMarker // list markers hanging in this row's gutters
	Cells   []Row       // table grid row: per-cell fragments side by side (mutually exclusive with Line/HR)
}

// RowMarker is one list marker hanging before its row. X is the px content
// edge of the OWNING list item (where its own text would start) - not
// necessarily the row's X: a text-less li whose first content line is a
// nested block hangs its marker in its own gutter while the row sits deeper.
type RowMarker struct {
	Type string // disc|circle|square|decimal
	X    int
}

// seam is the run of mutually-adjoining vertical margins since the last
// content edge, kept as running extrema: collapse(list) = max(pos) +
// min(neg) (weasyprint block.py collapse_margin). Appending a margin and
// consuming the seam are both O(1) - a margin list is never rescanned, so
// stacking N siblings stays O(N) even on hostile input.
type seam struct {
	maxPos int
	minNeg int
}

func (s *seam) add(m int) {
	if m > s.maxPos {
		s.maxPos = m
	}
	if m < s.minNeg {
		s.minNeg = m
	}
}

func (s *seam) take() int {
	g := s.maxPos + s.minNeg
	*s = seam{}
	return g
}

// geom is a block box's resolved geometry in px. Anonymous runs (Tag "")
// carry their container's shared style pointer and must read as zero: an
// anonymous box has no margins of its own.
func geom(b *Box) (mt, mr, mb, ml, pl int) {
	if b.Tag == "" || b.St == nil {
		return
	}
	return b.St.MarginTop, b.St.MarginRight, b.St.MarginBottom, b.St.MarginLeft, b.St.PadLeft
}

// LayoutBlock lays out the document's top-level flow boxes into an ordered
// px row stream at the given content width. Top-level content with no block
// child (a pure-inline body) lays out as one implicit run.
func LayoutBlock(bs []*Box, width int, m Metrics, norm bool) []Row {
	if !hasBlockChild(bs) {
		bs = []*Box{{Role: RoleBlock, Children: bs}}
	}
	var s seam
	return flow(bs, 0, width, &s, m, norm)
}

// flow stacks cs in their container's content box at (x0, w), threading one
// seam across the whole tree: a sibling's margin, its parent's margin, and a
// collapse-through descendant all land in the same run because no modeled
// border or padding interrupts a block's content edge. A box that emits no
// content row collapses through (its margins stay in the run).
func flow(cs []*Box, x0, w int, s *seam, m Metrics, norm bool) []Row {
	var rows []Row
	for _, c := range cs {
		mt, mr, mb, ml, pl := geom(c)
		s.add(mt)
		cx := x0 + ml + pl
		cw := w - ml - mr - pl
		if cw < 0 {
			cw = 0
		}
		first := len(rows)
		switch {
		case c.Tbl == "table":
			// the grid case runs before the block recursion: a table's
			// row-group children must not flow as stacked blocks
			rows = append(rows, tableRows(c, cx, cw, s, m, norm)...)
		case c.Tag == "hr":
			rows = append(rows, Row{Gap: s.take(), X: cx, W: cw, Box: c, HR: true})
		case hasBlockChild(c.Children):
			rows = append(rows, flow(c.Children, cx, cw, s, m, norm)...)
		default:
			for i, line := range LayoutInline(c, cw, m, norm) {
				gap := 0
				if i == 0 {
					gap = s.take() // only the first line consumes the seam
				}
				rows = append(rows, Row{Gap: gap, X: cx + line.X, W: cw, Box: c, Line: line})
			}
		}
		if c.Marker != "" {
			if len(rows) == first {
				// the item emitted no content row (empty li, or content that
				// collapsed away): its marker still gets a line (weasyprint)
				rows = append(rows, Row{Gap: s.take(), X: cx, W: cw, Box: c,
					Markers: []RowMarker{{Type: c.Marker, X: cx}}})
			} else {
				rows[first].Markers = append(rows[first].Markers,
					RowMarker{Type: c.Marker, X: cx})
			}
		}
		s.add(mb)
	}
	return rows
}
