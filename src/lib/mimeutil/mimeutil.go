package mimeutil

import (
	"bufio"
	"bytes"
	"fmt"
	"net/textproto"
)

// Type is a MIME media type used by the client.
type Type string

const (
	MultipartAlternative Type = "multipart/alternative"
	MultipartEncrypted   Type = "multipart/encrypted"
	MultipartMixed       Type = "multipart/mixed"
	MultipartSigned      Type = "multipart/signed"
	PGPEncrypted         Type = "application/pgp-encrypted"
	PGPSignature         Type = "application/pgp-signature"
	OctetStream          Type = "application/octet-stream"
	TextHTML             Type = "text/html"
	TextMarkdown         Type = "text/markdown"
	TextPlain            Type = "text/plain"
)

func (t Type) String() string { return string(t) }

// RawPart preserves one multipart part exactly as received. Raw includes the
// header, body, and delimiter-adjacent line ending.
type RawPart struct {
	Header textproto.MIMEHeader
	Body   []byte
	Raw    []byte
}

// RawParts parses a multipart body without normalizing part bytes. Detached
// signature verification uses Raw; other callers may use Header and Body.
func RawParts(body []byte, boundary string) ([]RawPart, error) {
	if boundary == "" {
		return nil, fmt.Errorf("mime: missing boundary")
	}
	marker := []byte("--" + boundary)
	separator := append([]byte("\n"), marker...)
	start := bytes.Index(body, marker)
	for start > 0 && body[start-1] != '\n' {
		next := bytes.Index(body[start+len(marker):], marker)
		if next < 0 {
			return nil, fmt.Errorf("mime: malformed boundary")
		}
		start += len(marker) + next
	}
	if start < 0 {
		return nil, fmt.Errorf("mime: missing boundary")
	}
	var parts []RawPart
	for {
		if !bytes.HasPrefix(body[start:], marker) {
			return nil, fmt.Errorf("mime: malformed boundary")
		}
		start += len(marker)
		if bytes.HasPrefix(body[start:], []byte("--")) {
			return parts, nil
		}
		lineEnd := bytes.IndexByte(body[start:], '\n')
		if lineEnd < 0 {
			return nil, fmt.Errorf("mime: unterminated boundary")
		}
		partStart := start + lineEnd + 1
		next := bytes.Index(body[partStart:], separator)
		if next < 0 {
			return nil, fmt.Errorf("mime: missing closing boundary")
		}
		next += partStart
		raw := body[partStart : next+1]
		header, content, err := splitPart(raw)
		if err != nil {
			return nil, err
		}
		parts = append(parts, RawPart{Header: header, Body: content, Raw: raw})
		start = next + 1
	}
}

func splitPart(raw []byte) (textproto.MIMEHeader, []byte, error) {
	reader := bufio.NewReader(bytes.NewReader(raw))
	header, err := textproto.NewReader(reader).ReadMIMEHeader()
	if err != nil {
		return nil, nil, err
	}
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		return header, raw[i+4:], nil
	}
	if i := bytes.Index(raw, []byte("\n\n")); i >= 0 {
		return header, raw[i+2:], nil
	}
	return nil, nil, fmt.Errorf("mime: missing part body")
}
