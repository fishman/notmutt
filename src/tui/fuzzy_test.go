// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import "testing"

// TestFuzzyAddressRanking pins the matcher's score ranking: query
// "Mila" must rank the exact display form "Mila <mila@stlab.test>"
// first, above every longer entry that shares the M-i-l-a subsequence
// (the old first-position tie-break ranked alphabetically and lost).
// Entries are fabricated - never real people's addresses.
func TestFuzzyAddressRanking(t *testing.T) {
	f := newFuzzy("address", "address:", []string{
		"Maria Lopez-Diaz <maria.lopez@design.test>",
		"Marielle de Santos <marielle.santos@mailco.test>",
		"MediaOutlet (c) Customer Care <info@shop482.mall.test>",
		"Mejia Rodriguez, Carlota <carlota.mejia@uniwest.test>",
		"Michał Mazurkiewicz <michal.mazurkiewicz@flylab.test>",
		"Mikołaj Mularczyk <mikolaj.mular@apptimia.test>",
		"Mila <mila@stlab.test>",
	})
	f.query = "Mila"
	idx := f.filtered()
	if len(idx) != 7 {
		t.Fatalf("all entries must match, got %d", len(idx))
	}
	if got := f.entries[idx[0]]; got != "Mila <mila@stlab.test>" {
		t.Fatalf("the consecutive run must rank first, got %q", got)
	}
}

func TestFuzzyFilteredRanking(t *testing.T) {
	f := newFuzzy("account", "account:", []string{"gmail", "jane", "gmail-work"})
	f.query = "gmail"
	got := f.filtered()
	// entries are sorted at construction: [gmail, gmail-work, jane];
	// both matches score the same run, the stable sort keeps entry
	// order, so [0, 1]
	if len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("filtered = %v", got)
	}
	f.query = "work"
	if got := f.filtered(); len(got) != 1 || f.entries[got[0]] != "gmail-work" {
		t.Fatalf("work filtered = %v", got)
	}
	f.query = "gm"
	if got := f.filtered(); len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("gm filtered = %v", got)
	}
	if f.query = "zzz"; len(f.filtered()) != 0 {
		t.Fatalf("zzz must not match anything")
	}
}

func TestFuzzyEmptyQueryShowsAll(t *testing.T) {
	f := newFuzzy("account", "account:", []string{"gmail", "jane"})
	if got := f.filtered(); len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("empty query must list every entry in order: %v", got)
	}
}

func TestFuzzyMoveAndSelect(t *testing.T) {
	f := newFuzzy("signature", "signature:", []string{"a", "b"})
	f.move(1)
	if f.sel != 1 {
		t.Fatalf("sel = %d", f.sel)
	}
	f.move(1)
	if f.sel != 1 {
		t.Fatalf("sel must clamp: %d", f.sel)
	}
	f.move(-3)
	if f.sel != 0 {
		t.Fatalf("sel must clamp at 0: %d", f.sel)
	}
	if entry, ok := f.selected(); !ok || entry != "a" {
		t.Fatalf("selected = %q %v", entry, ok)
	}
}

func TestFuzzyEmptyFilterNoPanic(t *testing.T) {
	f := newFuzzy("account", "account:", []string{"a", "b"})
	f.query = "zzz"
	f.move(1)
	if f.sel != 0 {
		t.Fatalf("sel must clamp to 0 on an empty filter: %d", f.sel)
	}
	if entry, ok := f.selected(); ok || entry != "" {
		t.Fatalf("empty filter must not select: %q %v", entry, ok)
	}
}
