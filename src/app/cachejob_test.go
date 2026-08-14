package app

import (
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
