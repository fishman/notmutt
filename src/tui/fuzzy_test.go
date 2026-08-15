package tui

import "testing"

func TestFuzzyMatch(t *testing.T) {
	if pos, ok := fuzzyMatch("gm", "gmail"); !ok || pos != 0 {
		t.Fatalf("gmail/gm = %d %v", pos, ok)
	}
	if pos, ok := fuzzyMatch("gmail", "gmail/me"); !ok || pos != 0 {
		t.Fatalf("gmail in gmail/me = %d %v", pos, ok)
	}
	if pos, ok := fuzzyMatch("gm", "dynamia"); ok {
		t.Fatalf("dynamia/gm must not match, pos = %d", pos)
	}
	if _, ok := fuzzyMatch("", "anything"); !ok {
		t.Fatal("empty query matches everything")
	}
}

func TestFuzzyFilteredRanking(t *testing.T) {
	f := newFuzzy("account", "account:", []string{"gmail", "jelveh", "gmail-work"})
	f.query = "gmail"
	got := f.filtered()
	// entries are sorted at construction: [gmail, gmail-work, jelveh];
	// first-match position ranks (both pos 0) - tie breaks by entry
	// order, so [0, 1]
	if len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("filtered = %v", got)
	}
	f.query = "work"
	if got := f.filtered(); len(got) != 1 || f.entries[got[0]] != "gmail-work" {
		t.Fatalf("work filtered = %v", got)
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
