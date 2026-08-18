// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"sort"

	sfuzzy "github.com/sahilm/fuzzy"
)

// fuzzy is the selector dialogue (R4): entries, the filter query, the
// selection. kind is the picker's identity ("account" | "address" |
// "signature"), title is the popup's prompt line. Matching is
// github.com/sahilm/fuzzy (the lazygit reference's matcher, v0.1.3):
// the bonus/penalty scorer ranks consecutive runs and word-boundary
// matches above scattered subsequences - "Mila" beats "Maria
// Lopez-Diaz" for a "Mila" query.
type fuzzy struct {
	kind    string
	title   string
	entries []string
	query   string
	sel     int
}

func newFuzzy(kind, title string, entries []string) *fuzzy {
	entries = append([]string(nil), entries...)
	sort.Strings(entries)
	return &fuzzy{kind: kind, title: title, entries: entries}
}

// filtered returns the matching entry indices, ranked by the
// matcher's score (best first, ties keep entry order - the sort is
// stable). An empty query matches everything in entry order.
func (f *fuzzy) filtered() []int {
	if f.query == "" {
		out := make([]int, len(f.entries))
		for i := range f.entries {
			out[i] = i
		}
		return out
	}
	ms := sfuzzy.Find(f.query, f.entries)
	out := make([]int, len(ms))
	for i, m := range ms {
		out[i] = m.Index
	}
	return out
}

func (f *fuzzy) move(n int) {
	idx := f.filtered()
	if len(idx) == 0 {
		f.sel = 0
		return
	}
	f.sel += n
	if f.sel < 0 {
		f.sel = 0
	}
	if max := len(idx) - 1; f.sel > max {
		f.sel = max
	}
}

func (f *fuzzy) selected() (string, bool) {
	idx := f.filtered()
	if len(idx) == 0 || f.sel < 0 || f.sel >= len(idx) {
		return "", false
	}
	return f.entries[idx[f.sel]], true
}
