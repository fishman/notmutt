package crypto

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

// PGPStatus is the machine-readable verdict emitted by gpg.
type PGPStatus struct {
	Encrypted bool
	Signed    bool
	Valid     bool
	Signer    string
	Err       string
	MICALG    string
}

// MaxMessageBytes bounds PGP's in-memory message and subprocess buffers.
const MaxMessageBytes = 32 << 20

var errMessageTooLarge = errors.New("pgp: message exceeds 32 MiB")

type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(p) > MaxMessageBytes-b.Len() {
		return 0, errMessageTooLarge
	}
	return b.Buffer.Write(p)
}

// PGPKey is one selectable secret-key identity.
type PGPKey struct {
	ID, Label string
}

// PGP uses the user's gpg-agent and external pinentry. It never accepts a
// passphrase, creates a shell, or logs message data.
type PGP struct{ Command string }

func NewPGP(command string) PGP {
	if command == "" {
		command = "gpg"
	}
	return PGP{Command: command}
}

func (p PGP) run(input []byte, args ...string) ([]byte, PGPStatus, error) {
	if len(input) > MaxMessageBytes {
		return nil, PGPStatus{}, errMessageTooLarge
	}
	argv := make([]string, 0, len(args)+4)
	argv = append(argv, "--batch", "--no-tty", "--status-fd=2")
	argv = append(argv, args...)
	cmd := exec.Command(p.Command, argv...)
	cmd.Stdin = bytes.NewReader(input)
	var stdout, stderr limitedBuffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if errors.Is(err, errMessageTooLarge) {
		return nil, PGPStatus{}, errMessageTooLarge
	}
	status := parsePGPStatus(stderr.Bytes())
	if err != nil {
		if status.Err != "" {
			return nil, status, fmt.Errorf("gpg: %s", status.Err)
		}
		return nil, status, fmt.Errorf("gpg: %w", err)
	}
	if status.Err != "" {
		return nil, status, fmt.Errorf("gpg: %s", status.Err)
	}
	return stdout.Bytes(), status, nil
}

func (p PGP) Sign(content []byte, key string) ([]byte, PGPStatus, error) {
	args := []string{"--armor", "--detach-sign"}
	if key != "" {
		args = append(args, "--local-user", key)
	}
	return p.run(content, args...)
}

func (p PGP) Encrypt(content []byte, recipients []string) ([]byte, PGPStatus, error) {
	args := []string{"--armor"}
	for _, recipient := range recipients {
		args = append(args, "--recipient", recipient)
	}
	args = append(args, "--encrypt")
	return p.run(content, args...)
}

func (p PGP) Decrypt(content []byte) ([]byte, PGPStatus, error) {
	out, status, err := p.run(content, "--decrypt")
	status.Encrypted = true
	return out, status, err
}

// SecretKeys lists selectable identities without exposing private material.
func (p PGP) SecretKeys() ([]PGPKey, error) {
	cmd := exec.Command(p.Command, "--batch", "--with-colons", "--list-secret-keys")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gpg: list secret keys: %w", err)
	}
	var keys []PGPKey
	var fpr string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(line, ":")
		if len(f) < 10 {
			continue
		}
		switch f[0] {
		case "fpr":
			fpr = f[9]
		case "uid":
			if fpr == "" || f[1] == "r" || f[1] == "e" {
				continue
			}
			label, err := url.PathUnescape(f[9])
			if err != nil {
				label = f[9]
			}
			keys = append(keys, PGPKey{ID: fpr, Label: label})
		}
	}
	return keys, nil
}

func parsePGPStatus(data []byte) PGPStatus {
	var s PGPStatus
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 || fields[0] != "[GNUPG:]" {
			continue
		}
		switch fields[1] {
		case "SIG_CREATED":
			s.Signed = true
			if len(fields) > 4 {
				s.MICALG = pgpMICALG(fields[4])
			}
		case "GOODSIG":
			s.Signed, s.Valid = true, true
			if len(fields) > 3 {
				s.Signer = strings.Join(fields[3:], " ")
			}
		case "VALIDSIG":
			s.Signed, s.Valid = true, true
			if len(fields) > 9 {
				s.MICALG = pgpMICALG(fields[9])
			}
		case "DECRYPTION_OKAY":
			s.Encrypted = true
		case "DECRYPTION_FAILED", "BADSIG", "EXPSIG", "EXPKEYSIG", "REVKEYSIG", "ERRSIG", "NO_PUBKEY":
			s.Signed = s.Signed || fields[1] != "DECRYPTION_FAILED"
			s.Err = strings.ToLower(strings.ReplaceAll(fields[1], "_", " "))
		case "FAILURE":
			if s.Err == "" {
				s.Err = "operation failed"
			}
		}
	}
	return s
}

func pgpMICALG(id string) string {
	switch id {
	case "1":
		return "pgp-md5"
	case "2":
		return "pgp-sha1"
	case "3":
		return "pgp-ripemd160"
	case "5":
		return "pgp-md2"
	case "6":
		return "pgp-tiger192"
	case "7":
		return "pgp-haval-5-160"
	case "8":
		return "pgp-sha256"
	case "9":
		return "pgp-sha384"
	case "10":
		return "pgp-sha512"
	case "11":
		return "pgp-sha224"
	default:
		return "pgp-unknown"
	}
}
