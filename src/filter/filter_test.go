package filter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	root := filepath.Join(dir, "mail")
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

// TestReadOnlyAccountNeverClassified: a readonly account's message is
// not classified at all - no folder tags, no account tag, no header
// tags, no writes. The entry drops out of the report even when the
// message's tags contradict its folder.
func TestReadOnlyAccountNeverClassified(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "mail")
	cfg := config.Default()
	cfg.Accounts = map[string]config.Account{
		"toptal": {Preset: "gmail", ReadOnly: true},
	}
	cfg.Filter.HeaderRules = []config.HeaderRule{{Query: "from:x", Add: []string{"work"}}}
	cfg.Filter.DryRun = false

	w := &fakeWorker{
		delta: []core.Message{{ID: "m1"}},
		snaps: []core.Message{
			{ID: "m1", Tags: []string{"gmail", "archive"}, Paths: []string{filepath.Join(root, "toptal/INBOX/cur/1")}},
		},
		header: map[string]bool{"m1": true},
	}

	rep, err := New(w, cfg, root).Run(0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) != 0 {
		t.Fatalf("entries = %+v, want none for a readonly account", rep.Entries)
	}
	if len(w.tagged) != 0 {
		t.Fatalf("readonly mail must never be tagged: %+v", w.tagged)
	}
}

// TestEntryPriority: an entry whose resolved tag set contains a
// [notify] priority tag is flagged; one without is not.
// TestHeaderRuleDedup: two matching header rules adding the same tag
// resolve to one op - the report and the apply carry the set, not the
// raw rule emissions (a dry-run digest rendered ++meeting).
func TestHeaderRuleDedup(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "mail")
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
	root := filepath.Join(dir, "mail")
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

// TestStaleFolderTagResolvesToLocation: a hard tag the message already
// carries while the file sits in another folder is STALE - the location
// is the home, the exclusive resolution drops the tag (the user's
// model: tag-groups.folder members match the account folders, and the
// member whose folder holds the file is the one that applies). Mail
// already home produces no entry (no report noise on every read-marked
// delta row).
func TestStaleFolderTagResolvesToLocation(t *testing.T) {
	cfg := config.Default()
	cfg.Accounts = map[string]config.Account{"gmail": {Preset: "gmail"}}
	w := &fakeWorker{
		delta: []core.Message{{ID: "m1"}, {ID: "m2"}, {ID: "m3"}},
		snaps: []core.Message{
			{ID: "m1", Tags: []string{"gmail", "spam"}, Paths: []string{"gmail/INBOX/cur/1"}},
			{ID: "m2", Tags: []string{"gmail", "archive"}, Paths: []string{"gmail/Archives/cur/2"}},
			{ID: "m3", Tags: []string{"gmail", "spam"}, Paths: []string{"gmail/Archives/cur/3"}},
		},
	}
	rep, err := New(w, cfg, "mail").Run(0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) != 2 {
		t.Fatalf("entries = %d, want 2 (already-home m2 must not appear): %+v", len(rep.Entries), rep.Entries)
	}
	e1 := rep.Entries[0]
	if e1.ID != "m1" || e1.Folder != "" || !equalOps(e1.Ops, []core.TagOp{{Tag: "inbox", Add: true}, {Tag: "spam", Add: false}}) {
		t.Fatalf("entry = %+v, want m1 -spam +inbox, no move", e1)
	}
	e3 := rep.Entries[1]
	if e3.ID != "m3" || e3.Folder != "archive" || !equalOps(e3.Ops, []core.TagOp{{Tag: "archive", Add: true}, {Tag: "spam", Add: false}}) {
		t.Fatalf("entry = %+v, want m3 -spam +archive", e3)
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

// TestTwoCopyResolvesToSent: a message with files in Sent and INBOX
// (the self-send shape - the client's fcc copy in Sent plus the
// mbsync-delivered copy in INBOX, one message id) resolves to sent:
// the location pass emits both members and the last member-add wins
// (the emission order is the reference priority ascending,
// muttrc/notmuch/tags - sent beats inbox), so the stale inbox tag
// drops while the message stays sent. Nothing moves.
func TestTwoCopyResolvesToSent(t *testing.T) {
	cfg := config.Default()
	cfg.Accounts = map[string]config.Account{"jelveh": {Preset: "gmail"}}
	w := &fakeWorker{
		delta: []core.Message{{ID: "m1"}},
		snaps: []core.Message{{
			ID:    "m1",
			Tags:  []string{"inbox", "sent", "jelveh", "unread", "replied", "newsletter"},
			Paths: []string{"jelveh/Sent/cur/1", "jelveh/INBOX/cur/2"},
		}},
	}
	rep, err := New(w, cfg, "mail").Run(0, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Entries) != 1 {
		t.Fatalf("entries = %d, want 1: %+v", len(rep.Entries), rep.Entries)
	}
	e := rep.Entries[0]
	if !equalOps(e.Ops, []core.TagOp{{Tag: "inbox", Add: false}}) {
		t.Fatalf("ops = %+v, want -inbox (sent wins, nothing else touched)", e.Ops)
	}
	if e.Folder != "" {
		t.Fatalf("folder = %q, want empty (already home, no move)", e.Folder)
	}
}

// TestMoverSkipsMessageAlreadyHome: a message with one of its files
// already in the resolved target tree (the self-send shape - the fcc
// copy in Sent plus the delivered copy in INBOX) moves nothing: the
// message is home, and the mbsync-owned delivered copy must never be
// touched (a move breaks its UID bookkeeping and the next sync
// re-downloads it).
func TestMoverSkipsMessageAlreadyHome(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "mail")
	for _, d := range []string{"INBOX", "Sent"} {
		if err := os.MkdirAll(filepath.Join(root, "jelveh", d, "cur"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	fcc := filepath.Join(root, "jelveh", "Sent", "cur", "123.notmutt:2,S")
	delivered := filepath.Join(root, "jelveh", "INBOX", "cur", "456.host:2,S,U=42")
	for _, f := range []string{fcc, delivered} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cfg := config.Default()
	cfg.Accounts = map[string]config.Account{"jelveh": {Preset: "gmail"}}
	cfg.Filter.DryRun = false
	w := &fakeWorker{}
	rep := &Report{Entries: []Entry{
		{ID: "m1", Account: "jelveh", Folder: "sent", Paths: []string{
			"jelveh/Sent/cur/123.notmutt:2,S",
			"jelveh/INBOX/cur/456.host:2,S,U=42",
		}},
	}}

	mr, err := NewMover(w, cfg, root).Move(rep)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range mr.Moves {
		if m.To != "" || m.Skip != "already home" {
			t.Fatalf("move = %+v, want skip already home", m)
		}
	}
	if _, err := os.Stat(delivered); err != nil {
		t.Fatal("delivered copy was moved")
	}
	if len(w.pathOps) != 0 {
		t.Fatalf("path ops = %+v, want none", w.pathOps)
	}
}
