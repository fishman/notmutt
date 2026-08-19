// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package core

import (
	"sort"
	"testing"
)

func TestMsgLessOrder(t *testing.T) {
	a := &Message{ID: "a", Timestamp: 100}
	b := &Message{ID: "b", Timestamp: 100}
	c := &Message{ID: "c", Timestamp: 200}
	msgs := []*Message{b, c, a}
	sort.Slice(msgs, func(i, j int) bool { return MsgLess(msgs[i], msgs[j]) })
	if msgs[0] != a || msgs[1] != b || msgs[2] != c {
		t.Fatalf("want [a b c], got [%s %s %s]", msgs[0].ID, msgs[1].ID, msgs[2].ID)
	}
}

func TestThreadLessOrder(t *testing.T) {
	x := &Thread{ID: "x", LastDate: 100}
	y := &Thread{ID: "y", LastDate: 100}
	z := &Thread{ID: "z", LastDate: 300}
	ts := []*Thread{y, z, x}
	sort.Slice(ts, func(i, j int) bool { return ThreadLess(ts[i], ts[j]) })
	if ts[0] != z || ts[1] != x || ts[2] != y {
		t.Fatalf("want [z x y], got [%s %s %s]", ts[0].ID, ts[1].ID, ts[2].ID)
	}
}
