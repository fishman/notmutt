// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

// TestAttachRowTable pins the 4-column table (mutt's attach-menu
// shape): the type marker column (- I / - A) lines up, entry numbers
// right-align in their fixed column, names stay left-aligned, the
// mime info right-aligns to the row edge. Column widths never shift
// with content (R11 slot discipline); a long name truncates.
func TestAttachRowTable(t *testing.T) {
	w := 80
	body := attachRow("I", 1, "/tmp/body.txt", "[text/plain, quoted-printable, utf-8, 0.1K]", w)
	att := attachRow("A", 12, "notes.md", "[text/markdown, base64, 40K]", w)
	if !strings.HasPrefix(body, "- I ") || !strings.HasPrefix(att, "- A ") {
		t.Fatalf("the marker column must line up:\n%q\n%q", body, att)
	}
	// the number column (cells 4-7, attachNumW): right-aligned, so the
	// last digit of 1 and 12 sits at the same cell
	if body[4:8] != "   1" || att[4:8] != "  12" {
		t.Fatalf("numbers must right-align in the fixed column:\n%q\n%q", body, att)
	}
	// the name starts at cell 9 in both rows (prefix "- I " + the
	// number field)
	if !strings.HasPrefix(body[9:], "/tmp/body.txt") || !strings.HasPrefix(att[9:], "notes.md") {
		t.Fatalf("the name column must start at the same cell:\n%q\n%q", body, att)
	}
	// the mime info right-aligns: both rows end at the same width with
	// the closing bracket on the row edge
	if !strings.HasSuffix(body, "[text/plain, quoted-printable, utf-8, 0.1K]") ||
		!strings.HasSuffix(att, "[text/markdown, base64, 40K]") {
		t.Fatalf("the mime column must right-align to the row edge:\n%q\n%q", body, att)
	}
	if got := runewidth.StringWidth(body); got != w {
		t.Fatalf("row width = %d, want %d", got, w)
	}
	if got := runewidth.StringWidth(att); got != w {
		t.Fatalf("row width = %d, want %d", got, w)
	}
	// a name longer than its area truncates, the row keeps the width
	long := attachRow("A", 2, "a-name-that-exceeds-the-whole-row-width.txt", "[text/plain, base64, 1K]", 40)
	if got := runewidth.StringWidth(long); got != 40 {
		t.Fatalf("truncated row width = %d, want 40", got)
	}
}

func TestSizeStr(t *testing.T) {
	for n, want := range map[int64]string{
		0:         "0.0K",
		100:       "0.1K",
		40960:     "40K",
		1024:      "1K",
		1572864:   "1.5M",
		104857600: "100M",
	} {
		if got := sizeStr(n); got != want {
			t.Fatalf("sizeStr(%d) = %q, want %q", n, got, want)
		}
	}
}
