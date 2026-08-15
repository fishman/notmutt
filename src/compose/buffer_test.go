package compose

import (
	"strings"
	"testing"
)

func TestBuildBuffer(t *testing.T) {
	s := NewCompose("gmail", "Bob <bob@example.com>", "gmail", "bob\nbob2")
	s.To = []string{"a@example.com", "b@example.org"}
	s.Cc = []string{"c@example.net"}
	s.Subject = "hello"
	s.Body = "line one"
	want := "To: a@example.com, b@example.org\n" +
		"Cc: c@example.net\n" +
		"Subject: hello\n\n" +
		"line one\n\n" +
		"-- \nbob\nbob2"
	if got := s.BuildBuffer(); got != want {
		t.Fatalf("buffer:\n%q\nwant:\n%q", got, want)
	}
}

func TestParseBufferRoundTrip(t *testing.T) {
	s := NewCompose("gmail", "Bob <bob@example.com>", "gmail", "bob")
	s.To = []string{"a@example.com", "b@example.org"}
	s.Cc = []string{"c@example.net"}
	s.Subject = "Re: hello"
	s.Body = "> quoted\nsecond line"
	to, cc, subject, body, sigName, sigBody := ParseBuffer(s.BuildBuffer(), s.Signature, s.SignatureBody)
	if len(to) != 2 || to[0] != "a@example.com" || to[1] != "b@example.org" {
		t.Fatalf("to = %v", to)
	}
	if len(cc) != 1 || cc[0] != "c@example.net" {
		t.Fatalf("cc = %v", cc)
	}
	if subject != "Re: hello" || body != "> quoted\nsecond line" {
		t.Fatalf("subject/body = %q %q", subject, body)
	}
	if sigName != "gmail" || sigBody != "bob" {
		t.Fatalf("sig = %q %q", sigName, sigBody)
	}
}

func TestParseBufferEditedSignatureDetaches(t *testing.T) {
	buf := "To: a@example.com\nCc: \nSubject: x\n\nbody\n\n-- \nbob\nEDITED"
	to, _, subject, body, sigName, sigBody := ParseBuffer(buf, "gmail", "bob")
	if to[0] != "a@example.com" || subject != "x" {
		t.Fatalf("headers = %v %q", to, subject)
	}
	// the edited tail stays as the user's text; the signature detaches
	if body != "body\n\n-- \nbob\nEDITED" {
		t.Fatalf("body = %q", body)
	}
	if sigName != "" || sigBody != "" {
		t.Fatalf("edited tail must detach the signature: %q %q", sigName, sigBody)
	}
}

func TestParseBufferNoSeparator(t *testing.T) {
	// the spec contract: a buffer without the separator blank line
	// parses as all-headers, empty body
	to, _, subject, body, _, _ := ParseBuffer("To: a@example.com\nSubject: x", "", "")
	if len(to) != 1 || subject != "x" || body != "" {
		t.Fatalf("no-separator parse = %v %q %q", to, subject, body)
	}
}

func TestParseBufferDropsUnknownHeaders(t *testing.T) {
	to, _, _, body, _, _ := ParseBuffer("To: a@example.com\nX-Custom: keep\nSubject: x\n\nbody", "", "")
	if len(to) != 1 || body != "body" {
		t.Fatalf("unknown header dropped: %v %q", to, body)
	}
}

func TestParseBufferBlankFields(t *testing.T) {
	to, cc, _, _, _, _ := ParseBuffer("To: a@example.com, , b@example.org\nCc: \nSubject: \n\nbody", "", "")
	if len(to) != 2 || to[1] != "b@example.org" {
		t.Fatalf("to = %v", to)
	}
	if len(cc) != 0 {
		t.Fatalf("cc = %v", cc)
	}
}

func TestParseBufferCRLF(t *testing.T) {
	s := NewCompose("gmail", "bob@example.com", "sig", "sig body")
	s.To = []string{"a@b.c"}
	s.Subject = "hello"
	s.Body = "line1\nline2"
	buf := strings.ReplaceAll(s.BuildBuffer(), "\n", "\r\n")
	to, cc, subject, body, sigName, sigBody := ParseBuffer(buf, s.Signature, s.SignatureBody)
	if len(to) != 1 || to[0] != "a@b.c" || subject != "hello" || body != "line1\nline2" || sigName != "sig" || sigBody != "sig body" {
		t.Fatalf("CRLF round trip: %v %v %q %q %q %q", to, cc, subject, body, sigName, sigBody)
	}
}
