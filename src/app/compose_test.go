package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"notmutt/compose"
	"notmutt/config"
	"notmutt/core"
)

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
