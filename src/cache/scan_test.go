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

// TestScanHtmlOnlyNoAttachment pins the html-body fix (the 2026-09
// report): a newsletter whose only parts are the html body and its
// inline images must not flag as an attachment mail - the related
// container's inline media and a named alternative html half are
// content, never files.
func TestScanHtmlOnlyNoAttachment(t *testing.T) {
	// multipart/related: the html body plus an image it references
	related := `From: a@x
Subject: t
Content-Type: multipart/related; boundary="R"

--R
Content-Type: text/html

<html>hi <img src="cid:pix"></html>
--R
Content-Type: image/png; name="pix.png"
Content-Disposition: inline; filename="pix.png"

DATA
--R--
`
	// an alternative whose html half names itself (Outlook's
	// message.html) - still the body, not a file
	alt := `From: a@x
Subject: t
Content-Type: multipart/alternative; boundary="A"

--A
Content-Type: text/plain

body
--A
Content-Type: text/html; name="message.html"

<html>hi</html>
--A--
`
	for name, raw := range map[string]string{"related": related, "alternative": alt} {
		p := filepath.Join(t.TempDir(), name+".eml")
		os.WriteFile(p, []byte(raw), 0600)
		atts, err := ScanAttachments(p)
		if err != nil {
			t.Fatal(err)
		}
		if len(atts) != 0 {
			t.Fatalf("%s: html-only mail must carry no attachments, got %+v", name, atts)
		}
	}
}

// TestScanHtmlPlusRealAttachment: a genuine file beside the html body
// still flags - the html fix must not suppress real attachments.
func TestScanHtmlPlusRealAttachment(t *testing.T) {
	raw := `From: a@x
Subject: t
Content-Type: multipart/mixed; boundary="M"

--M
Content-Type: multipart/alternative; boundary="A"

--A
Content-Type: text/plain

body
--A
Content-Type: text/html; name="message.html"

<html>hi</html>
--A--
--M
Content-Type: application/pdf; name="report.pdf"
Content-Disposition: attachment; filename="report.pdf"

DATA
--M--
`
	p := filepath.Join(t.TempDir(), "m.eml")
	os.WriteFile(p, []byte(raw), 0600)
	atts, err := ScanAttachments(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 1 || atts[0].Name != "report.pdf" {
		t.Fatalf("a real file beside html must still flag, got %+v", atts)
	}
}
