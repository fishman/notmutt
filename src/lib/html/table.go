// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import "math"

// Table grid layout (weasyprint auto-table-layout analog). A table box's
// children are row-group boxes - x/net/html's implied tbody gives every row
// a group, so no builder repair is needed (Task 1a). Layout walks them row
// by row, measures each cell's content once into per-column min/max,
// distributes the used width, then emits the grid rows into the px Row
// stream. A grid row is one horizontal strip: its stream Row carries Cells
// fragments, one per cell content line, at absolute X. The grid readers skip
// non-conforming children (a row-group box that is not a row, a row box that
// is not a cell): author CSS that demotes a real table tag out of the family
// leaves such a box, and layout must skip it, never assume - there is no
// anonymous repair to wrap it. A demoted table tag (display:block) is
// RoleBlock and flows its table-family descendants as plain blocks - the grid
// case keys on the RoleTable "table" slot, which only a real, un-demoted
// table carries.

const (
	tableSpacing = 2 // UA table border-spacing, px (probe-measured; html5_ua.css)
	tablePad     = 1 // UA td/th padding, px each side (html5_ua.css)
)

// gridCell is one placed cell in a grid row.
type gridCell struct {
	box     *Box
	start   int // first grid column the cell occupies
	colspan int // clamped to the row's columns (1 until Task 3)
}

type gridRow struct {
	cells []gridCell
}

// spanOf reads a cell's colspan/rowspan attributes. Task-2 boundary: the
// values are parsed and stored, but buildGrid/columnWidths ignore spans
// until Task 3.
func spanOf(cell *Box) (colspan, rowspan int) {
	colspan, rowspan = 1, 1
	if cell.Node != nil {
		if v := Attr(cell.Node, "colspan"); v != "" {
			if n := mustInt(v); n > 1 {
				colspan = n
			}
		}
		if v := Attr(cell.Node, "rowspan"); v != "" {
			if n := mustInt(v); n > 1 {
				rowspan = n
			}
		}
	}
	return
}

// runExtents measures one inline run's content px at infinite width: min is
// the widest unbreakable piece (a word); max is the whole run laid on one
// line (words plus their separators). A br splits the run: segments stack
// vertically, so each segment is measured alone and the run's extents are
// the max over segments.
func runExtents(as []atom, m Metrics) (minW, maxW int) {
	smin, smax := 0, 0
	flush := func() {
		if smin > minW {
			minW = smin
		}
		if smax > maxW {
			maxW = smax
		}
		smin, smax = 0, 0
	}
	for _, a := range as {
		if a.br {
			flush()
			continue
		}
		w := a.width(m)
		smax += w
		if a.sep {
			continue // a separator is a break point, not an unbreakable piece
		}
		if w > smin {
			smin = w
		}
	}
	flush()
	return minW, maxW
}

// cellExtents measures one cell's min and max column-box width (content +
// both paddings). Task-2/3 boundary: cell content is the uniform-inline
// case; block-in-cell (divs, nested tables) lands in Task 4.
func cellExtents(cell *Box, m Metrics) (minW, maxW int) {
	minW, maxW = runExtents(flattenInline(cell.Children), m)
	return minW + 2*tablePad, maxW + 2*tablePad
}

// buildGrid places every cell of a table's row-group children into grid
// rows, left to right, and returns the grid plus its column count. Column
// count is the widest row by cell count; a colspan is clamped to the row's
// remaining columns (Task 3 honors it). Rows that end up with no cell are
// dropped (an empty row emits no content line). Task-2 boundary: every cell
// occupies one column.
func buildGrid(t *Box) (rows []gridRow, cols int) {
	for _, g := range t.Children {
		if g.Tbl != "row-group" {
			continue
		}
		for _, rb := range g.Children {
			if rb.Tbl != "row" {
				continue
			}
			n := 0
			for _, cb := range rb.Children {
				if cb.Tbl == "cell" {
					n++
				}
			}
			if n > cols {
				cols = n
			}
		}
	}
	for _, g := range t.Children {
		if g.Tbl != "row-group" {
			continue
		}
		for _, rb := range g.Children {
			if rb.Tbl != "row" {
				continue
			}
			var gr gridRow
			cur := 0
			for _, cb := range rb.Children {
				if cb.Tbl != "cell" {
					continue
				}
				gr.cells = append(gr.cells, gridCell{box: cb, start: cur, colspan: 1})
				cur++
			}
			if len(gr.cells) > 0 {
				rows = append(rows, gr)
			}
		}
	}
	return rows, cols
}

