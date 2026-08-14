package tui

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"

	"notmutt/config"
	"notmutt/core"
)

func TestResolveFromConfig(t *testing.T) {
	st := ResolveStyles(config.Theme{Default: "dark", Variants: map[string]config.StyleTable{
		"dark": {Status: config.Style{Fg: "base0A"}},
	}}, config.Palette{Base: map[string]string{"base00": "#21252b", "base05": "#abb2bf", "base0A": "#e5c07b"}})
	if !strings.Contains(st.Status.Render("x"), "38;2;229;192;123") {
		t.Fatalf("status fg must resolve through the base palette: %q", st.Status.Render("x"))
	}
}

func TestRowStyled(t *testing.T) {
	row := core.Row{Msg: &core.Message{
		ID: "m1", ThreadID: "t1", Timestamp: 1755150000,
		Author: "Ann", Subject: "hello", Tags: []string{"inbox"},
	}}
	out := renderRow(1, row, DefaultStyles(), config.Default().UI, 1, false, config.Default().AccountTags())
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

func styledRow() string {
	row := core.Row{Msg: &core.Message{
		ID: "m1", ThreadID: "t1", Timestamp: 1755150000,
		Author: "Ann", Subject: "hello", Tags: []string{"inbox"},
	}}
	return renderRow(1, row, DefaultStyles(), config.Default().UI, 1, false, config.Default().AccountTags())
}

// TestRowSelectedMonochrome pins the cursor row look (R11): the indicator
// style replaces every slot style, so the highlighted row carries one
// highlight background and one text color - no per-slot colors survive.
func TestRowSelectedMonochrome(t *testing.T) {
	row := core.Row{Msg: &core.Message{
		ID: "m1", ThreadID: "t1", Timestamp: 1755150000,
		Author: "Ann", Subject: "hello", Tags: []string{"inbox"},
	}}
	out := renderRow(1, row, DefaultStyles(), config.Default().UI, 1, true, config.Default().AccountTags())
	if !strings.Contains(out, "48;2;229;192;123") { // indicator bg #e5c07b
		t.Fatalf("indicator background missing from selected row: %q", out)
	}
	if strings.Contains(out, "38;2;97;175;239") { // author blue #61afef
		t.Fatalf("slot color survived on the selected row: %q", out)
	}
}

// TestRowTagIconDisabled pins show-icons = false: mapped tags render their
// names, and the attachment marker falls back to the plain text marker.
func TestRowTagIconDisabled(t *testing.T) {
	ui := config.Default().UI
	ui.Tags.ShowIcons = false
	row := core.Row{Msg: &core.Message{
		ID: "m1", ThreadID: "t1", Timestamp: 1755150000,
		Author: "Ann", Subject: "hello", Tags: []string{"attachment", "inbox"},
	}}
	out := stripANSI(renderRow(1, row, DefaultStyles(), ui, 1, false, config.Default().AccountTags()))
	if !strings.HasPrefix(out, "1    A ") {
		t.Fatalf("attachment marker must fall back to text when icons are off: %q", out)
	}
	// tag names render, not the icon; "inbox" is cut to "inbo" by the
	// 4-cell glyph slot (truncation, not the tag's name)
	if !strings.Contains(out, "inbo") || strings.Contains(out, "📥") {
		t.Fatalf("tag names must render when icons are off: %q", out)
	}
}

// TestRowTagIcon pins the icons dict (ui.tags.icons): a mapped tag renders
// its icon instead of its name, and the attachment marker tag renders only
// in the row-start attachment slot - never repeated in the tag slot.
func TestRowTagIcon(t *testing.T) {
	ui := config.Default().UI
	ui.Tags.Icons = map[string]string{"attachment": "x", "inbox": "y"}
	row := core.Row{Msg: &core.Message{
		ID: "m1", ThreadID: "t1", Timestamp: 1755150000,
		Author: "Ann", Subject: "hello", Tags: []string{"attachment", "inbox"},
	}}
	out := stripANSI(renderRow(1, row, DefaultStyles(), ui, 1, false, config.Default().AccountTags()))
	// number + blank flags slot precede the attachment slot; the icon must
	// sit before the date column, not in the trailing tag slot
	if !strings.HasPrefix(out, "1    x ") {
		t.Fatalf("attachment icon must open the row (row-start slot): %q", out)
	}
	if strings.Contains(out, "attachment") {
		t.Fatalf("attachment tag must not repeat in the tag slot: %q", out)
	}
	if !strings.Contains(out, "y") {
		t.Fatalf("mapped tag icon missing from the tag slot: %q", out)
	}
	// the attachment slot reserves 2 cells: a double-width icon (the
	// paperclip) must not shift the date column vs a row without one
	plain := stripANSI(renderRow(1, core.Row{Msg: &core.Message{
		ID: "m2", ThreadID: "t1", Timestamp: 1755150000,
		Author: "Ann", Subject: "hello", Tags: []string{"inbox"},
	}}, DefaultStyles(), ui, 1, false, config.Default().AccountTags()))
	if strings.Index(out, "25/08/14") != strings.Index(plain, "25/08/14") {
		t.Fatalf("attachment icon shifted the date column:\n%q\n%q", out, plain)
	}
}

// TestPadRowTruncates pins the common production path: rows render wider
// than the terminal (the fixed slots sum to ~90 cells on an 80-wide
// terminal), so padRow must cut to exactly w visible cells with the row
// style's background still reaching the trailing cells.
func TestPadRowTruncates(t *testing.T) {
	st := DefaultStyles()
	out := padRow(styledRow(), 40, st.Indicator)
	if w := runewidth.StringWidth(stripANSI(out)); w != 40 {
		t.Fatalf("padRow truncated to %d cells, want 40: %q", w, out)
	}
	// the trailing cells of the truncated row must carry the indicator
	// background, not the terminal default
	if !strings.Contains(out, "48;2;229;192;123") {
		t.Fatalf("indicator background missing from truncated row: %q", out)
	}
}

// TestPadRowTruncatesWellFormed pins a cut that lands right after a slot
// reset at 5 cells: a broken cut (partial SGR sequence on the wire)
// would corrupt the terminal state, and a dangling open sequence must
// never appear.
func TestPadRowTruncatesWellFormed(t *testing.T) {
	st := DefaultStyles()
	out := padRow(styledRow(), 5, st.Indicator)
	if w := runewidth.StringWidth(stripANSI(out)); w != 5 {
		t.Fatalf("visible width %d, want 5: %q", w, out)
	}
	for i := 0; i < len(out); i++ {
		if out[i] != '\x1b' {
			continue
		}
		if i+1 >= len(out) || out[i+1] != '[' {
			t.Fatalf("ESC not starting a CSI at %d: %q", i, out)
		}
		if j := strings.IndexByte(out[i+2:], 'm'); j < 0 {
			t.Fatalf("unterminated CSI at %d: %q", i, out)
		}
	}
}
