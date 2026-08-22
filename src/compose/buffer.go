// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package compose

import (
	"strings"
)

// SigBlock is the signature block: a blank line, "-- ", the content.
// ONE definition - editor buffer, preview, and assembly share it (DRY).
func SigBlock(content string) string {
	return "\n\n-- \n" + content
}

// BodyWithSig joins body and signature block: the body's trailing
// newlines normalize, one blank line separates; no signature -> body
// untouched.
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
// buffer holds ONLY the mail content - body plus attached signature
// tail (mutt's msgbody); headers never live here, the dialogue fields
// build them at assembly. The tail detaches by exact match: a matched
// tail keeps the signature, an edited tail stays as user text. CRLF
// (vim fileformat=dos) normalizes to LF; a trailing newline strips.
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
