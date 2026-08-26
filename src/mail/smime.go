// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"os"

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
		return parseDetached(e, raw, params["boundary"])
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
// + openssl crlf_copy). The CMS is read at the entity level so its base64
// transfer encoding decodes.
func parseDetached(e *message.Entity, raw []byte, boundary string) (*Signature, error) {
	content, err := signedContent(raw, boundary)
	if err != nil {
		return nil, err
	}
	mr := e.MultipartReader()
	if mr == nil {
		return nil, errors.New("mail: multipart/signed has no parts")
	}
	defer mr.Close()
	c, err := mr.NextPart()
	if err != nil {
		return nil, err
	}
	// drain the content part so the reader advances to the signature part
	if _, err := io.Copy(io.Discard, c.Body); err != nil {
		return nil, err
	}
	sig, err := mr.NextPart()
	if err != nil {
		return nil, err
	}
	cms, err := readPart(sig)
	if err != nil {
		return nil, err
	}
	return &Signature{Detached: true, CMS: cms, Content: content}, nil
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
// headers and body as transmitted, ending before the CRLF that precedes the
// signature boundary - and normalizes line endings as a signer hashes them.
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
	if end >= 2 && raw[end-2] == '\r' && raw[end-1] == '\n' {
		end -= 2
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
