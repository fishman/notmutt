package crypto

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/textproto"
	"os"
	"sort"
)

func TransformPGP(p PGP, message []byte, sign, encrypt bool, key string, recipients []string) ([]byte, error) {
	if sign {
		var err error
		message, err = signMIME(p, message, key)
		if err != nil {
			return nil, err
		}
	}
	if encrypt {
		var err error
		message, err = encryptMIME(p, message, recipients)
		if err != nil {
			return nil, err
		}
	}
	return message, nil
}

// DecodePGPMIME opens a PGP/MIME message. handled is false for ordinary mail.
func DecodePGPMIME(p PGP, message []byte) ([]byte, PGPStatus, bool, error) {
	h, body, err := splitMIME(message)
	if err != nil {
		return nil, PGPStatus{}, false, err
	}
	ct, params, err := mime.ParseMediaType(h.Get("Content-Type"))
	if err != nil {
		return message, PGPStatus{}, false, nil
	}
	switch ct {
	case "multipart/encrypted":
		if params["protocol"] != "application/pgp-encrypted" {
			return message, PGPStatus{}, false, nil
		}
		parts, err := mimeParts(body, params["boundary"])
		if err != nil || len(parts) != 2 {
			return nil, PGPStatus{}, true, fmt.Errorf("pgp: invalid encrypted MIME envelope")
		}
		if parts[0].Header.Get("Content-Type") != "application/pgp-encrypted" {
			return nil, PGPStatus{}, true, fmt.Errorf("pgp: invalid encrypted MIME control part")
		}
		plain, status, err := p.Decrypt(parts[1].Data)
		if err != nil {
			return nil, status, true, err
		}
		inner, innerStatus, signed, err := DecodePGPMIME(p, plain)
		if err != nil {
			return nil, status, true, err
		}
		if signed {
			status.Signed, status.Valid, status.Signer, status.MICALG, status.Err = innerStatus.Signed, innerStatus.Valid, innerStatus.Signer, innerStatus.MICALG, innerStatus.Err
		}
		return mergeOuterHeaders(h, inner), status, true, nil
	case "multipart/signed":
		if params["protocol"] != "application/pgp-signature" {
			return message, PGPStatus{}, false, nil
		}
		parts, err := mimeParts(body, params["boundary"])
		if err != nil || len(parts) != 2 {
			return nil, PGPStatus{}, true, fmt.Errorf("pgp: invalid signed MIME envelope")
		}
		signed := append(writeMIME(parts[0].Header, parts[0].Data), "\r\n"...)
		status, _ := p.VerifyDetached(signed, parts[1].Data)
		// The pager renders the signed content with a warning when verification
		// fails.
		return mergeOuterHeaders(h, signed), status, true, nil
	default:
		return message, PGPStatus{}, false, nil
	}
}

func signMIME(p PGP, message []byte, key string) ([]byte, error) {
	h, body, err := splitMIME(message)
	if err != nil {
		return nil, err
	}
	entityHeader := cloneHeader(h)
	entityHeader.Del("Mime-Version")
	entity := writeMIME(entityHeader, body)
	sig, status, err := p.Sign(entity, key)
	if err != nil {
		return nil, err
	}
	boundary := multipart.NewWriter(io.Discard).Boundary()
	h.Set("MIME-Version", "1.0")
	h.Set("Content-Type", mime.FormatMediaType("multipart/signed", map[string]string{"boundary": boundary, "protocol": "application/pgp-signature", "micalg": status.MICALG}))
	var out bytes.Buffer
	out.Write(writeHeader(h))
	fmt.Fprintf(&out, "--%s\r\n", boundary)
	out.Write(entity)
	if !bytes.HasSuffix(entity, []byte("\r\n")) {
		out.WriteString("\r\n")
	}
	fmt.Fprintf(&out, "--%s\r\nContent-Type: application/pgp-signature\r\n\r\n", boundary)
	out.Write(sig)
	if !bytes.HasSuffix(sig, []byte("\r\n")) {
		out.WriteString("\r\n")
	}
	fmt.Fprintf(&out, "--%s--\r\n", boundary)
	return out.Bytes(), nil
}

