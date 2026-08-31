// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"errors"
	"fmt"
	"os"

	"notmutt/lib/crypto"
	"notmutt/mail"
)

// smimeVerifyOnce is the headless S/MIME verifier (`notmutt smime-verify
// FILE [CA-FILE]`): extract the signature from a .eml and verify it
// against the system pool, or a pinned bundle when given. Exit 0 for a
// valid signature and for an unsigned message, 1 for an invalid one -
// the script-consumable contract scripts/smime-compare.sh relies on.
func smimeVerifyOnce() error {
	if len(os.Args) < 3 {
		return errors.New("usage: notmutt smime-verify FILE [CA-FILE]")
	}
	ca := ""
	if len(os.Args) > 3 {
		ca = os.Args[3]
	}
	verdict, rc := smimeVerdict(os.Args[2], ca)
	fmt.Println(verdict)
	if rc != 0 {
		os.Exit(rc)
	}
	return nil
}

// smimeVerdict verifies one message file and returns the human verdict
// plus its exit code. "not signed" is a valid state (nothing to verify),
// so it exits 0; a parse failure or a failed signature exits 1.
func smimeVerdict(path, caFile string) (string, int) {
	sig, err := mail.ParseSignature(path)
	if err != nil {
		return fmt.Sprintf("error: %v", err), 1
	}
	if sig == nil {
		return "not signed", 0
	}
	v, err := crypto.New(caFile, true)
	if err != nil {
		return fmt.Sprintf("error: %v", err), 1
	}
	res, err := v.Verify(sig.CMS, sig.Content)
	if err != nil {
		return fmt.Sprintf("invalid: %v", err), 1
	}
	return fmt.Sprintf("valid: signer %s", res.Signer), 0
}
