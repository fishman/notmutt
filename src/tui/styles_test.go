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
	out := renderRow(1, row, DefaultStyles(), config.Default().UI, 1, 0, false, config.Default().AccountTags())
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
	return renderRow(1, row, DefaultStyles(), config.Default().UI, 1, 0, false, config.Default().AccountTags())
}

// TestRowSelectedMonochrome pins the cursor row look (R11): the indicator
// style replaces every slot style, so the highlighted row carries one
// highlight background and one text color - no per-slot colors survive.
func TestRowSelectedMonochrome(t *testing.T) {
	row := core.Row{Msg: &core.Message{
		ID: "m1", ThreadID: "t1", Timestamp: 1755150000,
		Author: "Ann", Subject: "hello", Tags: []string{"inbox"},
	}}
	out := renderRow(1, row, DefaultStyles(), config.Default().UI, 1, 0, true, config.Default().AccountTags())
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
	out := stripANSI(renderRow(1, row, DefaultStyles(), ui, 1, 5, false, config.Default().AccountTags()))
	if !strings.HasPrefix(out, "1    A ") {
		t.Fatalf("attachment marker must fall back to text when icons are off: %q", out)
	}
	// tag names render in full next to the sender, never truncated to a
	// glyph-slot width; the tag slot pads the row without tags to the
	// page width, so the subject column holds its place
	if !strings.Contains(out, "inbox") || strings.Contains(out, "📥") || strings.Index(out, "inbox") > strings.Index(out, "hello") {
		t.Fatalf("tag names must render in full before the subject when icons are off: %q", out)
	}
}

// TestRowFlagSlot pins the row-start marker slots: replied owns a flag
// letter (R, mutt's index flags) and never repeats in the tag slot;
// signed renders the lock icon in its own slot right after the
// attachment slot, and the slot reserves its cells on rows without it.
func TestRowFlagSlot(t *testing.T) {
	if got := flagChars([]string{"unread", "replied", "signed", "forwarded", "deleted"}); got != "NRFD" {
		t.Fatalf("flag letters: %q", got)
	}
	ui := config.Default().UI
	st := DefaultStyles()
	acc := config.Default().AccountTags()
	row := core.Row{Msg: &core.Message{
		ID: "m1", ThreadID: "t1", Timestamp: 1755150000,
		Author: "Ann", Subject: "hello", Tags: []string{"replied", "signed", "work"},
	}}
	out := stripANSI(renderRow(1, row, st, ui, 1, 4, false, acc))
	if !strings.HasPrefix(out, "1 R ") {
		t.Fatalf("flags slot must show replied: %q", out)
	}
	if !strings.Contains(out, "🔒") || strings.Index(out, "🔒") > strings.Index(out, "25/08/14") {
		t.Fatalf("signed lock must sit at the row start before the date: %q", out)
	}
	if strings.Contains(out, "replied") || strings.Contains(out, "signed") {
		t.Fatalf("marker tags must not repeat in the tag slot: %q", out)
	}
	// the signed slot reserves its cells: a row without the tag keeps
	// the date column in place
	plain := stripANSI(renderRow(1, core.Row{Msg: &core.Message{
		ID: "m2", ThreadID: "t1", Timestamp: 1755150000,
		Author: "Ann", Subject: "hello", Tags: []string{"work"},
	}}, st, ui, 1, 4, false, acc))
	col := func(s string) int { return runewidth.StringWidth(s[:strings.Index(s, "25/08/14")]) }
	if col(out) != col(plain) {
		t.Fatalf("the signed slot must reserve width:\n%q\n%q", out, plain)
	}
}

