// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"notmutt/compose"
	"notmutt/config"
	"notmutt/core"
	"notmutt/mail"
	"notmutt/notmuch"
)

// threadBackend serves one canned thread for replyPrefill's fetch path.
type threadBackend struct {
	msgs []core.Message
}

func (t *threadBackend) Open(ctx context.Context, p string) error { return nil }
func (t *threadBackend) Close(ctx context.Context) error          { return nil }
func (t *threadBackend) Query(ctx context.Context, q string, limit int, flat bool, emit func([]core.Message) bool) error {
	return nil
}
func (t *threadBackend) QueryMsgs(ctx context.Context, q string, emit func([]core.Message) bool) error {
	return nil
}
func (t *threadBackend) Snapshots(ctx context.Context, ids []string) ([]notmuch.Message, error) {
	return nil, nil
}
func (t *threadBackend) Count(ctx context.Context, q string) (int, error) { return len(t.msgs), nil }
func (t *threadBackend) CountMsgs(ctx context.Context, q string) (int, error) {
	return len(t.msgs), nil
}
func (t *threadBackend) Addresses(ctx context.Context, q string) ([]core.AddressEntry, error) {
	return nil, nil
}
func (t *threadBackend) Thread(ctx context.Context, id string) ([]core.Message, error) {
	return t.msgs, nil
}
func (t *threadBackend) Tag(ctx context.Context, q string, ops []notmuch.TagOp) error { return nil }
func (t *threadBackend) AddPaths(ctx context.Context, paths []string) error           { return nil }
func (t *threadBackend) RemovePaths(ctx context.Context, paths []string) error        { return nil }
func (t *threadBackend) Revision(ctx context.Context) (string, uint64, error) {
	return "uuid-1", 42, nil
}
func (t *threadBackend) New(ctx context.Context) (uint64, uint64, error) { return 41, 42, nil }
func (t *threadBackend) Reopen(ctx context.Context) error                { return nil }

func TestMailtoCompose(t *testing.T) {
	cfg := config.Default()
	cfg.Accounts = map[string]config.Account{"acme": {From: "me@acme.com"}}
	root := t.TempDir()

	st, err := mailtoCompose(cfg, root,
		"mailto:alpha@example.com,beta@example.com?subject=Hi%20there&cc=cc@example.com&bcc=secret@example.com&body=line1%0Aline2")
	if err != nil {
		t.Fatalf("mailtoCompose: %v", err)
	}
	if st.Mode != compose.ModeCompose {
		t.Fatalf("mode = %v, want compose", st.Mode)
	}
	if st.Account != "acme" || st.From != "me@acme.com" {
		t.Fatalf("sender = %q/%q, want acme/me@acme.com", st.Account, st.From)
	}
	if len(st.To) != 2 || st.To[0] != "alpha@example.com" || st.To[1] != "beta@example.com" {
		t.Fatalf("to = %v", st.To)
	}
	if len(st.Cc) != 1 || st.Cc[0] != "cc@example.com" {
		t.Fatalf("cc = %v", st.Cc)
	}
	if len(st.Bcc) != 1 || st.Bcc[0] != "secret@example.com" {
		t.Fatalf("bcc = %v", st.Bcc)
	}
	if st.Subject != "Hi there" {
		t.Fatalf("subject = %q", st.Subject)
	}
	if st.Body != "line1\nline2" {
		t.Fatalf("body = %q", st.Body)
	}
	if st.Fcc == "" {
		t.Fatal("fcc unset")
	}

	// a bare mailto:?subject with no recipient is valid (RFC 6068)
	st, err = mailtoCompose(cfg, root, "mailto:?subject=Just%20a%20note")
	if err != nil {
		t.Fatalf("empty-to mailtoCompose: %v", err)
	}
	if len(st.To) != 0 || st.Subject != "Just a note" {
		t.Fatalf("empty-to: to=%v subject=%q", st.To, st.Subject)
	}

	if _, err := mailtoCompose(cfg, root, "https://example.com/"); err == nil {
		t.Fatal("non-mailto url accepted")
	}
}

