// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const mimeSample = `From: a@x
To: b@x
Subject: t
MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="B"

--B
Content-Type: text/plain

body
--B
Content-Type: application/octet-stream
Content-Disposition: attachment; filename="evil.txt"

data
--B--
`

func TestScanAttachments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.eml")
	os.WriteFile(path, []byte(mimeSample), 0600)
	atts, err := ScanAttachments(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 1 || atts[0].Name != "evil.txt" {
		t.Fatalf("want 1 attachment evil.txt, got %+v", atts)
	}
}

func TestScanHostileFilename(t *testing.T) {
	hostile := strings.Replace(mimeSample, `evil.txt`, `$(rm -rf /).txt`, 1)
	path := filepath.Join(t.TempDir(), "m.eml")
	os.WriteFile(path, []byte(hostile), 0600)
	atts, err := ScanAttachments(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 1 || atts[0].Name != "$(rm -rf /).txt" {
		t.Fatalf("hostile name must be stored verbatim and inert: %+v", atts)
	}
}

func TestScanPlainTextNoAttachments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "p.eml")
	os.WriteFile(path, []byte("From: a@x\nSubject: t\nContent-Type: text/plain\n\nhi\n"), 0600)
	atts, err := ScanAttachments(path)
	if err != nil || len(atts) != 0 {
		t.Fatalf("plain text: %v %v", atts, err)
	}
}

func TestScanLatin1PartBeforeAttachment(t *testing.T) {
	latin1 := strings.Replace(mimeSample,
		"Content-Type: text/plain",
		`Content-Type: text/plain; charset=iso-8859-1`, 1)
	path := filepath.Join(t.TempDir(), "l.eml")
	os.WriteFile(path, []byte(latin1), 0600)
	atts, err := ScanAttachments(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 1 || atts[0].Name != "evil.txt" {
		t.Fatalf("attachment after latin-1 part must be found, got %+v", atts)
	}
}

func TestScanContentTypeNameParam(t *testing.T) {
	named := strings.Replace(mimeSample,
		"Content-Type: application/octet-stream\nContent-Disposition: attachment; filename=\"evil.txt\"",
		`Content-Type: application/pdf; name="report.pdf"`, 1)
	path := filepath.Join(t.TempDir(), "n.eml")
	os.WriteFile(path, []byte(named), 0600)
	atts, err := ScanAttachments(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 1 || atts[0].Name != "report.pdf" {
		t.Fatalf("want name= param attachment report.pdf, got %+v", atts)
	}
}
