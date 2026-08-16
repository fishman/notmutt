package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"notmutt/compose"
	"notmutt/config"
	"notmutt/core"
	"notmutt/notmuch"
)

// stubWorker records the actions a send job issues (ActNew, ActTag).
type stubWorker struct {
	actions []notmuch.Action
}

func (w *stubWorker) Call(a notmuch.Action) (notmuch.Reply, error) {
	w.actions = append(w.actions, a)
	return notmuch.Reply{}, nil
}

func TestSendJobFccStateWins(t *testing.T) {
	dir := t.TempDir()
	captured := filepath.Join(dir, "captured")
	stub := "#!/bin/sh\ncat > " + captured + "\n"
	if err := os.WriteFile(filepath.Join(dir, "send-stub"), []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Send = config.Send{Command: filepath.Join(dir, "send-stub")}
	cfg.Accounts["gmail"] = config.Account{SentFolder: filepath.Join(dir, "account-sent")}

	bus := core.NewBus()
	ch := bus.Subscribe()
	view := core.NewView("inbox", "tag:inbox")
	w := &stubWorker{}

	st := compose.NewCompose("gmail", "bob@example.com", "", "")
	st.ID = "tab1"
	st.To = []string{"a@b.c"}
	st.Subject = "x"
	st.Body = "y"
	st.Fcc = filepath.Join(dir, "state-sent")

	sendJob(bus, w, view, cfg, *st)

	if e := (<-ch).(core.SendResult); !e.OK {
		t.Fatalf("send failed: %v %q", e.Err, e.Output)
	}
	if entries, err := os.ReadDir(filepath.Join(dir, "state-sent", "new")); err != nil || len(entries) != 1 {
		t.Fatalf("the dialogue Fcc must win over the account sent_folder: %v %v", entries, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "account-sent")); !os.IsNotExist(err) {
		t.Fatal("the account sent_folder must not receive the copy")
	}
}

func TestSendJobDelivers(t *testing.T) {
	dir := t.TempDir()
	captured := filepath.Join(dir, "captured")
	stub := "#!/bin/sh\ncat > " + captured + "\n"
	if err := os.WriteFile(filepath.Join(dir, "send-stub"), []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}
	sent := filepath.Join(dir, "sent")
	cfg := config.Default()
	cfg.Send = config.Send{Command: filepath.Join(dir, "send-stub")}
	cfg.Accounts["gmail"] = config.Account{SentFolder: sent}
	cfg.Accounts["jelveh"] = config.Account{}

	bus := core.NewBus()
	ch := bus.Subscribe()
	view := core.NewView("inbox", "tag:inbox")
	w := &stubWorker{}

	st := compose.NewCompose("gmail", "Bob <bob@example.com>", "", "")
	st.ID = "tab1"
	st.To = []string{"alice@example.com"}
	st.Subject = "hello"
	st.Body = "the message body"

	sendJob(bus, w, view, cfg, *st)

	e := (<-ch).(core.SendResult)
	if !e.OK {
		t.Fatalf("send failed: %v %q", e.Err, e.Output)
	}
	data, err := os.ReadFile(captured)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Subject: hello") || !strings.Contains(string(data), "the message body") {
		t.Fatalf("transport must receive the assembled message:\n%s", data)
	}
	// fcc: one file in sent/new, 0600, the same bytes
	entries, err := os.ReadDir(filepath.Join(sent, "new"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("fcc files: %v %v", entries, err)
	}
	fi, err := entries[0].Info()
	if err != nil || fi.Mode().Perm() != 0600 {
		t.Fatalf("fcc perms: %v %v", fi.Mode(), err)
	}
	fcc, err := os.ReadFile(filepath.Join(sent, "new", entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if string(fcc) != string(data) {
		t.Fatal("fcc must be the exact assembled bytes")
	}
	// ActNew ran, and no reply tag without an original
	if len(w.actions) != 1 || w.actions[0].Kind != notmuch.ActNew {
		t.Fatalf("actions = %+v", w.actions)
	}
}

func TestSendJobTagsOriginalOnReply(t *testing.T) {
	dir := t.TempDir()
	stub := "#!/bin/sh\ncat > " + filepath.Join(dir, "captured") + "\n"
	if err := os.WriteFile(filepath.Join(dir, "send-stub"), []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Send = config.Send{Command: filepath.Join(dir, "send-stub")}
	cfg.Accounts["gmail"] = config.Account{SentFolder: filepath.Join(dir, "sent")}

	bus := core.NewBus()
	view := core.NewView("inbox", "tag:inbox")
	w := &stubWorker{}

	st := compose.NewCompose("gmail", "bob@example.com", "", "")
	st.ID = "tab2"
	st.To = []string{"a@b.c"}
	st.Subject = "x"
	st.Body = "y"
	st.Mode = compose.ModeReply
	st.OriginalID = "<orig@example.com>"

	sendJob(bus, w, view, cfg, *st)
	// the SendResult on the bus goes unread (drop-on-full is fine for
	// one message); the assertions run on the recorded actions
	if len(w.actions) != 2 {
		t.Fatalf("actions = %+v", w.actions)
	}
	if w.actions[1].Kind != notmuch.ActTag {
		t.Fatalf("second action must be the reply tag: %+v", w.actions[1])
	}
	if w.actions[1].Query != "id:\"<orig@example.com>\"" {
		t.Fatalf("tag query = %q", w.actions[1].Query)
	}
	if len(w.actions[1].TagOps) != 1 || w.actions[1].TagOps[0].Tag != "replied" || !w.actions[1].TagOps[0].Add {
		t.Fatalf("tag ops = %+v", w.actions[1].TagOps)
	}
}

func TestSendJobForwardTagAndQuoteEscape(t *testing.T) {
	dir := t.TempDir()
	stub := "#!/bin/sh\ncat > /dev/null\n"
	if err := os.WriteFile(filepath.Join(dir, "send-stub"), []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Send = config.Send{Command: filepath.Join(dir, "send-stub")}
	bus := core.NewBus()
	view := core.NewView("inbox", "tag:inbox")
	w := &stubWorker{}

	st := compose.NewCompose("gmail", "bob@example.com", "", "")
	st.ID = "tab5"
	st.To = []string{"a@b.c"}
	st.Subject = "x"
	st.Body = "y"
	st.Mode = compose.ModeForward
	st.OriginalID = "<a\"b@example.com>"

	sendJob(bus, w, view, cfg, *st)

	if len(w.actions) != 2 {
		t.Fatalf("actions = %+v", w.actions)
	}
	tag := w.actions[1]
	if tag.Query != "id:\"<a\"\"b@example.com>\"" {
		t.Fatalf("tag query = %q", tag.Query)
	}
	if len(tag.TagOps) != 1 || tag.TagOps[0].Tag != "forwarded" || !tag.TagOps[0].Add {
		t.Fatalf("tag ops = %+v", tag.TagOps)
	}
}

func TestSendJobFailureKeepsDialogue(t *testing.T) {
	dir := t.TempDir()
	stub := "#!/bin/sh\necho 'msmtp exploded' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "send-stub"), []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Send = config.Send{Command: filepath.Join(dir, "send-stub")}
	cfg.Accounts["gmail"] = config.Account{SentFolder: filepath.Join(dir, "sent")}

	bus := core.NewBus()
	ch := bus.Subscribe()
	view := core.NewView("inbox", "tag:inbox")
	w := &stubWorker{}

	st := compose.NewCompose("gmail", "bob@example.com", "", "")
	st.ID = "tab3"
	st.To = []string{"a@b.c"}
	st.Subject = "x"
	st.Body = "y"

	sendJob(bus, w, view, cfg, *st)

	e := (<-ch).(core.SendResult)
	if e.OK {
		t.Fatal("a failed transport must not report OK")
	}
	if !strings.Contains(e.Output, "msmtp exploded") {
		t.Fatalf("the captured output must be kept: %q", e.Output)
	}
	if len(w.actions) != 0 {
		t.Fatalf("a failed send must not fcc or tag: %+v", w.actions)
	}
	if _, err := os.Stat(filepath.Join(dir, "sent")); !os.IsNotExist(err) {
		t.Fatal("a failed send must not create the sent dir")
	}
}

// TestSendJobPassesEnvelopeRecipients pins the mutt sendmail
// contract: the transport argv is the configured args plus the
// envelope recipients (To + Cc + Bcc) - msmtp without them fails
// with "no recipients found". The config's args slice is never
// mutated.
func TestSendJobPassesEnvelopeRecipients(t *testing.T) {
	dir := t.TempDir()
	argv := filepath.Join(dir, "argv")
	stub := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argv + "\ncat > /dev/null\n"
	if err := os.WriteFile(filepath.Join(dir, "send-stub"), []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Send = config.Send{Command: filepath.Join(dir, "send-stub"), Args: []string{"--read-envelope-from"}}
	cfg.Accounts["gmail"] = config.Account{SentFolder: filepath.Join(dir, "sent")}

	bus := core.NewBus()
	ch := bus.Subscribe()
	view := core.NewView("inbox", "tag:inbox")
	w := &stubWorker{}

	st := compose.NewCompose("gmail", "bob@example.com", "", "")
	st.ID = "tab6"
	st.To = []string{"alice@example.com"}
	st.Cc = []string{"cc@example.org"}
	st.Bcc = []string{"bcc@example.net"}
	st.Subject = "x"
	st.Body = "y"

	sendJob(bus, w, view, cfg, *st)

	if e := (<-ch).(core.SendResult); !e.OK {
		t.Fatalf("send failed: %v %q", e.Err, e.Output)
	}
	data, err := os.ReadFile(argv)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	want := []string{"--read-envelope-from", "alice@example.com", "cc@example.org", "bcc@example.net"}
	if !slices.Equal(got, want) {
		t.Fatalf("transport argv = %v, want %v", got, want)
	}
	if len(cfg.Send.Args) != 1 || cfg.Send.Args[0] != "--read-envelope-from" {
		t.Fatalf("the config args must not mutate: %v", cfg.Send.Args)
	}
}

func TestSendJobFccErrorNotesButDelivers(t *testing.T) {
	dir := t.TempDir()
	stub := "#!/bin/sh\ncat > /dev/null\n"
	if err := os.WriteFile(filepath.Join(dir, "send-stub"), []byte(stub), 0755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Send = config.Send{Command: filepath.Join(dir, "send-stub")}
	// an unwritable sent path (the dir does not exist and its parent
	// path is a FILE)
	if err := os.WriteFile(filepath.Join(dir, "blocker"), []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.Accounts["gmail"] = config.Account{SentFolder: filepath.Join(dir, "blocker", "sent")}

	bus := core.NewBus()
	ch := bus.Subscribe()
	view := core.NewView("inbox", "tag:inbox")
	w := &stubWorker{}

	st := compose.NewCompose("gmail", "bob@example.com", "", "")
	st.ID = "tab4"
	st.To = []string{"a@b.c"}
	st.Subject = "x"
	st.Body = "y"

	sendJob(bus, w, view, cfg, *st)

	e := (<-ch).(core.SendResult)
	if !e.OK {
		t.Fatalf("a delivered message must report OK even with a fcc error: %v", e.Err)
	}
	if !strings.Contains(e.Output, "fcc") {
		t.Fatalf("the fcc note must surface: %q", e.Output)
	}
}
