// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package core

import (
	"fmt"
	"testing"
)

// TestClassifyRows pins the thread-tail marks: among the five rows
// with the latest Timestamp MarkRecent, the latest row not authored by
// me (sent tag or a From matching the me addresses) MarkOther,
// prominent winning when it is also recent. The classification is
// timestamp-based, never fetch-order based; rows without a message
// (ghosts) stay unmarked. The windowed-thread gate lives in the view's
// flatten (the caller), not here.
func TestClassifyRows(t *testing.T) {
	me := []string{"alpha@example.com"}
	rows := func(n int) []Row {
		out := make([]Row, n)
		for i := range n {
			out[i] = Row{Msg: &Message{
				ID: fmt.Sprintf("m%d", i), Timestamp: int64(100 + i),
				Author: "Atlas <atlas@example.com>",
			}}
		}
		return out
	}
	tail := []MsgMark{MarkNone, MarkNone, MarkNone, MarkNone, MarkRecent, MarkRecent, MarkRecent, MarkRecent, MarkOther}
	cases := []struct {
		name string
		rows []Row
		want []MsgMark
	}{
		{"thread of 9 marks the tail", rows(9), tail},
		{"last is mine: the previous is other, mine stays recent", func() []Row {
			r := rows(9)
			r[8].Msg.Tags = []string{"sent"}
			return r
		}(), []MsgMark{MarkNone, MarkNone, MarkNone, MarkNone, MarkRecent, MarkRecent, MarkRecent, MarkOther, MarkRecent}},
		{"me by address, no sent tag", func() []Row {
			r := rows(9)
			r[8] = Row{Msg: &Message{ID: "m8", Timestamp: 108, Author: "Alpha <alpha@example.com>"}}
			return r
		}(), []MsgMark{MarkNone, MarkNone, MarkNone, MarkNone, MarkRecent, MarkRecent, MarkRecent, MarkOther, MarkRecent}},
		{"unparseable author is never me", func() []Row {
			r := rows(9)
			r[8].Msg.Author = "not an address"
			return r
		}(), tail},
		{"ghost rows stay unmarked", func() []Row {
			return append(rows(9), Row{Ghost: true, ThreadID: "t"})
		}(), append(append([]MsgMark{}, tail...), MarkNone)},
		{"solo thread", []Row{{Msg: &Message{ID: "a", Timestamp: 1, Author: "Acme <acme@example.com>"}}}, []MsgMark{MarkOther}},
		{"short thread", []Row{
			{Msg: &Message{ID: "a", Timestamp: 1, Author: "Acme <acme@example.com>"}},
			{Msg: &Message{ID: "b", Timestamp: 2, Author: "Acme <acme@example.com>"}},
			{Msg: &Message{ID: "c", Timestamp: 3, Author: "Acme <acme@example.com>"}},
		}, []MsgMark{MarkRecent, MarkRecent, MarkOther}},
		{"empty", nil, nil},
	}
	for _, c := range cases {
		got := ClassifyRows(c.rows, me)
		if len(got) != len(c.want) {
			t.Errorf("%s: ClassifyRows = %v, want %v", c.name, got, c.want)
			continue
		}
		for i, mark := range c.want {
			if got[i] != mark {
				t.Errorf("%s: ClassifyRows[%d] = %v, want %v (all: %v)", c.name, i, got[i], mark, got)
			}
		}
	}
}
