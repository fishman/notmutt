// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMailcapPreview pins the preview contract: a copiousoutput entry
// tokenizes its command (quotes stripped, %s = the temp path), exact
// type overrides a built-in, "*" wildcard serves the rest, non-copious
// never previews.
func TestMailcapPreview(t *testing.T) {
	script := filepath.Join(t.TempDir(), "cat.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ncat \"$1\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	mc := DefaultMailcap()
	mc.Parse([]byte("application/pdf; /bin/true %s ;\n" +
		"audio/*; /bin/true %s ;\n" +
		"image/*; '" + script + "' %s; copiousoutput\n" +
		"text/html; elinks -dump '%s'; nametemplate=%s.html; copiousoutput;\n"))
	if _, ok := mc.PreviewCommand("application/pdf"); ok {
		t.Fatal("a non-copious entry must override the built-in pdf preview")
	}
	if _, ok := mc.PreviewCommand("audio/mpeg"); ok {
		t.Fatal("a non-copious wildcard must not preview its types")
	}
	if argv, ok := mc.PreviewCommand("image/png"); !ok || argv[0] != script {
		t.Fatalf("the wildcard entry must serve image/png: %v %v", argv, ok)
	}
	if argv, ok := mc.PreviewCommand("text/html"); !ok || strings.Join(argv, " ") != "elinks -dump %s" {
		t.Fatalf("the html entry must tokenize with quotes stripped: %v", argv)
	}
	out, err := RunPreview([]string{script, "%s"}, []byte("payload"))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "payload" {
		t.Fatalf("the preview must round-trip the data through the temp file, got %q", out)
	}
}
