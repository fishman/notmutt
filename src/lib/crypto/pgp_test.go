package crypto

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPGPMIMETransformRoundTrip(t *testing.T) {
	if _, err := exec.LookPath("gpg"); err != nil {
		t.Skip("gpg unavailable")
	}
	home := filepath.Join(t.TempDir(), "gnupg")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GNUPGHOME", home)
	cmd := exec.Command("gpg", "--batch", "--passphrase", "", "--quick-generate-key", "Alpha <alpha@example.com>", "default", "default", "never")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generate test key: %v: %s", err, out)
	}

	p := NewPGP("gpg")
	keys, err := p.SecretKeys()
	if err != nil || len(keys) != 1 {
		t.Fatalf("secret key list: %v %#v", err, keys)
	}
	message := []byte("From: Alpha <alpha@example.com>\r\nTo: Alpha <alpha@example.com>\r\nSubject: test\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nhello alpha\r\n")
	for _, security := range []struct{ sign, encrypt bool }{{true, false}, {false, true}, {true, true}} {
		wire, err := TransformPGP(p, message, security.sign, security.encrypt, keys[0].ID, []string{"alpha@example.com"})
		if err != nil {
			t.Fatalf("transform sign=%t encrypt=%t: %v", security.sign, security.encrypt, err)
		}
		plain, status, handled, err := DecodePGPMIME(p, wire)
		if err != nil {
			t.Fatalf("decode sign=%t encrypt=%t: %v", security.sign, security.encrypt, err)
		}
		if !handled || status.Encrypted != security.encrypt || status.Signed != security.sign || (security.sign && !status.Valid) {
			t.Fatalf("status sign=%t encrypt=%t: %+v handled=%t", security.sign, security.encrypt, status, handled)
		}
		if !bytes.Contains(plain, []byte("hello alpha")) {
			t.Fatalf("decoded body missing: %q", plain)
		}
	}
}

func TestParsePGPStatusUsesValidSigHashField(t *testing.T) {
	status := parsePGPStatus([]byte("[GNUPG:] VALIDSIG FINGERPRINT 20260917 0 0 4 0 1 8 00 PRIMARY\n"))
	if status.MICALG != "pgp-sha256" {
		t.Fatalf("micalg = %q, want pgp-sha256", status.MICALG)
	}
}

func TestParsePGPStatusShortRecords(t *testing.T) {
	for _, record := range []string{"[GNUPG:] SIG_CREATED", "[GNUPG:] VALIDSIG", "[GNUPG:] GOODSIG"} {
		_ = parsePGPStatus([]byte(record + "\n"))
	}
}
