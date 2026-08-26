// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"testing"

	"notmutt/core"
)

// TestPagerSMIME pins the R10 verdict banner: a valid signature prepends a
// green (OK) line naming the signer; a failed verify prepends a red line with
// the reason; an unsigned message adds nothing.
func TestPagerSMIME(t *testing.T) {
	cases := []struct {
		name string
		sm   *core.SMIMEStatus
		want string
		ok   bool
	}{
		{"valid", &core.SMIMEStatus{Present: true, Valid: true, Signer: "alpha@example.com"}, "[S/MIME] valid signature from alpha@example.com", true},
		{"revoked", &core.SMIMEStatus{Present: true, Valid: true, Signer: "alpha@example.com", Checked: true, Revoked: true}, "[S/MIME] valid signature from alpha@example.com (revoked)", true},
		{"error", &core.SMIMEStatus{Present: true, Err: "no roots"}, "[S/MIME] could not verify: no roots", false},
	}
	for _, c := range cases {
		p := newPager("", "", []core.Line{{Text: "body"}})
		p.setSMIME(c.sm)
		first := p.lines[0]
		if first.Text != c.want {
			t.Errorf("%s: banner = %q, want %q", c.name, first.Text, c.want)
		}
		if first.Kind != core.LineSecurity || first.OK != c.ok {
			t.Errorf("%s: banner kind=%v ok=%v, want LineSecurity ok=%v", c.name, first.Kind, first.OK, c.ok)
		}
	}
	p := newPager("", "", []core.Line{{Text: "body"}})
	p.setSMIME(nil)
	if len(p.lines) != 1 || p.lines[0].Text != "body" {
		t.Fatal("unsigned message must add no banner")
	}
}
