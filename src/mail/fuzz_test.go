// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

// Fuzz targets for the parser/render boundaries (AGENTS.md: parser-
// adjacent code passes SECURITY.md's fuzz targets). Properties:
// panic-freedom and bounded output.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/emersion/go-message/mail"
)

func FuzzRenderHTML(f *testing.F) {
	f.Add("plain text")
	f.Add("<p>hello <b>world</b></p>")
	f.Add("<table><tr><td>a</td><td>b</td></tr><tr><td>c</td></tr></table>")
	f.Add("<style>p { color: red }</style><p>x</p>")
	f.Add("<img src=\"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==\">")
	f.Add("<pre>  spaced\n\tlines\n</pre>")
	f.Fuzz(func(t *testing.T, body string) {
		lines := RenderHTML(body, nil, 0)
		if len(lines) > 2*maxHTMLLines+1 {
			t.Fatalf("render exceeded the line budget: %d lines", len(lines))
		}
	})
}

// FuzzParseDraft: ParseDraft must never panic on arbitrary bytes; the
// body stays bounded by the part cap and every ordinal stays in range.
func FuzzParseDraft(f *testing.F) {
	f.Add([]byte("To: alice@example.com\nSubject: s\nContent-Type: text/plain; charset=utf-8\n\nthe body\n"))
	f.Add([]byte("\x00\xff garbage \r\n-- \nno header at all"))
	var buf bytes.Buffer
	mw, _ := mail.CreateWriter(&buf, mail.Header{})
	bp, _ := mw.CreateSingleInline(mail.InlineHeader{})
	bp.Write([]byte("b"))
	bp.Close()
	ap := mail.AttachmentHeader{}
	ap.SetFilename("a.txt")
	att, _ := mw.CreateAttachment(ap)
	att.Write([]byte("data"))
	att.Close()
	mw.Close()
	f.Add(buf.Bytes())
	f.Fuzz(func(t *testing.T, raw []byte) {
		p := filepath.Join(t.TempDir(), "draft")
		if err := os.WriteFile(p, raw, 0600); err != nil {
			t.Fatal(err)
		}
		d, err := ParseDraft(p)
		if err != nil {
			return
		}
		if len(d.Body) > maxPartBytes {
			t.Fatalf("body exceeded the cap: %d", len(d.Body))
		}
		if len(d.Body) == maxPartBytes && !d.BodyTruncated {
			t.Fatal("cap-sized body must flag truncated")
		}
		for _, a := range d.Atts {
			if a.Ordinal < 0 {
				t.Fatalf("negative ordinal: %+v", a)
			}
		}
	})
}
