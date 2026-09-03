// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import (
	"reflect"
	"strings"
	"testing"
)

// rowsText renders the non-hr rows' text for assertions.
func rowsText(rs []Row) []string {
	var out []string
	for _, r := range rs {
		if r.HR {
			continue
		}
		var b strings.Builder
		for _, a := range r.Line.Atoms {
			b.WriteString(a.text)
		}
		out = append(out, b.String())
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
		if rs[i].Marker != want {
			t.Fatalf("row %d marker = %q, want %q", i, rs[i].Marker, want)
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
	if rs[0].Marker != "disc" || rs[1].Marker != "circle" {
		t.Fatalf("markers = %q/%q, want disc/circle", rs[0].Marker, rs[1].Marker)
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
