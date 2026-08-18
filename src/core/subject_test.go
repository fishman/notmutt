// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package core

import "testing"

// TestDecodeSubject pins the RFC 2047 decode: a pure encoded-word, the
// reply shape with an ASCII "Re: " prefix (the raw-header case - a
// whole-string decode would fail it), a base64 word, a latin-1 word
// via the charset reader, and the error paths keeping the raw text.
func TestDecodeSubject(t *testing.T) {
	cases := []struct{ in, want string }{
		{"hello", "hello"},
		{"", ""},
		{"=?utf-8?Q?Acme=20GmbH?=", "Acme GmbH"},
		{"Re: =?utf-8?Q?Acme=20GmbH?=", "Re: Acme GmbH"},
		{"=?utf-8?B?QWNtZSBHbWJI?=", "Acme GmbH"},
		{"=?iso-8859-1?Q?Acme=20M=FCnchen?=", "Acme München"},
		{"=?bogus-charset?Q?Acme?=", "=?bogus-charset?Q?Acme?="},
		{"Re: =?utf-8?Q?broken", "Re: =?utf-8?Q?broken"},
	}
	for _, c := range cases {
		if got := DecodeSubject(c.in); got != c.want {
			t.Errorf("DecodeSubject(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
