// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

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
// the real cgo thread fetch fills the fields the pager highlight needs
// (id, timestamp, author, tags) and ClassifyMsg marks the opened
// message against the full thread - the recent-5 window, the other
// side, the sent-tag identity, and an old message staying unmarked.
// notmuch reports message ids bare (no angle brackets).
func TestRealThreadClassifies(t *testing.T) {
	db, maildir := testutil.ScratchMailbox(t)
	// one thread of 8 fabricated messages; mine are alpha (the scratch
	// user), the rest sender@example.com; timestamps ascend by hour
	me := []string{"alpha@example.com"}
	var prev string
	for i := range 8 {
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
		if err := os.WriteFile(filepath.Join(maildir, fmt.Sprintf("c%d.eml", i)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	testutil.NotmuchNew(t)
	b := NewCGO()
	if err := b.Open(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	threadID := ""
	err := b.Query(context.Background(), "subject:classify thread", 10, func(chunk []core.Message) bool {
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
	if len(msgs) != 8 {
		t.Fatalf("the thread must fetch 8 messages, got %d", len(msgs))
	}
	byID := map[string]int{}
	for i, m := range msgs {
		if m.ID == "" || m.Timestamp == 0 || m.Author == "" {
			t.Fatalf("the fetch must fill id/timestamp/author: %+v", m)
		}
		byID[m.ID] = i
	}
	// c0: oldest, outside the recent-5 and not the latest other-side
	if got := core.ClassifyMsg(msgs, byID["c0@test.invalid"], me); got != core.MarkNone {
		t.Fatalf("the oldest message must be unmarked, got %v", got)
	}
	// c4: inside the recent-5 window
	if got := core.ClassifyMsg(msgs, byID["c4@test.invalid"], me); got != core.MarkRecent {
		t.Fatalf("a recent-window message must be recent, got %v", got)
	}
	// c7: the latest message from the other side
	if got := core.ClassifyMsg(msgs, byID["c7@test.invalid"], me); got != core.MarkOther {
		t.Fatalf("the latest other-side message must be other, got %v", got)
	}
	// tag c7 sent: it becomes mine, the other-side mark shifts to c6
	for i := range msgs {
		if msgs[i].ID == "c7@test.invalid" {
			msgs[i].Tags = append(msgs[i].Tags, "sent")
		}
	}
	if got := core.ClassifyMsg(msgs, byID["c6@test.invalid"], me); got != core.MarkOther {
		t.Fatalf("with the latest mine, the previous message must be other, got %v", got)
	}
	if got := core.ClassifyMsg(msgs, byID["c7@test.invalid"], me); got != core.MarkRecent {
		t.Fatalf("a sent-tagged latest message must be recent, got %v", got)
	}
}
