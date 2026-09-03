// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import (
	"reflect"
	"strings"
	"testing"
)

// rowsText renders the non-hr rows' text for assertions. A table row's
// Cells fragments render in order (their texts concatenate; the stage-2
// gutter is horizontal blank columns, not text).
func rowsText(rs []Row) []string {
	var rowText func(Row) string
	rowText = func(r Row) string {
		if len(r.Cells) > 0 {
			var b strings.Builder
			for _, f := range r.Cells {
				b.WriteString(rowText(f))
			}
			return b.String()
		}
		var b strings.Builder
		for _, a := range r.Line.Atoms {
			b.WriteString(a.Text)
		}
		return b.String()
	}
	var out []string
	for _, r := range rs {
		if r.HR {
			continue
		}
		out = append(out, rowText(r))
	}
	return out
}

// gap asserts the px gap above each row (a faithful stage-1 value; stage 2
// quantizes and drops the leading one).
func gaps(rs []Row) []int {
	out := make([]int, len(rs))
	for i, r := range rs {
		out[i] = r.Gap
	}
	return out
}

func TestBlockSiblingParagraphsCollapse(t *testing.T) {
	bs := buildBody(`<p>one</p><p>two</p>`)
	rs := LayoutBlock(bs, 200, mono(1), false)
	if got := rowsText(rs); !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Fatalf("rows = %q", got)
	}
	// each p is 16px top+bottom; the shared boundary collapses to one 16px
	if got := gaps(rs); !reflect.DeepEqual(got, []int{16, 16}) {
		t.Fatalf("gaps = %v, want [16 16]", got)
	}
}

func TestBlockLargerNeighborMarginWins(t *testing.T) {
	bs := buildBody(`<p style="margin:20px 0 30px">one</p><p style="margin:10px 0">two</p>`)
	rs := LayoutBlock(bs, 200, mono(1), false)
	// boundary: p1 mb 30 vs p2 mt 10 -> max 30; row 1 is p1's own mt 20
	if got := gaps(rs); !reflect.DeepEqual(got, []int{20, 30}) {
		t.Fatalf("gaps = %v, want [20 30]", got)
	}
}

func TestBlockEmptyBoxCollapsesThrough(t *testing.T) {
	bs := buildBody(`<p>one</p><p></p><p>two</p>`)
	rs := LayoutBlock(bs, 200, mono(1), false)
	if got := rowsText(rs); !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Fatalf("rows = %q (empty p must leave no row)", got)
	}
	if got := gaps(rs); !reflect.DeepEqual(got, []int{16, 16}) {
		t.Fatalf("gaps = %v, want [16 16] (empty p collapses through)", got)
	}
}

func TestBlockHRuleKeepsRuleRow(t *testing.T) {
	bs := buildBody(`<p>one</p><hr><p>two</p>`)
	rs := LayoutBlock(bs, 200, mono(1), false)
	if len(rs) != 3 || !rs[1].HR {
		t.Fatalf("want one, HR, two; got %d rows", len(rs))
	}
	// hr's 8px margins collapse with the 16px paragraph margins: 16 either
	// side of the 2px rule (measured against weasyprint)
	if got := gaps(rs); !reflect.DeepEqual(got, []int{16, 16, 16}) {
		t.Fatalf("gaps = %v, want [16 16 16]", got)
	}
}

func TestBlockHRuleTightNeighborsKeepHalfEm(t *testing.T) {
	// divs carry no UA margins: hr keeps its own 8px each side
	bs := buildBody(`<div>one</div><hr><div>two</div>`)
	rs := LayoutBlock(bs, 200, mono(1), false)
	if got := gaps(rs); !reflect.DeepEqual(got, []int{0, 8, 8}) {
		t.Fatalf("gaps = %v, want [0 8 8]", got)
	}
}

func TestBlockListContentInGutterWithMarker(t *testing.T) {
	bs := buildBody(`<ul><li>one</li><li>two</li></ul>`)
	rs := LayoutBlock(bs, 200, mono(1), false)
	if got := rowsText(rs); !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Fatalf("rows = %q", got)
	}
	// list mt 16 above the first item; items are contiguous; content sits at
	// the ul's 40px padding-left; each item's first row carries its marker
	if got := gaps(rs); !reflect.DeepEqual(got, []int{16, 0}) {
		t.Fatalf("gaps = %v, want [16 0]", got)
	}
	for i, x := range []int{40, 40} {
		if rs[i].X != x {
			t.Fatalf("row %d X = %d, want %d", i, rs[i].X, x)
		}
	}
	for i, want := range []string{"disc", "disc"} {
		if len(rs[i].Markers) != 1 || rs[i].Markers[0].Type != want {
			t.Fatalf("row %d markers = %+v, want [%s]", i, rs[i].Markers, want)
		}
	}
}

