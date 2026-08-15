package compose

import (
	"fmt"
	"strings"
)

// SigBlock is the signature block below the body: a blank line, the
// "-- " marker, the content. ONE definition - the editor buffer, the
// preview, and the assembled message all use it (DRY).
func SigBlock(content string) string {
	return "\n\n-- \n" + content
}

// BodyWithSig joins the body and the signature block: the body
// normalizes its trailing newlines, one blank line separates. Without
// a signature the body passes through untouched.
func BodyWithSig(body, sigBody string) string {
	if sigBody == "" {
		return body
	}
	return strings.TrimRight(body, "\n") + SigBlock(sigBody)
}

// BuildBuffer is the editor buffer contract (spec section 7): the
// header block, one blank separator line, the body, the signature
// block. No trailing newline (the file write may add one; the parse
// strips it).
func (s *State) BuildBuffer() string {
	var b strings.Builder
	fmt.Fprintf(&b, "To: %s\n", strings.Join(s.To, ", "))
	fmt.Fprintf(&b, "Cc: %s\n", strings.Join(s.Cc, ", "))
	fmt.Fprintf(&b, "Subject: %s\n\n", s.Subject)
	b.WriteString(BodyWithSig(s.Body, s.SignatureBody))
	return b.String()
}

// ParseBuffer parses the editor buffer back into the fields (spec
// section 7): headers up to the first blank line, the rest the body.
// CRLF line endings (vim fileformat=dos) normalize to LF at entry - a
// CRLF blank line is "\r\n\r\n", which would otherwise swallow the
// whole buffer as headers. Address lists split on commas; blank
// entries drop. Unknown header lines are dropped (the three fields
// own the block - pinned contract). The signature tail detaches by
// exact match with the previously attached block: a matched tail
// keeps the signature, an edited tail stays as user text and detaches
// it. A buffer without the separator parses as all-headers, empty
// body.
func ParseBuffer(buf, prevSigName, prevSigBody string) (to, cc []string, subject, body, sigName, sigBody string) {
	buf = strings.ReplaceAll(buf, "\r\n", "\n")
	buf = strings.TrimSuffix(buf, "\n")
	head, rest := buf, ""
	if i := strings.Index(buf, "\n\n"); i >= 0 {
		head, rest = buf[:i], buf[i+2:]
	}
	parse := func(pref string) []string {
		var out []string
		for _, l := range strings.Split(head, "\n") {
			if v, ok := strings.CutPrefix(l, pref); ok {
				for _, a := range strings.Split(v, ",") {
					if a = strings.TrimSpace(a); a != "" {
						out = append(out, a)
					}
				}
			}
		}
		return out
	}
	to, cc = parse("To:"), parse("Cc:")
	for _, l := range strings.Split(head, "\n") {
		if v, ok := strings.CutPrefix(l, "Subject:"); ok {
			subject = strings.TrimSpace(v)
		}
	}
	body = rest
	if prevSigBody != "" {
		block := SigBlock(prevSigBody)
		if strings.HasSuffix(body, block) {
			body = strings.TrimSuffix(body, block)
			return to, cc, subject, body, prevSigName, prevSigBody
		}
	}
	return to, cc, subject, body, "", ""
}
