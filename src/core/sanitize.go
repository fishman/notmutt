package core

import "strings"

// SanitizeControls drops C0/DEL/C1 control runes so mail-derived text
// can never inject terminal escapes (F1). Every render path that
// touches mail content goes through this - the mail package's render
// lines and the index rows - never trusting raw content at the
// terminal.
func SanitizeControls(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || (r >= 0x7F && r <= 0x9F) }) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r < 0x20 || (r >= 0x7F && r <= 0x9F) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
