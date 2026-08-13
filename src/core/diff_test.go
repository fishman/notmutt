package core

import (
	"math/rand"
	"sort"
	"strings"
	"testing"
)

func randHex(r *rand.Rand, n int) string {
	const hex = "0123456789abcdef"
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteByte(hex[r.Intn(16)])
	}
	return b.String()
}

func genMsgs(r *rand.Rand, n int, prefix string) []*Message {
	msgs := make([]*Message, n)
	for i := range msgs {
		msgs[i] = &Message{ID: prefix + "-" + randHex(r, 8), Timestamp: r.Int63n(100)}
	}
	sort.Slice(msgs, func(i, j int) bool { return MsgLess(msgs[i], msgs[j]) })
	return msgs
}

func msgKey(m *Message) string { return m.ID }

func sameMsgs(a, b []*Message) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			return false
		}
	}
	return true
}

func genThreads(r *rand.Rand, n int, prefix string) []*Thread {
	ts := make([]*Thread, n)
	for i := range ts {
		ts[i] = &Thread{ID: prefix + "-" + randHex(r, 8), LastDate: r.Int63n(100)}
	}
	sort.Slice(ts, func(i, j int) bool { return ThreadLess(ts[i], ts[j]) })
	return ts
}

func threadKey(t *Thread) string { return t.ID }

func sameThreads(a, b []*Thread) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			return false
		}
	}
	return true
}

func TestDiffApplyPropertyMessages(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for iter := 0; iter < 1000; iter++ {
		old := genMsgs(r, r.Intn(30), "old")
		new := genMsgs(r, r.Intn(30), "new")
		shared := r.Intn(min(len(old), len(new)) + 1)
		for i := 0; i < shared; i++ {
			new[i].ID = old[i].ID
			new[i].Timestamp = old[i].Timestamp
		}
		// timestamps are dense in [0,100), so a small shift crosses
		// neighbors: the element sinks (Move collapse) or rises (churn)
		if len(new) > 0 && r.Intn(4) == 0 {
			k := r.Intn(len(new))
			if r.Intn(2) == 0 {
				new[k].Timestamp += r.Int63n(10)
			} else {
				new[k].Timestamp -= r.Int63n(10)
			}
		}
		sort.Slice(new, func(i, j int) bool { return MsgLess(new[i], new[j]) })
		ops := DiffSorted(old, new, MsgLess, msgKey)
		got := Apply(old, ops)
		if !sameMsgs(got, new) {
			t.Fatalf("iter %d: apply(diff) != new\nold=%v\nnew=%v\ngot=%v", iter, msgIDs(old), msgIDs(new), msgIDs(got))
		}
	}
}

func TestDiffApplyPropertyThreads(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for iter := 0; iter < 1000; iter++ {
		old := genThreads(r, r.Intn(20), "t")
		new := genThreads(r, r.Intn(20), "t")
		shared := r.Intn(min(len(old), len(new)) + 1)
		for i := 0; i < shared; i++ {
			new[i].ID = old[i].ID
			new[i].LastDate = old[i].LastDate
		}
		if len(new) > 0 && r.Intn(4) == 0 {
			k := r.Intn(len(new))
			if r.Intn(2) == 0 {
				new[k].LastDate += r.Int63n(10)
			} else {
				new[k].LastDate -= r.Int63n(10)
			}
		}
		sort.Slice(new, func(i, j int) bool { return ThreadLess(new[i], new[j]) })
		ops := DiffSorted(old, new, ThreadLess, threadKey)
		got := Apply(old, ops)
		if !sameThreads(got, new) {
			t.Fatalf("iter %d: apply(diff) != new\nold=%v\nnew=%v\ngot=%v", iter, threadIDs(old), threadIDs(new), threadIDs(got))
		}
	}
}

func TestDiffMoveCollapse(t *testing.T) {
	// a sinks from newest to oldest: the walk emits Remove then Insert
	// of the same key - the second pass collapses the pair into a Move.
	old := []*Message{{ID: "a", Timestamp: 5}, {ID: "c", Timestamp: 3}, {ID: "b", Timestamp: 2}}
	new := []*Message{{ID: "c", Timestamp: 3}, {ID: "b", Timestamp: 2}, {ID: "a", Timestamp: 1}}
	ops := DiffSorted(old, new, MsgLess, msgKey)
	found := false
	for _, op := range ops {
		if op.Kind == OpMove && op.Key == "a" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a Move op for the sunk message, got %+v", ops)
	}
	if got := Apply(old, ops); !sameMsgs(got, new) {
		t.Fatalf("apply with move mismatch: %v != %v", msgIDs(got), msgIDs(new))
	}
}

func msgIDs(msgs []*Message) []string {
	ids := make([]string, len(msgs))
	for i, m := range msgs {
		ids[i] = m.ID
	}
	return ids
}

func threadIDs(ts []*Thread) []string {
	ids := make([]string, len(ts))
	for i, t := range ts {
		ids[i] = t.ID
	}
	return ids
}
