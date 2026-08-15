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
		Body:          Quote(orig, parsed.Parts),
		Signature:     sigName,
		SignatureBody: sigBody,
		MessageID:     parsed.MessageID,
		References:    refs,
		OriginalID:    orig.ID,
	}
}

// ReplyAll adds the original's To+Cc minus the account's own address
// (milestone 1 matches the exact bare address - normalization is
// future work) as the Cc. The original's From stays the To.
func ReplyAll(orig core.Message, parsed *mail.Message, account, from, own string, sigName, sigBody string) *State {
	s := Reply(orig, parsed, account, from, sigName, sigBody)
	s.Mode = ModeReplyAll
	for _, a := range parsed.To {
		if a != own {
			s.Cc = append(s.Cc, a)
		}
	}
	for _, a := range parsed.Cc {
		if a != own {
			s.Cc = append(s.Cc, a)
		}
	}
	return s
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
		Body:          Quote(orig, parsed.Parts),
		Signature:     sigName,
		SignatureBody: sigBody,
		MessageID:     parsed.MessageID,
		References:    refs,
		OriginalID:    orig.ID,
	}
}