// TestRowTagIcon pins the icons dict (ui.tags.icons): a mapped tag renders
// its icon instead of its name, and the attachment marker tag renders only
// in the row-start attachment slot - never repeated in the tag slot.
func TestRowTagIcon(t *testing.T) {
	ui := config.Default().UI
	ui.Tags.Icons = map[string]string{"attachment": "x", "inbox": "y", "newsletter": "z"}
	row := core.Row{Msg: &core.Message{
		ID: "m1", ThreadID: "t1", Timestamp: 1755150000,
		Author: "Ann", Subject: "hello", Tags: []string{"attachment", "inbox"},
	}}
	out := stripANSI(renderRow(1, row, DefaultStyles(), ui, 1, 1, false, config.Default().AccountTags()))
	// number + blank flags slot precede the attachment slot; the icon must
	// sit before the date column, not in the tag slot
	if !strings.HasPrefix(out, "1    x ") {
		t.Fatalf("attachment icon must open the row (row-start slot): %q", out)
	}
	if strings.Contains(out, "attachment") {
		t.Fatalf("attachment tag must not repeat in the tag slot: %q", out)
	}
	// the mapped icon sits in the tag slot right after the sender and
	// before the subject
	if !strings.Contains(out, "y") || strings.Index(out, "y") > strings.Index(out, "hello") {
		t.Fatalf("mapped tag icon missing from the tag slot: %q", out)
	}
	// two mapped icons: natural width, one separator space - neither
	// icons nor names carry fixed padding (a padded cell would leave
	// gaps between glyphs)
	row.Msg.Tags = []string{"inbox", "newsletter"}
	glyphs := stripANSI(renderRow(1, row, DefaultStyles(), ui, 1, 3, false, config.Default().AccountTags()))
	if !strings.Contains(glyphs, "y z") || strings.Contains(glyphs, "y  z") {
		t.Fatalf("icons must join with a single space: %q", glyphs)
	}
	// the attachment slot reserves 2 cells: a double-width icon (the
	// paperclip) must not shift the date column vs a row without one
	plain := stripANSI(renderRow(1, core.Row{Msg: &core.Message{
		ID: "m2", ThreadID: "t1", Timestamp: 1755150000,
		Author: "Ann", Subject: "hello", Tags: []string{"inbox"},
	}}, DefaultStyles(), ui, 1, 3, false, config.Default().AccountTags()))
	if strings.Index(out, "25/08/14") != strings.Index(plain, "25/08/14") {
		t.Fatalf("attachment icon shifted the date column:\n%q\n%q", out, plain)
	}
}

// TestRowTagSlotAlignsPage pins the per-page tag slot (the number-slot
// pattern): the widest tag run on the page sets the slot width; rows
// without tags reserve it, so the subject column aligns across the
// page. The width recomputes per render - the next page re-aligns.
func TestRowTagSlotAlignsPage(t *testing.T) {
	ui := config.Default().UI
	ui.Tags.ShowIcons = false // names only: "inbox work" is 10 cells wide
	st := DefaultStyles()
	acc := config.Default().AccountTags()
	wide := renderRow(1, core.Row{Msg: &core.Message{
		ID: "m1", ThreadID: "t1", Timestamp: 1755150000,
		Author: "Ann", Subject: "hello", Tags: []string{"inbox", "work"},
	}}, st, ui, 1, 10, false, acc)
	none := renderRow(2, core.Row{Msg: &core.Message{
		ID: "m2", ThreadID: "t1", Timestamp: 1755150000,
		Author: "Ann", Subject: "hello",
	}}, st, ui, 1, 10, false, acc)
	if si := strings.Index(stripANSI(wide), "hello"); si != strings.Index(stripANSI(none), "hello") {
		t.Fatalf("the subject column must align on the page:\n%q\n%q", wide, none)
	}
	// a wider page widens the slot: a 13-cell run re-aligns the next page
	wide13 := renderRow(1, core.Row{Msg: &core.Message{
		ID: "m3", ThreadID: "t1", Timestamp: 1755150000,
		Author: "Ann", Subject: "hello", Tags: []string{"inbox", "newsletter"},
	}}, st, ui, 1, 13, false, acc)
	if !strings.Contains(wide13, "hello") {
		t.Fatalf("the wider run must render in full: %q", wide13)
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
