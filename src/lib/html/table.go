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
	colspan int // clamped to the free columns remaining in the row
}

type gridRow struct {
	cells []gridCell
}

// spanOf reads a cell's colspan/rowspan attributes; values below 2 (and
// unparsable text) fall back to 1.
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
		if a.img != nil {
			// extent width, never the last layout's used px: a measure
			// pass must not read a % width resolved at a narrower avail
			w = imgExtentW(a.img)
		}
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

// flowExtents measures a vertical flow of boxes at infinite width: each
// child contributes its own min/max, the flow takes the max across children
// (blocks stack vertically, so the widest child governs the content width).
func flowExtents(cs []*Box, m Metrics) (minW, maxW int) {
	for _, c := range cs {
		cmin, cmax := boxExtents(c, m)
		if cmin > minW {
			minW = cmin
		}
		if cmax > maxW {
			maxW = cmax
		}
	}
	return minW, maxW
}

// boxExtents measures one box as a flow child: its content's min/max plus
// its horizontal insets (ml + pl on the left, mr on the right - the same
// insets flow applies). A nested table contributes its own border-box width
// (its margins/padding plus columns and spacing edges), so outer columns
// widen to seat it exactly as layout will.
func boxExtents(b *Box, m Metrics) (minW, maxW int) {
	_, mr, _, ml, pl := geom(b)
	if b.Tbl == "table" {
		tmin, tmax := tableExtents(b, m)
		return ml + pl + tmin + mr, ml + pl + tmax + mr
	}
	if b.Tbl == "cell" || len(b.Children) == 0 {
		return 0, 0 // stray cell/leaf contributes nothing as a flow child
	}
	inset := ml + mr + pl
	var cmin, cmax int
	if hasBlockChild(b.Children) {
		cmin, cmax = flowExtents(b.Children, m)
	} else {
		cmin, cmax = runExtents(flattenInline(b.Children), m)
	}
	return inset + cmin, inset + cmax
}

// tableExtents measures a nested table's min and max border-box width
// (column widths plus the surrounding border-spacing) at infinite width,
// memoized on the box (tblMin/tblMax/tblMeas). Content min/max are
// width-independent (measured at infinite width), so the first measure pass
// to reach a table computes its extents and every later ancestor measure and
// the table's own layout read the cache. Without the memo, each ancestor's
// measure and layout pass would re-descend the whole subtree below it -
// O(depth^2) buildGrid/atomize work on a deep chain of single-cell tables (a
// content-reachable DoS). measureColumns below the memo still runs per table
// when it lays out (assignColumns needs per-column arrays), but its
// cellExtents reads deeper tables' caches, so it costs only the table's own
// direct cell text - a constant, never a subtree.
func tableExtents(t *Box, m Metrics) (minW, maxW int) {
	if t.tblMeas {
		return t.tblMin, t.tblMax
	}
	rows, cols := buildGrid(t)
	if cols == 0 {
		t.tblMeas = true
		return 0, 0
	}
	min, max := measureColumns(rows, cols, m)
	sumMin, sumMax := tableSpacing*(cols+1), tableSpacing*(cols+1)
	for j := 0; j < cols; j++ {
		sumMin += min[j]
		sumMax += max[j]
	}
	t.tblMin, t.tblMax, t.tblMeas = sumMin, sumMax, true
	return sumMin, sumMax
}

// cellExtents measures one cell's min and max column-box width: content
// (inline runs or block children, recursively) plus both paddings.
func cellExtents(cell *Box, m Metrics) (minW, maxW int) {
	if hasBlockChild(cell.Children) {
		minW, maxW = flowExtents(cell.Children, m)
	} else {
		minW, maxW = runExtents(flattenInline(cell.Children), m)
	}
	return minW + 2*tablePad, maxW + 2*tablePad
}

// buildGrid places every cell of a table's row-group children into grid
// rows and returns the grid plus its column count. Column count is the
// widest row by cell count - a colspan never mints empty spacer columns
// beyond the cells that define them, so a 20-character colspan attribute
// cannot build a million-column grid. Cells are placed left to right,
// stepping past columns still claimed by a rowspan above; a colspan is
// clamped to the free columns remaining in the row. Rows that end up with
// no cell are dropped (they emit no content line), but rowspan claims
// still expire across them: every <tr> advances the row ordinal, empty or
// not. Claims hold an absolute expiry row and are consulted only when a
// row places cells, so an empty row costs O(1) - the per-row full-map tick
// of the first Task 3 cut was O(C x R) on a wide rowspan row above many
// empty rows (measured ~17 s at 1 MB, BUGS.org-1 class; Task 3a).
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
	busy := make(map[int]int) // grid col -> first row where its rowspan claim no longer blocks
	ri := 0                   // grid-row ordinal; every <tr> advances it (empty rows included)
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
				cs, rs := spanOf(cb)
				for cur < cols && busy[cur] > ri { // skip columns still claimed at this row
					cur++
				}
				if cur >= cols {
					break // only rowspan-claimed columns remain: drop the rest
				}
				if cs > cols-cur {
					cs = cols - cur
				}
				for {
					free := 0
					for cur+free < cols && free < cs && busy[cur+free] <= ri {
						free++
					}
					if free == cs {
						break
					}
					cur += free + 1 // a busy column blocks the span; retry after it
					for cur < cols && busy[cur] > ri {
						cur++
					}
					if cur >= cols {
						break
					}
				}
				if cur >= cols {
					break
				}
				gr.cells = append(gr.cells, gridCell{box: cb, start: cur, colspan: cs})
				if rs > 1 {
					busy[cur] = ri + rs // claims its column through row ri+rs-1 (this row included)
				}
				cur += cs
			}
			if len(gr.cells) > 0 {
				rows = append(rows, gr)
			}
			ri++ // one row passed: claims expire by row index, never by a map tick
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

