// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"

	"notmutt/config"
	"notmutt/core"
)

// TestIndexTagRunNeedsIcon pins the index tag slot: the row is a fixed
// glyph strip, so with icons on only tags that HAVE an icon render - a
// tag with no icon belongs to the status bar's tag list, not the row
// (it would otherwise eat a slot cell with a name of unbounded width).
// Icons off renders names, the explicit name-mode.
func TestIndexTagRunNeedsIcon(t *testing.T) {
	ui := config.Default().UI
	row := core.Row{Msg: &core.Message{
		ID: "m1", ThreadID: "t1", Timestamp: 1755150000,
		Author: "Ann", Subject: "hello", Tags: []string{"inbox", "travel"},
	}}
	out := stripANSI(renderRow(1, row, DefaultStyles(), ui, 1, 12, false, config.Default().AccountTags(), "", core.MarkNone))
	if !strings.Contains(out, ui.Tags.Icons["inbox"]) {
		t.Fatalf("an icon-mapped tag must render: %q", out)
	}
	if ui.Tags.Icons["travel"] != "" {
		t.Fatal("fixture: travel must be an unmapped tag")
	}
	if strings.Contains(out, "travel") {
		t.Fatalf("a tag with no icon must not render in the index row: %q", out)
	}

	ui.Tags.ShowIcons = false
	out = stripANSI(renderRow(1, row, DefaultStyles(), ui, 1, 12, false, config.Default().AccountTags(), "", core.MarkNone))
	if !strings.Contains(out, "travel") {
		t.Fatalf("icons off renders tag names: %q", out)
	}
}
