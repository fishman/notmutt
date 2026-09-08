// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

// Float-column layout (Stripo es-left/es-right tables): sibling tables that
// float (CSS float:left/right or the legacy align=left/right attribute) lay
// side by side in one horizontal band - lefts from the container's left edge,
// rights from its right edge - instead of stacking vertically. The band is a
// grid-style Row whose Cells hold one fragment per float column.

import (
	"strings"
	"testing"
)

// leaf returns the visible text fragments of a row's subtree with their
// absolute px X, descending table grids; text is blank-lead stripped.
func leaf(r Row, out *[]struct {
	t string
	x int
}) {
	if len(r.Cells) > 0 {
		for _, c := range r.Cells {
			leaf(c, out)
		}
		return
	}
	var b strings.Builder
	for _, a := range r.Line.Atoms {
		b.WriteString(a.Text)
	}
	if s := b.String(); strings.TrimSpace(s) != "" {
		*out = append(*out, struct {
			t string
			x int
		}{s, r.X})
	}
}

func TestFloatTablesBandSideBySide(t *testing.T) {
	body := "<table align=\"left\"><tr><td>L1</td></tr></table>\n" +
		"<table align=\"right\"><tr><td>R9</td></tr></table>"
	rs := LayoutBlock(buildBody(body), 30, mono(1), false)
	band := false
	var lx, rx int
	for _, r := range rs {
		var fs []struct {
			t string
			x int
		}
		leaf(r, &fs)
		hasL, hasR := false, false
		for _, f := range fs {
			if strings.Contains(f.t, "L1") {
				lx, hasL = f.x, true
			}
			if strings.Contains(f.t, "R9") {
				rx, hasR = f.x, true
			}
		}
		if hasL && hasR {
			band = true
		}
	}
	if !band {
		t.Fatalf("floated tables stacked vertically; want one grid row holding both L1 and R9:\n%v", rowsText(rs))
	}
	if rx <= lx {
		t.Fatalf("right float must sit right of left float: L1@%d R9@%d", lx, rx)
	}
}

func TestFloatTablesThreeColumns(t *testing.T) {
	body := "<table align=\"left\"><tr><td>L1</td></tr></table>\n" +
		"<table align=\"left\"><tr><td>L2</td></tr></table>\n" +
		"<table align=\"right\"><tr><td>R9</td></tr></table>"
	rs := LayoutBlock(buildBody(body), 40, mono(1), false)
	var lx, rx int
	found := false
	for _, r := range rs {
		var fs []struct {
			t string
			x int
		}
		leaf(r, &fs)
		hasL, hasR := false, false
		for _, f := range fs {
			if strings.Contains(f.t, "L1") {
				lx, hasL = f.x, true
			}
			if strings.Contains(f.t, "L2") && f.x > lx {
				lx = f.x // the second left column lands right of the first
			}
			if strings.Contains(f.t, "R9") {
				rx, hasR = f.x, true
			}
		}
		if hasL && hasR {
			found = true
		}
	}
	if !found {
		t.Fatalf("three floated tables did not share one grid row:\n%v", rowsText(rs))
	}
	if rx <= lx {
		t.Fatalf("right float not right of the left run: last-left@%d R9@%d", lx, rx)
	}
}

func TestFloatTablesCSSFloatTrigger(t *testing.T) {
	body := "<table align=\"left\"><tr><td>L1</td></tr></table>\n" +
		"<table style=\"float:right\"><tr><td>R9</td></tr></table>"
	rs := LayoutBlock(buildBody(body), 30, mono(1), false)
	band := false
	for _, r := range rs {
		var fs []struct {
			t string
			x int
		}
		leaf(r, &fs)
		hasL, hasR := false, false
		for _, f := range fs {
			if strings.Contains(f.t, "L1") {
				hasL = true
			}
			if strings.Contains(f.t, "R9") {
				hasR = true
			}
		}
		if hasL && hasR {
			band = true
		}
	}
	if !band {
		t.Fatalf("CSS float:right table did not band with the align=left sibling:\n%v", rowsText(rs))
	}
}

func TestLoneFloatAndNonTableFloatStayStacked(t *testing.T) {
	// one floated table among ordinary blocks keeps today's stacked path
	body := "<table align=\"left\"><tr><td>L1</td></tr></table>\n<div>tail</div>"
	rs := LayoutBlock(buildBody(body), 30, mono(1), false)
	lines := rowsText(rs)
	if len(lines) != 2 || strings.TrimSpace(lines[0]) != "L1" || strings.TrimSpace(lines[1]) != "tail" {
		t.Fatalf("lone float must stack below its block sibling, got:\n%v", lines)
	}
	// div floats are out of scope: they never band (no table grid)
	rs = LayoutBlock(buildBody("<div style=\"float:left\">a</div><div style=\"float:left\">b</div>"), 30, mono(1), false)
	if lines = rowsText(rs); len(lines) != 2 {
		t.Fatalf("div floats must stack as blocks, got:\n%v", lines)
	}
}
