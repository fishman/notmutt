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
	delta   []core.Message
	snaps   []core.Message
	header  map[string]bool // ids the header-rule query matches
	tagged  []notmuch.Action
	pathOps []notmuch.Action
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
	case notmuch.ActAddPaths, notmuch.ActRemovePaths:
		f.pathOps = append(f.pathOps, a)
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

// TestEntryPriority: an entry whose resolved tag set contains a
// [notify] priority tag is flagged; one without is not.
// TestHeaderRuleDedup: two matching header rules adding the same tag
// resolve to one op - the report and the apply carry the set, not the
// raw rule emissions (a dry-run digest rendered ++meeting).
func TestHeaderRuleDedup(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	root := filepath.Join(dir, "mail")
	if err := os.MkdirAll(filepath.Join(root, "gmail", "INBOX", "cur"), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Accounts = map[string]config.Account{"gmail": {Preset: "gmail"}}
	cfg.Filter.HeaderRules = []config.HeaderRule{
		{Query: "from:x", Add: []string{"work"}},
		{Query: "from:y", Add: []string{"work"}},
	}
	w := &fakeWorker{
		delta:  []core.Message{{ID: "m1"}},
		snaps:  []core.Message{{ID: "m1", Tags: []string{"inbox"}, Paths: []string{filepath.Join(root, "gmail/INBOX/cur/1")}}},
		header: map[string]bool{"m1": true},
	}
	rep, err := New(w, cfg, root).Run(0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(rep.Entries))
	}
	adds := 0
	for _, op := range rep.Entries[0].Ops {
		if op.Add && op.Tag == "work" {
			adds++
		}
	}
	if adds != 1 {
		t.Fatalf("work ops = %d, want 1 (deduped): %+v", adds, rep.Entries[0].Ops)
	}
}

