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

// boxWithImg builds a one-image body, returns the img box with its
// intrinsic set, for driving usedImg directly (a pure sizing function;
// probe appendix A-N).
func imgBox(t *testing.T, body string, iw, ih int) *Box {
	t.Helper()
	bs := buildBody(body)
	if len(bs) != 1 {
		t.Fatalf("body = %d boxes, want 1", len(bs))
	}
	b := bs[0]
	if b.Role != RoleImg {
		t.Fatalf("box role = %d, want RoleImg", b.Role)
	}
	if b.res == nil {
		b.res = &imgRes{}
	}
	b.res.iw, b.res.ih = iw, ih
	return b
}

func TestUsedImgSizingProbes(t *testing.T) {
	cases := []struct {
		name   string
		body   string // the <img ...> markup (CSS in style, attrs on the tag)
		iw, ih int    // intrinsic px (the probe's source image)
		avail  int    // containing-block px
		wantW  int
		wantH  int
	}{
		// A: neither specified -> intrinsic
		{"A auto -> intrinsic", `<img src="m60x30.png">`, 60, 30, 800, 60, 30},
		// B/C: attrs alone size (NOTMUTT extension; weasyprint ignores them)
		{"B width attr only", `<img src="m60x30.png" width="120">`, 60, 30, 800, 120, 60},
		{"C both attrs", `<img src="m60x30.png" width="120" height="40">`, 60, 30, 800, 120, 40},
		// D: CSS width beats the attr, ratio scales height
		{"D css width beats attr", `<img src="m60x30.png" width="120" style="width:90px">`, 60, 30, 800, 90, 45},
		// E/F: max-width clamps, ratio rescales height
		{"E max-width px", `<img src="m60x30.png" style="max-width:50px">`, 60, 30, 800, 50, 25},
		{"F max-width 100%", `<img src="m200x100.png" style="max-width:100%">`, 200, 100, 100, 100, 50},
		// G: % width resolves against avail
		{"G width 50%", `<img src="m200x100.png" style="width:50%">`, 200, 100, 200, 100, 50},
		// H: both css axes -> both honored, ratio dropped
		{"H both css axes", `<img src="m60x30.png" style="width:120px;height:40px">`, 60, 30, 800, 120, 40},
		// I: css height only -> width from ratio
		{"I height only", `<img src="m60x30.png" style="height:120px">`, 60, 30, 800, 240, 120},
		// J/K: fractional ratio rounds half away from zero
		{"J fractional up", `<img src="m300x200.png" style="width:250px">`, 300, 200, 800, 250, 167},
		{"K fractional up", `<img src="m300x200.png" style="width:100px">`, 300, 200, 800, 100, 67},
		// M/N: % height is auto
		{"M pct height alone", `<img src="m200x100.png" style="height:50%">`, 200, 100, 800, 200, 100},
		{"N pct height with width", `<img src="m200x100.png" style="width:120px;height:50%">`, 200, 100, 800, 120, 60},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := imgBox(t, tc.body, tc.iw, tc.ih)
			w, h := usedImg(b, tc.avail)
			if w != tc.wantW || h != tc.wantH {
				t.Fatalf("usedImg = %dx%d, want %dx%d", w, h, tc.wantW, tc.wantH)
			}
		})
	}
}

func TestImgExtentWidth(t *testing.T) {
	// px and intrinsic images contribute their width; a % width seats the
	// intrinsic natural width; nothing -> 0
	if b := imgBox(t, `<img src="m60x30.png" width="120">`, 60, 30); imgExtentW(b) != 120 {
		t.Fatalf("attr width extent = %d, want 120", imgExtentW(b))
	}
	if b := imgBox(t, `<img src="m60x30.png">`, 60, 30); imgExtentW(b) != 60 {
		t.Fatalf("intrinsic extent = %d, want 60", imgExtentW(b))
	}
	if b := imgBox(t, `<img src="m60x30.png" style="width:50%">`, 60, 30); imgExtentW(b) != 60 {
		t.Fatalf("pct width extent = %d, want intrinsic 60 (never a %% number)", imgExtentW(b))
	}
	if b := imgBox(t, `<img src="broken.png">`, 0, 0); imgExtentW(b) != 0 {
		t.Fatalf("no-intrinsic extent = %d, want 0", imgExtentW(b))
	}
}

