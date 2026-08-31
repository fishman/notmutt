// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"

	"notmutt/core"
)

// TestPagerSMIME pins the R10 verdict banner: a valid signature prepends a
// green (OK) line naming the signer; a failed verify prepends a red line with
// the reason; an unsigned message adds nothing.
func TestPagerSMIME(t *testing.T) {
	cases := []struct {
		name string
		sm   *core.SMIMEStatus
		want string
		ok   bool
	}{
		{"valid", &core.SMIMEStatus{Present: true, Valid: true, Signer: "alpha@example.com"}, "[S/MIME] valid signature from alpha@example.com", true},
		{"revoked", &core.SMIMEStatus{Present: true, Valid: true, Signer: "alpha@example.com", Checked: true, Revoked: true}, "[S/MIME] valid signature from alpha@example.com (revoked)", true},
		{"error", &core.SMIMEStatus{Present: true, Err: "no roots"}, "[S/MIME] could not verify: no roots", false},
	}
	for _, c := range cases {
		p := newPager("", "", []core.Line{{Text: "body"}})
		p.setSMIME(c.sm)
		first := p.lines[0]
		if first.Text != c.want {
			t.Errorf("%s: banner = %q, want %q", c.name, first.Text, c.want)
		}
		if first.Kind != core.LineSecurity || first.OK != c.ok {
			t.Errorf("%s: banner kind=%v ok=%v, want LineSecurity ok=%v", c.name, first.Kind, first.OK, c.ok)
		}
	}
	p := newPager("", "", []core.Line{{Text: "body"}})
	p.setSMIME(nil)
	if len(p.lines) != 1 || p.lines[0].Text != "body" {
		t.Fatal("unsigned message must add no banner")
	}
}

// TestAppendTextWraps pins the AI summary stream's wrap (the one
// exception to the truncate-never-wrap rule): a streamed delta wraps to
// the window width, a newline keeps a blank line, and a partial delta
// extends the last open line in place. The invariant that matters is
// layout stability: the same text arriving whole or token-fragmented
// lands on the SAME rows - a chunk boundary must never add or drop a
// word or a separator. (pagerText cannot round-trip wrapped text: the
// inter-row separator space is legitimately consumed by the wrap.)
func TestAppendTextWraps(t *testing.T) {
	const width = 12
	want := []string{"The quick", "brown fox", "jumps over", "the lazy dog"}
	assertRows := func(p *pager, msg string) {
		t.Helper()
		got := make([]string, len(p.lines))
		for i, l := range p.lines {
			got[i] = l.Text
			if w := runewidth.StringWidth(l.Text); w > width {
				t.Errorf("%s: line %d %q exceeds width %d", msg, i, l.Text, width)
			}
		}
		if len(got) != len(want) {
			t.Fatalf("%s: rows = %q, want %q", msg, got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("%s: rows = %q, want %q", msg, got, want)
			}
		}
	}
	// one streamed delta
	p := &pager{width: width}
	p.appendText("The quick brown fox jumps over the lazy dog")
	assertRows(p, "single delta")
	// fragmented across deltas - the trailing space on the open line must
	// survive the wrap or the next word concatenates to the last
	p = &pager{width: width}
	for _, d := range []string{"The quick ", "brown fox ", "jumps over the lazy ", "dog"} {
		p.appendText(d)
	}
	assertRows(p, "fragmented")
	// a complete line closes and a blank line survives
	p = &pager{width: width}
	p.appendText("para one\n\npara two\n")
	if got, want := pagerText(p), "para one\n\npara two\n"; got != want {
		t.Fatalf("newlines: pagerText = %q, want %q", got, want)
	}
	// an overlong word hard-breaks
	p = &pager{width: 5}
	p.appendText("supercalifragilistic")
	for i, l := range p.lines {
		if w := runewidth.StringWidth(l.Text); w > 5 {
			t.Errorf("hard-break line %d %q exceeds width 5", i, l.Text)
		}
	}
	if got, want := pagerText(p), "supercalifragilistic"; got != want {
		t.Fatalf("hard-break reassembly = %q, want %q", got, want)
	}
	// width <= 0 never wraps
	p = &pager{width: 0}
	p.appendText("one long line that stays put")
	if len(p.lines) != 1 {
		t.Fatalf("width 0 must not wrap, got %d lines", len(p.lines))
	}
}

// TestAppendTextNewlineClosesOpen pins the streamed-newline garble (the
// AI summary line-split bug): the splitter may deliver a partial line,
// then a delta whose newline should CLOSE that open line. The text
// before the newline must land on the open line, never a fresh row - a
// dangling open line absorbs the next delta's text ("yet" + "**Next"
// -> "yet**Next", the reported symptom).
func TestAppendTextNewlineClosesOpen(t *testing.T) {
	p := &pager{width: 40}
	p.appendText("No replies; no action taken yet")
	p.appendText(".\n\n**Next steps:**")
	first := strings.TrimSuffix(p.lines[0].Text, "\n")
	if !strings.HasSuffix(first, "yet.") {
		t.Fatalf("the newline must close the open line, got %q", first)
	}
	for _, l := range p.lines {
		if l.Text == ".\n" {
			t.Fatal("the period must close the open line, not become its own row")
		}
	}
}
