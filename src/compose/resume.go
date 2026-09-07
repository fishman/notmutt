// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package compose

import (
	"path/filepath"

	"notmutt/core"
	"notmutt/mail"
)

// Resume prefills a resumed saved draft (spec section 3): the envelope
// as saved, the body with its signature tail detached structurally
// (name lost - the text is authoritative), no account default signature
// injected, and no thread identity - Mode stays compose, so Assemble
// issues a fresh Message-ID and no replied/forwarded tag ever fires.
// ResumePath is the stored file being edited (retired on a successful
// send). Attachments are not extracted anywhere: each maps to the stored
// draft with its part ordinal (DraftPart), so assemble streams the bytes
// out of the still-present file.
func Resume(orig core.Message, d *mail.Draft, account, from string) *State {
	body, sig := SplitSignature(d.Body)
	st := &State{
		Mode:          ModeCompose,
		Account:       account,
		From:          from,
		To:            d.To,
		Cc:            d.Cc,
		Bcc:           d.Bcc,
		ReplyTo:       d.ReplyTo,
		Subject:       d.Subject,
		Body:          body,
		SignatureBody: sig,
	}
	if len(orig.Paths) > 0 {
		st.ResumePath = orig.Paths[0]
		for _, a := range d.Atts {
			st.Attachments = append(st.Attachments, Attachment{
				Name: filepath.Base(a.Name), Path: st.ResumePath,
				DraftPart: a.Ordinal + 1, Size: a.Size, MimeType: a.MimeType,
			})
		}
	}
	return st
}