func encryptMIME(p PGP, message []byte, recipients []string) ([]byte, error) {
	h, body, err := splitMIME(message)
	if err != nil {
		return nil, err
	}
	ciphertext, _, err := p.Encrypt(writeMIME(contentHeaders(h), body), recipients)
	if err != nil {
		return nil, err
	}
	boundary := multipart.NewWriter(io.Discard).Boundary()
	h.Set("MIME-Version", "1.0")
	h.Set("Content-Type", mime.FormatMediaType("multipart/encrypted", map[string]string{"boundary": boundary, "protocol": "application/pgp-encrypted"}))
	var out bytes.Buffer
	out.Write(writeHeader(h))
	fmt.Fprintf(&out, "--%s\r\nContent-Type: application/pgp-encrypted\r\n\r\nVersion: 1\r\n--%s\r\nContent-Type: application/octet-stream\r\n\r\n", boundary, boundary)
	out.Write(ciphertext)
	if !bytes.HasSuffix(ciphertext, []byte("\r\n")) {
		out.WriteString("\r\n")
	}
	fmt.Fprintf(&out, "--%s--\r\n", boundary)
	return out.Bytes(), nil
}

func splitMIME(data []byte) (textproto.MIMEHeader, []byte, error) {
	reader := bufio.NewReader(bytes.NewReader(data))
	h, err := textproto.NewReader(reader).ReadMIMEHeader()
	if err != nil {
		return nil, nil, err
	}
	body, err := io.ReadAll(reader)
	return h, body, err
}
func writeHeader(h textproto.MIMEHeader) []byte { return writeMIME(h, nil) }
func writeMIME(h textproto.MIMEHeader, body []byte) []byte {
	keys := make([]string, 0, len(h))
	for key := range h {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b bytes.Buffer
	for _, key := range keys {
		for _, value := range h[key] {
			fmt.Fprintf(&b, "%s: %s\r\n", key, value)
		}
	}
	b.WriteString("\r\n")
	b.Write(body)
	return b.Bytes()
}
func cloneHeader(h textproto.MIMEHeader) textproto.MIMEHeader {
	out := make(textproto.MIMEHeader, len(h))
	for k, values := range h {
		out[k] = append([]string(nil), values...)
	}
	return out
}
func contentHeaders(h textproto.MIMEHeader) textproto.MIMEHeader {
	out := textproto.MIMEHeader{}
	for k, values := range h {
		if k == "Content-Type" || k == "Content-Transfer-Encoding" || k == "Content-Disposition" || k == "Content-Id" || k == "Mime-Version" {
			out[k] = append([]string(nil), values...)
		}
	}
	return out
}
func mergeOuterHeaders(outer textproto.MIMEHeader, entity []byte) []byte {
	inner, body, err := splitMIME(entity)
	if err != nil {
		return entity
	}
	for k := range contentHeaders(outer) {
		outer.Del(k)
	}
	for k, values := range inner {
		outer[k] = append([]string(nil), values...)
	}
	return writeMIME(outer, body)
}

type mimePart struct {
	Header textproto.MIMEHeader
	Data   []byte
}

func mimeParts(body []byte, boundary string) ([]mimePart, error) {
	if boundary == "" {
		return nil, fmt.Errorf("missing boundary")
	}
	mr := multipart.NewReader(bytes.NewReader(body), boundary)
	var parts []mimePart
	for {
		p, err := mr.NextPart()
		if err == io.EOF {
			return parts, nil
		}
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(p)
		if err != nil {
			return nil, err
		}
		parts = append(parts, mimePart{Header: cloneHeader(p.Header), Data: data})
	}
}

// VerifyDetached presents the signature by a private temporary path because
// gpg's detached-verify interface accepts the signed entity on stdin.
func (p PGP) VerifyDetached(content, signature []byte) (PGPStatus, error) {
	f, err := os.CreateTemp("", "notmutt-pgp-signature-*")
	if err != nil {
		return PGPStatus{}, err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return PGPStatus{}, err
	}
	if _, err := f.Write(signature); err != nil {
		f.Close()
		return PGPStatus{}, err
	}
	if err := f.Close(); err != nil {
		return PGPStatus{}, err
	}
	_, status, err := p.run(content, "--verify", f.Name(), "-")
	status.Signed = true
	return status, err
}
