package tui

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"

	"notmutt/core"
)

func TestRowStyled(t *testing.T) {
	row := core.Row{Msg: &core.Message{
		ID: "m1", ThreadID: "t1", Timestamp: 1755150000,
		Author: "Ann", Subject: "hello", Tags: []string{"inbox"},
	}}
	out := renderRow(1, row, DefaultStyles())
	if !strings.Contains(out, "\x1b[38;2;97;175;239m") { // onedark author blue #61afef
		t.Fatalf("author slot must carry its style:\n%q", out)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("subject missing:\n%q", out)
	}
}

func TestPadRow(t *testing.T) {
	st := DefaultStyles()
	out := padRow("x", 5, st.Indicator)
	if runewidth.StringWidth(stripANSI(out)) != 5 {
		t.Fatalf("padRow must produce exactly 5 visible cells: %q", out)
	}
	// lipgloss merges fg+bg into one CSI; assert the indicator
	// background rgb (#e5c07b) is present on the padded line
	if !strings.Contains(out, "48;2;229;192;123") {
		t.Fatalf("outer style must color the padding: %q", out)
	}
}