func TestBlockNestedListIndentsAndMarks(t *testing.T) {
	bs := buildBody(`<ul><li>outer<ul><li>inner</li></ul></li></ul>`)
	rs := LayoutBlock(bs, 200, mono(1), false)
	if got := rowsText(rs); !reflect.DeepEqual(got, []string{"outer", "inner"}) {
		t.Fatalf("rows = %q", got)
	}
	// nested list has no vertical margins: inner is contiguous under outer
	if got := gaps(rs); !reflect.DeepEqual(got, []int{16, 0}) {
		t.Fatalf("gaps = %v, want [16 0]", got)
	}
	if rs[0].X != 40 || rs[1].X != 80 {
		t.Fatalf("content X = %d/%d, want 40/80", rs[0].X, rs[1].X)
	}
	if len(rs[0].Markers) != 1 || rs[0].Markers[0].Type != "disc" ||
		len(rs[1].Markers) != 1 || rs[1].Markers[0].Type != "circle" {
		t.Fatalf("markers = %+v / %+v, want [disc] / [circle]", rs[0].Markers, rs[1].Markers)
	}
}

func TestBlockBlockquoteInsetsContent(t *testing.T) {
	bs := buildBody(`<p>one</p><blockquote>two</blockquote><p>three</p>`)
	rs := LayoutBlock(bs, 200, mono(1), false)
	if got := rowsText(rs); !reflect.DeepEqual(got, []string{"one", "two", "three"}) {
		t.Fatalf("rows = %q", got)
	}
	// blockquote: 16px margins + 40px each side; its line wraps at 200-80
	if rs[1].X != 40 || rs[1].W != 120 {
		t.Fatalf("blockquote X/W = %d/%d, want 40/120", rs[1].X, rs[1].W)
	}
}

func TestBlockEmptyListItemGetsMarkerRow(t *testing.T) {
	// An empty <li> renders its marker on its own line (weasyprint probe):
	// it must emit a marker-only row, not vanish.
	bs := buildBody(`<ul><li></li><li>two</li></ul>`)
	rs := LayoutBlock(bs, 200, mono(1), false)
	if got := rowsText(rs); !reflect.DeepEqual(got, []string{"", "two"}) {
		t.Fatalf("rows = %q, want [\"\" \"two\"] (empty li keeps a marker row)", got)
	}
	if got := gaps(rs); !reflect.DeepEqual(got, []int{16, 0}) {
		t.Fatalf("gaps = %v, want [16 0]", got)
	}
	if len(rs) != 2 || rs[0].X != 40 || len(rs[0].Markers) != 1 || rs[0].Markers[0].Type != "disc" {
		t.Fatalf("row0 = X%d markers%v, want X40 [disc]", rs[0].X, rs[0].Markers)
	}
}

func TestBlockTextlessNestedLiHangsOuterInOwnGutter(t *testing.T) {
	// A text-less li whose content is a nested ul hangs BOTH markers on the
	// nested first line: the inner (circle) at the nested content edge x80 and
	// the outer (disc) at the outer li's own content edge x40 (weasyprint probe).
	bs := buildBody(`<ul><li><ul><li>inner</li></ul></li></ul>`)
	rs := LayoutBlock(bs, 200, mono(1), false)
	if got := rowsText(rs); !reflect.DeepEqual(got, []string{"inner"}) {
		t.Fatalf("rows = %q", got)
	}
	if len(rs) != 1 || rs[0].X != 80 {
		t.Fatalf("want one row at X80, got %d row(s) X%d", len(rs), rs[0].X)
	}
	var circle, disc *RowMarker
	for i := range rs[0].Markers {
		switch rs[0].Markers[i].Type {
		case "circle":
			circle = &rs[0].Markers[i]
		case "disc":
			disc = &rs[0].Markers[i]
		}
	}
	if circle == nil || circle.X != 80 {
		t.Fatalf("inner circle marker = %+v, want X80", circle)
	}
	if disc == nil || disc.X != 40 {
		t.Fatalf("outer disc marker = %+v, want X40", disc)
	}
}

func TestBlockOrderedListNumbersItems(t *testing.T) {
	bs := buildBody(`<ol><li>one</li><li>two</li></ol>`)
	rs := LayoutBlock(bs, 200, mono(1), false)
	if len(rs) != 2 {
		t.Fatalf("rows = %d, want 2", len(rs))
	}
	for i, ord := range []int{1, 2} {
		if len(rs[i].Markers) != 1 || rs[i].Markers[0].Type != "decimal" || rs[i].Markers[0].Ord != ord {
			t.Fatalf("row %d markers = %+v, want decimal Ord %d", i, rs[i].Markers, ord)
		}
	}
	// a second ol restarts at 1
	bs = buildBody(`<ol><li>a</li></ol><ol><li>b</li></ol>`)
	rs = LayoutBlock(bs, 200, mono(1), false)
	if len(rs) != 2 || rs[1].Markers[0].Ord != 1 {
		t.Fatalf("second ol must restart at 1: %+v", rs)
	}
}
