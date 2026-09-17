package crypto

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	mimeutil "notmutt/lib/mimeutil"
)

func TestPGPMIMETransformRoundTrip(t *testing.T) {
	p, key := testPGP(t)
	message := []byte(fmt.Sprintf("From: Alpha <alpha@example.com>\r\nTo: Alpha <alpha@example.com>\r\nSubject: test\r\nContent-Type: %s; charset=utf-8\r\n\r\nhello alpha\r\n", mimeutil.TextPlain))
	for _, security := range []struct{ sign, encrypt bool }{{true, false}, {false, true}, {true, true}} {
		wire, err := TransformPGP(p, message, security.sign, security.encrypt, key, []string{"alpha@example.com"})
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

func TestPGPMICALG(t *testing.T) {
	for id, want := range map[string]string{"5": "pgp-md2", "6": "pgp-tiger192", "7": "pgp-haval-5-160", "8": "pgp-sha256"} {
		if got := pgpMICALG(id); got != want {
			t.Fatalf("micalg(%s) = %q, want %q", id, got, want)
		}
	}
}

func TestDecodePGPMIMEVerifiesOriginalSignedPart(t *testing.T) {
	p, key := testPGP(t)
	entity := []byte(fmt.Sprintf("X-Zeta: z\r\nX-Alpha: first\r\n\tcontinued\r\nContent-Type: %s\r\n\r\nalpha\r\n", mimeutil.TextPlain))
	sig, _, err := p.Sign(entity, key)
	if err != nil {
		t.Fatal(err)
	}
	wire := []byte(fmt.Sprintf("Content-Type: %s; boundary=alpha; protocol=\"%s\"\r\n\r\n--alpha\r\n%s--alpha\r\nContent-Type: %s\r\n\r\n%s\r\n--alpha--\r\n", mimeutil.MultipartSigned, mimeutil.PGPSignature, entity, mimeutil.PGPSignature, sig))
	_, status, handled, err := DecodePGPMIME(p, wire)
	if err != nil || !handled || !status.Valid {
		t.Fatalf("status=%+v handled=%t err=%v", status, handled, err)
	}
}

func TestDecodePGPMIMEDecodesEncryptedPartTransferEncoding(t *testing.T) {
	p, _ := testPGP(t)
	entity := []byte(fmt.Sprintf("Content-Type: %s\r\n\r\nalpha\r\n", mimeutil.TextPlain))
	ciphertext, _, err := p.Encrypt(entity, []string{"alpha@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	wire := []byte(fmt.Sprintf("Content-Type: %s; boundary=alpha; protocol=\"%s\"\r\n\r\n--alpha\r\nContent-Type: %s\r\n\r\nVersion: 1\r\n--alpha\r\nContent-Type: %s\r\nContent-Transfer-Encoding: base64\r\n\r\n%s\r\n--alpha--\r\n", mimeutil.MultipartEncrypted, mimeutil.PGPEncrypted, mimeutil.PGPEncrypted, mimeutil.OctetStream, base64.StdEncoding.EncodeToString(ciphertext)))
	plain, status, handled, err := DecodePGPMIME(p, wire)
	if err != nil || !handled || !status.Encrypted || !bytes.Contains(plain, []byte("alpha")) {
		t.Fatalf("plain=%q status=%+v handled=%t err=%v", plain, status, handled, err)
	}
}

func TestDecodePGPMIMEHandlesExchangeMixedEnvelope(t *testing.T) {
	p, _ := testPGP(t)
	entity := []byte(fmt.Sprintf("Content-Type: %s\r\n\r\nalpha\r\n", mimeutil.TextPlain))
	ciphertext, _, err := p.Encrypt(entity, []string{"alpha@example.com"})
	if err != nil {
		t.Fatal(err)
	}
	wire := []byte(fmt.Sprintf("Content-Type: %s; boundary=alpha\r\n\r\n--alpha\r\nContent-Type: %s\r\n\r\n\r\n--alpha\r\nContent-Type: %s\r\n\r\nVersion: 1\r\n--alpha\r\nContent-Type: %s\r\nContent-Transfer-Encoding: base64\r\n\r\n%s\r\n--alpha--\r\n", mimeutil.MultipartMixed, mimeutil.TextPlain, mimeutil.PGPEncrypted, mimeutil.OctetStream, base64.StdEncoding.EncodeToString(ciphertext)))
	plain, status, handled, err := DecodePGPMIME(p, wire)
	if err != nil || !handled || !status.Encrypted || !bytes.Contains(plain, []byte("alpha")) {
		t.Fatalf("plain=%q status=%+v handled=%t err=%v", plain, status, handled, err)
	}
}

func TestPGPRejectsOversizeInput(t *testing.T) {
	_, _, err := NewPGP("gpg").Decrypt(make([]byte, MaxMessageBytes+1))
	if !errors.Is(err, errMessageTooLarge) {
		t.Fatalf("error = %v, want size limit", err)
	}
}

func testPGP(t *testing.T) (PGP, string) {
	t.Helper()
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
	keys, err := NewPGP("gpg").SecretKeys()
	if err != nil || len(keys) != 1 {
		t.Fatalf("secret key list: %v %#v", err, keys)
	}
	return NewPGP("gpg"), keys[0].ID
}