// assignColumns resolves used column box widths (px) from measured column
// min/max at the available content width. auto-table-layout clamp
// (weasyprint auto_table_layout): U = tableMax when available >= tableMax
// (shrinkwrap), U = available when tableMin < available < tableMax (fill),
// U = tableMin when available <= tableMin in author mode; under normalize a
// tight available width caps U at the container so columns squeeze below
// min-content and cell text char-breaks. The width left for columns
// themselves is dist = U - spacing*(cols+1); each column lands between its
// min and max proportionally to the (max-min) gap (CSS tables-3), all min
// below colMin, all max above colMax. Rounding is pushed onto the last
// column, so the widths always sum exactly to dist.
func assignColumns(min, max []int, cols, avail int, norm bool) (U int, colX, colW []int) {
	colMin, colMax := 0, 0
	for j := 0; j < cols; j++ {
		colMin += min[j]
		colMax += max[j]
	}
	switch {
	case avail >= colMax+tableSpacing*(cols+1):
		U = colMax + tableSpacing*(cols+1)
	case avail <= colMin+tableSpacing*(cols+1) && !norm:
		U = colMin + tableSpacing*(cols+1)
	default:
		U = avail
	}
	dist := U - tableSpacing*(cols+1)
	if dist < 0 {
		dist = 0
	}
	colW = make([]int, cols)
	switch {
	case dist <= colMin:
		if colMin > 0 { // squeeze at/below min-content (normalize char-break)
			for j := 0; j < cols; j++ {
				colW[j] = min[j] * dist / colMin
			}
		}
	case dist >= colMax:
		copy(colW, max)
	default:
		sum := 0
		for j := 0; j < cols-1; j++ {
			colW[j] = int(math.Round(float64(min[j]) + float64(max[j]-min[j])*float64(dist-colMin)/float64(colMax-colMin)))
			sum += colW[j]
		}
		colW[cols-1] = dist - sum // the last column absorbs rounding
		if colW[cols-1] < 0 {
			colW[cols-1] = 0
		}
	}
	colX = make([]int, cols)
	cur := tableSpacing
	for j := 0; j < cols; j++ {
		colX[j] = cur
		cur += colW[j] + tableSpacing
	}
	return U, colX, colW
}

// columnWidths measures a grid's single-span columns (Task-2 boundary) and
// resolves their widths. Task 3 replaces the measurement with
// measureColumns and keeps this call shape.
func columnWidths(rows []gridRow, cols, avail int, norm bool, m Metrics) (U int, colX, colW []int) {
	min := make([]int, cols)
	max := make([]int, cols)
	for _, gr := range rows {
		for _, c := range gr.cells {
			cmin, cmax := cellExtents(c.box, m)
			if cmin > min[c.start] {
				min[c.start] = cmin
			}
			if cmax > max[c.start] {
				max[c.start] = cmax
			}
		}
	}
	return assignColumns(min, max, cols, avail, norm)
}

// cellRows lays out a cell's uniform-inline content at its content width,
// returning one Row per filled line, X relative to the cell content box's
// left edge (0).
func cellRows(cell *Box, w int, m Metrics, norm bool) []Row {
	var rs []Row
	for _, line := range LayoutInline(cell, w, m, norm) {
		rs = append(rs, Row{X: line.X, W: w, Box: cell, Line: line})
	}
	return rs
}

// tableRows emits a table box into the row stream at content (x, w),
// consuming the ambient seam on its first emitted line. Task-2 boundary:
// cells occupy one column each; cell content is uniform-inline.
func tableRows(t *Box, x, w int, s *seam, m Metrics, norm bool) []Row {
	rows, cols := buildGrid(t)
	if len(rows) == 0 || cols == 0 {
		return nil
	}
	U, colX, colW := columnWidths(rows, cols, w, norm, m)
	var out []Row
	for _, gr := range rows {
		type laid struct {
			cell gridCell
			rs   []Row
		}
		var all []laid
		maxLines := 0
		for _, c := range gr.cells {
			boxW := colW[c.start] // single span: one column (Task 3 sums spans)
			contentW := boxW - 2*tablePad
			if contentW < 0 {
				contentW = 0
			}
			contentX := x + colX[c.start] + tablePad
			ls := cellRows(c.box, contentW, m, norm)
			for i := range ls {
				ls[i].W = contentW
				ls[i] = shiftRow(ls[i], contentX)
			}
			all = append(all, laid{c, ls})
			if len(ls) > maxLines {
				maxLines = len(ls)
			}
		}
		if maxLines == 0 {
			continue // every cell empty: the grid row emits no content line
		}
		for k := 0; k < maxLines; k++ {
			var frags []Row
			for _, l := range all {
				if k < len(l.rs) {
					frags = append(frags, l.rs[k])
				}
			}
			gap := 0
			if len(out) == 0 {
				gap = s.take() // the table's first emitted line consumes the seam
			}
			out = append(out, Row{Gap: gap, X: x, W: U, Box: t, Cells: frags})
		}
	}
	return out
}

// shiftRow translates a row and everything nested in its Cells by dx px: a
// cell fragment that hosts a nested table row carries that table's column
// fragments, and they all share the same horizontal coordinate space.
func shiftRow(r Row, dx int) Row {
	r.X += dx
	for i := range r.Cells {
		r.Cells[i] = shiftRow(r.Cells[i], dx)
	}
	return r
}
