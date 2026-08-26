// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"go.mozilla.org/pkcs7"

	"notmutt/lib/crypto"
)

// TestSMIMEValidation drives the whole read path with generated (never real)
// fixtures: a test CA + S/MIME leaf for alpha@example.com, a detached
// signature, and a signed .eml. It asserts detect -> extract -> verify, plus
// the security properties that must hold: a tampered body fails and an
// untrusted root does not anchor.
func TestSMIMEValidation(t *testing.T) {
	dir := t.TempDir()
	caCert, caKey := testCA(t)
	leaf, leafKey := testLeaf(t, caCert, caKey)
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCert.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	// the signed content is the FULL first part as transmitted - its MIME
	// headers plus body (neomutt crypt_write_signed), ending with a newline
	// as real mail does
	contentPart := []byte("Content-Type: text/plain\r\n\r\nhello alpha\r\nsecond line\r\n")

	// detached multipart/signed: sign the raw part line-normalized, the exact
	// bytes extraction reproduces for the digest
	cms := signCMS(t, canonicalWire(contentPart), leaf, leafKey, true)
	detached := filepath.Join(dir, "signed.eml")
	writeDetached(t, detached, contentPart, cms)

	v, err := crypto.New(caPath, true)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := ParseSignature(detached)
	if err != nil || sig == nil || !sig.Detached {
		t.Fatalf("detached signed message must detect, got sig=%+v err=%v", sig, err)
	}
	res, err := v.Verify(sig.CMS, sig.Content)
	if err != nil {
		t.Fatalf("valid signature must verify: %v", err)
	}
	if !res.Valid {
		t.Fatal("valid signature must be Valid")
	}
	if res.Signer != "alpha@example.com" {
		t.Fatalf("signer = %q, want alpha@example.com", res.Signer)
	}
	if got, want := string(sig.Content), string(canonicalWire(contentPart)); got != want {
		t.Fatalf("detached content = %q, want %q", got, want)
	}

	// tampered body fails the digest
	tampered := filepath.Join(dir, "tampered.eml")
	writeDetached(t, tampered, []byte("Content-Type: text/plain\r\n\r\nhello alpha\r\nCHANGED line\r\n"), cms)
	if sig, err := ParseSignature(tampered); err != nil || sig == nil {
		t.Fatalf("tampered message must still detect: sig=%+v err=%v", sig, err)
	} else if _, err := v.Verify(sig.CMS, sig.Content); err == nil {
		t.Fatal("tampered body must not verify")
	}

	// wrong roots do not anchor (R10: pinned, never the system pool)
	otherCA, otherKey := testCA(t)
	_ = otherKey
	otherPath := filepath.Join(dir, "other.pem")
	if err := os.WriteFile(otherPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: otherCA.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	other, err := crypto.New(otherPath, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Verify(sig.CMS, sig.Content); err == nil {
		t.Fatal("a signature chained to an untrusted root must not verify")
	}

	// attached application/pkcs7-mime signed-data
	attachedCMS := signCMS(t, canonicalWire(contentPart), leaf, leafKey, false)
	attached := filepath.Join(dir, "attached.eml")
	writeAttached(t, attached, attachedCMS)
	sig, err = ParseSignature(attached)
	if err != nil || sig == nil || sig.Detached {
		t.Fatalf("attached signed-data must detect, got sig=%+v err=%v", sig, err)
	}
	if len(sig.Content) != 0 {
		t.Fatalf("attached form must carry no detached content, got %d bytes", len(sig.Content))
	}
	if res, err := v.Verify(sig.CMS, nil); err != nil || !res.Valid {
		t.Fatalf("attached signed-data must verify: %+v err=%v", res, err)
	}

	// nested-multipart content must still locate the signature - the BER
	// regression: a flattened part read took a content leaf (the html) as the
	// CMS, which pkcs7.Parse rejects as "BER tag length too long". The signed
	// first part is the alternative header plus its subtree body, hashed as a
	// signer does; extraction reproduces it byte-exact.
	subtree := []byte("--altb\r\nContent-Type: text/plain\r\n\r\ntext body\r\n" +
		"--altb\r\nContent-Type: text/html\r\n\r\n<html><body>html</body></html>\r\n" +
		"--altb--\r\n")
	nestedPart := []byte("Content-Type: multipart/alternative; boundary=\"altb\"\r\n\r\n" + string(subtree))
	nestedCMS := signCMS(t, canonicalWire(nestedPart), leaf, leafKey, true)
	nested := filepath.Join(dir, "nested.eml")
	writeDetachedNested(t, nested, nestedPart, nestedCMS)
	sig, err = ParseSignature(nested)
	if err != nil || sig == nil || !sig.Detached {
		t.Fatalf("nested signed message must detect, sig=%+v err=%v", sig, err)
	}
	if _, err := pkcs7.Parse(sig.CMS); err != nil {
		t.Fatalf("nested message CMS must be the real signature, not a content leaf: %v", err)
	}
	if got, want := string(sig.Content), string(canonicalWire(nestedPart)); got != want {
		t.Fatalf("nested content = %q, want %q", got, want)
	}
	if res, err := v.Verify(sig.CMS, sig.Content); err != nil || !res.Valid {
		t.Fatalf("nested signed content must verify (full-part canonicalization): %+v err=%v", res, err)
	}

	// unsigned message is not detected
	plain := filepath.Join(dir, "plain.eml")
	if err := os.WriteFile(plain, []byte("From: alpha@example.com\r\nTo: beta@example.com\r\nSubject: x\r\n\r\nplain\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if sig, err := ParseSignature(plain); err != nil || sig != nil {
		t.Fatalf("unsigned message must not detect, got sig=%+v err=%v", sig, err)
	}
}

// TestCanonicalWire pins the S/MIME line normalization (neomutt
// crypt_write_signed): a lone LF becomes CRLF, while CRLF and lone CR are
// preserved - the digest must reproduce from exactly the recovered bytes.
func TestCanonicalWire(t *testing.T) {
	got := canonicalWire([]byte("a\r\nb\nc\rd"))
	want := "a\r\nb\r\nc\rd"
	if string(got) != want {
		t.Fatalf("canonicalWire = %q, want %q", got, want)
	}
}

func testCA(t *testing.T) (*x509.Certificate, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test smime ca", Organization: []string{"example"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return ca, key
}

func testLeaf(t *testing.T, ca *x509.Certificate, caKey *rsa.PrivateKey) (*x509.Certificate, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "alpha", Organization: []string{"example"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection},
		EmailAddresses:        []string{"alpha@example.com"},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return leaf, key
}

func signCMS(t *testing.T, content []byte, cert *x509.Certificate, key *rsa.PrivateKey, detached bool) []byte {
	t.Helper()
	sd, err := pkcs7.NewSignedData(content)
	if err != nil {
		t.Fatal(err)
	}
	sd.SetDigestAlgorithm(pkcs7.OIDDigestAlgorithmSHA256)
	if err := sd.AddSigner(cert, key, pkcs7.SignerInfoConfig{}); err != nil {
		t.Fatal(err)
	}
	if detached {
		sd.Detach()
	}
	cms, err := sd.Finish()
	if err != nil {
		t.Fatal(err)
	}
	return cms
}

// writeDetached writes a multipart/signed whose first part is content - the
// full part (MIME headers plus body) exactly as a signer hashes it.
func writeDetached(t *testing.T, path string, content, cms []byte) {
	t.Helper()
	b := "From: alpha@example.com\r\n" +
		"To: beta@example.com\r\n" +
		"Subject: signed test\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/signed; protocol=\"application/x-pkcs7-signature\"; micalg=sha-256; boundary=\"sigb\"\r\n" +
		"\r\n" +
		"--sigb\r\n" +
		string(content) + "\r\n" +
		"--sigb\r\n" +
		"Content-Type: application/x-pkcs7-signature; name=\"smime.p7s\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"Content-Disposition: attachment; filename=\"smime.p7s\"\r\n" +
		"\r\n" +
		base64.StdEncoding.EncodeToString(cms) + "\r\n" +
		"--sigb--\r\n"
	if err := os.WriteFile(path, []byte(b), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeDetachedNested writes a multipart/signed whose signed first part is a
// multipart/alternative - the real-world shape that used to defeat CMS
// extraction. content is the full part (alternative header plus subtree body).
func writeDetachedNested(t *testing.T, path string, content, cms []byte) {
	t.Helper()
	b := "From: alpha@example.com\r\n" +
		"To: beta@example.com\r\n" +
		"Subject: nested signed test\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/signed; protocol=\"application/x-pkcs7-signature\"; micalg=sha-256; boundary=\"sigb\"\r\n" +
		"\r\n" +
		"--sigb\r\n" +
		string(content) + "\r\n" +
		"--sigb\r\n" +
		"Content-Type: application/x-pkcs7-signature; name=\"smime.p7s\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		base64.StdEncoding.EncodeToString(cms) + "\r\n" +
		"--sigb--\r\n"
	if err := os.WriteFile(path, []byte(b), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeAttached(t *testing.T, path string, cms []byte) {
	t.Helper()
	b := "From: alpha@example.com\r\n" +
		"To: beta@example.com\r\n" +
		"Subject: attached signed test\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: application/pkcs7-mime; smime-type=signed-data; name=\"smime.p7m\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		base64.StdEncoding.EncodeToString(cms) + "\r\n"
	if err := os.WriteFile(path, []byte(b), 0o600); err != nil {
		t.Fatal(err)
	}
}
