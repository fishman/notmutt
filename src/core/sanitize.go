// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package core

import "strings"

// SanitizeControls drops C0/DEL/C1 control runes so mail-derived text
// can never inject terminal escapes (F1). Every render path touching
// mail content - the mail package's render lines and the index rows -
// goes through this; raw content is never trusted at the terminal.
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

// SanitizeText is SanitizeControls for multi-line text (the AI summary
// stream): \n and \t survive, the rest of the C0/DEL/C1 range drops - a
// streamed paragraph keeps its structure, escape injection does not.
func SanitizeText(s string) string {
	if !strings.ContainsFunc(s, func(r rune) bool {
		return (r < 0x20 && r != '\n' && r != '\t') || (r >= 0x7F && r <= 0x9F)
	}) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if (r < 0x20 && r != '\n' && r != '\t') || (r >= 0x7F && r <= 0x9F) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
