// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import (
	"testing"

	xhtml "golang.org/x/net/html"
)

// rawTable builds a body whose single table element carries the given
// literal child nodes and returns the body's top-level boxes. The HTML5
// parser never hands a table raw tr/td/text children: it wraps rows and
// cells in an implied tbody>tr and foster-parents stray runs out of the
// table. The anonymous-repair tests below therefore construct the author's
// raw structure directly so buildElement/tableKids/fixTable actually see a
// stray row, cell, or run under a table.
func rawTable(children ...*xhtml.Node) []*Box {
	table := &xhtml.Node{Type: xhtml.ElementNode, Data: "table"}
	for _, c := range children {
		table.AppendChild(c)
	}
	body := &xhtml.Node{Type: xhtml.ElementNode, Data: "body"}
	body.AppendChild(table)
	html := &xhtml.Node{Type: xhtml.ElementNode, Data: "html"}
	html.AppendChild(body)
	doc := &xhtml.Node{Type: xhtml.DocumentNode}
	doc.AppendChild(html)
	return Build(doc, nil)
}

func tnode(tag string, children ...*xhtml.Node) *xhtml.Node {
	n := &xhtml.Node{Type: xhtml.ElementNode, Data: tag}
	for _, c := range children {
		n.AppendChild(c)
	}
	return n
}

func tnodeText(s string) *xhtml.Node {
	return &xhtml.Node{Type: xhtml.TextNode, Data: s}
}

func TestTableStrayWhitespaceDrops(t *testing.T) {
	bs := buildBody("<table>\n  <tr><td>a</td></tr>\n  </table>")
	tbl := bs[0]
	if len(tbl.Children) != 1 || tbl.Children[0].Tbl != "row-group" {
		t.Fatalf("pretty-printed whitespace must vanish; children = %+v", tbl.Children)
	}
}

func TestTableStrayTdGetsAnonymousRow(t *testing.T) {
	// <table><td>a</td></table>
	bs := rawTable(tnode("td", tnodeText("a")))
	tbl := bs[0]
	grp := tbl.Children[0]
	if len(tbl.Children) != 1 || grp.Tbl != "row-group" || grp.Tag != "" {
		t.Fatalf("stray td child = %+v, want one anonymous row-group", tbl.Children)
	}
	row := grp.Children[0]
	if row.Tbl != "row" || row.Tag != "" {
		t.Fatalf("row-group child = %+v, want an anonymous row", grp.Children)
	}
	cell := row.Children[0]
	if cell.Tbl != "cell" || cell.Tag != "td" {
		t.Fatalf("row child = %+v, want the td cell", row.Children)
	}
}

func TestTableStrayTrGetsAnonymousGroup(t *testing.T) {
	// <table><tr><td>a</td></tr></table>
	bs := rawTable(tnode("tr", tnode("td", tnodeText("a"))))
	tbl := bs[0]
	grp := tbl.Children[0]
	if len(tbl.Children) != 1 || grp.Tbl != "row-group" || grp.Tag != "" {
		t.Fatalf("stray tr child = %+v, want one anonymous row-group", tbl.Children)
	}
	if len(grp.Children) != 1 || grp.Children[0].Tag != "tr" {
		t.Fatalf("anonymous group child = %+v, want the real tr", grp.Children)
	}
}

func TestTableStrayRunGetsAnonymousCell(t *testing.T) {
	// <table>stray<tr><td>a</td></tr></table>
	bs := rawTable(tnodeText("stray"), tnode("tr", tnode("td", tnodeText("a"))))
	tbl := bs[0]
	if len(tbl.Children) != 2 {
		t.Fatalf("children = %d, want anon-cell group then the tr group", len(tbl.Children))
	}
	cell := tbl.Children[0].Children[0].Children[0]
	if cell.Tbl != "cell" || cell.Tag != "" || len(cell.Children) != 1 || cell.Children[0].Text != "stray" {
		t.Fatalf("stray run cell = %+v, want anonymous cell wrapping 'stray'", cell)
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
