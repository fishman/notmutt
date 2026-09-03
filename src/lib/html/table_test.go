// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestTableStrayWhitespaceDrops(t *testing.T) {
	bs := buildBody("<table>\n  <tr><td>a</td></tr>\n  </table>")
	tbl := bs[0]
	if len(tbl.Children) != 1 || tbl.Children[0].Tbl != "row-group" {
		t.Fatalf("pretty-printed whitespace must vanish; children = %+v", tbl.Children)
	}
}

func TestTableFosteredRunLandsBeforeTable(t *testing.T) {
	// <table>stray<tr>... - x/net/html foster-parents the non-whitespace run
	// out of the table (before it in the body); the builder must not invent an
	// anonymous cell to hold it.
	// Top-level text before a block table wraps in an anonymous run
	// (splitRuns), exactly as text before any other block does.
	bs := buildBody(`<table>stray<tr><td>a</td></tr></table>`)
	if len(bs) != 2 || bs[0].Role != RoleBlock || len(bs[0].Children) != 1 ||
		bs[0].Children[0].Role != RoleText || bs[0].Children[0].Text != "stray" {
		t.Fatalf("body = %+v, want the stray run (anon block) then the table", bs)
	}
	tbl := bs[1]
	if tbl.Role != RoleTable || len(tbl.Children) != 1 || tbl.Children[0].Tag != "tbody" {
		t.Fatalf("table = %+v, want one implied tbody row-group", tbl.Children)
	}
}

func TestAuthorDisplayTableOnDivRendersBlock(t *testing.T) {
	// display:table on a non-table element is not a grid: x/net/html cannot
	// nest it (parser keys off element names) and mail never uses it, so the
	// div renders as block (walker parity) and its children as ordinary flow.
	bs := buildBody(`<div style="display:table"><em style="display:table-cell">a</em></div>`)
	if len(bs) != 1 || bs[0].Role != RoleBlock || bs[0].Tag != "div" {
		t.Fatalf("display:table div = %+v, want RoleBlock (not RoleTable)", bs[0])
	}
}

func TestTheadTbodyFootAreRowGroups(t *testing.T) {
	bs := buildBody(`<table><thead><tr><td>h</td></tr></thead><tbody><tr><td>b</td></tr></tbody></table>`)
	tbl := bs[0]
	if len(tbl.Children) != 2 || tbl.Children[0].Tag != "thead" || tbl.Children[1].Tag != "tbody" {
		t.Fatalf("children = %+v, want thead then tbody as row-groups", tbl.Children)
	}
	if tbl.Children[0].Tbl != "row-group" || tbl.Children[1].Tbl != "row-group" {
		t.Fatalf("thead/tbody Tbl = %q/%q, want row-group", tbl.Children[0].Tbl, tbl.Children[1].Tbl)
	}
}

func TestThBoldNotCentered(t *testing.T) {
	bs := buildBody(`<table><tr><th>h</th><td>d</td></tr></table>`)
	row := bs[0].Children[0].Children[0]
	th, td := row.Children[0], row.Children[1]
	if !th.St.Bold {
		t.Fatal("th must be UA-bold")
	}
	if th.Children[0].St.Bold != true {
		t.Fatal("th text leaf must inherit the bold style")
	}
	if td.St.Bold {
		t.Fatal("td must not be bold")
	}
	if th.St.Align != "" {
		t.Fatalf("th Align = %q, want '' (not centered)", th.St.Align)
	}
}

func TestThAuthorFontWeightBeatsUA(t *testing.T) {
	bs := buildBody(`<table><tr><th style="font-weight:normal">h</th><th>i</th></tr></table>`)
	row := bs[0].Children[0].Children[0]
	if row.Children[0].St.Bold {
		t.Fatal("th with font-weight:normal must not be bold")
	}
	if !row.Children[1].St.Bold {
		t.Fatal("bare th stays UA-bold")
	}
}

func TestCellContentBlockifies(t *testing.T) {
	// a td holding a div carries block content, not a flat inline run
	bs := buildBody(`<table><tr><td><div>a</div></td></tr></table>`)
	row := bs[0].Children[0].Children[0]
	cell := row.Children[0]
	if len(cell.Children) != 1 || cell.Children[0].Role != RoleBlock || cell.Children[0].Tag != "div" {
		t.Fatalf("cell children = %+v, want the div block", cell.Children)
	}
}

