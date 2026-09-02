// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

// TestOpenFromFlatSearchTabUnmerged: opening a message from a flat
// search tab before the walk has merged it into the view. The cursor
// yields the synthetic thread id (= the message id); threadFromViews
// misses, and notmuch's thread:<msgid> matches NOTHING (thread ids are
// opaque - proven against the live DB). The fallback must resolve the
// message by id (Snapshots) instead of failing with "no messages in
// thread" (mail.RenderThread on an empty set). The faithful fake models
// exactly that: ActThread(msgid) = empty, ActThread(t1) = the thread.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"notmutt/core"
	"notmutt/notmuch"
	"notmutt/tui"
)

// flatThreadWorker models real notmuch: a thread fetch by a message id
// (the synthetic flat-view thread id) matches nothing; by a real thread
// id it returns the thread; a snapshot by message id resolves it.
type flatThreadWorker struct {
	threads map[string][]core.Message
}

func (f *flatThreadWorker) Call(a notmuch.Action) (notmuch.Reply, error) {
	r := notmuch.Reply{ID: a.ID}
	switch a.Kind {
	case notmuch.ActThread:
		r.Msgs = f.threads[a.ThreadID]
	case notmuch.ActSnapshots:
		for _, id := range a.Paths {
			for _, msgs := range f.threads {
				for _, m := range msgs {
					if m.ID == id {
						r.Msgs = append(r.Msgs, m)
					}
				}
			}
		}
	}
	return r, nil
}

func TestOpenFromFlatSearchTabUnmerged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "msg")
	raw := "From: sender@example.com\nTo: alpha@example.com\nSubject: travel confirm\n" +
		"Date: Tue, 01 Jan 2019 00:00:00 +0000\n\nbody\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	msgID := "m1@example.com"
	m1 := core.Message{ID: msgID, ThreadID: "t1", Timestamp: 100, Author: "sender@example.com",
		Subject: "travel confirm", Tags: []string{"inbox"}, Paths: []string{path}}

	// the inbox holds the real thread; the search tab is EMPTY - its
	// walk has not merged the message yet
	inbox := core.NewView("inbox", "tag:inbox")
	inbox.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{&m1})})
	search := core.NewView("tag:new", "tag:new")
	search.SetThreaded(false)
	views := map[string]*core.View{"inbox": inbox, "tag:new": search}

	fw := &flatThreadWorker{threads: map[string][]core.Message{"t1": {m1}}}

	// cursorThread in the flat search tab yields tid = the message id
	tl := runOpen(t, fw, views, tui.OpenReq{ThreadID: msgID, MsgID: msgID, Preview: true, Width: 80})
	if tl.Err != nil {
		t.Fatalf("open from an unmerged search tab failed: %v", tl.Err)
	}
	if len(tl.Lines) == 0 {
		t.Fatal("open from an unmerged search tab rendered nothing")
	}
	found := false
	for _, l := range tl.Lines {
		if strings.Contains(l.Text, "travel confirm") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("open from an unmerged search tab did not render the message subject")
	}
}