// measureColumns accumulates each grid column's min/max column-box width
// from the cells that start in it. Single-span cells seed the base; a
// spanning cell's excess over the current sum of its columns is distributed
// proportionally to the columns' max widths (weasyprint preferred.py, which
// passes max_content_widths as the weights for both the min and max
// passes). Spans run in row-major order and see earlier distributions.
func measureColumns(rows []gridRow, cols int, m Metrics) (min, max []int) {
	min = make([]int, cols)
	max = make([]int, cols)
	distribute := func(arr []int, a, b, excess int, weight []int) {
		total := 0
		for j := a; j <= b; j++ {
			total += weight[j]
		}
		if total == 0 {
			share := excess / (b - a + 1)
			rem := excess % (b - a + 1)
			for j := a; j <= b; j++ {
				arr[j] += share
				if j-a < rem {
					arr[j]++
				}
			}
			return
		}
		used := 0
		for j := a; j < b; j++ {
			add := excess * weight[j] / total
			arr[j] += add
			used += add
		}
		arr[b] += excess - used
	}
	// pass 1: base from single-span cells
	for _, gr := range rows {
		for _, c := range gr.cells {
			if c.colspan != 1 {
				continue
			}
			cmin, cmax := cellExtents(c.box, m)
			if cmin > min[c.start] {
				min[c.start] = cmin
			}
			if cmax > max[c.start] {
				max[c.start] = cmax
			}
		}
	}
	// pass 2: spanning cells distribute their excess (row-major order)
	for _, gr := range rows {
		for _, c := range gr.cells {
			if c.colspan == 1 {
				continue
			}
			a, b := c.start, c.start+c.colspan-1
			if b >= cols {
				b = cols - 1
			}
			spanSum := func(arr []int) int {
				sum := tableSpacing * (b - a)
				for j := a; j <= b; j++ {
					sum += arr[j]
				}
				return sum
			}
			cmin, cmax := cellExtents(c.box, m)
			if ex := cmax - spanSum(max); ex > 0 {
				distribute(max, a, b, ex, max)
			}
			if ex := cmin - spanSum(min); ex > 0 {
				distribute(min, a, b, ex, max)
			}
		}
	}
	return min, max
}

// columnWidths measures a grid's columns and resolves their used widths at
// the available width.
func columnWidths(rows []gridRow, cols, avail int, norm bool, m Metrics) (U int, colX, colW []int) {
	min, max := measureColumns(rows, cols, m)
	return assignColumns(min, max, cols, avail, norm)
}

// cellRows lays out a cell's content at its content width and returns the
// cell's visual lines, X relative to the cell content box's left edge (0).
// Uniform-inline content fills lines directly; block content (divs, lists,
// a nested table) runs through the ordinary block engine, so a nested
// RoleTable recurses into tableRows. Intra-cell vertical margins are
// flattened (Gap 0): a grid row is one horizontal strip, so a paragraph
// gap inside one cell cannot push only that cell down.
func cellRows(cell *Box, w int, m Metrics, norm bool) []Row {
	if !hasBlockChild(cell.Children) {
		var rs []Row
		for _, line := range LayoutInline(cell, w, m, norm) {
			rs = append(rs, Row{X: line.X, W: w, Box: cell, Line: line})
		}
		return rs
	}
	rs := LayoutBlock(cell.Children, w, m, norm)
	for i := range rs {
		rs[i].Gap = 0
	}
	return rs
}

// tableRows emits a table box into the row stream at content (x, w),
// consuming the ambient seam on its first emitted line. A spanning cell's
// box sums its columns' widths plus the gutters between them. Cell content
// may be uniform-inline or block content (a nested table recurses here).
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
			boxW := 0
			for j := c.start; j < c.start+c.colspan && j < cols; j++ {
				boxW += colW[j]
				if j > c.start {
					boxW += tableSpacing
				}
			}
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
// fragments, and they all share the same horizontal coordinate space. A
// block child's list markers hang at cell-local X too, so they translate
// with their row.
func shiftRow(r Row, dx int) Row {
	r.X += dx
	for i := range r.Markers {
		r.Markers[i].X += dx
	}
	for i := range r.Cells {
		r.Cells[i] = shiftRow(r.Cells[i], dx)
	}
	return r
}
