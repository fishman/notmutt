package compose

import (
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

// SplitAddrs splits an address list on commas and drops blanks - the
// one canonical list parse for the compose field editors (DRY).
func SplitAddrs(s string) []string {
	var out []string
	for _, a := range strings.Split(s, ",") {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// ParseBuffer parses the editor buffer back (spec section 7): the
// buffer holds ONLY the mail content - the body and the attached
// signature tail (mutt's msgbody shape). The email header never
// lives here; the dialogue fields build it at assembly. The
// signature tail detaches by exact match with the previously
// attached block: a matched tail keeps the signature, an edited tail
// stays as user text and detaches it. CRLF line endings (vim
// fileformat=dos) normalize to LF at entry; a trailing newline
// strips.
func ParseBuffer(buf, prevSigName, prevSigBody string) (body, sigName, sigBody string) {
	buf = strings.ReplaceAll(buf, "\r\n", "\n")
	body = strings.TrimSuffix(buf, "\n")
	if prevSigBody != "" {
		block := SigBlock(prevSigBody)
		if strings.HasSuffix(body, block) {
			body = strings.TrimSuffix(body, block)
			return body, prevSigName, prevSigBody
		}
	}
	return body, "", ""
}
