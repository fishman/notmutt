// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

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
	"strings"
	"testing"
	"time"

	"go.mozilla.org/pkcs7"
)

// TestSMIMEVerdict drives the headless verifier's contract end to end
// (scripts/smime-compare.sh): a signed .eml verifies against its CA
// (exit 0), the same file fails against an unrelated CA (exit 1), and an
// unsigned message is "not signed" (exit 0).
func TestSMIMEVerdict(t *testing.T) {
	dir := t.TempDir()
	ca, caKey := smimeCA(t)
	leaf, leafKey := smimeLeaf(t, ca, caKey)
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	other, _ := smimeCA(t)
	otherPath := filepath.Join(dir, "other.pem")
	if err := os.WriteFile(otherPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: other.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}

	signed := filepath.Join(dir, "signed.eml")
	sd, err := pkcs7.NewSignedData([]byte("hello alpha\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	sd.SetDigestAlgorithm(pkcs7.OIDDigestAlgorithmSHA256)
	if err := sd.AddSigner(leaf, leafKey, pkcs7.SignerInfoConfig{}); err != nil {
		t.Fatal(err)
	}
	cms, err := sd.Finish()
	if err != nil {
		t.Fatal(err)
	}
	writeSMIMEMessage(t, signed, cms)

	if v, rc := smimeVerdict(signed, caPath); rc != 0 || v != "valid: signer alpha@example.com" {
		t.Fatalf("valid message: verdict=%q rc=%d", v, rc)
	}
	if v, rc := smimeVerdict(signed, otherPath); rc != 1 || !strings.HasPrefix(v, "invalid:") {
		t.Fatalf("untrusted root: verdict=%q rc=%d", v, rc)
	}
	plain := filepath.Join(dir, "plain.eml")
	if err := os.WriteFile(plain, []byte("From: a@example.com\r\n\r\nplain\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if v, rc := smimeVerdict(plain, ""); rc != 0 || v != "not signed" {
		t.Fatalf("unsigned message: verdict=%q rc=%d", v, rc)
	}
}

func smimeCA(t *testing.T) (*x509.Certificate, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test smime ca"},
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
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return c, key
}

func smimeLeaf(t *testing.T, ca *x509.Certificate, caKey *rsa.PrivateKey) (*x509.Certificate, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(2),
		Subject:               pkix.Name{CommonName: "alpha"},
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
	l, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return l, key
}

// writeSMIMEMessage writes an attached application/pkcs7-mime signed-data
// message - the compact fixture shape for the verifier contract.
func writeSMIMEMessage(t *testing.T, path string, cms []byte) {
	t.Helper()
	b := "From: alpha@example.com\r\n" +
		"To: beta@example.com\r\n" +
		"Subject: signed test\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: application/pkcs7-mime; smime-type=signed-data; name=\"smime.p7m\"\r\n" +
		"Content-Transfer-Encoding: base64\r\n" +
		"\r\n" +
		base64.StdEncoding.EncodeToString(cms) + "\r\n"
	if err := os.WriteFile(path, []byte(b), 0o600); err != nil {
		t.Fatal(err)
	}
}
