// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"notmutt/core"
)

// TestExportMessagePins the generic export job end to end: the message
// resolves by thread, its html rides the form's renderer stdin (a stub
// binary copies it to its output arg), and the produced file renames over
// the generated YYYYMMDD-<slug>.pdf name. A failing renderer publishes an
// error and leaves nothing at the target.
func TestExportMessage(t *testing.T) {
	html := "<html><body><p>hi</p></body></html>"
	dir := filepath.Join(t.TempDir(), "exports") + "/"

	msgPath := filepath.Join(t.TempDir(), "msg")
	raw := "From: a@example.com\nTo: b@example.com\nSubject: Test Subject!\n" +
		"MIME-Version: 1.0\nContent-Type: text/html; charset=utf-8\n\n" + html + "\n"
	if err := os.WriteFile(msgPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	fw := &fakeWorker{}
	fw.setMsgs([]core.Message{{
		ID: "a", ThreadID: "t1", Timestamp: time.Date(2019, 1, 2, 0, 0, 0, 0, time.UTC).Unix(),
		Subject: "Test Subject!", Paths: []string{msgPath},
	}})

	stub := filepath.Join(t.TempDir(), "weasyprint")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\ncat > \"$2\"\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	pdf := exportForms["pdf"]
	exportForms["pdf"] = exportForm{ext: pdf.ext, argv: []string{stub}}
	defer func() { exportForms["pdf"] = pdf }()

	bus := core.NewBus()
	ch := bus.Subscribe()
	exportMessage(fw, bus, nil, exportParams{threadID: "t1", msgID: "a", target: dir, form: "pdf", paper: "a4"})

	want := filepath.Join(strings.TrimSuffix(dir, "/"), "20190102-test-subject.pdf")
	select {
	case e := <-ch:
		ev, ok := e.(core.ExportPdfResult)
		if !ok {
			t.Fatalf("the export must publish ExportPdfResult, got %T", e)
		}
		if ev.Err != nil {
			t.Fatal(ev.Err)
		}
		if ev.ThreadID != "t1" {
			t.Fatalf("the export must echo the exported thread, got %q", ev.ThreadID)
		}
		if ev.Path != want {
			t.Fatalf("path = %q, want %q", ev.Path, want)
		}
		data, err := os.ReadFile(want)
		if err != nil || strings.TrimSpace(string(data)) != strings.TrimSpace(printDoc(html, "a4")) {
			t.Fatalf("exported file = %q, %v", data, err)
		}
		if fi, err := os.Stat(want); err != nil || fi.Mode().Perm() != 0o600 {
			t.Fatalf("the exported file must be 0600, mode=%v", fi.Mode().Perm())
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the export result")
	}

	// a literal path target is written verbatim, not renamed by the form
	explicit := filepath.Join(t.TempDir(), "custom.pdf")
	exportMessage(fw, bus, nil, exportParams{threadID: "t1", msgID: "a", target: explicit, form: "pdf", paper: "a4"})
	select {
	case e := <-ch:
		ev, ok := e.(core.ExportPdfResult)
		if !ok || ev.Err != nil {
			t.Fatalf("the literal-path export must succeed, got %+v", e)
		}
		if ev.Path != explicit {
			t.Fatalf("path = %q, want %q", ev.Path, explicit)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the literal-path export")
	}

	// a failing renderer reports the error and leaves no file behind
	exportForms["pdf"] = exportForm{ext: pdf.ext, argv: []string{failStub(t)}}
	failDir := filepath.Join(t.TempDir(), "boom") + "/"
	exportMessage(fw, bus, nil, exportParams{threadID: "t1", msgID: "a", target: failDir, form: "pdf", paper: "a4"})
	select {
	case e := <-ch:
		ev, ok := e.(core.ExportPdfResult)
		if !ok || ev.Err == nil {
			t.Fatalf("the failed renderer must publish an error, got %+v", e)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the failed export result")
	}
	failWant := filepath.Join(strings.TrimSuffix(failDir, "/"), "20190102-test-subject.pdf")
	if _, err := os.Stat(failWant); !os.IsNotExist(err) {
		t.Fatalf("a failed export must leave no file at %s, err=%v", failWant, err)
	}
	if tmps, err := filepath.Glob(filepath.Join(strings.TrimSuffix(failDir, "/"), ".notmutt-export-*.tmp")); err != nil || len(tmps) != 0 {
		t.Fatalf("a failed export must leave no render temp behind, got %v (%v)", tmps, err)
	}
}

// TestPrintDocDeclaresPageAndWrapping pins the print stylesheet: the
// requested paper reaches @page, long text cannot clip at the page edge
// (pre-wrap + max-width), nowrap lines that would set a page-wide
// min-content are made wrappable, and the mail body survives the wrapper.
func TestPrintDocDeclaresPageAndWrapping(t *testing.T) {
	doc := printDoc("<html><body><pre>line\n</pre></body></html>", "letter")
	if !strings.Contains(doc, "@page { size: letter; margin: 1.5cm }") {
		t.Fatalf("printDoc must declare the paper size, got %q", doc)
	}
	if !strings.Contains(doc, "max-width: 100%") {
		t.Fatalf("printDoc must constrain width, got %q", doc)
	}
	if !strings.Contains(doc, "white-space: normal !important") {
		t.Fatalf("printDoc must break nowrap lines (they clip at the page edge), got %q", doc)
	}
	if !strings.Contains(doc, "pre { white-space: pre-wrap !important }") {
		t.Fatalf("printDoc must keep <pre> spacing while wrapping, got %q", doc)
	}
	if !strings.HasSuffix(doc, "</body></html>") {
		t.Fatalf("printDoc must preserve the mail body, got %q", doc)
	}
}

func failStub(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "weasyprint-fail")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho boom >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}
