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
// (0600, F5); later writes reuse the same path (mutt's msgbody - the
// message-text row shows it for the tab's lifetime). The buffer holds
// ONLY the mail content: the email header is built from the dialogue
// fields, never the editor. The dialogue state never leaves the model.
func writeEditorBuffer(st compose.State, path string) (string, error) {
	if path == "" {
		// Neovim detects the suffix and selects Markdown syntax rendering.
		pattern := "mutt-notmutt-*"
		if st.Markdown {
			pattern += ".md"
		}
		f, err := os.CreateTemp("", pattern)
		if err != nil {
			return "", err
		}
		path = f.Name()
		f.Close()
	}
	if err := os.WriteFile(path, []byte(compose.MessageBody(st)), 0600); err != nil {
		return "", err
	}
	return path, nil
}

// editorCmd builds the $EDITOR run (fallback vi): whitespace-tokenized
// argv with the buffer path appended (F4 - no shell). EDITOR is trusted config.
func editorCmd(path string) *exec.Cmd {
	e := os.Getenv("EDITOR")
	if strings.TrimSpace(e) == "" {
		e = "vi"
	}
	parts := strings.Fields(e)
	return exec.Command(parts[0], append(parts[1:], path)...)
}

// applyEditorResult reads the edited buffer back: the body plus the
// signature tail (an edited tail detaches and stays as the user's
// text). Header fields never parse from the buffer - the dialogue rows own them.
func applyEditorResult(st compose.State, path string) (compose.State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return st, err
	}
	if st.Markdown {
		body, sigBody := compose.SplitMarkdownBuffer(string(data), st.SignatureBody)
		st.Body, st.SignatureBody = body, sigBody
		if sigBody == "" {
			st.Signature = ""
		}
		return st, nil
	}
	body, sigName, sigBody := compose.ParseBuffer(string(data), st.Signature, st.SignatureBody)
	st.Body, st.Signature, st.SignatureBody = body, sigName, sigBody
	return st, nil
}
