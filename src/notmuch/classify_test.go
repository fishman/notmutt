// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

//go:build !cli

package notmuch

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"notmutt/core"
	"notmutt/lib/testutil"
)

// TestRealThreadClassifies pins the production fetch -> classify path:
// the cgo thread fetch fills the fields the index highlight needs (id,
// timestamp, author, tags), and the rows classify the thread's tail -
// the recent-5 window, the other side, the sent-tag identity, old
// messages unmarked. notmuch reports ids bare (no angle brackets).
func TestRealThreadClassifies(t *testing.T) {
	e := testutil.Setup(t)
	// one thread of 9 fabricated messages; mine are alpha (the scratch
	// user), the rest sender@example.com; timestamps ascend by hour
	me := []string{"alpha@example.com"}
	var prev string
	for i := range 9 {
		author := "Sender <sender@example.com>"
		if i <= 3 {
			author = "Alpha <alpha@example.com>"
		}
		id := fmt.Sprintf("<c%d@test.invalid>", i)
		body := fmt.Sprintf("From: %s\nTo: alpha@example.com\nSubject: classify thread\n"+
			"Date: Sat, 16 Aug 2026 1%d:00:00 +0000\nMessage-ID: %s\n", author, i, id)
		if prev != "" {
			body += "References: " + prev + "\n"
		}
		body += "\nsynthetic fixture body\n"
		prev = id
		if err := os.WriteFile(filepath.Join(e.Maildir, fmt.Sprintf("c%d.eml", i)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	testutil.NotmuchNew(t)
	b := newTestBackend(t, e)
	threadID := ""
	err := b.Query(context.Background(), "subject:classify thread", 10, false, func(chunk []core.Message) bool {
		if len(chunk) > 0 {
			threadID = chunk[0].ThreadID
		}
		return false
	})
	if err != nil {
		t.Fatal(err)
	}
	if threadID == "" {
		t.Fatal("the query must resolve the thread id")
	}
	msgs, err := b.Thread(context.Background(), threadID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 9 {
		t.Fatalf("the thread must fetch 9 messages, got %d", len(msgs))
	}
	for _, m := range msgs {
		if m.ID == "" || m.Timestamp == 0 || m.Author == "" {
			t.Fatalf("the fetch must fill id/timestamp/author: %+v", m)
		}
	}
	// the index builds one row per message; the marks classify the rows
	// positionally - the row's message identity only selects assertions
	rows := make([]core.Row, len(msgs))
	byID := make(map[string]int, len(msgs))
	for i := range msgs {
		rows[i] = core.Row{Msg: &msgs[i]}
		byID[msgs[i].ID] = i
	}
	marks := core.ClassifyRows(rows, me)
	// c0: oldest, outside the recent-5 and not the latest other-side
	if got := marks[byID["c0@test.invalid"]]; got != core.MarkNone {
		t.Fatalf("the oldest message must be unmarked, got %v (all: %v)", got, marks)
	}
	// c4: inside the recent-5 window; c8: the latest other-side winner
	if got := marks[byID["c4@test.invalid"]]; got != core.MarkRecent {
		t.Fatalf("a recent-window message must be recent, got %v (all: %v)", got, marks)
	}
	if got := marks[byID["c8@test.invalid"]]; got != core.MarkOther {
		t.Fatalf("the latest other-side message must be other, got %v (all: %v)", got, marks)
	}
	// tag c8 sent: it becomes mine, the other-side mark shifts to c7
	for i := range msgs {
		if msgs[i].ID == "c8@test.invalid" {
			msgs[i].Tags = append(msgs[i].Tags, "sent")
		}
	}
	marks = core.ClassifyRows(rows, me)
	if got := marks[byID["c7@test.invalid"]]; got != core.MarkOther {
		t.Fatalf("with the latest mine, the previous message must be other, got %v (all: %v)", got, marks)
	}
	if got := marks[byID["c8@test.invalid"]]; got != core.MarkRecent {
		t.Fatalf("a sent-tagged latest message must be recent, got %v (all: %v)", got, marks)
	}
}
