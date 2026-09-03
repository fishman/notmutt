// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import "testing"

func TestCascadeImageSizingLengths(t *testing.T) {
	// px, %, and max-width all land on the style
	bs := buildBody(`<img style="width:120px;height:50%;max-width:300px" src="x">`)
	st := bs[0].St
	if st.Width != (CSSLen{Px: 120}) {
		t.Fatalf("width = %+v, want 120px", st.Width)
	}
	if st.Height != (CSSLen{Px: 50, Pct: true}) {
		t.Fatalf("height = %+v, want 50%%", st.Height)
	}
	if st.MaxWidth != (CSSLen{Px: 300}) {
		t.Fatalf("max-width = %+v, want 300px", st.MaxWidth)
	}
}

func TestImageSizingLengthsDoNotInherit(t *testing.T) {
	bs := buildBody(`<p style="width:200px">x<b style="max-width:100px">y</b></p>`)
	if bs[0].St.Width != (CSSLen{Px: 200}) {
		t.Fatalf("p width = %+v, want 200px", bs[0].St.Width)
	}
	// the b box is inline: find it under the p and check geometry zeroed
	var b *Box
	for _, c := range bs[0].Children {
		if c.Role == RoleInline {
			b = c
		}
	}
	if b == nil {
		t.Fatal("no inline b box under p")
	}
	if b.St.Width != (CSSLen{}) || b.St.Height != (CSSLen{}) {
		t.Fatalf("b width/height = %+v/%+v, want zeroed (not inherited)",
			b.St.Width, b.St.Height)
	}
	if b.St.MaxWidth != (CSSLen{Px: 100}) {
		t.Fatalf("b max-width = %+v, want 100px (its own declaration)", b.St.MaxWidth)
	}
}
