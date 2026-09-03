// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import (
	"reflect"
	"strings"
	"testing"
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
		b.WriteString(a.text)
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
