// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"sort"

	sfuzzy "github.com/sahilm/fuzzy"
)

// fuzzy is the selector dialogue (R4): entries, the filter query, the
// selection. kind is the picker's identity ("account" | "address" |
// "signature"), title the popup's prompt line. Matching is
// github.com/sahilm/fuzzy (the lazygit matcher, v0.1.3): the scorer
// ranks consecutive runs and word-boundary matches above scattered
// subsequences - "Mila" beats "Maria Lopez-Diaz" for a "Mila" query.
type fuzzy struct {
	kind    string
	title   string
	entries []string
	payload []string // opaque per-entry data (the scheduled ids); nil when unused
	query   string
	sel     int
	// marks: the file chooser's attachment marks (the t key); nil in every other picker.
	marks map[string]bool
}

func newFuzzy(kind, title string, entries []string) *fuzzy {
	entries = append([]string(nil), entries...)
	sort.Strings(entries)
	return &fuzzy{kind: kind, title: title, entries: entries}
}

// newFuzzyPayload builds the selector with per-entry payloads (the
// scheduled ids): entries and payloads sort together, so the selected
// payload always matches the selected entry. A payload count that
// does not line up with the entries drops the payloads.
func newFuzzyPayload(kind, title string, entries, payload []string) *fuzzy {
	if len(payload) != len(entries) {
		payload = nil
	}
	idx := make([]int, len(entries))
	for i := range idx {
		idx[i] = i
	}
	// sort.Slice is not stable: the index sort keeps every entry with
	// its own payload, even when two entries are equal
	sort.Slice(idx, func(i, j int) bool { return entries[idx[i]] < entries[idx[j]] })
	se := make([]string, len(entries))
	sp := make([]string, len(entries))
	for i, j := range idx {
		se[i] = entries[j]
		if payload != nil {
			sp[i] = payload[j]
		}
	}
	return &fuzzy{kind: kind, title: title, entries: se, payload: sp}
}

// filtered returns the matching entry indices ranked by score (best
// first, ties keep entry order - the sort is stable); an empty query
// matches everything in entry order.
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
	f.sel = max(0, min(f.sel+n, len(idx)-1))
}

func (f *fuzzy) selected() (string, bool) {
	idx := f.filtered()
	if len(idx) == 0 || f.sel < 0 || f.sel >= len(idx) {
		return "", false
	}
	return f.entries[idx[f.sel]], true
}

// selectedPayload returns the selected entry's payload ("", false
// when the picker carries none or nothing is selected).
func (f *fuzzy) selectedPayload() (string, bool) {
	idx := f.filtered()
	if f.payload == nil || len(idx) == 0 || f.sel < 0 || f.sel >= len(idx) {
		return "", false
	}
	return f.payload[idx[f.sel]], true
}
