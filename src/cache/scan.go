package cache

import (
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/emersion/go-message"

	"notmutt/core"
)

// ScanAttachments parses a message file and returns its attachment list.
// Content never leaves this function; the result feeds the row slot only.
func ScanAttachments(path string) ([]core.Attachment, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	m, err := message.Read(f)
	if err != nil {
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
	if err != nil {
		return ""
	}
	return params["filename"]
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
