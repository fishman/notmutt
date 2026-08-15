package compose

import (
	"os"
	"strings"

	"notmutt/core"
)

// ToEvent/FromEvent map the dialogue across the bus: the app opens a
// dialogue (builds the State) and publishes core.ComposeOpened; the
// TUI rebuilds the State from the event. The bus contract is core
// types only (core cannot import compose) - compose owns the mapping.
func ToEvent(s *State) core.ComposeOpened {
	e := core.ComposeOpened{
		TabID: s.ID, Mode: s.Mode.String(), Account: s.Account, From: s.From,
		To: s.To, Cc: s.Cc, Bcc: s.Bcc, ReplyTo: s.ReplyTo,
		Subject: s.Subject, Body: s.Body, Fcc: s.Fcc, Security: s.Security.String(),
		Signature: s.Signature, SigContent: s.SignatureBody,
		MessageID: s.MessageID, References: s.References, OriginalID: s.OriginalID,
	}
	for _, a := range s.Attachments {
		e.Attachments = append(e.Attachments, core.ComposeAttachment{Name: a.Name, Path: a.Path, Size: a.Size})
	}
	return e
}

func FromEvent(e core.ComposeOpened) *State {
	s := &State{
		ID: e.TabID, Mode: parseMode(e.Mode), Account: e.Account, From: e.From,
		To: e.To, Cc: e.Cc, Bcc: e.Bcc, ReplyTo: e.ReplyTo,
		Subject: e.Subject, Body: e.Body, Fcc: e.Fcc, Security: parseSecurity(e.Security),
		Signature: e.Signature, SignatureBody: e.SigContent,
		MessageID: e.MessageID, References: e.References, OriginalID: e.OriginalID,
	}
	for _, a := range e.Attachments {
		s.Attachments = append(s.Attachments, Attachment{Name: a.Name, Path: a.Path, Size: a.Size})
	}
	return s
}

func parseMode(s string) Mode {
	switch s {
	case "reply":
		return ModeReply
	case "reply-all":
		return ModeReplyAll
	case "forward":
		return ModeForward
	}
	return ModeCompose
}

func parseSecurity(s string) Security {
	switch s {
	case "sign":
		return SecuritySign
	case "encrypt":
		return SecurityEncrypt
	case "sign+encrypt":
		return SecuritySignEncrypt
	}
	return SecurityNone
}

// ExpandHome expands a leading ~ to the user's home dir (the config
// path style; the client knows no mail root - the sent_folder is a
// full path, muttrc record shape).
func ExpandHome(p string) string {
	if !strings.HasPrefix(p, "~") {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	return home + p[1:]
}
