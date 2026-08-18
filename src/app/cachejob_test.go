// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"notmutt/core"
)

func TestScanVisible(t *testing.T) {
	msg := "From: a@example.test\n" +
		"To: b@example.test\n" +
		"Subject: att\n" +
		"MIME-Version: 1.0\n" +
		"Content-Type: multipart/mixed; boundary=\"bb\"\n" +
		"\n" +
		"--bb\n" +
		"Content-Type: text/plain\n" +
		"\n" +
		"body\n" +
		"--bb\n" +
		"Content-Type: application/octet-stream\n" +
		"Content-Disposition: attachment; filename=\"f.bin\"\n" +
		"\n" +
		"DATA\n" +
		"--bb--\n"
	file := filepath.Join(t.TempDir(), "msg.eml")
	if err := os.WriteFile(file, []byte(msg), 0600); err != nil {
		t.Fatal(err)
	}

	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{{ID: "m1", Paths: []string{file}}})})
	bus := core.NewBus()
	ch := bus.Subscribe()
	cj := newCacheJob(bus, &fakeWorker{}, view, filepath.Join(t.TempDir(), "cache.db"))

	// scan path: cache miss, file parsed, atts land in the view
	cj.scanVisible(make(chan struct{}, 2))
	r := readResult(t, ch)
	if r.MsgID != "m1" || len(r.Atts) != 1 || r.Atts[0].Name != "f.bin" {
		t.Fatalf("scan path result wrong: %+v", r)
	}
	rows := view.Rows()
	if len(rows) != 1 || len(rows[0].Msg.Atts) != 1 || rows[0].Msg.Atts[0].Name != "f.bin" {
		t.Fatalf("view must carry the scanned atts: %+v", rows)
	}
	readProgress(t, ch) // the scan publishes progress after its CacheResult

	// A filled row is skipped by scanVisible, so reset the message to the
	// fresh state a re-fetched thread has (empty Atts, same path); the
	// second pass then exercises the cache-hit Get branch.
	view.SetAtts("m1", nil)
	cj.scanVisible(make(chan struct{}, 2))
	r = readResult(t, ch)
	if r.MsgID != "m1" || len(r.Atts) != 1 || r.Atts[0].Name != "f.bin" {
		t.Fatalf("cache-hit path result wrong: %+v", r)
	}
	rows = view.Rows()
	if len(rows[0].Msg.Atts) != 1 || rows[0].Msg.Atts[0].Name != "f.bin" {
		t.Fatalf("view atts must be restored from cache: %+v", rows)
	}
}

func TestScanVisibleGhost(t *testing.T) {
	msg := "From: a@example.test\n" +
		"To: b@example.test\n" +
		"Subject: att\n" +
		"MIME-Version: 1.0\n" +
		"Content-Type: multipart/mixed; boundary=\"bb\"\n" +
		"\n" +
		"--bb\n" +
		"Content-Type: text/plain\n" +
		"\n" +
		"body\n" +
		"--bb\n" +
		"Content-Type: application/octet-stream\n" +
		"Content-Disposition: attachment; filename=\"f.bin\"\n" +
		"\n" +
		"DATA\n" +
		"--bb--\n"
	file := filepath.Join(t.TempDir(), "msg.eml")
	if err := os.WriteFile(file, []byte(msg), 0600); err != nil {
		t.Fatal(err)
	}

	// Two unreferenced messages form two roots: buildTree emits a
	// synthetic ghost root (nil Msg) above them. scanVisible must skip it,
	// not deref nil (the 2026-08-14 segfault on a real mailbox).
	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{
		{ID: "m1", Paths: []string{file}},
		{ID: "m2"},
	})})
	bus := core.NewBus()
	ch := bus.Subscribe()
	cj := newCacheJob(bus, &fakeWorker{}, view, filepath.Join(t.TempDir(), "cache.db"))

	cj.scanVisible(make(chan struct{}, 2))
	r := readResult(t, ch)
	if r.MsgID != "m1" || len(r.Atts) != 1 || r.Atts[0].Name != "f.bin" {
		t.Fatalf("ghost-row scan result wrong: %+v", r)
	}
	rows := view.Rows()
	if len(rows) != 3 || !rows[0].Ghost || len(rows[1].Msg.Atts) != 1 {
		t.Fatalf("view must keep ghost + scanned atts: %+v", rows)
	}
}

func readResult(t *testing.T, ch <-chan core.Event) core.CacheResult {
	t.Helper()
	select {
	case e := <-ch:
		r, ok := e.(core.CacheResult)
		if !ok {
			t.Fatalf("expected CacheResult, got %T", e)
		}
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("no CacheResult within timeout")
		return core.CacheResult{}
	}
}

