// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package core

import "testing"

// TestClassifyMsg pins the thread-position marks: the five messages
// with the latest Timestamp are recent, the latest message not
// authored by me (sent tag or a From matching the me addresses) is
// the other-side one - prominent wins when it is also recent. The
// classification is timestamp-based, never fetch-order based.
func TestClassifyMsg(t *testing.T) {
	me := []string{"alpha@example.com"}
	msgs := func() []Message {
		var out []Message
		for i := range 8 {
			out = append(out, Message{
				ID: "m" + string(rune('a'+i)), Timestamp: int64(100 + i),
				Author: "Atlas <atlas@example.com>",
			})
		}
		return out
	}
	cases := []struct {
		name string
		msgs []Message
		i    int
		want MsgMark
	}{
		{"recent window", msgs(), 4, MarkRecent},
		{"recent edge", msgs(), 3, MarkRecent},
		{"old", msgs(), 2, MarkNone},
		{"latest other side", msgs(), 7, MarkOther},
		{"out of range", msgs(), 99, MarkNone},
		{"last is mine: the previous is other", func() []Message {
			m := msgs()
			m[7].Tags = []string{"sent"}
			return m
		}(), 6, MarkOther},
		{"last is mine: mine stays recent", func() []Message {
			m := msgs()
			m[7].Tags = []string{"sent"}
			return m
		}(), 7, MarkRecent},
		{"me by address, no sent tag", func() []Message {
			m := msgs()
			m[7] = Message{ID: "mh", Timestamp: 107, Author: "Alpha <alpha@example.com>"}
			return m
		}(), 6, MarkOther},
		{"unparseable author is never me", func() []Message {
			m := msgs()
			m[7].Author = "not an address"
			return m
		}(), 7, MarkOther},
		{"solo thread other side", []Message{{ID: "a", Timestamp: 1, Author: "Acme <acme@example.com>"}}, 0, MarkOther},
		{"solo thread mine", []Message{{ID: "a", Timestamp: 1, Author: "Alpha <alpha@example.com>", Tags: []string{"sent"}}}, 0, MarkRecent},
		{"short thread all recent", func() []Message {
			return []Message{
				{ID: "a", Timestamp: 1, Author: "Acme <acme@example.com>"},
				{ID: "b", Timestamp: 2, Author: "Acme <acme@example.com>"},
				{ID: "c", Timestamp: 3, Author: "Acme <acme@example.com>"},
			}
		}(), 1, MarkRecent},
	}
	for _, c := range cases {
		if got := ClassifyMsg(c.msgs, c.i, me); got != c.want {
			t.Errorf("%s: ClassifyMsg(i=%d) = %v, want %v", c.name, c.i, got, c.want)
		}
	}
}
