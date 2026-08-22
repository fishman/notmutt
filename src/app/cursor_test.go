// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"math/rand"
	"testing"

	"notmutt/core"
)

// TestCursorInvariant: the cursor survives a merge when its message
// does; random lists and mutations, deterministic seed.
func TestCursorInvariant(t *testing.T) {
	rng := rand.New(rand.NewSource(11))
	for i := 0; i < 500; i++ {
		v := core.NewView("inbox", "tag:inbox")
		n := rng.Intn(21)
		msgs := make([]*core.Message, n)
		for j := range msgs {
			msgs[j] = &core.Message{ID: randID(rng), Timestamp: rng.Int63n(1e9), ThreadID: "t"}
		}
		if len(msgs) == 0 {
			continue
		}
		v.MergeThreads([]*core.Thread{core.NewThread("t", msgs)})
		target := msgs[rng.Intn(len(msgs))].ID
		v.SetCursor(target)
		for k := rng.Intn(6); k > 0; k-- {
			switch rng.Intn(3) {
			case 0:
				msgs = append(msgs, &core.Message{ID: randID(rng), Timestamp: rng.Int63n(1e9), ThreadID: "t"})
			case 1:
				if len(msgs) > 1 {
					msgs = msgs[1:]
				}
			case 2:
				msgs[rng.Intn(len(msgs))].Timestamp += rng.Int63n(10)
			}
			v.MergeThreads([]*core.Thread{core.NewThread("t", msgs)})
		}
		row, ok := v.CursorRow()
		if !ok {
			continue
		}
		present := false
		for _, m := range msgs {
			if m.ID == target {
				present = true
				break
			}
		}
		if present && (row.Msg == nil || row.Msg.ID != target) {
			t.Fatalf("cursor lost: target %s present but row holds %v", target, row.Msg)
		}
	}
}

func randID(rng *rand.Rand) string {
	const hex = "0123456789abcdef"
	b := make([]byte, 16)
	for i := range b {
		b[i] = hex[rng.Intn(len(hex))]
	}
	return string(b)
}