func TestResolveAccountChain(t *testing.T) {
	cfg := config.Default()
	cfg.Accounts = map[string]config.Account{"acme": {}, "globex": {}, "nimbus": {}}
	if got := resolveAccount(cfg, []string{"inbox", "acme", "work"}, nil); got != "acme" {
		t.Fatalf("message tag first: %q", got)
	}
	if got := resolveAccount(cfg, []string{"inbox"}, []string{"globex"}); got != "globex" {
		t.Fatalf("cursor fallback: %q", got)
	}
	if got := resolveAccount(cfg, nil, nil); got != "acme" {
		// generated accounts are sorted, first wins
		t.Fatalf("first account fallback: %q", got)
	}
}

func TestDefaultSig(t *testing.T) {
	cfg := config.Default()
	cfg.Accounts = map[string]config.Account{
		"acme":   {DefaultSignature: "personal"},
		"globex": {},
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "acme"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "acme", "personal"), []byte("sig text\n"), 0600); err != nil {
		t.Fatal(err)
	}
	old := sigDir
	sigDir = dir
	defer func() { sigDir = old }()

	name, body := defaultSig(cfg, "acme")
	if name != "personal" || body != "sig text" {
		t.Fatalf("default sig = %q %q", name, body)
	}
	if name, _ := defaultSig(cfg, "globex"); name != "" {
		t.Fatalf("account without default must resolve empty, got %q", name)
	}
	to := cfg.Accounts["globex"]
	to.DefaultSignature = "absent"
	cfg.Accounts["globex"] = to
	if name, body := defaultSig(cfg, "globex"); name != "" || body != "" {
		t.Fatalf("missing signature file must resolve empty, got %q %q", name, body)
	}
}

func TestBuildComposeReply(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "mail")
	path := filepath.Join(dir, "msg.eml")
	eml := "From: Alice <alice@example.com>\n" +
		"To: Bob <bob@example.com>, Carole <carole@example.com>\n" +
		"Subject: hello\n" +
		"Message-Id: <m1@example.com>\n" +
		"Date: Tue, 14 Aug 2026 10:00:00 +0000\n\n" +
		"body line\n"
	if err := os.WriteFile(path, []byte(eml), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	g := cfg.Accounts["gmail"]
	g.From = "Bob <bob@example.com>"
	g.Folders = map[string]string{"sent": "Sent"}
	g.DefaultSignature = ""
	cfg.Accounts["gmail"] = g
	view := core.NewView("inbox", "tag:inbox")
	msg := &core.Message{
		ID: "<m1@example.com>", ThreadID: "t1",
		Timestamp: time.Date(2026, 8, 14, 10, 0, 0, 0, time.UTC).Unix(),
		Author:    "Alice <alice@example.com>", Subject: "hello",
		Tags:  []string{"inbox", "gmail"},
		Paths: []string{path},
	}

	st := buildCompose(cfg, view, msg, "reply", root)
	if st == nil {
		t.Fatal("reply must build")
	}
	if st.Account != "gmail" || st.From != "Bob <bob@example.com>" {
		t.Fatalf("account/from = %q %q", st.Account, st.From)
	}
	if len(st.To) != 1 || st.To[0] != "alice@example.com" {
		t.Fatalf("To = %v", st.To)
	}
	if st.MessageID != "<m1@example.com>" || st.OriginalID != "<m1@example.com>" {
		t.Fatalf("ids = %q %q", st.MessageID, st.OriginalID)
	}
	if st.Fcc != filepath.Join(root, "gmail", "Sent") {
		t.Fatalf("Fcc = %q, want the derived sent folder", st.Fcc)
	}

	if st := buildCompose(cfg, view, msg, "reply-all", root); st.Mode != compose.ModeReplyAll || len(st.Cc) != 1 || st.Cc[0] != "carole@example.com" {
		t.Fatalf("reply-all must exclude the own address: %+v", st)
	}
	if st := buildCompose(cfg, view, msg, "forward", root); len(st.To) != 0 {
		t.Fatalf("forward must have no To: %+v", st)
	}
	if st := buildCompose(cfg, view, nil, "compose", root); st == nil || st.Mode != compose.ModeCompose {
		t.Fatalf("blank compose must build: %+v", st)
	}
	if st := buildCompose(cfg, view, nil, "reply", root); st != nil {
		t.Fatal("reply without a message must return nil")
	}
}

