// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"os"
	"os/exec"
	"strings"

	"notmutt/compose"
)

// writeEditorBuffer writes the editor buffer to the tab's buffer file
// (0600, F5): the first write creates the file, later writes reuse the
// same path - the message-text row shows it for the tab's lifetime
// (mutt's msgbody). The buffer holds ONLY the mail content (mutt's
// shape - the email header is built from the dialogue fields, never
// the editor). The write is the buffer contract's local half - the
// dialogue state itself never leaves the model.
func writeEditorBuffer(st compose.State, path string) (string, error) {
	if path == "" {
		// mutt-family temp name: neovim's filetype detection maps
		// mutt-*/neomutt-* basenames to the mail filetype
		f, err := os.CreateTemp("", "mutt-notmutt-*")
		if err != nil {
			return "", err
		}
		path = f.Name()
		f.Close() // the rewrite below reopens it
	}
	if err := os.WriteFile(path, []byte(compose.BodyWithSig(st.Body, st.SignatureBody)), 0600); err != nil {
		return "", err
	}
	return path, nil
}

// editorCmd builds the $EDITOR run (fallback vi): whitespace-tokenized
// argv, the buffer path appended (F4 - no shell, no interpolation).
// The EDITOR value is trusted config, not mail content.
func editorCmd(path string) *exec.Cmd {
	e := os.Getenv("EDITOR")
	if strings.TrimSpace(e) == "" {
		e = "vi"
	}
	parts := strings.Fields(e)
	return exec.Command(parts[0], append(parts[1:], path)...)
}

// applyEditorResult reads the edited buffer and applies it back to
// the state: the content (body + signature tail, detached per its
// rule - an edited tail stays as the user's text). The header fields
// never parse from the buffer; the dialogue rows own them.
func applyEditorResult(st compose.State, path string) (compose.State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return st, err
	}
	body, sigName, sigBody := compose.ParseBuffer(string(data), st.Signature, st.SignatureBody)
	st.Body, st.Signature, st.SignatureBody = body, sigName, sigBody
	return st, nil
}
