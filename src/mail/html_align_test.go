// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

// Alignment-signature tests (never mail content): an explicit text-align
// on a block sets that block's alignment and overrides anything inherited.
// Regression: the walker only honored explicit center/right, so a block
// declaring text-align: left under a centered ancestor rendered centered -
// the LinkedIn footer (declares left inside a centered container) shifted
// right instead of staying left.

import (
	"strings"
	"testing"

	"notmutt/core"
)

func TestHTMLExplicitLeftClearsInheritedCenter(t *testing.T) {
	lines := RenderHTML(`<div style="text-align:center"><div style="text-align:left">aa bb cc dd</div></div>`, nil, 0)
	if lead := leftPad(lines[0]); lead != 0 {
		t.Fatalf("explicit left must clear inherited center, lead=%d", lead)
	}
}

func TestHTMLExplicitLeftClearsInheritedRight(t *testing.T) {
	lines := RenderHTML(`<div style="text-align:right"><div style="text-align:left">aa bb cc dd</div></div>`, nil, 0)
	if lead := leftPad(lines[0]); lead != 0 {
		t.Fatalf("explicit left must clear inherited right, lead=%d", lead)
	}
}

func TestHTMLExplicitCenterStillPads(t *testing.T) {
	lines := RenderHTML(`<div style="text-align:center">aa bb cc dd</div>`, nil, 0)
	if lead := leftPad(lines[0]); lead == 0 {
		t.Fatalf("explicit center must still center the line")
	}
}

// TestHTMLNestedRightCellDoesNotLeak: a nested table whose last cell is
// right-aligned must not leak that one-shot split onto the cell content
// that follows the table - the LinkedIn footer's right spacer cell was
// right-shifting the unsubscribe block after it.
func TestHTMLNestedRightCellDoesNotLeak(t *testing.T) {
	lines := RenderHTML(`<table><tr><td>before <table><tr><td align="right">tail</td></tr></table> after</td></tr></table>`, nil, 0)
	tailLead, afterLead := -1, -1
	for _, l := range lines {
		lead := leftPad(l)
		switch {
		case strings.HasPrefix(strings.TrimSpace(l.Text), "tail"):
			tailLead = lead
		case strings.HasPrefix(strings.TrimSpace(l.Text), "after"):
			afterLead = lead
		}
	}
	if tailLead <= 0 {
		t.Fatalf("the right cell must stay right-aligned, lead=%d", tailLead)
	}
	if afterLead != 0 {
		t.Fatalf("content after a nested right table must not inherit its split, lead=%d", afterLead)
	}
}

// leftPad counts the leading cells (the render pads aligned lines with
// spaces; left-aligned content has none).
func leftPad(l core.Line) int {
	return len(l.Text) - len(strings.TrimLeft(l.Text, " "))
}
