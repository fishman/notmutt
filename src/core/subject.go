// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package core

import (
	"mime"
	"strings"

	"github.com/emersion/go-message/charset"
)

// subjectDecoder decodes RFC 2047 encoded-words in header values.
// notmuch stores subjects verbatim (raw header bytes), so every
// boundary that feeds a display surface - query ingest, file parse -
// runs through DecodeSubject; the raw form stays only in the h-key
// header block and the cache keys.
var subjectDecoder = &mime.WordDecoder{CharsetReader: charset.Reader}

// DecodeSubject decodes a header value to its display form. Embedded
// encoded-words decode in place - an ASCII prefix survives ("Re:
// =?utf-8?Q?...?="), the common reply shape. Malformed or unknown-
// charset words keep the raw text: a display wart beats a lost
// subject.
func DecodeSubject(s string) string {
	if !strings.Contains(s, "=?") {
		return s
	}
	dec, err := subjectDecoder.DecodeHeader(s)
	if err != nil {
		return s
	}
	return dec
}
