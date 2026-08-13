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
