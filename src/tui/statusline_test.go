package tui

import (
	"strings"
	"testing"

	"notmutt/config"
	"notmutt/core"
)

func TestStatusLineSegments(t *testing.T) {
	ui := config.Default().UI
	row := statusLine(DefaultStyles(), ui, statusData{view: "inbox", visible: 5})
	if !strings.Contains(row, "inbox") || !strings.Contains(row, "5") {
		t.Fatalf("segments must render: %q", row)
	}
	if !strings.Contains(row, ui.Glyphs.StatuslineSeparator) {
		t.Fatalf("segments must join with the separator: %q", row)
	}
}

// TestStatusLineLegend pins the icon library: the selected message's
// tags with icons render as "icon name" pairs in row order; unmapped
// tags are skipped; account tags are skipped (they own the account
// segment); icons off renders nothing.
func TestStatusLineLegend(t *testing.T) {
	ui := config.Default().UI
	accounts := config.Default().AccountTags()
	legend := iconLegend([]string{"inbox", "work", "unmapped", "gmail", "archive"}, ui.Tags, accounts)
	if !strings.Contains(legend, ui.Tags.Icons["inbox"]+" inbox") {
		t.Fatalf("legend must pair icon and name: %q", legend)
	}
	if !strings.Contains(legend, ui.Tags.Icons["work"]+" work") {
		t.Fatalf("soft tags must appear: %q", legend)
	}
	if strings.Contains(legend, "unmapped") {
		t.Fatalf("tags without icons must be skipped: %q", legend)
	}
	if strings.Contains(legend, "gmail") {
		t.Fatalf("account tags must not leak into the legend: %q", legend)
	}
	// row order preserved: work before archive
	if strings.Index(legend, "work") > strings.Index(legend, "archive") {
		t.Fatalf("row tag order must survive: %q", legend)
	}
	row := statusLine(DefaultStyles(), ui, statusData{view: "inbox", visible: 2, legend: legend})
	if !strings.Contains(row, ui.Tags.Icons["inbox"]) {
		t.Fatalf("legend must render in the status row: %q", row)
	}
	ui.Tags.ShowIcons = false
	if iconLegend([]string{"inbox"}, ui.Tags, nil) != "" {
		t.Fatal("no legend when icons are off")
	}
}

// TestStatusLineAccount pins the account segment: the cursor message's
// account tag renders in the status row and in no other segment - the
// account surface is the status bar, not the mail title.
func TestStatusLineAccount(t *testing.T) {
	ui := config.Default().UI
	d := statusData{view: "inbox", visible: 2, account: "gmail"}
	row := statusLine(DefaultStyles(), ui, d)
	if !strings.Contains(row, "gmail") {
		t.Fatalf("account must render in the status row: %q", row)
	}
	if accountTag([]string{"inbox", "gmail", "work"}, config.Default().AccountTags()) != "gmail" {
		t.Fatal("accountTag must find the account tag in the tag list")
	}
	if accountTag([]string{"inbox", "work"}, config.Default().AccountTags()) != "" {
		t.Fatal("no account tag must resolve empty")
	}
	empty := statusLine(DefaultStyles(), ui, statusData{view: "inbox", visible: 2})
	if strings.Contains(empty, "gmail") {
		t.Fatalf("no account, no segment: %q", empty)
	}
}

// TestStatusLinePowerlineGlyphs pins the powerline model (tmux2k
// onedark reference): the left-group chevron is the default seam, the
// right group has its own mirrored glyph, and replacing the separator
// in config turns the powerline look off.
func TestStatusLinePowerlineGlyphs(t *testing.T) {
	ui := config.Default().UI
	if ui.Glyphs.StatuslineSeparator != "" || ui.Glyphs.StatuslineSeparatorR != "" {
		t.Fatalf("powerline chevrons must be the defaults: %q %q",
			ui.Glyphs.StatuslineSeparator, ui.Glyphs.StatuslineSeparatorR)
	}
	row := statusLine(DefaultStyles(), ui, statusData{view: "inbox", visible: 5})
	if !strings.Contains(row, "") {
		t.Fatalf("the left-group chevron must join segments: %q", row)
	}
	ui.Glyphs.StatuslineSeparator = " "
	plain := statusLine(DefaultStyles(), ui, statusData{view: "inbox", visible: 5})
	if strings.Contains(plain, "") {
		t.Fatalf("the separator config must replace the chevron: %q", plain)
	}
}

func TestStatusLineDropsLowPriorityOnNarrow(t *testing.T) {
	ui := config.Default().UI
	// non-default fill glyph: proves the bar consumes config data
	ui.Glyphs.ProgressFill = "="
	d := statusData{view: "inbox", visible: 5, prog: &core.Progress{Done: 1, Total: 5}, on: true}
	full := statusLine(DefaultStyles(), ui, d)
	if !strings.Contains(full, "=") {
		t.Fatalf("progress fill glyph must render at full width: %q", full)
	}
	narrow := statusLineWidth(DefaultStyles(), ui, d, 8)
	if strings.Contains(narrow, "=") {
		t.Fatalf("progress must drop first on a narrow terminal: %q", narrow)
	}
	if !strings.Contains(narrow, "inbox") {
		t.Fatalf("the view name must survive: %q", narrow)
	}
}
