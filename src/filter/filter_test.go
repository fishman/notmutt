package filter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"notmutt/config"
	"notmutt/core"
	"notmutt/notmuch"
)

// fakeWorker serves the engine's reads from canned data and records the
// ActTag writes.
type fakeWorker struct {
	delta  []core.Message
	snaps  []core.Message
	header map[string]bool // ids the header-rule query matches
	tagged []notmuch.Action
}

func (f *fakeWorker) Call(a notmuch.Action) (notmuch.Reply, error) {
	switch a.Kind {
	case notmuch.ActQueryMsgs:
		// the worker contract: QueryMsgs delivers via the emit closure,
		// never the reply
		var msgs []core.Message
		if strings.HasPrefix(a.Query, "lastmod:") {
			msgs = f.delta
		} else {
			for _, m := range f.delta {
				if f.header[m.ID] {
					msgs = append(msgs, core.Message{ID: m.ID})
				}
			}
		}
		if a.Emit != nil {
			a.Emit(msgs)
		}
	case notmuch.ActSnapshots:
		return notmuch.Reply{Msgs: f.snaps}, nil
	case notmuch.ActTag:
		f.tagged = append(f.tagged, a)
	}
	return notmuch.Reply{}, nil
}

func TestEngineClassification(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	root := filepath.Join(dir, "mail")
	inboxDir := filepath.Join(root, "gmail", "INBOX", "cur")
	if err := os.MkdirAll(inboxDir, 0o700); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(inboxDir, "2")
	if err := os.WriteFile(fresh, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	mark := filepath.Join(dir, ".cache", "mail-sync-mark")
	if err := os.MkdirAll(filepath.Dir(mark), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(mark, nil, 0o600)
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(mark, past, past); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Accounts = map[string]config.Account{"gmail": {Preset: "gmail"}}
	cfg.Filter.HeaderRules = []config.HeaderRule{{Query: "from:x", Add: []string{"work"}}}
	cfg.Filter.DryRun = false

	w := &fakeWorker{
		delta: []core.Message{{ID: "m1"}, {ID: "m2"}, {ID: "m3"}},
		snaps: []core.Message{
			{ID: "m1", Tags: []string{"inbox"}, Paths: []string{"gmail/Archives/cur/1"}},
			{ID: "m2", Tags: []string{"inbox", "spam"}, Paths: []string{filepath.Join(root, "gmail/INBOX/cur/2")}},
			{ID: "m3", Tags: []string{"gmail", "archive"}, Paths: []string{"gmail/Archives/cur/3"}},
		},
		header: map[string]bool{"m2": true},
	}

	rep, err := New(w, cfg, root).Run(0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) != 2 {
		t.Fatalf("entries = %d, want 2: %+v", len(rep.Entries), rep.Entries)
	}
	e1 := rep.Entries[0]
	if e1.ID != "m1" || e1.Account != "gmail" {
		t.Fatalf("entry 1 = %+v", e1)
	}
	want1 := []core.TagOp{
		{Tag: "archive", Add: true}, {Tag: "gmail", Add: true}, {Tag: "inbox"},
	}
	if !equalOps(e1.Ops, want1) {
		t.Fatalf("m1 ops = %+v, want %+v", e1.Ops, want1)
	}
	e2 := rep.Entries[1]
	if e2.ID != "m2" {
		t.Fatalf("entry 2 = %+v", e2)
	}
	want2 := []core.TagOp{
		{Tag: "gmail", Add: true}, {Tag: "spam"}, {Tag: "work", Add: true},
	}
	if !equalOps(e2.Ops, want2) {
		t.Fatalf("m2 ops = %+v, want %+v", e2.Ops, want2)
	}
	if len(w.tagged) != 2 {
		t.Fatalf("ActTag calls = %d, want 2", len(w.tagged))
	}
	if w.tagged[0].Query != "id:m1" || w.tagged[1].Query != "id:m2" {
		t.Fatalf("ActTag queries = %q, %q", w.tagged[0].Query, w.tagged[1].Query)
	}

	// dry-run: the same classification, zero writes
	cfg.Filter.DryRun = true
	dry := &fakeWorker{delta: w.delta, snaps: w.snaps, header: w.header}
	rep, err = New(dry, cfg, root).Run(0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.DryRun || len(rep.Entries) != 2 || len(dry.tagged) != 0 {
		t.Fatalf("dry-run: report %+v, writes %d", rep, len(dry.tagged))
	}
}

func equalOps(a, b []core.TagOp) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