func TestScanVisiblePublishesProgress(t *testing.T) {
	msg := "From: a@example.test\n" +
		"To: b@example.test\n" +
		"Subject: att\n" +
		"MIME-Version: 1.0\n" +
		"Content-Type: multipart/mixed; boundary=\"bb\"\n" +
		"\n" +
		"--bb\n" +
		"Content-Type: text/plain\n" +
		"\n" +
		"body\n" +
		"--bb\n" +
		"Content-Type: application/octet-stream\n" +
		"Content-Disposition: attachment; filename=\"f.bin\"\n" +
		"\n" +
		"DATA\n" +
		"--bb--\n"
	file := filepath.Join(t.TempDir(), "msg.eml")
	if err := os.WriteFile(file, []byte(msg), 0600); err != nil {
		t.Fatal(err)
	}

	view := core.NewView("inbox", "tag:inbox")
	view.MergeThreads([]*core.Thread{core.NewThread("t1", []*core.Message{{ID: "m1", Paths: []string{file}}})})
	bus := core.NewBus()
	ch := bus.Subscribe()
	cj := newCacheJob(bus, &fakeWorker{}, view, filepath.Join(t.TempDir(), "cache.db"))

	cj.scanVisible(make(chan struct{}, 2))
	// the scan's CacheResult lands first, then the progress count
	r := readResult(t, ch)
	if r.MsgID != "m1" {
		t.Fatalf("result wrong: %+v", r)
	}
	p := readProgress(t, ch)
	if p.Job != "cache" || p.Done != 1 || p.Total != 1 {
		t.Fatalf("progress wrong: %+v", p)
	}
}

func readProgress(t *testing.T, ch <-chan core.Event) core.Progress {
	t.Helper()
	for {
		select {
		case e := <-ch:
			if p, ok := e.(core.Progress); ok {
				return p
			}
			// skip other events: multi-row scans interleave CacheResults
			// between Progress events
		case <-time.After(2 * time.Second):
			t.Fatal("no Progress within timeout")
			return core.Progress{}
		}
	}
}

func TestScanVisibleProgressMonotonic(t *testing.T) {
	bus := core.NewBus()
	ch := bus.Subscribe()
	view := core.NewView("inbox", "tag:inbox")
	var threads []*core.Thread
	for i := 1; i <= 3; i++ {
		msg := fmt.Sprintf("From: a@example.test\n"+
			"To: b@example.test\n"+
			"Subject: att%d\n"+
			"MIME-Version: 1.0\n"+
			"Content-Type: multipart/mixed; boundary=\"bb\"\n"+
			"\n"+
			"--bb\n"+
			"Content-Type: text/plain\n"+
			"\n"+
			"body\n"+
			"--bb\n"+
			"Content-Type: application/octet-stream\n"+
			"Content-Disposition: attachment; filename=\"f%d.bin\"\n"+
			"\n"+
			"DATA\n"+
			"--bb--\n", i, i)
		file := filepath.Join(t.TempDir(), fmt.Sprintf("m%d.eml", i))
		if err := os.WriteFile(file, []byte(msg), 0600); err != nil {
			t.Fatal(err)
		}
		threads = append(threads, core.NewThread(fmt.Sprintf("t%d", i),
			[]*core.Message{{ID: fmt.Sprintf("m%d", i), Paths: []string{file}}}))
	}
	// Filler rows (no paths) are skipped by the scan: the bar totals
	// eligible rows, not page rows. One merge: MergeThreads replaces the
	// view's thread set with its input.
	var filler []*core.Thread
	for i := 4; i <= 12; i++ {
		filler = append(filler, core.NewThread(fmt.Sprintf("t%d", i),
			[]*core.Message{{ID: fmt.Sprintf("f%d", i)}}))
	}
	view.MergeThreads(append(threads, filler...))
	cj := newCacheJob(bus, &fakeWorker{}, view, filepath.Join(t.TempDir(), "cache.db"))

	cj.scanVisible(make(chan struct{}, 2))
	// done increments once per completed scan: the sequence must be
	// strictly 1,2,3 - never a regression or an early 100%.
	seen := map[int]bool{}
	var prev int
	for i := 0; i < 3; i++ {
		p := readProgress(t, ch)
		if p.Job != "cache" || p.Total != 3 {
			t.Fatalf("bad progress: %+v", p)
		}
		if p.Done <= prev {
			t.Fatalf("progress must be strictly increasing: %d after %d", p.Done, prev)
		}
		prev = p.Done
		seen[p.Done] = true
	}
	if prev != 3 || len(seen) != 3 {
		t.Fatalf("progress must cover 1..3 and end at 3/3: %v", seen)
	}
}