func TestRoleImgBoxCarriesRes(t *testing.T) {
	// buildElement must allocate res on every RoleImg box; without it the
	// imgBox helper's nil fallback would mask a deleted allocation
	bs := buildBody(`<img src="m60x30.png">`)
	if len(bs) != 1 || bs[0].Role != RoleImg {
		t.Fatalf("body = %d boxes role %d, want one RoleImg", len(bs), bs[0].Role)
	}
	if bs[0].res == nil {
		t.Fatal("buildElement must allocate res on a RoleImg box")
	}
}

func TestMaxWidthClampHeightSurvival(t *testing.T) {
	// CSS 2.1 10.4: a non-auto height survives a max-width clamp; only an
	// auto height recomputes from the intrinsic ratio (probe maxh, maxh2).
	// The E/F rows re-pin the auto-height rescale so the whole matrix locks.
	cases := []struct {
		name   string
		body   string
		iw, ih int
		avail  int
		wantW  int
		wantH  int
	}{
		// auto height: clamp rescales height by ratio
		{"E auto h px clamp", `<img src="m60x30.png" style="max-width:50px">`, 60, 30, 800, 50, 25},
		{"F auto h pct clamp", `<img src="m200x100.png" style="max-width:100%">`, 200, 100, 100, 100, 50},
		// specified height: clamp caps width, height survives
		{"both axes + clamp", `<img src="m200x100.png" style="width:120px;height:40px;max-width:50px">`, 200, 100, 800, 50, 40},
		{"height only + clamp", `<img src="m200x100.png" style="height:200px;max-width:150px">`, 200, 100, 800, 150, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := imgBox(t, tc.body, tc.iw, tc.ih)
			w, h := usedImg(b, tc.avail)
			if w != tc.wantW || h != tc.wantH {
				t.Fatalf("usedImg = %dx%d, want %dx%d", w, h, tc.wantW, tc.wantH)
			}
		})
	}
}

func TestResolveImagesFillsIntrinsics(t *testing.T) {
	load := func(src string) (w, h int, ok bool) {
		switch src {
		case "ok.png":
			return 60, 30, true
		case "deep.png":
			return 12, 6, true
		case "broken.png":
			return 40, 20, false // decode failed: nonzero dims must NOT land
		}
		return 0, 0, false // empty src, unknown
	}
	bs := buildBody(`<p><img src="ok.png"></p><div><ul><li><img src="deep.png"></li></ul></div><p><img src="broken.png"><img src=""></p>`)
	ResolveImages(bs, load)
	if b := bs[0].Children[0].res; b == nil || b.iw != 60 || b.ih != 30 {
		t.Fatalf("ok.png intrinsic = %+v, want 60x30", b)
	}
	// descend to the img under div > ul > li: the walk must reach depth 3
	div := bs[1]
	li := div.Children[0].Children[0]
	if b := li.Children[0].res; b == nil || b.iw != 12 || b.ih != 6 {
		t.Fatalf("deep.png intrinsic = %+v, want 12x6", b)
	}
	p3 := bs[2]
	if b := p3.Children[0].res; b == nil || b.iw != 0 || b.ih != 0 {
		t.Fatalf("broken.png intrinsic = %+v, want 0x0 (load ok=false)", b)
	}
	if b := p3.Children[1].res; b == nil || b.iw != 0 {
		t.Fatalf("empty-src intrinsic = %+v, want 0x0", b)
	}
}

func TestResolveImagesNilLoaderIsNoop(t *testing.T) {
	bs := buildBody(`<p><img src="ok.png"></p>`)
	ResolveImages(bs, nil) // must not panic
	if b := bs[0].Children[0].res; b == nil || b.iw != 0 {
		t.Fatalf("nil loader intrinsic = %+v, want untouched 0x0", b)
	}
}