func TestReplyPrefillFetchesThreadFromIndexRow(t *testing.T) {
	dir := t.TempDir()
	mroot := filepath.Join(dir, "mail")
	rootPath := filepath.Join(dir, "root.eml")
	replyPath := filepath.Join(dir, "reply.eml")
	root := "From: Alice <alice@example.com>\n" +
		"To: Bob <bob@example.com>\n" +
		"Subject: hello\n" +
		"Message-Id: <m1@example.com>\n" +
		"Date: Tue, 11 Aug 2026 10:00:00 +0000\n\n" +
		"root body\n"
	repl := "From: Dave <dave@example.com>\n" +
		"To: Bob <bob@example.com>\n" +
		"Subject: Re: hello\n" +
		"Message-Id: <m2@example.com>\n" +
		"Date: Wed, 12 Aug 2026 10:00:00 +0000\n\n" +
		"reply body\n"
	if err := os.WriteFile(rootPath, []byte(root), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replyPath, []byte(repl), 0600); err != nil {
		t.Fatal(err)
	}

	bus := core.NewBus()
	worker := notmuch.NewWorker(bus, &threadBackend{msgs: []core.Message{
		{ID: "<m1@example.com>", ThreadID: "t1",
			Timestamp: time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC).Unix(),
			Author:    "Alice <alice@example.com>", Subject: "hello",
			Tags: []string{"inbox", "gmail"}, Paths: []string{rootPath}},
		{ID: "<m2@example.com>", ThreadID: "t1",
			Timestamp: time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC).Unix(),
			Author:    "Dave <dave@example.com>", Subject: "Re: hello",
			Tags: []string{"inbox", "gmail"}, Paths: []string{replyPath}},
	}}, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go worker.Start(ctx)

	cfg := config.Default()
	g := cfg.Accounts["gmail"]
	g.From = "Bob <bob@example.com>"
	g.Folders = map[string]string{"sent": "Sent"}
	cfg.Accounts["gmail"] = g
	view := core.NewView("inbox", "tag:inbox")
	// the index row: thread summary only - no id, no paths
	row := &core.Message{ThreadID: "t1",
		Timestamp: time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC).Unix(),
		Author:    "Alice <alice@example.com>, Dave <dave@example.com>",
		Subject:   "Re: hello", Tags: []string{"inbox", "gmail"}}

	st, err := replyPrefill(cfg, view, worker, row, "reply", mroot)
	if st == nil || err != nil {
		t.Fatalf("index-row reply must fetch the thread and build: st=%v err=%v", st, err)
	}
	if len(st.To) != 1 || st.To[0] != "dave@example.com" {
		t.Fatalf("To = %v, want the newest message's sender", st.To)
	}
	if st.OriginalID != "<m2@example.com>" {
		t.Fatalf("OriginalID = %q, want the newest message", st.OriginalID)
	}
	if st.Fcc != filepath.Join(mroot, "gmail", "Sent") {
		t.Fatalf("Fcc = %q, want the derived sent folder", st.Fcc)
	}

	st, err = replyPrefill(cfg, view, worker, row, "reply-all", mroot)
	if err != nil || st.Mode != compose.ModeReplyAll || len(st.Cc) != 0 {
		t.Fatalf("reply-all: st=%+v err=%v", st, err)
	}
	st, err = replyPrefill(cfg, view, worker, row, "forward", mroot)
	if err != nil || st.Mode != compose.ModeForward || len(st.To) != 0 {
		t.Fatalf("forward: st=%+v err=%v", st, err)
	}
	st, err = replyPrefill(cfg, view, worker, row, "compose", mroot)
	if err != nil || st.Mode != compose.ModeCompose {
		t.Fatalf("blank compose must not fetch: st=%+v err=%v", st, err)
	}
	st, err = replyPrefill(cfg, view, worker, nil, "reply", mroot)
	if st != nil || err != nil {
		t.Fatalf("nil message must stay nil: st=%v err=%v", st, err)
	}
}

