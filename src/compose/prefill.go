package compose

import (
	"fmt"
	"strings"
	"time"

	"notmutt/core"
	"notmutt/mail"
)

// quoteDepth is the quoted-depth cap; it must match mail.splitBody's
// strip cap - bodies arrive with depth stored in Part.Quoted, never
// deeper.
const quoteDepth = 5

// Quote builds the mutt-style quoted reply body (spec section 6): the
// attribution line and the original body with one extra quote level
// per line (capped at quoteDepth). Lines already quoted keep their
// depth plus one; the bare text re-prefixes so levels stay canonical.
// The original's signature is never quoted.
func Quote(orig core.Message, parts []mail.Part) string {
	var b strings.Builder
	fmt.Fprintf(&b, "On %s, %s wrote:\n", time.Unix(orig.Timestamp, 0).Format("Mon, Jan 2 2006"), orig.Author)
	for _, p := range parts {
		if p.Signature {
			continue
		}
		for _, line := range strings.Split(p.Body, "\n") {
			b.WriteString(quoteLine(p.Quoted, line))
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// quoteLine strips the line's remaining quote markers, then re-prefixes
// one level deeper. base is the part's stored depth (splitBody's
// strip runs past the cap only up to it - production bodies carry
// residual markers beyond the cap, so base + residual markers is the
// true depth). The true depth clamps at quoteDepth: a line deeper than
// the cap re-prefixes at cap+1 - never deeper.
func quoteLine(base int, line string) string {
	depth := base
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

// subjectPrefix strips repeated Re:/Fwd:/Fw: prefixes and returns the
// subject with one prefix of p (mutt's rule: "Re: " replies, "Fwd: "
// forwards). An empty subject after stripping gets the prefix alone.
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

// Reply prefills a reply (spec section 6): To from the original's
// From, the quoted original as the body, one "Re: " prefix, the reply
// headers (In-Reply-To = original message-id, References = the
// original chain plus its own message-id).
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
// mutt_fix_reply_recipients: the original's From stays the To; the
// original's To+Cc minus the account's own address becomes the Cc
// (mailbox-part compare, case-insensitive - a case variant of the own
// address is still the own address), deduped, with entries already in
// the To dropped. When the To ends up empty the Cc becomes the To
// (neomutt's swap).
func ReplyAll(orig core.Message, parsed *mail.Message, account, from, own string, sigName, sigBody string) *State {
	s := Reply(orig, parsed, account, from, sigName, sigBody)
	s.Mode = ModeReplyAll
	s.Cc = replyAllCc(parsed.To, parsed.Cc, own, s.To)
	// a failed From parse leaves To = [""] - treat it as empty, the Cc
	// becomes the To (neomutt's swap)
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

// replyAllCc is the Cc build above: the own address (EqualFold on the
// mailbox part - addresses arrive as bare addr-specs from the parse)
// never lands in the Cc, entries appear once, and entries already in
// the To are not repeated in the Cc.
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