func TestCaptionStoredNotGrid(t *testing.T) {
	bs := buildBody(`<table><caption>cap</caption><tr><td>a</td></tr></table>`)
	tbl := bs[0]
	if len(tbl.Children) != 2 || tbl.Children[0].Tbl != "caption" {
		t.Fatalf("children = %+v, want caption then anon row-group", tbl.Children)
	}
	if len(tbl.Children[0].Children) != 1 || tbl.Children[0].Children[0].Text != "cap" {
		t.Fatalf("caption content = %+v, want the built text", tbl.Children[0].Children)
	}
}

// fragText renders one fragment row's text: a cell content line, recursing
// into Cells for a nested table's grid row.
func fragText(r Row) string {
	if len(r.Cells) > 0 {
		var b strings.Builder
		for _, c := range r.Cells {
			b.WriteString(fragText(c))
		}
		return b.String()
	}
	var b strings.Builder
	for _, a := range r.Line.Atoms {
		b.WriteString(a.Text)
	}
	return b.String()
}

func TestTableTwoCellsShrinkwrap(t *testing.T) {
	rs := LayoutBlock(buildBody(`<table><tr><td>a</td><td>b</td></tr></table>`), 20, mono(1), false)
	if len(rs) != 1 {
		t.Fatalf("rows = %d, want 1", len(rs))
	}
	r := rs[0]
	if r.X != 0 || r.W != 12 {
		t.Fatalf("table row X/W = %d/%d, want 0/12 (shrunk to max-content, not 20)", r.X, r.W)
	}
	if len(r.Cells) != 2 {
		t.Fatalf("cells = %d, want 2", len(r.Cells))
	}
	// columns 3px wide each (1px content + 1px padding each side); col0 box at
	// x2 (2px leading border-spacing), content at x3; col1 box at x7, content
	// at x8; the 2px gutter sits between the boxes.
	for i, want := range []struct {
		x int
		s string
	}{{3, "a"}, {8, "b"}} {
		c := r.Cells[i]
		if c.X != want.x || fragText(c) != want.s {
			t.Fatalf("cell %d = X%d %q, want X%d %q", i, c.X, fragText(c), want.x, want.s)
		}
	}
}

func TestTableFillsBetweenMinAndMax(t *testing.T) {
	// col0 "aaa bbb": content min 3, max 7 -> column [5, 9].
	// col1 "cc dd ee ff gg": content min 2, max 14 -> column [4, 16].
	// tableMin = 5+4+6 = 15, tableMax = 9+16+6 = 31. Available 23 is between:
	// the table fills the available width and columns interpolate at ratio 1/2
	// -> col0 = 5+2 = 7, col1 = 4+6 = 10 (content widths 5 and 8).
	bs := buildBody(`<table><tr><td>aaa bbb</td><td>cc dd ee ff gg</td></tr></table>`)
	rs := LayoutBlock(bs, 23, mono(1), false)
	if len(rs) != 2 {
		t.Fatalf("rows = %d, want 2 (both cells wrap to two lines)", len(rs))
	}
	if rs[0].W != 23 || rs[1].W != 23 {
		t.Fatalf("row W = %d/%d, want 23/23", rs[0].W, rs[1].W)
	}
	for i, want := range []string{"aaa", "cc dd ee"} {
		if len(rs[0].Cells) != 2 || fragText(rs[0].Cells[i]) != want {
			t.Fatalf("line0 cell %d = %q, want %q", i, fragText(rs[0].Cells[i]), want)
		}
	}
	if rs[0].Cells[0].X != 3 || rs[0].Cells[1].X != 12 {
		t.Fatalf("line0 cell X = %d/%d, want 3/12", rs[0].Cells[0].X, rs[0].Cells[1].X)
	}
	if fragText(rs[1].Cells[0]) != "bbb" || rs[1].Cells[0].X != 3 {
		t.Fatalf("line1 col0 = X%d %q, want X3 bbb", rs[1].Cells[0].X, fragText(rs[1].Cells[0]))
	}
	if fragText(rs[1].Cells[1]) != "ff gg" || rs[1].Cells[1].X != 12 {
		t.Fatalf("line1 col1 = X%d %q, want X12 'ff gg'", rs[1].Cells[1].X, fragText(rs[1].Cells[1]))
	}
}

