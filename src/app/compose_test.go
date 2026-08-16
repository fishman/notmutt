package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"notmutt/compose"
	"notmutt/config"
	"notmutt/core"
	"notmutt/notmuch"
)

// threadBackend serves one canned thread for replyPrefill's fetch path.
type threadBackend struct {
	msgs []core.Message
}

func (t *threadBackend) Open(ctx context.Context, p string) error { return nil }
func (t *threadBackend) Close(ctx context.Context) error          { return nil }
func (t *threadBackend) Query(ctx context.Context, q string, limit int, emit func([]core.Message) bool) error {
	return nil
}
func (t *threadBackend) QueryMsgs(ctx context.Context, q string, emit func([]core.Message) bool) error {
	return nil
}
func (t *threadBackend) Snapshots(ctx context.Context, ids []string) ([]notmuch.Message, error) {
	return nil, nil
}
func (t *threadBackend) Count(ctx context.Context, q string) (int, error) { return len(t.msgs), nil }
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
func (t *threadBackend) New(ctx context.Context) error { return nil }

func TestResolveAccountChain(t *testing.T) {
	cfg := config.Default()
	if got := resolveAccount(cfg, []string{"inbox", "gmail", "work"}, nil); got != "gmail" {
		t.Fatalf("message tag first: %q", got)
	}
	if got := resolveAccount(cfg, []string{"inbox"}, []string{"dynamia"}); got != "dynamia" {
		t.Fatalf("cursor fallback: %q", got)
	}
	if got := resolveAccount(cfg, nil, nil); got != "dynamia" {
		// default accounts are gmail, jelveh, toptal, dynamia - sorted,
		// first is dynamia
		t.Fatalf("first account fallback: %q", got)
	}
}

func TestDefaultSig(t *testing.T) {
	cfg := config.Default()
	g := cfg.Accounts["gmail"]
	g.DefaultSignature = "personal"
	cfg.Accounts["gmail"] = g
	cfg.Accounts["dynamia"] = config.Account{}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "gmail"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gmail", "personal"), []byte("sig text\n"), 0600); err != nil {
		t.Fatal(err)
	}
	old := sigDir
	sigDir = dir
	defer func() { sigDir = old }()

	name, body := defaultSig(cfg, "gmail")
	if name != "personal" || body != "sig text" {
		t.Fatalf("default sig = %q %q", name, body)
	}
	if name, _ := defaultSig(cfg, "dynamia"); name != "" {
		t.Fatalf("account without default must resolve empty, got %q", name)
	}
	to := cfg.Accounts["toptal"]
	to.DefaultSignature = "absent"
	cfg.Accounts["toptal"] = to
	if name, body := defaultSig(cfg, "toptal"); name != "" || body != "" {
		t.Fatalf("missing signature file must resolve empty, got %q %q", name, body)
	}
}

func TestBuildComposeReply(t *testing.T) {
	dir := t.TempDir()
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
	g.SentFolder = "/tmp/sent"
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

	st := buildCompose(cfg, view, msg, "reply")
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
	if st.Fcc != "/tmp/sent" {
		t.Fatalf("Fcc = %q, want the account sent_folder", st.Fcc)
	}

	if st := buildCompose(cfg, view, msg, "reply-all"); st.Mode != compose.ModeReplyAll || len(st.Cc) != 1 || st.Cc[0] != "carole@example.com" {
		t.Fatalf("reply-all must exclude the own address: %+v", st)
	}
	if st := buildCompose(cfg, view, msg, "forward"); len(st.To) != 0 {
		t.Fatalf("forward must have no To: %+v", st)
	}
	if st := buildCompose(cfg, view, nil, "compose"); st == nil || st.Mode != compose.ModeCompose {
		t.Fatalf("blank compose must build: %+v", st)
	}
	if st := buildCompose(cfg, view, nil, "reply"); st != nil {
		t.Fatal("reply without a message must return nil")
	}
}

func TestReplyPrefillFetchesThreadFromIndexRow(t *testing.T) {
	dir := t.TempDir()
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
	g.SentFolder = "/tmp/sent"
	cfg.Accounts["gmail"] = g
	view := core.NewView("inbox", "tag:inbox")
	// the index row: thread summary only - no id, no paths
	row := &core.Message{ThreadID: "t1",
		Timestamp: time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC).Unix(),
		Author:    "Alice <alice@example.com>, Dave <dave@example.com>",
		Subject:   "Re: hello", Tags: []string{"inbox", "gmail"}}

	st := replyPrefill(cfg, view, worker, row, "reply")
	if st == nil {
		t.Fatal("index-row reply must fetch the thread and build")
	}
	if len(st.To) != 1 || st.To[0] != "dave@example.com" {
		t.Fatalf("To = %v, want the newest message's sender", st.To)
	}
	if st.OriginalID != "<m2@example.com>" {
		t.Fatalf("OriginalID = %q, want the newest message", st.OriginalID)
	}
	if st.Fcc != "/tmp/sent" {
		t.Fatalf("Fcc = %q", st.Fcc)
	}

	st = replyPrefill(cfg, view, worker, row, "reply-all")
	if st.Mode != compose.ModeReplyAll || len(st.Cc) != 0 {
		t.Fatalf("reply-all: %+v", st)
	}
	st = replyPrefill(cfg, view, worker, row, "forward")
	if st.Mode != compose.ModeForward || len(st.To) != 0 {
		t.Fatalf("forward: %+v", st)
	}
	if st := replyPrefill(cfg, view, worker, row, "compose"); st.Mode != compose.ModeCompose {
		t.Fatalf("blank compose must not fetch: %+v", st)
	}
	if st := replyPrefill(cfg, view, worker, nil, "reply"); st != nil {
		t.Fatal("nil message must stay nil")
	}
}
