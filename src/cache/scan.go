// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package cache

import (
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset"

	"notmutt/core"
)

// ScanAttachments parses a message file; content never leaves this
// function, the result feeds the row slot only.
func ScanAttachments(path string) ([]core.Attachment, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m, err := message.Read(f)
	if err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
		return nil, err
	}
	var atts []core.Attachment
	walk(m, &atts)
	return atts, nil
}

func walk(m *message.Entity, atts *[]core.Attachment) {
	mt, _, err := m.Header.ContentType()
	if err != nil || !strings.HasPrefix(mt, "multipart/") {
		return
	}
	mr := m.MultipartReader()
	if mr == nil {
		return
	}
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			return
		}
		if message.IsUnknownCharset(err) || message.IsUnknownEncoding(err) {
			continue // bodies are never read, so these are spurious here
		}
		if err != nil {
			return
		}
		if name := filename(p); name != "" || isAttachment(p) {
			*atts = append(*atts, core.Attachment{
				Name:     name,
				MimeType: p.Header.Get("Content-Type"),
				Size:     contentLength(p.Header.Get("Content-Length")),
			})
		}
		walk(p, atts)
	}
}

func filename(p *message.Entity) string {
	_, params, err := p.Header.ContentDisposition()
	if err == nil && params["filename"] != "" {
		return params["filename"]
	}
	// Legacy mail uses Content-Type name= instead of Content-Disposition.
	if _, params, err := p.Header.ContentType(); err == nil && params != nil {
		return params["name"]
	}
	return ""
}

func isAttachment(p *message.Entity) bool {
	cd, _, err := p.Header.ContentDisposition()
	if err != nil {
		return false
	}
	return strings.HasPrefix(cd, "attachment")
}

func contentLength(s string) int64 {
	if s == "" {
		return 0
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	return n
}
