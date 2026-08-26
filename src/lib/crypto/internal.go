package crypto

import (
	"crypto/x509"
	"fmt"

	"go.mozilla.org/pkcs7"
)

// smimeInternal is the in-process S/MIME backend (R10 read path): pkcs7
// does the CMS parse, digest and signature math; the trust anchoring is
// stdlib x509 against the pinned roots with an emailProtection EKU gate.
type smimeInternal struct {
	roots *x509.CertPool
}

func (p *smimeInternal) Verify(signedData, detachedContent []byte) (*VerifyResult, error) {
	p7, err := pkcs7.Parse(signedData)
	if err != nil {
		return nil, fmt.Errorf("smime: parse cms: %w", err)
	}
	if len(detachedContent) > 0 {
		p7.Content = detachedContent // detached form: the p7s carries no content
	}

	// the signer, extracted - not yet trusted
	signer := p7.GetOnlySigner()
	if signer == nil {
		return nil, fmt.Errorf("smime: expected exactly one signer")
	}

	// EKU gate (openssl's -purpose smimesign): a cert constrained to
	// serverAuth/other purposes must not pass as a mail signer. pkcs7's
	// VerifyWithChain uses ExtKeyUsageAny, so this gate is ours. An
	// unconstrained cert (no EKU extension) is valid for S/MIME per RFC 5280.
	if !usableForEmail(signer) {
		return nil, fmt.Errorf("smime: signer cert is not valid for S/MIME email protection")
	}

	// digest + signature binding + chain to OUR pinned roots. pkcs7 builds
	// the chain from the message's embedded certs as intermediates and
	// anchors to roots; it checks validity at the signing time attribute
	// when present, else now.
	if err := p7.VerifyWithChain(p.roots); err != nil {
		return nil, fmt.Errorf("smime: signature or chain: %w", err)
	}

	// revocation, per account policy (fail-open/closed/not-checked; never
	// claim a check that did not run - R10)
	revoked, checked := revoke(signer)

	return &VerifyResult{
		Content: p7.Content,
		Signer:  certEmail(signer),
		Valid:   true,
		Revoked: revoked,
		Checked: checked,
	}, nil
}

func (p *smimeInternal) Sign([]byte, string, PromptFunction) ([]byte, error) {
	return nil, errSendPath("sign")
}
func (p *smimeInternal) Encrypt([]byte, []string) ([]byte, error) {
	return nil, errSendPath("encrypt")
}
func (p *smimeInternal) Decrypt([]byte, string, PromptFunction) ([]byte, error) {
	return nil, errSendPath("decrypt")
}

// usableForEmail reports whether the cert may sign mail: an EKU extension
// present but not including emailProtection (or Any) fails; absent EKU is
// unconstrained and allowed.
func usableForEmail(c *x509.Certificate) bool {
	if len(c.ExtKeyUsage) == 0 {
		return true
	}
	for _, u := range c.ExtKeyUsage {
		if u == x509.ExtKeyUsageEmailProtection || u == x509.ExtKeyUsageAny {
			return true
		}
	}
	return false
}

// certEmail returns the signer's first email: stdlib populates
// EmailAddresses from both the SAN rfc822Name and the deprecated subject
// email attribute.
func certEmail(c *x509.Certificate) string {
	if len(c.EmailAddresses) > 0 {
		return c.EmailAddresses[0]
	}
	return ""
}

// revoke is the R10 revocation policy seam: per-account fail-open vs
// fail-closed vs not-checked. Default not-checked (ecosystem parity), never
// claiming a check that did not run. Wire CRL (stdlib) / OCSP
// (golang.org/x/crypto/ocsp) here when an account opts in.
func revoke(_ *x509.Certificate) (revoked, checked bool) {
	return false, false
}

// errSendPath marks the send-path methods as a separate follow-on (R10):
// sign/encrypt/decrypt touch the private key and inherit the shared prompt
// path, and are not implemented yet.
func errSendPath(op string) error {
	return fmt.Errorf("smime: %s: send-path backend not yet implemented", op)
}
