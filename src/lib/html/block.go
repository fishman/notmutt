// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import "strings"

// Row is one emitted content row of block flow: a filled text line, the
// 2px hr rule, a marker-only row for an item that emitted no content row
// (empty li), or a horizontal strip (Cells) carrying one fragment per
// table-grid cell or floated column, positioned in px. Strips are what make
// a flat ordered stream lossless for stage 2.
type Row struct {
	Gap     int         // collapsed px of margin above this row's content edge
	X       int         // absolute px left edge of the content box
	W       int         // content-box px width (wrap/align budget)
	Box     *Box        // the block that owns the row (style/theme)
	Line    LineBox     // filled content line (unused when HR or marker-only)
	HR      bool        // this row is the 2px hr rule
	Markers []RowMarker // list markers hanging in this row's gutters
	Cells   []Row       // horizontal strip: per-cell/per-column fragments side by side (mutually exclusive with Line/HR)
}

// RowMarker is one list marker hanging before its row. X is the px content
// edge of the OWNING list item (where its own text would start) - not
// necessarily the row's X: a text-less li whose first content line is a
// nested block hangs its marker in its own gutter while the row sits deeper.
type RowMarker struct {
	Type string // disc|circle|square|decimal
	X    int
	Ord  int // 1-based ordinal for a decimal marker of an ordered list (0 otherwise)
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
// content row collapses through (its margins stay in the run). A run of two
// or more floated sibling tables lays side by side as a horizontal band
// instead of stacking (floatSide).
func flow(cs []*Box, x0, w int, s *seam, m Metrics, norm bool) []Row {
	var rows []Row
	for i := 0; i < len(cs); i++ {
		c := cs[i]
		if c.Tbl == "table" && floatSide(c) != "" {
			if j := floatRun(cs, i); j-i >= 2 {
				mt, _, _, _, _ := geom(c)
				s.add(mt)
				rows = append(rows, emitFloatBand(cs[i:j], x0, w, s, m, norm)...)
				_, _, mb, _, _ := geom(cs[j-1])
				s.add(mb)
				i = j - 1 // the whitespace between floats was part of the band
				continue
			}
		}
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
					Markers: []RowMarker{{Type: c.Marker, X: cx, Ord: c.Ord}}})
			} else {
				rows[first].Markers = append(rows[first].Markers,
					RowMarker{Type: c.Marker, X: cx, Ord: c.Ord})
			}
		}
		s.add(mb)
	}
	return rows
}

// floatSide reports a table box's float side, "" for none: CSS float:left/right
// or the legacy align=left/right attribute float a table (align=center is
// text-align only, never a float here). td align, div floats, and other boxes
// are out of scope - only RoleTable "table" boxes float in this model.
func floatSide(b *Box) string {
	if b.Tbl != "table" {
		return ""
	}
	if b.St != nil {
		switch b.St.Float {
		case "left", "right":
			return b.St.Float
		}
	}
	switch Attr(b.Node, "align") {
	case "left":
		return "left"
	case "right":
		return "right"
	}
	return ""
}

// blankBox reports a box flow drops entirely (pretty-print whitespace
// between blocks): a whitespace-only text leaf, or an anonymous run holding
// only such leaves. A real block (a div, a p) is never blank, even if empty.
func blankBox(b *Box) bool {
	switch b.Role {
	case RoleText:
		return strings.TrimSpace(b.Text) == ""
	case RoleBlock:
		if b.Tag != "" {
			return false
		}
		for _, k := range b.Children {
			if !blankBox(k) {
				return false
			}
		}
		return true
	}
	return false
}

// floatRun returns the index just past the last floated table of the maximal
// run starting at cs[i] (a float): whitespace-only text between the floats
// does not break the run; the scan stops at the first box that is neither a
// float nor blank.
func floatRun(cs []*Box, i int) int {
	last := i
	for j := i + 1; j < len(cs); j++ {
		switch {
		case cs[j].Tbl == "table" && floatSide(cs[j]) != "":
			last = j
		case blankBox(cs[j]):
			// a whitespace separator between floats: transparent to the run
		default:
			return last + 1
		}
	}
	return last + 1
}

// emitFloatBand lays a run of two or more floated sibling tables side by
// side, one grid strip (Row.Cells) per vertical line - a table grid row in
// which each float is a column. Each float is laid alone at the full width
// so an auto table shrinkwraps to its natural size; that used width becomes
// the column width (tableRows stamps W = U on every grid row). Left floats
// pack from the container's left edge in DOM order, right floats against its
// right edge; a band wider than the container overflows right contiguously
// (Stripo sizes float columns to fit - no wrap-around text in this model).
// stage 2 renders Row.Cells grids already, so the floats need no extra
// handling beyond being cells of one strip row.
func emitFloatBand(fs []*Box, x0, w int, s *seam, m Metrics, norm bool) []Row {
	type col struct {
		b    *Box
		rows []Row
		dx   int // absolute px left edge of this float's columns
	}
	var cols []col
	for _, f := range fs {
		var tmp seam // floats have no modeled vertical margins; drop any seam
		rs := tableRows(f, 0, w, &tmp, m, norm)
		for i := range rs {
			rs[i].Gap = 0 // one band strip is one pager line: flatten margins
		}
		if len(rs) > 0 {
			cols = append(cols, col{b: f, rows: rs})
		}
	}
	if len(cols) == 0 {
		return nil
	}
	lsum, rsum := 0, 0
	for i := range cols {
		wi := cols[i].rows[0].W
		if floatSide(cols[i].b) == "right" {
			rsum += wi
		} else {
			lsum += wi
		}
	}
	if lsum+rsum <= w {
		x := x0 // lefts, DOM order, from the container's left edge
		for i := range cols {
			if floatSide(cols[i].b) == "right" {
				continue
			}
			cols[i].dx = x
			x += cols[i].rows[0].W
		}
		x = x0 + w // rights, DOM order, packed against the right edge
		for i := len(cols) - 1; i >= 0; i-- {
			if floatSide(cols[i].b) != "right" {
				continue
			}
			x -= cols[i].rows[0].W
			cols[i].dx = x
		}
	} else { // the band cannot fit: lay every float contiguously (overflow right)
		x := x0
		for i := range cols {
			cols[i].dx = x
			x += cols[i].rows[0].W
		}
	}
	maxLines := 0
	for _, c := range cols {
		if len(c.rows) > maxLines {
			maxLines = len(c.rows)
		}
	}
	var out []Row
	for k := 0; k < maxLines; k++ {
		var frags []Row
		for _, c := range cols {
			if k < len(c.rows) {
				frags = append(frags, shiftRow(c.rows[k], c.dx))
			}
		}
		gap := 0
		if len(out) == 0 {
			gap = s.take() // the band's first line consumes the seam
		}
		out = append(out, Row{Gap: gap, X: x0, W: w, Box: cols[0].b, Cells: frags})
	}
	return out
}
