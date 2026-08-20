// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"notmutt/core"
	"notmutt/lib/testutil"
	"notmutt/notmuch"
)

// TestRealOpenThreadMarks rides the production path end to end: the
// real notmuch worker fetches the thread, openThread classifies the
// opened message, and ThreadLoaded carries the mark - the piece the
// fake-worker tests cannot see (the real fetch's ids, timestamps,
// authors, tags).
func TestRealOpenThreadMarks(t *testing.T) {
	_, maildir := testutil.ScratchMailbox(t)
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
	bus := core.NewBus()
	worker := notmuch.NewWorker(bus, notmuch.New(), 10*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go worker.Start(ctx)
	if rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActOpen, Query: ""}); err != nil || rpl.Err != nil {
		t.Fatalf("open: %v %v", err, rpl.Err)
	}
	var found []core.Message
	if rpl, err := worker.Call(notmuch.Action{Kind: notmuch.ActQuery, Query: "subject:classify thread", Limit: 10,
		Emit: func(chunk []core.Message) bool {
			found = append(found, chunk...)
			return true
		}}); err != nil || rpl.Err != nil {
		t.Fatalf("query: %v %v", err, rpl.Err)
	}
	if len(found) == 0 {
		t.Fatal("the query must resolve the thread")
	}
	ch := bus.Subscribe()
	// the worker's tag publishes (mark-read) interleave on the bus;
	// the reply is the first ThreadLoaded
	loaded := func() core.ThreadLoaded {
		for {
			select {
			case e := <-ch:
				if tl, ok := e.(core.ThreadLoaded); ok {
					return tl
				}
			case <-time.After(5 * time.Second):
				t.Fatal("no ThreadLoaded")
				return core.ThreadLoaded{}
			}
		}
	}
	// the marks classify the thread's tail, keyed by message id: the
	// latest other-side message carries MarkOther, the oldest stays
	// unmarked - the same map whichever message opened
	openThread(worker, bus, found[0].ThreadID, "c7@test.invalid", false, core.RenderPlain, false, 80, false, nil, me)
	if tl := loaded(); tl.Marks["c7@test.invalid"] != core.MarkOther {
		t.Fatalf("the latest other-side message must carry MarkOther, got %v", tl.Marks)
	}
	openThread(worker, bus, found[0].ThreadID, "c0@test.invalid", false, core.RenderPlain, false, 80, false, nil, me)
	if tl := loaded(); tl.Marks["c0@test.invalid"] != core.MarkNone {
		t.Fatalf("the oldest message must stay unmarked, got %v", tl.Marks)
	}
}