// TestReplyPrefillFallsThroughBrokenNewest: the thread's newest message
// has a vanished file, so the reply builds from the older parseable
// message instead of failing silently.
func TestReplyPrefillFallsThroughBrokenNewest(t *testing.T) {
	dir := t.TempDir()
	mroot := filepath.Join(dir, "mail")
	rootPath := filepath.Join(dir, "root.eml")
	root := "From: Alice <alice@example.com>\n" +
		"To: Bob <bob@example.com>\n" +
		"Subject: hello\n" +
		"Message-Id: <m1@example.com>\n" +
		"Date: Tue, 11 Aug 2026 10:00:00 +0000\n\n" +
		"root body\n"
	if err := os.WriteFile(rootPath, []byte(root), 0600); err != nil {
		t.Fatal(err)
	}
	bus := core.NewBus()
	worker := notmuch.NewWorker(bus, &threadBackend{msgs: []core.Message{
		{ID: "<m1@example.com>", ThreadID: "t1",
			Timestamp: time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC).Unix(),
			Author:    "Alice <alice@example.com>", Subject: "hello",
			Tags: []string{"inbox", "gmail"}, Paths: []string{rootPath}},
		{ID: "<m2@example.com>", ThreadID: "t1",
			Timestamp: time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC).Unix(),
			Author:    "Dave <dave@example.com>", Subject: "Re: hello",
			Tags: []string{"inbox", "gmail"}, Paths: []string{filepath.Join(dir, "gone.eml")}},
	}}, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go worker.Start(ctx)

	cfg := config.Default()
	g := cfg.Accounts["gmail"]
	g.From = "Bob <bob@example.com>"
	cfg.Accounts["gmail"] = g
	view := core.NewView("inbox", "tag:inbox")
	row := &core.Message{ThreadID: "t1",
		Timestamp: time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC).Unix(),
		Subject:   "Re: hello", Tags: []string{"inbox", "gmail"}}

	st, err := replyPrefill(cfg, view, worker, row, "reply", mroot)
	if err != nil || st == nil {
		t.Fatalf("an older parseable message must build: st=%v err=%v", st, err)
	}
	if st.OriginalID != "<m1@example.com>" {
		t.Fatalf("OriginalID = %q, want the older parseable message", st.OriginalID)
	}
	if len(st.To) != 1 || st.To[0] != "alice@example.com" {
		t.Fatalf("To = %v", st.To)
	}
}

// TestReplyPrefillNoParseable: when no thread message parses, the
// prefill returns an error (the handler logs it and publishes JobError)
// - a reply never fails silently.
func TestReplyPrefillNoParseable(t *testing.T) {
	bus := core.NewBus()
	worker := notmuch.NewWorker(bus, &threadBackend{msgs: []core.Message{
		{ID: "<m2@example.com>", ThreadID: "t1",
			Timestamp: time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC).Unix(),
			Author:    "Dave <dave@example.com>", Subject: "Re: hello",
			Tags: []string{"inbox", "gmail"}, Paths: []string{"/nonexistent/msg.eml"}},
	}}, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go worker.Start(ctx)

	cfg := config.Default()
	view := core.NewView("inbox", "tag:inbox")
	row := &core.Message{ThreadID: "t1", Timestamp: 0}

	st, err := replyPrefill(cfg, view, worker, row, "reply", "")
	if st != nil || err == nil {
		t.Fatalf("no parseable message must error: st=%v err=%v", st, err)
	}
}

