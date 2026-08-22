// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package compose

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"notmutt/mail"
)

// TestAssembleRoundTripParse pins re-parseability: the assembled bytes
// must round-trip through mail.ParseMessage - a sent message is the
// reply prefill's parse source (the newest thread message is often the
// sender's own copy). The wire tests pin shapes; this pins the parser.
func TestAssembleRoundTripParse(t *testing.T) {
	st := NewCompose("alpha", "alpha@example.com", "", "")
	st.Subject = "round trip"
	st.To = []string{"bob@example.com"}
	st.Body = "hello world\n\nthis is the body\n"
	var buf bytes.Buffer
	if err := st.Assemble(&buf); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "m")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := mail.ParseMessage(path)
	if err != nil {
		t.Fatalf("single-part round trip: %v\nwire:\n%s", err, buf.String())
	}
	if m.Subject != "round trip" || len(m.To) != 1 || m.To[0] != "bob@example.com" {
		t.Fatalf("headers wrong: %+v", m)
	}

	st2 := NewCompose("alpha", "alpha@example.com", "", "")
	st2.Subject = "attached"
	st2.To = []string{"bob@example.com"}
	st2.Body = "see the file\n"
	att := filepath.Join(t.TempDir(), "doc.txt")
	if err := os.WriteFile(att, []byte("the file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := st2.AddAttachment(att); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := st2.Assemble(&buf); err != nil {
		t.Fatal(err)
	}
	path2 := filepath.Join(t.TempDir(), "m2")
	if err := os.WriteFile(path2, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := mail.ParseMessage(path2); err != nil {
		t.Fatalf("multipart round trip: %v\nwire:\n%s", err, buf.String())
	}
}