func TestEntryPriority(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	root := filepath.Join(dir, "mail")
	if err := os.MkdirAll(filepath.Join(root, "gmail", "INBOX", "cur"), 0o700); err != nil {
		t.Fatal(err)
	}
	mark := filepath.Join(dir, ".cache", "mail-sync-mark")
	if err := os.MkdirAll(filepath.Dir(mark), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(mark, nil, 0o600)

	cfg := config.Default()
	cfg.Accounts = map[string]config.Account{"gmail": {Preset: "gmail"}}
	cfg.Filter.HeaderRules = []config.HeaderRule{{Query: "from:x", Add: []string{"work"}}}
	cfg.Filter.DryRun = true
	cfg.Notify.Priority = []string{"work"}

	w := &fakeWorker{
		delta:  []core.Message{{ID: "m1"}},
		snaps:  []core.Message{{ID: "m1", Tags: []string{"inbox"}, Paths: []string{"gmail/INBOX/cur/2"}}},
		header: map[string]bool{"m1": true},
	}
	rep, err := New(w, cfg, root).Run(0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) != 1 || !rep.Entries[0].Priority {
		t.Fatalf("priority entry missing: %+v", rep.Entries)
	}
	cfg.Notify.Priority = []string{"urgent"}
	rep, err = New(w, cfg, root).Run(0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) != 1 || rep.Entries[0].Priority {
		t.Fatalf("unrelated priority tag flagged: %+v", rep.Entries)
	}
}

func TestMover(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "mail")
	// the gmail account shape: INBOX and Archives exist (the resolved
	// archive target - first candidate wins over "Archive"); AWS is an
	// organizational folder the mover leaves alone.
	for _, d := range []string{"INBOX", "Archives", "AWS"} {
		if err := os.MkdirAll(filepath.Join(root, "gmail", d, "cur"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	src := filepath.Join(root, "gmail", "INBOX", "cur", "1")
	if err := os.WriteFile(src, []byte("mail"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "gmail", "Archives", "cur", "2"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(root, "gmail", "AWS", "cur", "3"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(root, "gmail", "INBOX", "cur", "4"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(root, "gmail", "Archives", "cur", "4"), []byte("x"), 0o600)

	cfg := config.Default()
	cfg.Accounts = map[string]config.Account{"gmail": {Preset: "gmail"}}
	cfg.Filter.DryRun = false
	w := &fakeWorker{}
	rep := &Report{Entries: []Entry{
		{ID: "m1", Account: "gmail", Folder: "archive", Paths: []string{"gmail/INBOX/cur/1"}},
		{ID: "m2", Account: "gmail", Folder: "archive", Paths: []string{"gmail/Archives/cur/2"}},
		{ID: "m3", Account: "gmail", Folder: "archive", Paths: []string{"gmail/AWS/cur/3"}},
		{ID: "m4", Account: "gmail", Folder: "archive", Paths: []string{"gmail/INBOX/cur/4"}},
		{ID: "m5", Account: "gmail", Folder: "archive", Paths: []string{"gmail/INBOX/cur/5"}},
		{ID: "m6", Folder: ""},
	}}

	mr, err := NewMover(w, cfg, root).Move(rep)
	if err != nil {
		t.Fatal(err)
	}
	if len(mr.Moves) != 5 {
		t.Fatalf("moves = %d, want 5: %+v", len(mr.Moves), mr.Moves)
	}
	if got := mr.Moves[0]; got.To != "gmail/Archives/cur/1" || got.Skip != "" {
		t.Fatalf("m1 = %+v, want move to gmail/Archives/cur/1", got)
	}
	wantSkips := []struct {
		id, skip string
	}{{"m2", "already home"}, {"m3", "not managed"}, {"m4", "dest exists"}, {"m5", "source gone"}}
	for i, want := range wantSkips {
		if got := mr.Moves[i+1]; got.ID != want.id || got.Skip != want.skip || got.To != "" {
			t.Fatalf("move %d = %+v, want %s: %s", i+1, got, want.id, want.skip)
		}
	}
	// copy-then-delete: the source moved (content preserved), and the
	// database saw AddPaths before RemovePaths
	got, err := os.ReadFile(filepath.Join(root, "gmail", "Archives", "cur", "1"))
	if err != nil || string(got) != "mail" {
		t.Fatalf("dest = %q, %v", got, err)
	}
	if _, err := os.Stat(src); err == nil {
		t.Fatal("source still exists after the move")
	}
	if len(w.pathOps) != 2 || w.pathOps[0].Kind != notmuch.ActAddPaths || w.pathOps[1].Kind != notmuch.ActRemovePaths {
		t.Fatalf("path ops = %+v, want add then remove", w.pathOps)
	}
	if len(w.pathOps[0].Paths) != 1 || w.pathOps[0].Paths[0] != filepath.Join(root, "gmail/Archives/cur/1") {
		t.Fatalf("add paths = %v", w.pathOps[0].Paths)
	}
	if len(w.pathOps[1].Paths) != 1 || w.pathOps[1].Paths[0] != src {
		t.Fatalf("remove paths = %v", w.pathOps[1].Paths)
	}

	// dry-run: the same report, zero writes
	cfg.Filter.DryRun = true
	src2 := filepath.Join(root, "gmail", "INBOX", "cur", "6")
	os.WriteFile(src2, []byte("x"), 0o600)
	w2 := &fakeWorker{}
	rep2 := &Report{Entries: []Entry{{ID: "m7", Account: "gmail", Folder: "archive", Paths: []string{"gmail/INBOX/cur/6"}}}}
	if _, err := NewMover(w2, cfg, root).Move(rep2); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src2); err != nil {
		t.Fatal("dry-run removed the source")
	}
	if len(w2.pathOps) != 0 {
		t.Fatalf("dry-run path ops = %d, want 0", len(w2.pathOps))
	}
}

// TestAlreadyTaggedMoves: a hard tag the message already carries (manual
// apply, an external tool) must physically move the file - the mover
// works off the tag's folder, not just the ops this pass produced. Mail
// that already sits in its tag's folder produces no entry (no report
// noise on every read-marked delta row).
func TestAlreadyTaggedMoves(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	root := filepath.Join(dir, "mail")
	for _, d := range []string{"INBOX", "Spam"} {
		if err := os.MkdirAll(filepath.Join(root, "gmail", d, "cur"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	src := filepath.Join(root, "gmail", "INBOX", "cur", "1")
	if err := os.WriteFile(src, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Accounts = map[string]config.Account{"gmail": {Preset: "gmail"}}
	cfg.Filter.DryRun = false
	w := &fakeWorker{
		delta: []core.Message{{ID: "m1"}, {ID: "m2"}},
		snaps: []core.Message{
			{ID: "m1", Tags: []string{"gmail", "spam"}, Paths: []string{"gmail/INBOX/cur/1"}},
			{ID: "m2", Tags: []string{"gmail", "archive"}, Paths: []string{"gmail/Archives/cur/2"}},
		},
	}
	rep, err := New(w, cfg, root).Run(0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) != 1 {
		t.Fatalf("entries = %d, want 1 (already-home m2 must not appear): %+v", len(rep.Entries), rep.Entries)
	}
	e := rep.Entries[0]
	if e.ID != "m1" || e.Folder != "spam" || len(e.Ops) != 0 {
		t.Fatalf("entry = %+v, want m1 folder spam with no ops", e)
	}
	if len(w.tagged) != 0 {
		t.Fatalf("ActTag calls = %d, want 0 (no new ops)", len(w.tagged))
	}
	// the mover physically follows the tag
	if _, err := NewMover(w, cfg, root).Move(rep); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "gmail", "Spam", "cur", "1")); err != nil {
		t.Fatalf("spam-tagged mail did not move: %v", err)
	}
	if _, err := os.Stat(src); err == nil {
		t.Fatal("source still exists after the move")
	}
}

// TestMoverStripsMbsyncUID: an mbsync filename (the IMAP UID embedded
// as ,U=NNN) moves WITHOUT the UID - a copy keeping it would collide
// with mbsync's UID tracking on the next sync ("duplicate UID 1234").
// Plain maildir names pass through untouched; detection is the
// marker's presence, never a config option (afew rename=auto).
func TestMoverStripsMbsyncUID(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "mail")
	for _, d := range []string{"INBOX", "Archives"} {
		if err := os.MkdirAll(filepath.Join(root, "gmail", d, "cur"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	mbsync := filepath.Join(root, "gmail", "INBOX", "cur", "1234567890_1_1.host:2,S,U=42")
	if err := os.WriteFile(mbsync, []byte("mail"), 0o600); err != nil {
		t.Fatal(err)
	}
	plain := filepath.Join(root, "gmail", "INBOX", "cur", "1234567890_2_2.host:2,S")
	if err := os.WriteFile(plain, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Accounts = map[string]config.Account{"gmail": {Preset: "gmail"}}
	cfg.Filter.DryRun = false
	w := &fakeWorker{}
	rep := &Report{Entries: []Entry{
		{ID: "m1", Account: "gmail", Folder: "archive", Paths: []string{"gmail/INBOX/cur/1234567890_1_1.host:2,S,U=42"}},
		{ID: "m2", Account: "gmail", Folder: "archive", Paths: []string{"gmail/INBOX/cur/1234567890_2_2.host:2,S"}},
	}}

	mr, err := NewMover(w, cfg, root).Move(rep)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"gmail/Archives/cur/1234567890_1_1.host:2,S", "gmail/Archives/cur/1234567890_2_2.host:2,S"}
	for i, to := range want {
		if mr.Moves[i].To != to {
			t.Fatalf("move %d To = %q, want %q", i, mr.Moves[i].To, to)
		}
		if _, err := os.Stat(filepath.Join(root, to)); err != nil {
			t.Fatalf("dest %s: %v", to, err)
		}
	}
	if _, err := os.Stat(mbsync); err == nil {
		t.Fatal("mbsync source still exists")
	}
}

// TestMoverReadOnlyAccount: a readonly account (R2 - toptal, a dead
// account) never moves: no file ops, no path ops, the source stays.
// Non-readonly accounts in the same report still move.
func TestMoverReadOnlyAccount(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "mail")
	for _, d := range []string{"INBOX", "Archives"} {
		for _, a := range []string{"gmail", "toptal"} {
			if err := os.MkdirAll(filepath.Join(root, a, d, "cur"), 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}
	src := filepath.Join(root, "toptal", "INBOX", "cur", "1")
	if err := os.WriteFile(src, []byte("mail"), 0o600); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "gmail", "INBOX", "cur", "2"), []byte("x"), 0o600)

	cfg := config.Default()
	cfg.Accounts = map[string]config.Account{
		"gmail":  {Preset: "gmail"},
		"toptal": {Preset: "gmail", ReadOnly: true},
	}
	cfg.Filter.DryRun = false
	w := &fakeWorker{}
	rep := &Report{Entries: []Entry{
		{ID: "m1", Account: "toptal", Folder: "archive", Paths: []string{"toptal/INBOX/cur/1"}},
		{ID: "m2", Account: "gmail", Folder: "archive", Paths: []string{"gmail/INBOX/cur/2"}},
	}}

	mr, err := NewMover(w, cfg, root).Move(rep)
	if err != nil {
		t.Fatal(err)
	}
	if len(mr.Moves) != 1 || mr.Moves[0].ID != "m2" {
		t.Fatalf("moves = %+v, want only the gmail move", mr.Moves)
	}
	if _, err := os.Stat(src); err != nil {
		t.Fatal("readonly source was moved")
	}
	if len(w.pathOps) != 2 {
		t.Fatalf("path ops = %+v, want only the gmail add+remove", w.pathOps)
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
