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
