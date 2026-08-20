// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package core

import "testing"

// TestClassifyMsgs pins the thread-position marks: the five messages
// with the latest Timestamp are recent, the latest message not
// authored by me (sent tag or a From matching the me addresses) is
// the other-side one - prominent wins when it is also recent. The
// classification is timestamp-based, never fetch-order based.
func TestClassifyMsgs(t *testing.T) {
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
		want map[string]MsgMark
	}{
		{"plain thread", msgs(), map[string]MsgMark{
			"md": MarkRecent, "me": MarkRecent, "mf": MarkRecent, "mg": MarkRecent,
			"mh": MarkOther,
		}},
		{"last is mine: the previous is other, mine stays recent", func() []Message {
			m := msgs()
			m[7].Tags = []string{"sent"}
			return m
		}(), map[string]MsgMark{
			"md": MarkRecent, "me": MarkRecent, "mf": MarkRecent,
			"mg": MarkOther, "mh": MarkRecent,
		}},
		{"me by address, no sent tag", func() []Message {
			m := msgs()
			m[7] = Message{ID: "mh", Timestamp: 107, Author: "Alpha <alpha@example.com>"}
			return m
		}(), map[string]MsgMark{
			"md": MarkRecent, "me": MarkRecent, "mf": MarkRecent,
			"mg": MarkOther, "mh": MarkRecent,
		}},
		{"unparseable author is never me", func() []Message {
			m := msgs()
			m[7].Author = "not an address"
			return m
		}(), map[string]MsgMark{
			"md": MarkRecent, "me": MarkRecent, "mf": MarkRecent, "mg": MarkRecent,
			"mh": MarkOther,
		}},
		{"solo thread other side", []Message{{ID: "a", Timestamp: 1, Author: "Acme <acme@example.com>"}},
			map[string]MsgMark{"a": MarkOther}},
		{"solo thread mine", []Message{{ID: "a", Timestamp: 1, Author: "Alpha <alpha@example.com>", Tags: []string{"sent"}}},
			map[string]MsgMark{"a": MarkRecent}},
		{"short thread all recent", []Message{
			{ID: "a", Timestamp: 1, Author: "Acme <acme@example.com>"},
			{ID: "b", Timestamp: 2, Author: "Acme <acme@example.com>"},
			{ID: "c", Timestamp: 3, Author: "Acme <acme@example.com>"},
		}, map[string]MsgMark{"a": MarkRecent, "b": MarkRecent, "c": MarkOther}},
		{"empty thread", nil, map[string]MsgMark{}},
	}
	for _, c := range cases {
		got := ClassifyMsgs(c.msgs, me)
		if len(got) != len(c.want) {
			t.Errorf("%s: ClassifyMsgs = %v, want %v", c.name, got, c.want)
			continue
		}
		for id, mark := range c.want {
			if got[id] != mark {
				t.Errorf("%s: ClassifyMsgs[%q] = %v, want %v (all: %v)", c.name, id, got[id], mark, got)
			}
		}
	}
}
