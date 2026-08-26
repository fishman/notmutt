package crypto

import (
	"crypto/x509"
	"strings"
	"testing"
)

// TestUsableForEmail pins the EKU gate (the openssl -purpose smimesign
// equivalent): a cert constrained to a non-mail purpose must fail, while
// emailProtection, Any, or an unconstrained cert pass.
func TestUsableForEmail(t *testing.T) {
	cases := []struct {
		name string
		eku  []x509.ExtKeyUsage
		want bool
	}{
		{"unconstrained (no EKU) passes", nil, true},
		{"emailProtection passes", []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection}, true},
		{"Any passes", []x509.ExtKeyUsage{x509.ExtKeyUsageAny}, true},
		{"emailProtection+serverAuth passes", []x509.ExtKeyUsage{x509.ExtKeyUsageEmailProtection, x509.ExtKeyUsageServerAuth}, true},
		{"serverAuth only fails", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, false},
		{"codeSigning only fails", []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning}, false},
	}
	for _, c := range cases {
		if got := usableForEmail(&x509.Certificate{ExtKeyUsage: c.eku}); got != c.want {
			t.Errorf("%s: usableForEmail = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestNewEmptyUsesSystemPool pins the trust-root default: an empty ca-file
// loads the system CA pool (mainstream out-of-the-box posture, R10 EKU gate
// still enforced), not the old fail-closed error. On hosts with no system
// roots the pool error is environment, not a config mistake.
func TestNewEmptyUsesSystemPool(t *testing.T) {
	v, err := New("", true)
	if err != nil {
		if strings.Contains(err.Error(), "requires a ca-file") {
			t.Fatalf("empty ca-file must no longer fail closed: %v", err)
		}
		t.Logf("system cert pool unavailable on this host: %v", err)
		return
	}
	if v == nil {
		t.Fatal("New(\"\") returned nil verifier with nil error")
	}
}

// TestNewFailClosed pins the system-pool disable: an empty ca-file with
// use-system-pool off must not verify anything, never silently.
func TestNewFailClosed(t *testing.T) {
	if _, err := New("", false); err == nil {
		t.Fatal("New(\"\", false) must error: no ca-file and system pool disabled")
	}
}