func TestTableStaysMinWhenContainerTight(t *testing.T) {
	// Available 8 is below tableMin 15 (col0 [5,9] for "aaa bbb", col1 [4,4]
	// for "cc"): author mode takes min-content, so the columns hold their
	// min widths (5/4) and the table overflows the tight 8px container.
	bs := buildBody(`<table><tr><td>aaa bbb</td><td>cc</td></tr></table>`)
	rs := LayoutBlock(bs, 8, mono(1), false)
	if rs[0].W != 15 {
		t.Fatalf("table W = %d, want 15 (min-content, overflowing the tight container)", rs[0].W)
	}
}

func TestTableDemotedToBlockFlowsCellsAsBlocks(t *testing.T) {
	// display:block on a real <table> demotes the box to RoleBlock, so its
	// tbody/tr/td descendants have no RoleTable "table" root above them and
	// never form a grid: flow renders each cell as a stacked block line
	// (content stays visible), never a Cells fragment row.
	bs := buildBody(`<table style="display:block"><tr><td>a</td><td>b</td></tr></table>`)
	rs := LayoutBlock(bs, 80, mono(1), false)
	if got := rowsText(rs); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("rows = %q, want [a b] (demoted table flows as blocks)", got)
	}
	for i, r := range rs {
		if len(r.Cells) != 0 {
			t.Fatalf("row %d carries Cells fragments, want plain block lines", i)
		}
	}
}

func TestColspanDistributesOverColumns(t *testing.T) {
	// base row gives col0 (aaa -> content 3, box 5) and col1 (bbb -> box 5).
	// The colspan-2 cell's text is 16px + 2px padding = 18 > base max sum
	// (5+5+2 spacing = 12): excess 6 distributes to the two equal base
	// columns, +3 each -> columns 8/8. tableMin = tableMax = 8+8+6 = 22.
	bs := buildBody(`<table><tr><td>aaa</td><td>bbb</td></tr>` +
		`<tr><td colspan="2">abcdefghijklmnop</td></tr></table>`)
	rs := LayoutBlock(bs, 40, mono(1), false)
	if len(rs) != 2 {
		t.Fatalf("rows = %d, want 2", len(rs))
	}
	if rs[0].W != 22 {
		t.Fatalf("table W = %d, want 22 (shrinkwrapped max-content, not the 40 container)", rs[0].W)
	}
	// row0: aaa at x3, bbb at x13 (col1 box x12, content +1)
	if fragText(rs[0].Cells[0]) != "aaa" || rs[0].Cells[0].X != 3 {
		t.Fatalf("row0 col0 = X%d %q, want X3 aaa", rs[0].Cells[0].X, fragText(rs[0].Cells[0]))
	}
	if fragText(rs[0].Cells[1]) != "bbb" || rs[0].Cells[1].X != 13 {
		t.Fatalf("row0 col1 = X%d %q, want X13 bbb", rs[0].Cells[1].X, fragText(rs[0].Cells[1]))
	}
	// row1: the spanning cell's box is 8 + 2 spacing + 8 = 18 wide; its text
	// starts at the first column's content x3 and fills all 16px.
	if len(rs[1].Cells) != 1 || fragText(rs[1].Cells[0]) != "abcdefghijklmnop" || rs[1].Cells[0].X != 3 {
		t.Fatalf("row1 = X%d %q, want X3 spanning text", rs[1].Cells[0].X, fragText(rs[1].Cells[0]))
	}
	if rs[1].Cells[0].W != 16 {
		t.Fatalf("spanning content W = %d, want 16", rs[1].Cells[0].W)
	}
}

func TestRowspanOccupiesLaterRowColumn(t *testing.T) {
	// col0's cell rowspans 2: it renders in row0 and leaves row1's col0
	// blank. col1 has b (row0) and c (row1). All columns are 3px.
	bs := buildBody(`<table><tr><td rowspan="2">a</td><td>b</td></tr>` +
		`<tr><td>c</td></tr></table>`)
	rs := LayoutBlock(bs, 20, mono(1), false)
	if len(rs) != 2 {
		t.Fatalf("rows = %d, want 2", len(rs))
	}
	if fragText(rs[0].Cells[0]) != "a" || rs[0].Cells[0].X != 3 {
		t.Fatalf("row0 col0 = X%d %q, want X3 a", rs[0].Cells[0].X, fragText(rs[0].Cells[0]))
	}
	if fragText(rs[0].Cells[1]) != "b" || rs[0].Cells[1].X != 8 {
		t.Fatalf("row0 col1 = X%d %q, want X8 b", rs[0].Cells[1].X, fragText(rs[0].Cells[1]))
	}
	if len(rs[1].Cells) != 1 || fragText(rs[1].Cells[0]) != "c" || rs[1].Cells[0].X != 8 {
		t.Fatalf("row1 = %d cells X%d %q, want only c at X8 (rowspan blanks col0)", len(rs[1].Cells), rs[1].Cells[0].X, fragText(rs[1].Cells[0]))
	}
}

