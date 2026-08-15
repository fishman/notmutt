// Package compose is the send/reply dialogue state machine (R4): pure
// Go - no UI code, no notmuch handle (R5). The tui renders it, the
// app runs its sends; the state survives pauses (tab parking, editor
// runs) because it lives here, not in a widget.
package compose

import (
	"fmt"
	"os"
	"path/filepath"
)

// Mode is the dialogue kind.
type Mode int

const (
	ModeCompose Mode = iota
	ModeReply
	ModeReplyAll
	ModeForward
)

func (m Mode) String() string {
	switch m {
	case ModeReply:
		return "reply"
	case ModeReplyAll:
		return "reply-all"
	case ModeForward:
		return "forward"
	}
	return "compose"
}

// Phase is the dialogue state machine (spec section 5): editing ->
// (y) sending -> sent (tab closes) | failed (Output kept, e retries);
// q arms aborting, a second q confirms.
type Phase int

const (
	PhaseEditing Phase = iota
	PhaseAborting
	PhaseSending
	PhaseFailed
)

// Attachment is one composed attachment: the file path, its base name
// on the wire, its size.
type Attachment struct {
	Name, Path string
	Size       int64
}

// Security is the dialogue's crypto flag set (R10): none, sign,
// encrypt, sign+encrypt. A dialogue flag only - the transport ignores
// it (no crypto engine yet); it renders and survives the event round
// trip.
type Security int

const (
	SecurityNone Security = iota
	SecuritySign
	SecurityEncrypt
	SecuritySignEncrypt
)

func (s Security) String() string {
	switch s {
	case SecuritySign:
		return "sign"
	case SecurityEncrypt:
		return "encrypt"
	case SecuritySignEncrypt:
		return "sign+encrypt"
	}
	return "none"
}

func (s Security) Next() Security {
	return Security((s + 1) % (SecuritySignEncrypt + 1))
}

// State is one dialogue (R4): fields, attachments, send progress,
// error output. The signature is stored SEPARATELY from the body
// (SignatureBody) - the body is the user's edited text, the signature
// is re-attached at buffer build and assembly. A parsed-back buffer
// whose tail no longer matches the attached signature detaches it
// (the tail is the user's text now), so Body never carries an
// attached block - the spec's exact-tail replace is structural.
type State struct {
	ID            string
	Mode          Mode
	Account       string
	From          string
	To, Cc        []string
	Bcc, ReplyTo  []string
	Subject       string
	Body          string
	Attachments   []Attachment
	Signature     string // signature name ("" = none)
	SignatureBody string
	Fcc           string // sent-folder path, derived from the account
	Security      Security
	MessageID     string // original message-id (In-Reply-To)
	References    []string
	OriginalID    string // original notmuch id (reply/forward tagging)
	Phase         Phase
	Output        string // send job captured output (failed)
}

// NewCompose opens a blank compose dialogue.
func NewCompose(account, from, sigName, sigBody string) *State {
	return &State{
		Mode: ModeCompose, Account: account, From: from,
		Signature: sigName, SignatureBody: sigBody,
	}
}

// SetSignature switches the signature (the fuzzy picker): the body is
// untouched - the previous block, if still attached, is stored
// separately, so replacing it is a field swap.
func (s *State) SetSignature(name, body string) {
	s.Signature, s.SignatureBody = name, body
}

// AddAttachment stats path and appends it (name = base, size =
// stat). Directories and missing paths error - the prompt stays open.
func (s *State) AddAttachment(path string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if fi.IsDir() {
		return fmt.Errorf("%s: is a directory", path)
	}
	s.Attachments = append(s.Attachments, Attachment{Name: filepath.Base(path), Path: path, Size: fi.Size()})
	return nil
}
