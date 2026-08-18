// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package compose

import "testing"

func TestContentTypeOf(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{"plain text body", "text/plain"},
		{"> quoted\nplain", "text/plain"},
		{"just **bold**", "text/plain"},
		{"# t\n\n- x", "text/markdown"},
		{"## h\n```\ncode\n```", "text/markdown"},
		{"[link](https://x)\n\n- item", "text/markdown"},
	}
	for _, c := range cases {
		if got := ContentTypeOf(c.body); got != c.want {
			t.Fatalf("ContentTypeOf(%q) = %q, want %q", c.body, got, c.want)
		}
	}
}
