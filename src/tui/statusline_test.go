// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

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
	if !strings.Contains(row, " inbox ") || !strings.Contains(row, " 5 ") {
		t.Fatalf("segments must render as padded pills: %q", row)
	}
	if !strings.Contains(row, "152;195;121") || !strings.Contains(row, "229;192;123") {
		t.Fatalf("view and count must carry their own pill colors: %q", row)
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
// account tag renders in the status row as its own colored pill, set
// apart from the count by whitespace - never connected. The account
// surface is the status bar, not the mail title.
func TestStatusLineAccount(t *testing.T) {
	ui := config.Default().UI
	d := statusData{view: "inbox", visible: 2, account: "gmail"}
	row := statusLine(DefaultStyles(), ui, d)
	strip := stripANSI(row)
	if !strings.Contains(row, " gmail ") {
		t.Fatalf("account must render as a padded pill: %q", row)
	}
	if !strings.Contains(row, "97;175;239") {
		t.Fatalf("the account pill must carry its own background: %q", row)
	}
	ci, gi := strings.Index(strip, "2"), strings.Index(strip, "gmail")
	if ci < 0 || ci > gi {
		t.Fatalf("count must sit before the account: %q", strip)
	}
	if strings.Trim(strip[ci+1:gi], " ") != "" {
		t.Fatalf("the pills must be separated by whitespace only: %q", strip)
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

// TestStatusLineMessage pins the reserved right slot: the transient
// send status message renders rightmost in the row (the row never
// shifts with it), the error form carries the error style, and no
// message leaves the row exactly as before.
func TestStatusLineMessage(t *testing.T) {
	ui := config.Default().UI
	st := DefaultStyles()
	base := statusLine(st, ui, statusData{view: "inbox", visible: 2})
	row := statusLine(st, ui, statusData{view: "inbox", visible: 2, msg: "sent to a@b.c"})
	if !strings.HasSuffix(stripANSI(row), " sent to a@b.c ") {
		t.Fatalf("the message must be the rightmost pill: %q", row)
	}
	if !strings.Contains(strings.TrimSuffix(row, " sent to a@b.c "), " inbox ") {
		t.Fatalf("the left group must be untouched: %q", row)
	}
	rowErr := statusLine(st, ui, statusData{view: "inbox", visible: 2, msg: "send failed", msgErr: true})
	if !strings.Contains(rowErr, "224;108;117") {
		t.Fatalf("a failed send must carry the error foreground: %q", rowErr)
	}
	if got := statusLine(st, ui, statusData{view: "inbox", visible: 2, msg: "sent to a@b.c"}); got == base {
		t.Fatalf("the message must render: %q", got)
	}
	// truncation is pre-fitted: a message wider than the leftover drops
	// cells, never the view pill
	narrow := statusLineWidth(st, ui, statusData{view: "inbox", visible: 2, msg: "sent to a@b.c"}, 20)
	if !strings.Contains(narrow, "inbox") {
		t.Fatalf("the view must survive a narrow fit: %q", narrow)
	}
}