func TestRowspanSkipsBusyColumnInWiderRow(t *testing.T) {
	// row0: a (rowspan 2) plus b and c -> grid is 3 wide. row1: a rowspan
	// cell at col0 spans down into row1, so row1's first cell steps to col1
	// while col0 stays blank. This pins the busy-column skip, not just the
	// simple two-column case.
	bs := buildBody(`<table><tr><td rowspan="2">a</td><td>b</td><td>c</td></tr>` +
		`<tr><td>d</td><td>e</td></tr></table>`)
	rs := LayoutBlock(bs, 30, mono(1), false)
	if len(rs) != 2 {
		t.Fatalf("rows = %d, want 2", len(rs))
	}
	if len(rs[1].Cells) != 2 {
		t.Fatalf("row1 cells = %d, want 2 (d at col1, e at col2; col0 blank)", len(rs[1].Cells))
	}
	if fragText(rs[1].Cells[0]) != "d" || rs[1].Cells[0].X != 8 {
		t.Fatalf("row1 cell0 = X%d %q, want X8 d (stepped past the rowspanned col0)", rs[1].Cells[0].X, fragText(rs[1].Cells[0]))
	}
}

func TestRowspanBusyDoesNotScaleWithEmptyRows(t *testing.T) {
	// One row of 20000 rowspan cells above 60000 empty rows is O(C x R) under
	// the per-row tick of the first Task 3 cut (~17 s at ~1 MB, BUGS.org-1
	// class). Expiry-by-row-index makes empty rows O(1); this finishes in
	// milliseconds on the fixed code and a reintroduced tick hangs.
	var b strings.Builder
	b.WriteString(`<table><tr>`)
	for i := 0; i < 20000; i++ {
		b.WriteString(`<td rowspan="99999">x</td>`)
	}
	b.WriteString(`</tr>`)
	for i := 0; i < 60000; i++ {
		b.WriteString(`<tr></tr>`)
	}
	b.WriteString(`</table>`)
	bs := buildBody(b.String())
	tbl := bs[0]
	done := make(chan []gridRow, 1)
	go func() {
		rows, _ := buildGrid(tbl)
		done <- rows
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("buildGrid over 60000 empty rows did not finish in 3s (per-row busy tick)")
	}
}

func TestRowspanExpiresAcrossEmptyRow(t *testing.T) {
	// a has rowspan 2 at row0; the empty row1 consumes one row of the claim,
	// so at row2 col0 is free and c lands there (start 0), not at col1.
	bs := buildBody(`<table><tr><td rowspan="2">a</td><td>b</td></tr><tr></tr><tr><td>c</td></tr></table>`)
	rows, _ := buildGrid(bs[0])
	if len(rows) != 2 || len(rows[0].cells) != 2 || len(rows[1].cells) != 1 {
		t.Fatalf("grid = %+v, want row0 (a,b) then row2 (c)", rows)
	}
	if rows[0].cells[0].start != 0 || rows[0].cells[1].start != 1 {
		t.Fatalf("row0 starts = %d/%d, want 0/1", rows[0].cells[0].start, rows[0].cells[1].start)
	}
	c := rows[1].cells[0]
	if c.start != 0 {
		t.Fatalf("row2 cell start = %d, want 0 (rowspan 2 expired across the empty row)", c.start)
	}
	if len(c.box.Children) != 1 || c.box.Children[0].Text != "c" {
		t.Fatalf("row2 cell = %+v, want the 'c' text cell", c.box.Children)
	}
}

