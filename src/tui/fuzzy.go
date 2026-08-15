package tui

import (
	"sort"
	"strings"
)

// fuzzyMatch reports whether s contains query as a case-insensitive
// subsequence, plus the first match position (earlier = better rank).
func fuzzyMatch(query, s string) (int, bool) {
	if query == "" {
		return 0, true
	}
	q, str := strings.ToLower(query), strings.ToLower(s)
	qi, si, start := 0, 0, -1
	for si < len(str) && qi < len(q) {
		if str[si] == q[qi] {
			if qi == 0 {
				start = si
			}
			qi++
		}
		si++
	}
	if qi != len(q) {
		return 0, false
	}
	return start, true
}

// fuzzy is the selector dialogue (R4): entries, the filter query, the
// selection. In-process matcher - no fzf subprocess, no new exec
// surface. kind is the picker's identity ("account" | "signature"),
// title is the popup's prompt line.
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

// filtered returns the matching entry indices, ranked by first match
// position then entry order (sorted at construction).
func (f *fuzzy) filtered() []int {
	var out []int
	for i, e := range f.entries {
		if _, ok := fuzzyMatch(f.query, e); ok {
			out = append(out, i)
		}
	}
	sort.SliceStable(out, func(a, b int) bool {
		pa, _ := fuzzyMatch(f.query, f.entries[out[a]])
		pb, _ := fuzzyMatch(f.query, f.entries[out[b]])
		return pa < pb
	})
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
