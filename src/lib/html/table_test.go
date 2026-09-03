// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import "testing"

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