func TestNestedTableIndentsInsideCell(t *testing.T) {
	// Outer: one cell holding a nested 2-column table. Nested col0 text is
	// 10px -> column box 12; col1 text is 20px -> column box 22; nested table
	// is 12+22+2*3 = 40px wide. Outer cell content is that nested table, so
	// the outer column is 40 + 2px padding = 42 and the outer table is 42 +
	// 2*2 = 46px (measured against weasyprint: nested table left edge = outer
	// spacing 2 + outer pad 1 = x3; inner col0 box x5, content x6).
	bs := buildBody(`<table><tr><td><table>` +
		`<tr><td>aaaaaaaaaa</td><td>bbbbbbbbbbbbbbbbbbbb</td></tr>` +
		`</table></td></tr></table>`)
	rs := LayoutBlock(bs, 100, mono(1), false)
	if len(rs) != 1 {
		t.Fatalf("rows = %d, want 1", len(rs))
	}
	if rs[0].W != 46 {
		t.Fatalf("outer table W = %d, want 46", rs[0].W)
	}
	if got := fragText(rs[0]); got != "aaaaaaaaaabbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("nested text = %q (len %d), want 10 a's then 20 b's", got, len(got))
	}
	outer := rs[0].Cells[0]
	if len(outer.Cells) != 2 {
		t.Fatalf("outer cell fragments = %d, want the nested row's 2 columns", len(outer.Cells))
	}
	if outer.Cells[0].X != 6 || outer.Cells[1].X != 20 {
		t.Fatalf("nested fragment X = %d/%d, want 6/20 (nested col0 content x6, col1 x20)", outer.Cells[0].X, outer.Cells[1].X)
	}
}

func TestTextThenNestedTableStackInCell(t *testing.T) {
	// A cell holding text and then a nested table blockifies into two block
	// children: the text run renders on its own line, then the nested table
	// lines follow - two stream rows total (both children single-line).
	bs := buildBody(`<table><tr><td>hi<table><tr><td>x</td></tr></table></td></tr></table>`)
	rs := LayoutBlock(bs, 60, mono(1), false)
	if got := rowsText(rs); !reflect.DeepEqual(got, []string{"hi", "x"}) {
		t.Fatalf("rows = %q, want [hi x]", got)
	}
}

func TestCellListMarkersTranslateWithCell(t *testing.T) {
	// A list inside a td is a block child, so its li row is laid out at the
	// cell content origin (0): the li text line and its disc marker both sit
	// at X 40 (the ul's 40px padding gutter). tableRows then shifts the row
	// by the cell content X (3), so the marker must translate with the row:
	// the emitted fragment lands at X 43 and its marker must too.
	bs := buildBody(`<table><tr><td><ul><li>item</li></ul></td></tr></table>`)
	rs := LayoutBlock(bs, 60, mono(1), false)
	if len(rs) != 1 || len(rs[0].Cells) != 1 {
		t.Fatalf("rows = %d cells = %d, want 1 row with 1 cell fragment", len(rs), len(rs[0].Cells))
	}
	frag := rs[0].Cells[0]
	if len(frag.Markers) != 1 || frag.Markers[0].Type != "disc" {
		t.Fatalf("fragment markers = %+v, want one disc", frag.Markers)
	}
	if frag.X != 43 {
		t.Fatalf("fragment X = %d, want 43 (li row shifted by the cell content X)", frag.X)
	}
	if frag.Markers[0].X != frag.X {
		t.Fatalf("marker X = %d, want %d (marker translates with the cell content)", frag.Markers[0].X, frag.X)
	}
}

func TestCellTableListMarkerTranslatesWithRow(t *testing.T) {
	// A text-less li whose content is a nested table hangs its disc marker on
	// the table's first grid row (which carries Cells fragments). Inside a td
	// that row is shifted by the cell content X (3), and the marker on it must
	// translate too - it sits on the row, not in the Cells recursion.
	bs := buildBody(`<table><tr><td><ul><li><table><tr><td>x</td></tr></table></li></ul></td></tr></table>`)
	rs := LayoutBlock(bs, 60, mono(1), false)
	if len(rs) != 1 || len(rs[0].Cells) != 1 {
		t.Fatalf("rows = %d cells = %d, want 1 row with 1 cell fragment", len(rs), len(rs[0].Cells))
	}
	row := rs[0].Cells[0]
	if len(row.Cells) != 1 {
		t.Fatalf("row cells = %d, want the nested table's 1 column", len(row.Cells))
	}
	if len(row.Markers) != 1 || row.Markers[0].Type != "disc" {
		t.Fatalf("row markers = %+v, want one disc on the nested grid row", row.Markers)
	}
	if row.X != 43 {
		t.Fatalf("row X = %d, want 43 (nested grid row shifted by the cell content X)", row.X)
	}
	if row.Markers[0].X != row.X {
		t.Fatalf("marker X = %d, want %d (marker on a Cells row translates with it)", row.Markers[0].X, row.X)
	}
}
