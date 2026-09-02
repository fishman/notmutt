// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"notmutt/config"
	"notmutt/core"
	"notmutt/notmuch"
)

// fixtureMail writes a multipart/mixed mail (pdfName + photo.png) and
// returns its path. Fabricated data only.
func fixtureMail(t *testing.T, dir, name, subject, from, pdfName string, ts time.Time) string {
	t.Helper()
	msg := "From: " + from + "\nTo: alpha@example.com\nSubject: " + subject + "\n" +
		"Date: " + ts.Format(time.RFC1123Z) + "\nMIME-Version: 1.0\n" +
		"Content-Type: multipart/mixed; boundary=x\n\n" +
		"--x\nContent-Type: text/plain; charset=utf-8\n\nbody text\n" +
		"--x\nContent-Type: application/pdf\nContent-Disposition: attachment; filename=\"" + pdfName + "\"\n\nfake pdf bytes\n" +
		"--x\nContent-Type: image/png\nContent-Disposition: attachment; filename=\"photo.png\"\n\nfake png bytes\n" +
		"--x--\n"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(msg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSanitizeSegment(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"invoice.pdf", "invoice.pdf"},
		{"a/b\\c", "a_b_c"},
		{"a\x1bb", "ab"}, // control runes dropped (the F1 rule)
		{"", ""},
		{".", ""},
		{"..", ""},
	} {
		if got := sanitizeSegment(c.in); got != c.want {
			t.Fatalf("sanitizeSegment(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAttachmentFolder(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := config.Default()
	if got := attachmentFolder(cfg); got != filepath.Join(home, "Downloads", "Attachments") {
		t.Fatalf("default folder = %q", got)
	}
	cfg.Attachments.Folder = "~/dl"
	if got := attachmentFolder(cfg); got != filepath.Join(home, "dl") {
		t.Fatalf("~ folder = %q", got)
	}
	cfg.Attachments.Folder = "/abs"
	if got := attachmentFolder(cfg); got != "/abs" {
		t.Fatalf("absolute folder = %q", got)
	}
}

func TestAttachmentTarget(t *testing.T) {
	ts := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	meta := AttachMeta{Date: ts.Unix()}
	// a bare category slots into the layout (the legacy shape)
	if got := attachmentTarget("/dl", "YYYY-MM", meta, "travel", "boarding-pass.pdf"); got != "/dl/2026-08/travel/boarding-pass.pdf" {
		t.Fatalf("category target = %q", got)
	}
	// the layout is a config pattern, not a fixed month dir
	if got := attachmentTarget("/dl", "YYYY/MM", meta, "travel", "boarding-pass.pdf"); got != "/dl/2026/08/travel/boarding-pass.pdf" {
		t.Fatalf("nested layout target = %q", got)
	}
	// empty layout = no date directory
	if got := attachmentTarget("/dl", "", meta, "travel", "boarding-pass.pdf"); got != "/dl/travel/boarding-pass.pdf" {
		t.Fatalf("no-date target = %q", got)
	}
	// a full path is used verbatim: the plugin owns structure and filename, the layout is bypassed
	if got := attachmentTarget("/dl", "YYYY-MM", meta, "travel/flights/Boarding Pass.pdf", "x.pdf"); got != "/dl/travel/flights/Boarding Pass.pdf" {
		t.Fatalf("full path target = %q", got)
	}
	// traversal segments are dropped, never joined
	if got := attachmentTarget("/dl", "YYYY-MM", meta, "travel/../x.pdf", "x.pdf"); got != "/dl/travel/x.pdf" {
		t.Fatalf("traversal target = %q", got)
	}
	for _, c := range []struct{ rel, name string }{
		{"", "x.pdf"}, {"travel", ""}, {".", "x.pdf"}, {"..", "x.pdf"},
	} {
		if got := attachmentTarget("/dl", "YYYY-MM", meta, c.rel, c.name); got != "" {
			t.Fatalf("unsafe %q/%q must be rejected, got %q", c.rel, c.name, got)
		}
	}
}

func TestSaveMessageAttachments(t *testing.T) {
	saved := categorizeHooks
	defer func() { categorizeHooks = saved }()
	// the hook fetches the list through the handle it received - the
	// registry round trip is the contract
	RegisterCategorizeHook(func(handle string, m AttachMeta) (map[int]string, error) {
		atts, ok := attachmentsForHandle(handle)
		if !ok || len(atts) == 0 || atts[0].Name != "invoice.pdf" {
			return nil, nil
		}
		return map[int]string{1: "receipt"}, nil
	})
	dir := t.TempDir()
	ts := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	path := fixtureMail(t, dir, "m1.eml", "hotel invoice", "Delta <delta@example.com>", "invoice.pdf", ts)
	meta := AttachMeta{From: "delta@example.com", Subject: "hotel invoice", Date: ts.Unix()}
	dl := filepath.Join(dir, "dl")
	target := filepath.Join(dl, "2026-08", "receipt", "invoice.pdf")

	saves := saveMessageAttachments(path, meta, dl, "YYYY-MM", true)
	if len(saves) != 1 || saves[0].Target != target || saves[0].Exists {
		t.Fatalf("dry-run plan = %+v, want the single save at %s", saves, target)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("a dry run must not write")
	}

	saves = saveMessageAttachments(path, meta, dl, "YYYY-MM", false)
	if len(saves) != 1 || saves[0].Err != nil || saves[0].Exists {
		t.Fatalf("live saves = %+v, want one clean save", saves)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "fake pdf bytes" {
		t.Fatalf("saved bytes = %q", data)
	}
	fi, err := os.Stat(target)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v (err %v), want 0600", fi.Mode(), err)
	}
	di, err := os.Stat(filepath.Dir(target))
	if err != nil || di.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v (err %v), want 0700", di.Mode(), err)
	}

	before := fi.ModTime()
	saves = saveMessageAttachments(path, meta, dl, "YYYY-MM", false)
	if len(saves) != 1 || !saves[0].Exists {
		t.Fatalf("re-run = %+v, want the exists skip", saves)
	}
	after, err := os.Stat(target)
	if err != nil || !after.ModTime().Equal(before) {
		t.Fatal("the exists skip must not rewrite")
	}
}

// TestSaveMessageAttachmentsHookError: a hook error surfaces as the
// message's Err entry (the review surface), nothing saves for that
// message - a clean message still saves.
func TestSaveMessageAttachmentsHookError(t *testing.T) {
	saved := categorizeHooks
	defer func() { categorizeHooks = saved }()
	RegisterCategorizeHook(func(handle string, m AttachMeta) (map[int]string, error) {
		if atts, ok := attachmentsForHandle(handle); ok && len(atts) > 0 && atts[0].Name == "invoice.pdf" {
			return nil, errors.New("boom")
		}
		return map[int]string{1: "photo"}, nil
	})
	dir := t.TempDir()
	ts := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	meta := AttachMeta{Date: ts.Unix()}
	p1 := fixtureMail(t, dir, "m1.eml", "hotel invoice", "Delta <delta@example.com>", "invoice.pdf", ts)
	saves := saveMessageAttachments(p1, meta, filepath.Join(dir, "dl"), "YYYY-MM", false)
	if len(saves) != 1 || saves[0].Err == nil {
		t.Fatalf("the hook error must surface as the message entry: %+v", saves)
	}
	p2 := fixtureMail(t, dir, "m2.eml", "airline receipt", "Delta <delta@example.com>", "boarding.pdf", ts)
	saves = saveMessageAttachments(p2, meta, filepath.Join(dir, "dl"), "YYYY-MM", false)
	if len(saves) != 1 || saves[0].Err != nil || !strings.HasSuffix(saves[0].Target, filepath.Join("photo", "boarding.pdf")) {
		t.Fatalf("a clean message must still save: %+v", saves)
	}
}

// TestSaveMessageAttachmentsOutOfRange: a category key beyond the
// message's attachment count is an error entry; the in-range saves
// proceed.
func TestSaveMessageAttachmentsOutOfRange(t *testing.T) {
	saved := categorizeHooks
	defer func() { categorizeHooks = saved }()
	RegisterCategorizeHook(func(handle string, m AttachMeta) (map[int]string, error) {
		return map[int]string{1: "receipt", 5: "receipt"}, nil
	})
	dir := t.TempDir()
	ts := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	path := fixtureMail(t, dir, "m1.eml", "hotel invoice", "Delta <delta@example.com>", "invoice.pdf", ts)
	saves := saveMessageAttachments(path, AttachMeta{Date: ts.Unix()}, filepath.Join(dir, "dl"), "YYYY-MM", false)
	if len(saves) != 2 {
		t.Fatalf("saves = %+v, want the clean save and the out-of-range error", saves)
	}
	if saves[0].Err != nil || !strings.HasSuffix(saves[0].Target, filepath.Join("receipt", "invoice.pdf")) {
		t.Fatalf("the in-range save must proceed: %+v", saves[0])
	}
	if saves[1].Err == nil || !strings.Contains(saves[1].Err.Error(), "out of range") {
		t.Fatalf("the out-of-range ordinal must error: %+v", saves[1])
	}
}

func TestParseAttachmentsSpec(t *testing.T) {
	for _, c := range []struct {
		name      string
		args      []string
		wantDry   bool
		wantQuery string
		wantErr   bool
	}{
		{"no args", nil, false, "*", false},
		{"dry run", []string{"--dry-run"}, true, "*", false},
		{"query", []string{"tag:inbox"}, false, "tag:inbox", false},
		{"dry run and query", []string{"--dry-run", "has:attachment"}, true, "has:attachment", false},
		{"two queries", []string{"a", "b"}, false, "", true},
		{"unknown flag", []string{"-x"}, false, "", true},
	} {
		dry, q, err := parseAttachmentsSpec(c.args)
		if (err != nil) != c.wantErr || dry != c.wantDry || q != c.wantQuery {
			t.Fatalf("%s: parseAttachmentsSpec(%v) = %v %q %v", c.name, c.args, dry, q, err)
		}
	}
}

// recWorker wraps fjWorker and records the queries it was asked for.
type recWorker struct {
	fjWorker
	queries []string
}

func (w *recWorker) Call(a notmuch.Action) (notmuch.Reply, error) {
	if a.Kind == notmuch.ActQueryMsgs {
		w.queries = append(w.queries, a.Query)
	}
	return w.fjWorker.Call(a)
}

// TestCategorizeThread pins the hotkey pass: thread:<tid> messages,
// save/skip lines and tallies as CategorizeResult, errors publish
// instead of wedging.
func TestCategorizeThread(t *testing.T) {
	saved := categorizeHooks
	defer func() { categorizeHooks = saved }()
	RegisterCategorizeHook(func(handle string, m AttachMeta) (map[int]string, error) {
		return map[int]string{1: "receipt"}, nil
	})
	dir := t.TempDir()
	ts := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	p := fixtureMail(t, dir, "m1.eml", "hotel invoice", "Delta <delta@example.com>", "invoice.pdf", ts)
	bus := core.NewBus()
	ch := bus.Subscribe()
	w := &recWorker{fjWorker: fjWorker{
		delta: []core.Message{{ID: "m1"}},
		snaps: []core.Message{
			{ID: "m1", Author: "delta@example.com", Subject: "hotel invoice", Timestamp: ts.Unix(), Paths: []string{p}},
		},
	}}
	cfg := config.Default()
	cfg.Attachments.Folder = filepath.Join(dir, "dl")

	go categorizeThread(w, bus, "t1", &cfg)
	select {
	case e := <-ch:
		res, ok := e.(core.CategorizeResult)
		if !ok {
			t.Fatalf("expected CategorizeResult, got %T", e)
		}
		if res.Err != nil || res.Saved != 1 || res.Skipped != 0 || len(res.Lines) != 1 {
			t.Fatalf("result = %+v, want one save line", res)
		}
		if _, err := os.Stat(filepath.Join(dir, "dl", "2026-08", "receipt", "invoice.pdf")); err != nil {
			t.Fatalf("the hotkey pass must save: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("no CategorizeResult")
	}

	// a second pass skips the existing target (idempotency)
	go categorizeThread(w, bus, "t1", &cfg)
	select {
	case e := <-ch:
		res := e.(core.CategorizeResult)
		if res.Err != nil || res.Saved != 0 || res.Skipped != 1 {
			t.Fatalf("re-run = %+v, want the exists skip", res)
		}
	case <-time.After(time.Second):
		t.Fatal("no CategorizeResult")
	}

	// a stale snapshot path (an external maildir rename between notmuch
	// new runs) degrades to a skip line - the pass never aborts
	go categorizeThread(&recWorker{fjWorker: fjWorker{
		delta: []core.Message{{ID: "m2"}},
		snaps: []core.Message{{ID: "m2", Author: "a@example.com", Subject: "s", Paths: []string{filepath.Join(dir, "gone.eml")}}},
	}}, bus, "t2", &cfg)
	select {
	case e := <-ch:
		res := e.(core.CategorizeResult)
		if res.Err != nil || res.Saved != 0 || len(res.Lines) != 1 || !strings.Contains(res.Lines[0], "skip") {
			t.Fatalf("stale path: want one skip line, no error, got %+v", res)
		}
	case <-time.After(time.Second):
		t.Fatal("no CategorizeResult")
	}

	// a failing query publishes the error, never a hang
	go categorizeThread(failWorker{}, bus, "t1", &cfg)
	select {
	case e := <-ch:
		res := e.(core.CategorizeResult)
		if res.Err == nil {
			t.Fatalf("a failing query must surface: %+v", res)
		}
	case <-time.After(time.Second):
		t.Fatal("no CategorizeResult")
	}
}

// msgIDWorker models real notmuch for the flat-tab categorize case: a
// query with no id: term (thread:<msgid>) matches nothing - thread ids
// are opaque - while an id: term resolves the message. The hotkey pass
// builds the OR fallback; this fake proves a plain thread query on a
// message id stays empty.
type msgIDWorker struct {
	msgs []core.Message
}

func (w *msgIDWorker) Call(a notmuch.Action) (notmuch.Reply, error) {
	r := notmuch.Reply{ID: a.ID}
	switch a.Kind {
	case notmuch.ActQueryMsgs:
		var chunk []core.Message
		for _, id := range queryIDs(a.Query) {
			for _, m := range w.msgs {
				if m.ID == id {
					chunk = append(chunk, core.Message{ID: id})
				}
			}
		}
		if a.Emit != nil {
			a.Emit(chunk)
		}
	case notmuch.ActSnapshots:
		for _, id := range a.Paths {
			for _, m := range w.msgs {
				if m.ID == id {
					r.Msgs = append(r.Msgs, m)
				}
			}
		}
	}
	return r, nil
}

// queryIDs extracts the id:"..." terms from a query. A plain
// thread:<msgid> has none - the model's notion of a matchless query.
func queryIDs(q string) []string {
	var ids []string
	for rest := q; ; {
		i := strings.Index(rest, "id:")
		if i < 0 {
			return ids
		}
		rest = rest[i+3:]
		if open := strings.Index(rest, "\""); open >= 0 {
			rest = rest[open+1:]
			if end := strings.Index(rest, "\""); end >= 0 {
				ids = append(ids, rest[:end])
				rest = rest[end+1:]
			}
		}
	}
}

// TestCategorizeThreadFlatSearchTab: the hotkey pass in a flat search
// tab - the cursor yields the message id as the thread id, and
// thread:<msgid> matches nothing (opaque thread ids), so the pass was a
// silent no-op (the diag log showed "categorize ids=0"). The OR
// fallback must resolve the message by id or nothing saves.
func TestCategorizeThreadFlatSearchTab(t *testing.T) {
	saved := categorizeHooks
	defer func() { categorizeHooks = saved }()
	RegisterCategorizeHook(func(handle string, m AttachMeta) (map[int]string, error) {
		return map[int]string{1: "receipt"}, nil
	})
	dir := t.TempDir()
	ts := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	p := fixtureMail(t, dir, "m1.eml", "hotel invoice", "Delta <delta@example.com>", "invoice.pdf", ts)
	bus := core.NewBus()
	ch := bus.Subscribe()
	msgID := "m1@example.com"
	w := &msgIDWorker{msgs: []core.Message{
		{ID: msgID, Author: "delta@example.com", Subject: "hotel invoice", Timestamp: ts.Unix(), Paths: []string{p}},
	}}
	cfg := config.Default()
	cfg.Attachments.Folder = filepath.Join(dir, "dl")

	go categorizeThread(w, bus, msgID, &cfg)
	select {
	case e := <-ch:
		res, ok := e.(core.CategorizeResult)
		if !ok {
			t.Fatalf("expected CategorizeResult, got %T", e)
		}
		if res.Err != nil || res.Saved != 1 || res.Skipped != 0 {
			t.Fatalf("flat-tab categorize = %+v, want one save (the id fallback resolved it)", res)
		}
		if _, err := os.Stat(filepath.Join(dir, "dl", "2026-08", "receipt", "invoice.pdf")); err != nil {
			t.Fatalf("the flat-tab pass must save: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("no CategorizeResult")
	}
}

// failWorker answers every call with an error - the worker-error path.
type failWorker struct{}

func (failWorker) Call(notmuch.Action) (notmuch.Reply, error) {
	return notmuch.Reply{}, errors.New("boom")
}

// TestRunAttachmentBackfill pins the command body: ActQueryMsgs ids,
// snapshot paths, the per-message save pass - the filter engine's
// two-step. Dry run writes nothing; re-runs skip existing targets.
func TestRunAttachmentBackfill(t *testing.T) {
	saved := categorizeHooks
	defer func() { categorizeHooks = saved }()
	RegisterCategorizeHook(func(handle string, m AttachMeta) (map[int]string, error) {
		return map[int]string{1: "receipt"}, nil
	})
	dir := t.TempDir()
	ts := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	p1 := fixtureMail(t, dir, "m1.eml", "hotel invoice", "Delta <delta@example.com>", "invoice.pdf", ts)
	p2 := fixtureMail(t, dir, "m2.eml", "airline receipt", "Delta <delta@example.com>", "boarding.pdf", ts)
	w := &recWorker{fjWorker: fjWorker{
		delta: []core.Message{{ID: "m1"}, {ID: "m2"}},
		snaps: []core.Message{
			{ID: "m1", Author: "delta@example.com", Subject: "hotel invoice", Timestamp: ts.Unix(), Paths: []string{p1}},
			{ID: "m2", Author: "delta@example.com", Subject: "hotel invoice", Timestamp: ts.Unix(), Paths: []string{p2}},
		},
	}}
	dl := filepath.Join(dir, "dl")
	target := filepath.Join(dl, "2026-08", "receipt", "invoice.pdf")

	savedN, skipped, err := runAttachmentBackfill(w, "", dl, "YYYY-MM", "*", true)
	if err != nil || savedN != 2 || skipped != 0 {
		t.Fatalf("dry-run backfill = %d saved %d skipped err %v", savedN, skipped, err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("a dry run must not write")
	}
	if len(w.queries) != 1 || w.queries[0] != "*" {
		t.Fatalf("the query must pass through, got %v", w.queries)
	}

	savedN, skipped, err = runAttachmentBackfill(w, "", dl, "YYYY-MM", "*", false)
	if err != nil || savedN != 2 || skipped != 0 {
		t.Fatalf("live backfill = %d saved %d skipped err %v", savedN, skipped, err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatal(err)
	}

	savedN, skipped, err = runAttachmentBackfill(w, "", dl, "YYYY-MM", "*", false)
	if err != nil || savedN != 0 || skipped != 2 {
		t.Fatalf("re-run = %d saved %d skipped err %v, want the exists skips", savedN, skipped, err)
	}
}

// TestRunAttachmentBackfillRelative: a snapshot path relative to root
// resolves against it.
func TestRunAttachmentBackfillRelative(t *testing.T) {
	saved := categorizeHooks
	defer func() { categorizeHooks = saved }()
	RegisterCategorizeHook(func(handle string, m AttachMeta) (map[int]string, error) {
		return map[int]string{1: "receipt"}, nil
	})
	dir := t.TempDir()
	ts := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	root := filepath.Join(dir, "mail")
	mailDir := filepath.Join(root, "cur")
	if err := os.MkdirAll(mailDir, 0o700); err != nil {
		t.Fatal(err)
	}
	fixtureMail(t, mailDir, "m1.eml", "hotel invoice", "Delta <delta@example.com>", "invoice.pdf", ts)
	w := &recWorker{fjWorker: fjWorker{
		delta: []core.Message{{ID: "m1"}},
		snaps: []core.Message{
			{ID: "m1", Author: "delta@example.com", Subject: "hotel invoice", Timestamp: ts.Unix(), Paths: []string{"cur/m1.eml"}},
		},
	}}
	dl := filepath.Join(dir, "dl")
	savedN, _, err := runAttachmentBackfill(w, root, dl, "YYYY-MM", "*", false)
	if err != nil || savedN != 1 {
		t.Fatalf("backfill = %d saved err %v", savedN, err)
	}
	if _, err := os.Stat(filepath.Join(dl, "2026-08", "receipt", "invoice.pdf")); err != nil {
		t.Fatalf("the relative path must resolve against root: %v", err)
	}
}
