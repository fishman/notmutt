// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package mail

// Mailcap preview support (mutt's mailcap format, R6): an attachment
// whose type matches a copiousoutput entry previews in the pager as
// the command's stdout - a pdf renders as pdftotext text, never as
// bytes. Commands tokenize at parse (F4: argv exec, no shell - a
// pipeline or redirect in a mailcap command is a literal argument
// here); the %s token substitutes the attachment's temp file. User
// entries override the built-ins by type.

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// previewCap bounds a preview command's stdout - the pager render
// budget; a runaway dump must not fill memory.
const previewCap = 1 << 20

// mailcapEntry is one parsed mailcap line: the type key (lowercased,
// "*" wildcard suffix allowed), the tokenized command, and whether
// the command's stdout is the preview text.
type mailcapEntry struct {
	typ     string
	argv    []string // "%s" marks the attachment file slot
	copious bool
}

// Mailcap is the preview-command table: built-ins plus the user's
// mailcap file entries, which override by type.
type Mailcap struct {
	entries []mailcapEntry
}

// DefaultMailcap ships the zero-config previews: a pdf opens as
// pdftotext text (the layout flag keeps columns aligned). The user's
// mailcap file overrides by type.
func DefaultMailcap() *Mailcap {
	return &Mailcap{entries: []mailcapEntry{
		{typ: "application/pdf", argv: []string{"pdftotext", "-layout", "%s", "-"}, copious: true},
	}}
}

// Parse folds one mailcap file's entries in; a later entry of the
// same type replaces the earlier (mutt semantics).
func (mc *Mailcap) Parse(data []byte) {
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ";")
		if len(fields) < 2 {
			continue
		}
		argv := tokenize(fields[1])
		if len(argv) == 0 {
			continue
		}
		copious := false
		for _, f := range fields[2:] { // the flags may span several fields
			for _, w := range strings.Fields(f) {
				if w == "copiousoutput" {
					copious = true
				}
			}
		}
		e := mailcapEntry{typ: strings.ToLower(strings.TrimSpace(fields[0])), argv: argv, copious: copious}
		for i := range mc.entries {
			if mc.entries[i].typ == e.typ {
				mc.entries[i] = e
				e.typ = ""
				break
			}
		}
		if e.typ != "" {
			mc.entries = append(mc.entries, e)
		}
	}
}

// tokenize splits a mailcap command into argv: whitespace splits,
// single quotes group (mutt quotes the %s slot as '%s'); quote
// characters never reach the argv.
func tokenize(s string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == '\'':
			inQuote = !inQuote
		case !inQuote && (r == ' ' || r == '\t'):
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

// PreviewCommand returns the tokenized preview argv for a content
// type: the matching copiousoutput entry's command (exact type first,
// then the "*" wildcard). A non-copious match (exact or wildcard)
// handles the type without a preview - the raw dump never reaches the
// pager (mutt semantics; the reference mailcap's image/* openfile
// entry is exactly this shape).
func (mc *Mailcap) PreviewCommand(typ string) ([]string, bool) {
	typ = strings.ToLower(typ)
	var wildcard *mailcapEntry // the first wildcard match in file order
	for i := range mc.entries {
		e := &mc.entries[i]
		if e.typ == typ {
			if !e.copious {
				return nil, false
			}
			return e.argv, true
		}
		if wildcard == nil && strings.HasSuffix(e.typ, "/*") {
			if strings.HasPrefix(typ, e.typ[:len(e.typ)-1]) {
				wildcard = e
			}
		}
	}
	if wildcard == nil || !wildcard.copious {
		return nil, false
	}
	return wildcard.argv, true
}

// RunPreview executes a preview command: the attachment bytes land in
// a 0600 temp file (F5), the %s token becomes its path, stdout is
// captured capped at previewCap (the excess drains - a big dump must
// not deadlock the Wait). The command dies after 15s: a hung preview
// must not park the bus goroutine.
func RunPreview(argv []string, data []byte) ([]byte, error) {
	f, err := os.CreateTemp("", "notmutt-preview-*")
	if err != nil {
		return nil, err
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	args := make([]string, len(argv)-1)
	for i, a := range argv[1:] {
		if a == "%s" {
			args[i] = path
		} else {
			args[i] = a
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	out, _ := io.ReadAll(io.LimitReader(stdout, previewCap+1))
	io.Copy(io.Discard, stdout) // drain the remainder before Wait
	if err := cmd.Wait(); err != nil {
		return nil, fmt.Errorf("%s: %w: %s", argv[0], err, strings.TrimSpace(errb.String()))
	}
	if len(out) > previewCap {
		out = out[:previewCap]
	}
	return out, nil
}
