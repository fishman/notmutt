// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package compose

import (
	"fmt"
	"strings"
	"time"

	"notmutt/core"
	"notmutt/mail"
)

// quoteDepth is the quoted-depth cap; must match mail.splitBody's strip
// cap - bodies arrive with depth in Part.Quoted, never deeper.
const quoteDepth = 5

// Quote builds the mutt-style quoted reply body (spec section 6): the
// attribution line and the original with one extra quote level per
// line (capped at quoteDepth). The original's signature is never quoted.
func Quote(orig core.Message, parts []mail.Part) string {
	var b strings.Builder
	fmt.Fprintf(&b, "On %s, %s wrote:\n", time.Unix(orig.Timestamp, 0).Format("Mon, Jan 2 2006"), orig.Author)
	for _, p := range parts {
		if p.Signature {
			continue
		}
		for _, line := range strings.Split(p.Body, "\n") {
			b.WriteString(quoteLine(line))
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// quoteLine counts quote markers, then re-prefixes one level deeper.
// The markers are the text itself (splitBody stores the raw line, never
// a stripped copy); true depth clamps at quoteDepth - a deeper line
// re-prefixes at cap+1, never deeper.
func quoteLine(line string) string {
	depth := 0
	for {
		rest := strings.TrimPrefix(line, ">")
		if rest == line {
			break
		}
		depth++
		line = strings.TrimPrefix(rest, " ")
	}
	if depth > quoteDepth {
		depth = quoteDepth
	}
	return strings.Repeat(">", depth+1) + " " + line
}

// subjectPrefix strips repeated Re:/Fwd:/Fw: prefixes, returns the
// subject with one prefix of p (mutt's rule: "Re: " replies, "Fwd: "
// forwards); empty after stripping -> the prefix alone.
func subjectPrefix(subject, p string) string {
	for {
		t := strings.TrimSpace(subject)
		l := strings.ToLower(t)
		stripped := false
		for _, pre := range []string{"re:", "fwd:", "fw:"} {
			if strings.HasPrefix(l, pre) {
				subject = t[len(pre):]
				stripped = true
				break
			}
		}
		if !stripped {
			break
		}
	}
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return p
	}
	return p + subject
}

// Reply prefills a reply (spec section 6): To = original's From, the
// quoted original as the body, one "Re: " prefix, reply headers
// (In-Reply-To = original message-id, References = chain + own message-id).
func Reply(orig core.Message, parsed *mail.Message, account, from, sigName, sigBody string) *State {
	refs := orig.References
	if parsed.MessageID != "" {
		refs = append(append([]string{}, orig.References...), parsed.MessageID)
	}
	return &State{
		Mode:          ModeReply,
		Account:       account,
		From:          from,
		To:            []string{parsed.From},
		Subject:       subjectPrefix(parsed.Subject, "Re: "),
		Body:          Quote(orig, mail.QuoteParts(parsed.Parts, 0)),
		Signature:     sigName,
		SignatureBody: sigBody,
		MessageID:     parsed.MessageID,
		References:    refs,
		OriginalID:    orig.ID,
	}
}

// ReplyAll builds the recipients per neomutt's mutt_fetch_recips /
// mutt_fix_reply_recipients: the original's From stays the To; To+Cc
// minus the account's own address becomes the Cc (mailbox-part compare,
// case-insensitive - a case variant of the own address is still the own
// address), deduped, To entries dropped. An empty To takes the Cc
// (neomutt's swap).
func ReplyAll(orig core.Message, parsed *mail.Message, account, from, own string, sigName, sigBody string) *State {
	s := Reply(orig, parsed, account, from, sigName, sigBody)
	s.Mode = ModeReplyAll
	s.Cc = replyAllCc(parsed.To, parsed.Cc, own, s.To)
	// a failed From parse leaves To = [""] - the Cc becomes the To (neomutt's swap)
	empty := true
	for _, t := range s.To {
		if t != "" {
			empty = false
			break
		}
	}
	if len(s.Cc) > 0 && empty {
		s.To = s.Cc
		s.Cc = nil
	}
	return s
}

// replyAllCc builds the Cc: the own address (EqualFold on the mailbox
// part - addresses arrive as bare addr-specs) never lands in it,
// entries appear once, and To entries are not repeated.
func replyAllCc(to, cc []string, own string, inTo []string) []string {
	var out []string
	for _, a := range append(append([]string{}, to...), cc...) {
		if a == "" || strings.EqualFold(a, own) {
			continue
		}
		dup := false
		for _, o := range out {
			if strings.EqualFold(a, o) {
				dup = true
				break
			}
		}
		if !dup {
			for _, t := range inTo {
				if strings.EqualFold(a, t) {
					dup = true
					break
				}
			}
		}
		if !dup {
			out = append(out, a)
		}
	}
	return out
}

// Forward prefills a forward: no recipients, one "Fwd: " prefix, the
// quoted original as the body.
func Forward(orig core.Message, parsed *mail.Message, account, from, sigName, sigBody string) *State {
	refs := orig.References
	if parsed.MessageID != "" {
		refs = append(append([]string{}, orig.References...), parsed.MessageID)
	}
	return &State{
		Mode:          ModeForward,
		Account:       account,
		From:          from,
		Subject:       subjectPrefix(parsed.Subject, "Fwd: "),
		Body:          Quote(orig, mail.QuoteParts(parsed.Parts, 0)),
		Signature:     sigName,
		SignatureBody: sigBody,
		MessageID:     parsed.MessageID,
		References:    refs,
		OriginalID:    orig.ID,
	}
}
