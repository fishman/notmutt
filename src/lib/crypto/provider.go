// Package crypto is the R10 crypto boundary, split into per-algorithm seams
// because their surfaces differ: SMIME (this file, in-process) and a future
// PGP seam (gpg CLI, aerc gpgbin pattern). S/MIME is in-process: pkcs7 does
// the CMS parse, digest and signature math; stdlib x509 anchors the chain to
// the configured roots (system CA pool by default, a pinned bundle when
// [crypto] ca-file is set) with an emailProtection EKU gate. No secret on
// the read path, so no CLI - S/MIME is internal always. The seam never
// prompts - it takes a PromptFunction (R10). Verify is live;
// sign/encrypt/decrypt are the send-path follow-on.
package crypto

import (
	"crypto/x509"
	"errors"
	"fmt"
	"os"
)

// PromptFunction is the sole secret-entry path (R10): gpg-agent + external
// pinentry with TUI suspend/resume. A provider asks, never prompts.
type PromptFunction func(prompt string) (string, error)

// SMIME is the S/MIME crypto boundary. Verify is the read path; the caller
// matches VerifyResult.Signer against the From header (identity is
// independent of crypto validity - R10).
type SMIME interface {
	// Verify verifies an S/MIME signature. signedData is the CMS (a
	// detached p7s part, or attached application/pkcs7-mime signed-data);
	// detachedContent is empty for the attached form, else the
	// canonicalized signed bytes.
	Verify(signedData, detachedContent []byte) (*VerifyResult, error)
	// Sign signs a canonicalized MIME part; passphrase-protected keys route
	// through prompt. (Send-path follow-on.)
	Sign(content []byte, keyPath string, prompt PromptFunction) ([]byte, error)
	// Encrypt produces an application/pkcs7-mime envelope. (Send-path
	// follow-on.)
	Encrypt(content []byte, recipientCerts []string) ([]byte, error)
	// Decrypt opens an application/pkcs7-mime envelope. (Send-path
	// follow-on.)
	Decrypt(envelope []byte, keyPath string, prompt PromptFunction) ([]byte, error)
}

// VerifyResult is the S/MIME read-path verdict: Valid and the signer
// identity are separate (R10) - a valid signature from someone else's cert
// must render as a warning, never green.
type VerifyResult struct {
	Content []byte
	Signer  string // cert email (SAN rfc822Name / subject email)
	Valid   bool
	Revoked bool // revocation check ran and found a revoked cert
	Checked bool // revocation was actually checked (fail-open vs unknown)
}

// New builds the in-process S/MIME verifier. A caFile pins trust to exactly
// that PEM bundle - the strict posture, never the bare system pool for
// high-assurance mail. An empty caFile trusts the system CA pool when
// useSystemPool is set (the mainstream, out-of-the-box posture: Thunderbird/
// Outlook trust OS roots; the emailProtection EKU gate still applies), else
// fails closed - a deliberate choice not to trust the system pool.
func New(caFile string, useSystemPool bool) (SMIME, error) {
	roots := x509.NewCertPool()
	if caFile == "" {
		if !useSystemPool {
			return nil, errors.New("crypto: no ca-file and system pool disabled - no S/MIME trust roots")
		}
		sys, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("crypto: system cert pool: %w", err)
		}
		roots = sys
	} else {
		pemBytes, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("crypto: load ca-file: %w", err)
		}
		if !roots.AppendCertsFromPEM(pemBytes) {
			return nil, errors.New("no certificates in ca-file")
		}
	}
	return &smimeInternal{roots: roots}, nil
}
