// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"strings"

	"github.com/emersion/go-message"
)

// Signature is the S/MIME signed material pulled from a message for
// verification (R10 read path): the CMS plus, for the detached form, the
// signed content. Verify never opens a mail file itself - it consumes these
// bytes (F4).
type Signature struct {
	Detached bool
	CMS      []byte
	Content  []byte // canonicalized signed bytes; empty for the attached form
}

// ParseSignature detects and extracts an S/MIME signature from a message
// file: multipart/signed with an application/pkcs7-signature part (detached,
// the common form) or an application/pkcs7-mime signed-data body (attached).
// It returns nil, nil for a message that is not S/MIME-signed.
func ParseSignature(path string) (*Signature, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxPartBytes))
	if err != nil {
		return nil, err
	}
	e, err := message.Read(bytes.NewReader(raw))
	if err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
		return nil, err
	}
	media, params, err := mime.ParseMediaType(e.Header.Get("Content-Type"))
	if err != nil {
		return nil, nil // no usable Content-Type: not an S/MIME structure
	}
	switch {
	case media == "multipart/signed" && isPkcs7Signature(params["protocol"]):
		return parseDetached(raw, params["boundary"])
	case media == "application/pkcs7-mime" && params["smime-type"] == "signed-data":
		return parseAttached(e)
	}
	return nil, nil
}

// isPkcs7Signature matches the protocol parameter of the signature part, in
// its registered and legacy x- form.
func isPkcs7Signature(protocol string) bool {
	return protocol == "application/pkcs7-signature" || protocol == "application/x-pkcs7-signature"
}

// parseDetached extracts the raw signed content and the detached CMS. The
// content is the FULL first part (its MIME headers plus body) as transmitted,
// line-normalized - the exact bytes a signer hashes (neomutt crypt_write_signed
// + openssl crlf_copy). Both parts are located by raw byte position: a
// headerless content part (openssl's default `smime -sign` output, legal MIME
// per RFC 2046) raises go-message's multipart reader, so the CMS never goes
// through it.
func parseDetached(raw []byte, boundary string) (*Signature, error) {
	content, err := signedContent(raw, boundary)
	if err != nil {
		return nil, err
	}
	cms, err := signaturePart(raw, boundary)
	if err != nil {
		return nil, err
	}
	return &Signature{Detached: true, CMS: cms, Content: content}, nil
}

// signaturePart extracts the pkcs7-signature part of a multipart/signed and
// transfer-decodes it (RFC 5751: the part is base64 or binary). It mirrors
// signedContent's raw-byte search, so it never depends on the multipart
// reader.
func signaturePart(raw []byte, boundary string) ([]byte, error) {
	marker := []byte("--" + boundary)
	idx := bytes.Index(raw, marker)
	if idx < 0 {
		return nil, errors.New("mail: boundary not found")
	}
	start := idx + len(marker)
	if nl := bytes.IndexByte(raw[start:], '\n'); nl >= 0 {
		start += nl + 1
	} else {
		return nil, errors.New("mail: unterminated boundary line")
	}
	// the content part ends at the second boundary, where the signature
	// part begins
	next := bytes.Index(raw[start:], marker)
	if next < 0 {
		return nil, errors.New("mail: signature part not found")
	}
	partStart := start + next
	if nl := bytes.IndexByte(raw[partStart:], '\n'); nl >= 0 {
		partStart += nl + 1
	} else {
		return nil, errors.New("mail: unterminated boundary line")
	}
	close := bytes.Index(raw[partStart:], marker)
	if close < 0 {
		return nil, errors.New("mail: signature part unterminated")
	}
	part := raw[partStart : partStart+close]

	// the part's header block ends at the first blank line
	blank := bytes.Index(part, []byte("\r\n\r\n"))
	if blank < 0 {
		blank = bytes.Index(part, []byte("\n\n"))
	}
	if blank < 0 {
		return nil, errors.New("mail: signature part has no body")
	}
	cte := "base64" // RFC 5751 default for the signature part
	for _, line := range bytes.Split(part[:blank], []byte("\n")) {
		line = bytes.TrimSpace(line)
		if i := bytes.Index(line, []byte(":")); i > 0 {
			if name := strings.ToLower(string(bytes.TrimSpace(line[:i]))); name == "content-transfer-encoding" {
				cte = strings.ToLower(string(bytes.TrimSpace(line[i+1:])))
				break
			}
		}
	}
	body := bytes.TrimSpace(part[blank:])
	switch cte {
	case "binary", "8bit", "7bit":
		return body, nil
	default:
		dec := make([]byte, base64.StdEncoding.DecodedLen(len(body)))
		n, err := base64.StdEncoding.Decode(dec, body)
		if err != nil {
			return nil, fmt.Errorf("mail: decode signature part: %w", err)
		}
		return dec[:n], nil
	}
}

// parseAttached reads an application/pkcs7-mime signed-data body: the whole
// part is the CMS, the signed content is inside it.
func parseAttached(e *message.Entity) (*Signature, error) {
	cms, err := readPart(e)
	if err != nil {
		return nil, err
	}
	return &Signature{CMS: cms}, nil
}

// readPart drains an entity body (transfer-decoded; a nested multipart
// re-encodes its subtree), bounded like every mail parse (F4).
func readPart(e *message.Entity) ([]byte, error) {
	return io.ReadAll(io.LimitReader(e.Body, maxPartBytes))
}

// signedContent extracts the raw first part of a multipart/signed - its MIME
// headers and body as transmitted, ending before the line ending that
// precedes the signature boundary - and normalizes line endings as a signer
// hashes them. The boundary delimiter is CRLF in the canonical form but a
// lone LF in the LF-delimited output openssl emits, so exactly one line
// ending is stripped - never the content's own.
func signedContent(raw []byte, boundary string) ([]byte, error) {
	marker := []byte("--" + boundary)
	idx := bytes.Index(raw, marker)
	if idx < 0 {
		return nil, errors.New("mail: boundary not found")
	}
	start := idx + len(marker)
	if nl := bytes.IndexByte(raw[start:], '\n'); nl >= 0 {
		start += nl + 1
	} else {
		return nil, errors.New("mail: unterminated boundary line")
	}
	next := bytes.Index(raw[start:], marker)
	if next < 0 {
		return nil, errors.New("mail: signature part not found")
	}
	end := start + next
	switch {
	case end >= 2 && raw[end-2] == '\r' && raw[end-1] == '\n':
		end -= 2
	case end >= 1 && raw[end-1] == '\n':
		end--
	}
	return canonicalWire(raw[start:end]), nil
}

// canonicalWire normalizes line endings as a signer hashes them (neomutt
// crypt_write_signed): a lone LF becomes CRLF, while existing CRLF and lone CR
// are left untouched.
func canonicalWire(b []byte) []byte {
	var out []byte
	hadcr := false
	for _, c := range b {
		if c == '\r' {
			hadcr = true
			out = append(out, c)
			continue
		}
		if c == '\n' && !hadcr {
			out = append(out, '\r')
		}
		hadcr = false
		out = append(out, c)
	}
	return out
}
