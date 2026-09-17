package crypto

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/textproto"
	"os"
	"sort"
	"strings"

	mail "notmutt/mail"
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
	case mail.MIMETypeMultipartEncrypted.String(), mail.MIMETypeMultipartMixed.String():
		parts, err := mail.RawMIMEParts(body, params["boundary"])
		if err != nil {
			return nil, PGPStatus{}, false, err
		}
		payload, encrypted := pgpEncryptedPart(ct, params, parts)
		if !encrypted {
			return message, PGPStatus{}, false, nil
		}
		ciphertext, err := decodeTransfer(payload)
		if err != nil {
			return nil, PGPStatus{}, true, err
		}
		plain, status, err := p.Decrypt(ciphertext)
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
	case mail.MIMETypeMultipartSigned.String():
		if params["protocol"] != mail.MIMETypePGPSignature.String() {
			return message, PGPStatus{}, false, nil
		}
		parts, err := mail.RawMIMEParts(body, params["boundary"])
		if err != nil || len(parts) != 2 {
			return nil, PGPStatus{}, true, fmt.Errorf("pgp: invalid signed MIME envelope")
		}
		signed := parts[0].Raw
		signature, err := decodeTransfer(parts[1])
		if err != nil {
			return nil, PGPStatus{}, true, err
		}
		status, verifyErr := p.VerifyDetached(signed, signature)
		if verifyErr != nil && status.Err == "" {
			status.Err = verifyErr.Error()
		}
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
	entity := canonicalCRLF(writeMIME(entityHeader, body))
	sig, status, err := p.Sign(entity, key)
	if err != nil {
		return nil, err
	}
	boundary := multipart.NewWriter(io.Discard).Boundary()
	h.Set("MIME-Version", "1.0")
	h.Set("Content-Type", mime.FormatMediaType(mail.MIMETypeMultipartSigned.String(), map[string]string{"boundary": boundary, "protocol": mail.MIMETypePGPSignature.String(), "micalg": status.MICALG}))
	var out bytes.Buffer
	out.Write(writeHeader(h))
	fmt.Fprintf(&out, "--%s\r\n", boundary)
	out.Write(entity)
	if !bytes.HasSuffix(entity, []byte("\r\n")) {
		out.WriteString("\r\n")
	}
	fmt.Fprintf(&out, "--%s\r\nContent-Type: %s\r\n\r\n", boundary, mail.MIMETypePGPSignature)
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
	h.Set("Content-Type", mime.FormatMediaType(mail.MIMETypeMultipartEncrypted.String(), map[string]string{"boundary": boundary, "protocol": mail.MIMETypePGPEncrypted.String()}))
	var out bytes.Buffer
	out.Write(writeHeader(h))
	fmt.Fprintf(&out, "--%s\r\nContent-Type: %s\r\n\r\nVersion: 1\r\n--%s\r\nContent-Type: %s\r\n\r\n", boundary, mail.MIMETypePGPEncrypted, boundary, mail.MIMETypeOctetStream)
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

func canonicalCRLF(data []byte) []byte {
	if !bytes.Contains(data, []byte("\n")) {
		return data
	}
	var out bytes.Buffer
	out.Grow(len(data))
	for i, b := range data {
		if b == '\n' && (i == 0 || data[i-1] != '\r') {
			out.WriteByte('\r')
		}
		out.WriteByte(b)
	}
	return out.Bytes()
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

func pgpEncryptedPart(typ string, params map[string]string, parts []mail.RawMIMEPart) (mail.RawMIMEPart, bool) {
	if typ == mail.MIMETypeMultipartEncrypted.String() && strings.EqualFold(params["protocol"], mail.MIMETypePGPEncrypted.String()) && len(parts) == 2 && mediaType(parts[0].Header) == mail.MIMETypePGPEncrypted.String() && mediaType(parts[1].Header) == mail.MIMETypeOctetStream.String() {
		return parts[1], true
	}
	if typ == mail.MIMETypeMultipartMixed.String() && len(parts) == 3 && mediaType(parts[0].Header) == mail.MIMETypeTextPlain.String() && len(bytes.TrimSpace(parts[0].Body)) == 0 && mediaType(parts[1].Header) == mail.MIMETypePGPEncrypted.String() && mediaType(parts[2].Header) == mail.MIMETypeOctetStream.String() {
		return parts[2], true
	}
	return mail.RawMIMEPart{}, false
}

func mediaType(header textproto.MIMEHeader) string {
	typ, _, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil {
		return ""
	}
	return strings.ToLower(typ)
}

func decodeTransfer(part mail.RawMIMEPart) ([]byte, error) {
	switch strings.ToLower(part.Header.Get("Content-Transfer-Encoding")) {
	case "", "7bit", "8bit", "binary":
		return part.Body, nil
	case "base64":
		return io.ReadAll(base64.NewDecoder(base64.StdEncoding, bytes.NewReader(part.Body)))
	case "quoted-printable":
		return io.ReadAll(quotedprintable.NewReader(bytes.NewReader(part.Body)))
	default:
		return nil, fmt.Errorf("pgp: unsupported content-transfer-encoding %q", part.Header.Get("Content-Transfer-Encoding"))
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
