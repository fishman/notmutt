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
	e, err := message.Read(f)
	if err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err) {
		return nil, err
	}
	media, params, err := mime.ParseMediaType(e.Header.Get("Content-Type"))
	if err != nil {
		return nil, nil // no usable Content-Type: not an S/MIME structure
	}
	switch {
	case media == "multipart/signed" && isPkcs7Signature(params["protocol"]):
		return parseDetached(e)
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

// parseDetached reads the multipart/signed structure at the ENTITY level
// (not the flattened mail.Reader, which turns a nested-multipart content
// into several leaves and moves the signature): child one is the whole
// signed content subtree, child two the detached CMS signature.
func parseDetached(e *message.Entity) (*Signature, error) {
	mr := e.MultipartReader()
	if mr == nil {
		return nil, errors.New("mail: multipart/signed has no parts")
	}
	defer mr.Close()
	content, err := mr.NextPart()
	if err != nil {
		return nil, err
	}
	body, err := readPart(content)
	if err != nil {
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
	return &Signature{Detached: true, CMS: cms, Content: canonicalMIME(body)}, nil
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

// canonicalMIME reproduces the form a signer hashes (RFC 5751): every line
// ending CRLF and a final CRLF - the detached content must match the digest
// exactly, and most real signers (openssl smime) add the closing line break
// that a MIME parser strips at the boundary.
func canonicalMIME(b []byte) []byte {
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	b = bytes.ReplaceAll(b, []byte("\r"), []byte("\n"))
	b = bytes.ReplaceAll(b, []byte("\n"), []byte("\r\n"))
	if len(b) > 0 && !bytes.HasSuffix(b, []byte("\r\n")) {
		b = append(b, "\r\n"...)
	}
	return b
}