// buildDraft writes a real saved draft (the client's own Assemble
// shape) with one file attachment and returns its path.
func buildDraft(t *testing.T, attachContent []byte) string {
	t.Helper()
	dir := t.TempDir()
	att := filepath.Join(dir, "data.txt")
	if err := os.WriteFile(att, attachContent, 0600); err != nil {
		t.Fatal(err)
	}
	st := compose.NewCompose("gmail", "bob@example.com", "", "")
	st.To = []string{"alice@example.com"}
	st.Bcc = []string{"hidden@example.net"}
	st.Subject = "resume me"
	st.Body = "line one\n\t tab"
	if err := st.AddAttachment(att); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := st.Assemble(&buf); err != nil {
		t.Fatal(err)
	}
	draft := filepath.Join(dir, "draft.eml")
	if err := os.WriteFile(draft, buf.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return draft
}

// TestResumePrefill: a draft-tagged message with a path parses back
// into a dialogue - envelope restored (Bcc included), body as authored,
// the attachment mapped to the stored draft (DraftPart), ResumePath
// set, fcc resolved, and the default signature NOT injected.
func TestResumePrefill(t *testing.T) {
	root := t.TempDir()
	draft := buildDraft(t, []byte("attachment bytes"))
	cfg := config.Default()
	cfg.Accounts["gmail"] = config.Account{From: "bob@example.com", Folders: map[string]string{"sent": "Sent"}}
	view := core.NewView("inbox", "tag:inbox")
	msg := &core.Message{ID: "id:x", ThreadID: "thread:x", Tags: []string{"draft", "gmail"}, Paths: []string{draft}}

	st, err := resumePrefill(cfg, view, nil, msg, root)
	if err != nil {
		t.Fatal(err)
	}
	if st == nil {
		t.Fatal("a draft row must resume")
	}
	if st.Subject != "resume me" || len(st.To) != 1 || st.To[0] != "alice@example.com" {
		t.Fatalf("envelope: %q %v", st.Subject, st.To)
	}
	if len(st.Bcc) != 1 || st.Bcc[0] != "hidden@example.net" {
		t.Fatalf("Bcc must restore: %v", st.Bcc)
	}
	if st.Body != "line one\n\t tab" {
		t.Fatalf("Body = %q (tab must survive)", st.Body)
	}
	if st.ResumePath != draft {
		t.Fatalf("ResumePath = %q", st.ResumePath)
	}
	if len(st.Attachments) != 1 {
		t.Fatalf("attachments = %+v", st.Attachments)
	}
	a := st.Attachments[0]
	if a.Name != "data.txt" || a.Path != draft || a.DraftPart != 1 {
		t.Fatalf("attachment = %+v, want the stored-draft stream (DraftPart 1)", a)
	}
	if a.Size == 0 || a.MimeType == "" {
		t.Fatalf("attachment carries no size/type: %+v", a)
	}
	var got bytes.Buffer
	if _, err := mail.WriteDraftAttachment(draft, 0, &got); err != nil {
		t.Fatal(err)
	}
	if got.String() != "attachment bytes" {
		t.Fatalf("the draft stream = %q, want the attachment bytes", got.String())
	}
	if st.Signature != "" || st.SignatureBody != "" {
		t.Fatalf("resume must not inject a default signature: %q %q", st.Signature, st.SignatureBody)
	}
	if want := filepath.Join(root, "gmail", "Sent"); st.Fcc != want {
		t.Fatalf("Fcc = %q, want %q", st.Fcc, want)
	}
}

// TestResumePrefillGate: a non-draft row is a silent no-op (nil, nil);
// so is a nil message.
func TestResumePrefillGate(t *testing.T) {
	cfg := config.Default()
	cfg.Accounts["gmail"] = config.Account{}
	view := core.NewView("inbox", "tag:inbox")

	st, err := resumePrefill(cfg, view, nil, &core.Message{ID: "id:i", Tags: []string{"inbox"}, Paths: []string{"/x"}}, t.TempDir())
	if err != nil || st != nil {
		t.Fatalf("an inbox row must no-op: %v %+v", err, st)
	}
	st, err = resumePrefill(cfg, view, nil, nil, t.TempDir())
	if err != nil || st != nil {
		t.Fatalf("nil must no-op: %v %+v", err, st)
	}
}
