// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package table

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

// qCols are the CRM queue's column shape exercised generically: an elastic
// name, company, title, a fixed date, and a free-form status tail.
var qCols = []Col{{Floor: 12, Cap: 24}, {Floor: 8, Cap: 20}, {Floor: 8, Cap: 18}, {Floor: 10, Cap: 10}}

func TestSizesFillRow(t *testing.T) {
	cases := []struct {
		sep   string
		width int
		want  []int
	}{
		{"|", 60, []int{24, 14, 8, 10}},  // surplus: name to its cap, then company
		{"|", 42, []int{12, 8, 8, 10}},   // exactly the floors
		{"|", 30, []int{12, 4, 0, 10}},   // under the floors: flexible columns give back, date holds
		{"│", 60, []int{24, 14, 8, 10}},  // a 1-cell UTF-8 separator budgets identically
		{" │ ", 60, []int{22, 8, 8, 10}}, // a 3-cell padded separator budgets 12 cells of gutters
	}
	for _, c := range cases {
		l := Layout{Cols: qCols, Sep: c.sep}
		got := l.Sizes(c.width, true)
		if len(got) != len(c.want) {
			t.Fatalf("Sizes(%d, %q) = %d columns, want %d", c.width, c.sep, len(got), len(c.want))
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("Sizes(%d, %q)[%d] = %d, want %d (all %v)", c.width, c.sep, i, got[i], c.want[i], got)
			}
		}
	}
}

// cellAt is a cell text's column offset (runewidth of the byte prefix), so a
// multi-byte separator never confuses the seam positions.
func cellAt(s, sub string) int {
	i := strings.Index(s, sub)
	if i < 0 {
		return -1
	}
	return runewidth.StringWidth(s[:i])
}

// TestLineColumnEdges pins that every row lays its cells into the same fixed
// columns: seam positions and total width never shift with cell content, so
// differing rows (and a blank company) align, and no cell overruns its column.
func TestLineColumnEdges(t *testing.T) {
	l := Layout{Cols: qCols, Sep: "│"}
	sizes := l.Sizes(60, true)
	rows := []struct {
		cells              []string
		wantDate, wantTail int
	}{
		{[]string{"Alpha Able", "Acme", "CEO", "2026-09-01", "new"}, 49, 60},
		{[]string{"Beta", "Globex Industries GmbH", "VP Sales", "2026-09-02", "briefing"}, 49, 60},
		{[]string{"Carol", "", "", "2026-09-03"}, 49, -1}, // blank company/title still reserve the gutter
	}
	for _, r := range rows {
		line := l.Line(r.cells, sizes)
		if strings.Contains(line, "|") {
			t.Errorf("Line(%v) carries an ASCII pipe: %q", r.cells, line)
		}
		if d := cellAt(line, "2026-09-0"); d != r.wantDate {
			t.Errorf("date at offset %d, want %d: %q", d, r.wantDate, line)
		}
		if r.wantTail >= 0 {
			if i := cellAt(line, r.cells[len(r.cells)-1]); i != r.wantTail {
				t.Errorf("status at offset %d, want %d: %q", i, r.wantTail, line)
			}
		}
	}

	// no tail: the bounded block keeps the column sum + gutters wide whatever
	// the cells hold, so a huge name or company never shifts the edges
	bounded := 0
	for _, s := range sizes {
		bounded += s
	}
	bounded += runewidth.StringWidth("│") * (len(sizes) - 1)
	a := l.Line([]string{"Alpha Able", "Acme", "CEO", "2026-09-01"}, sizes)
	b := l.Line([]string{strings.Repeat("X", 200), strings.Repeat("Y", 200), strings.Repeat("Z", 200), "2026-09-01"}, sizes)
	if cellAt(a, "2026-09-01") != 49 || cellAt(b, "2026-09-01") != 49 {
		t.Errorf("date drifted across rows: %q vs %q", a, b)
	}
	if runewidth.StringWidth(a) != bounded || runewidth.StringWidth(b) != bounded {
		t.Errorf("bounded widths %d and %d, want %d for both", runewidth.StringWidth(a), runewidth.StringWidth(b), bounded)
	}
}

// TestLinePaddedGutter pins the padded-separator shape the CRM queue uses (a
// theme glyph framed by a space either side): the seam is 3 cells, the glyph
// never touches the cells it separates, and the date/status edges still hold
// across tailed and tail-less rows.
func TestLinePaddedGutter(t *testing.T) {
	l := Layout{Cols: qCols, Sep: " │ "}
	sizes := l.Sizes(90, true)
	if sizes[0] != 24 || sizes[1] != 20 || sizes[2] != 18 || sizes[3] != 10 {
		t.Fatalf("Sizes(90) = %v", sizes)
	}
	full := l.Line([]string{"Alpha Able", "Acme", "CEO", "2026-09-01", "new"}, sizes)
	short := l.Line([]string{"Alpha Able", "Acme", "CEO", "2026-09-01"}, sizes)
	if got := strings.Count(full, " │ "); got != 4 { // three bounded seams + the tail seam
		t.Errorf("row has %d padded gutters, want 4: %q", got, full)
	}
	if strings.Contains(full, "A│") || strings.Contains(full, "│n") || strings.Contains(full, "1│") {
		t.Errorf("glyph sits flush against a cell: %q", full)
	}
	for _, row := range []string{full, short} {
		if d := cellAt(row, "2026-09-01"); d != 71 {
			t.Errorf("date at offset %d, want 71: %q", d, row)
		}
	}
	if i := cellAt(full, "new"); i != 84 {
		t.Errorf("status at offset %d, want 84: %q", i, full)
	}
}
