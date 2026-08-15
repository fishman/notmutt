package compose

import (
	"testing"
)

func TestEventRoundTrip(t *testing.T) {
	s := NewCompose("gmail", "Bob <bob@example.com>", "gmail", "sig")
	s.ID = "t1"
	s.Mode = ModeReplyAll
	s.To = []string{"a@b.c"}
	s.Cc = []string{"c@d.e"}
	s.Subject = "Re: x"
	s.Body = "quoted body"
	s.Attachments = []Attachment{{Name: "n.txt", Path: "/tmp/n.txt", Size: 3}}
	s.MessageID = "<m@x>"
	s.References = []string{"<r@x>"}
	s.OriginalID = "<m@x>"

	e := ToEvent(s)
	if e.TabID != "t1" || e.Mode != "reply-all" || e.Account != "gmail" {
		t.Fatalf("event = %+v", e)
	}
	if len(e.Attachments) != 1 || e.Attachments[0].Path != "/tmp/n.txt" {
		t.Fatalf("event attachments = %+v", e.Attachments)
	}

	got := FromEvent(e)
	if got.ID != "t1" || got.Mode != ModeReplyAll || got.Account != "gmail" {
		t.Fatalf("state = %+v", got)
	}
	if got.Body != "quoted body" || got.SignatureBody != "sig" {
		t.Fatalf("state body/sig = %q %q", got.Body, got.SignatureBody)
	}
	if len(got.Attachments) != 1 || got.Attachments[0].Name != "n.txt" {
		t.Fatalf("state attachments = %+v", got.Attachments)
	}
	if got.OriginalID != "<m@x>" || got.MessageID != "<m@x>" {
		t.Fatalf("state ids = %q %q", got.OriginalID, got.MessageID)
	}
	if len(got.References) != 1 || got.References[0] != "<r@x>" {
		t.Fatalf("state references = %v", got.References)
	}
}

func TestParseModeUnknown(t *testing.T) {
	if parseMode("bogus") != ModeCompose {
		t.Fatal("unknown mode must fall back to compose")
	}
}

func TestExpandHome(t *testing.T) {
	if ExpandHome("/abs/path") != "/abs/path" {
		t.Fatal("absolute paths pass through")
	}
	got := ExpandHome("~/Mail/sent")
	if got == "~/Mail/sent" || got[:1] != "/" {
		t.Fatalf("~ must expand to the home dir, got %q", got)
	}
}
