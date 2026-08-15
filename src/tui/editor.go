package tui

import (
	"os"
	"os/exec"
	"strings"

	"notmutt/compose"
)

// writeEditorBuffer writes the editor buffer to a 0600 temp file (F5)
// and returns its path. The write is the buffer contract's local half
// - the dialogue state itself never leaves the model.
func writeEditorBuffer(st compose.State) (string, error) {
	f, err := os.CreateTemp("", "notmutt-compose-*")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(st.BuildBuffer()); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
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
// the state: headers parsed, the signature tail detached per its rule
// (an edited tail stays as the user's text).
func applyEditorResult(st compose.State, path string) (compose.State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return st, err
	}
	to, cc, subject, body, sigName, sigBody := compose.ParseBuffer(string(data), st.Signature, st.SignatureBody)
	st.To, st.Cc, st.Subject, st.Body = to, cc, subject, body
	st.Signature, st.SignatureBody = sigName, sigBody
	return st, nil
}
