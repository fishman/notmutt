// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import (
	"math"
	"strconv"
	"strings"
)

// imgRes is one RoleImg box's geometry. Intrinsic (iw/ih) is the decoded
// pixel size from ResolveImages (0 when the source has no loadable image);
// used (uW/uH/uSet) is the px size resolved at layout by usedImg, valid
// for the one layout pass that reached the box.
type imgRes struct {
	iw, ih int
	uW, uH int
	uSet   bool
}

// imgSpec is an image's effective specified geometry after the cascade and
// the width/height attribute hints.
type imgSpec struct {
	wPx, hPx int  // effective specified px; 0 = auto
	wPct     bool // width is a percentage of the containing width
	mwPx     int  // max-width px; 0 = none
	mwPct    bool // max-width is a percentage of the containing width
}

// specImg resolves an image's effective specified width/height/max-width.
// CSS width/height win over the same-named attribute; the attributes are a
// NOTMUTT extension - the pinned mail contract sizes declared-size images
// from them, and weasyprint ignores them (probes B, C). width/height:auto
// and width:0 are "no value", so the attribute hint then fills its axis.
// A percentage height is auto (weasyprint: a % height against an
// auto-height containing block is auto, probes M, N), so it never reaches
// hPx and the height attribute survives it.
func specImg(b *Box) imgSpec {
	var s imgSpec
	if b.Node != nil {
		s.wPx, s.wPct = attrLen(Attr(b.Node, "width"))
		s.hPx, _ = attrLen(Attr(b.Node, "height"))
	}
	if b.St != nil {
		if b.St.Width.Pct {
			s.wPx, s.wPct = b.St.Width.Px, true
		} else if b.St.Width.Px > 0 {
			s.wPx = b.St.Width.Px
		}
		if !b.St.Height.Pct && b.St.Height.Px > 0 {
			s.hPx = b.St.Height.Px
		}
		if b.St.MaxWidth.Pct {
			s.mwPx, s.mwPct = b.St.MaxWidth.Px, true
		} else if b.St.MaxWidth.Px > 0 {
			s.mwPx = b.St.MaxWidth.Px
		}
	}
	return s
}

// attrLen folds a width/height attribute to a length: a positive px
// number, or a positive percentage (Pct true). Junk, 0, and other units
// are not values.
func attrLen(v string) (px int, pct bool) {
	v = strings.TrimSpace(v)
	if strings.HasSuffix(v, "%") {
		if n, err := strconv.Atoi(strings.TrimSuffix(v, "%")); err == nil && n > 0 {
			return n, true
		}
		return 0, false
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return n, false
	}
	return 0, false
}

// imgExtentW is the image's content-width contribution to an inline run
// measured at infinite available width: a specified px width, else the
// intrinsic width. A % width cannot resolve at infinite width and never
// forces a column wider than the image is, so it contributes the intrinsic
// natural width (0 when the image has none). Min equals max: an image is
// atomic (the widest unbreakable piece is the whole).
func imgExtentW(b *Box) int {
	s := specImg(b)
	if !s.wPct && s.wPx > 0 {
		return s.wPx
	}
	if b.res != nil && b.res.iw > 0 {
		return b.res.iw
	}
	return 0
}

// usedImg resolves an image's used px width/height at the available width
// and memoizes them on the box. Weasyprint-faithful on the CSS surface
// (probe appendix A-N): both axes specified -> both honored (ratio dropped,
// probe H); one axis -> the other scales by the intrinsic ratio (D, I); a
// percentage height is auto (M, N); neither specified -> intrinsic (A).
// max-width clamps the resolved width and rescales the height by the ratio
// (E, F). Rounding is half away from zero (math.Round), matching the
// measured fractional probes J/K.
func usedImg(b *Box, avail int) (w, h int) {
	s := specImg(b)
	iw, ih := 0, 0
	if b.res != nil {
		iw, ih = b.res.iw, b.res.ih
	}
	ratio := iw > 0 && ih > 0
	at := func(px int, pct bool) int {
		if pct {
			return px * avail / 100
		}
		return px
	}
	wSet := s.wPx > 0 || s.wPct
	switch {
	case wSet && s.hPx > 0:
		w, h = at(s.wPx, s.wPct), s.hPx
	case wSet:
		w = at(s.wPx, s.wPct)
		if ratio {
			h = int(math.Round(float64(w) * float64(ih) / float64(iw)))
		} else {
			h = ih
		}
	case s.hPx > 0:
		h = s.hPx
		if ratio {
			w = int(math.Round(float64(h) * float64(iw) / float64(ih)))
		} else {
			w = iw
		}
	default:
		w, h = iw, ih
	}
	if mw := at(s.mwPx, s.mwPct); mw > 0 && w > mw {
		w = mw
		// a specified height survives the clamp (CSS 2.1 10.4); only an
		// auto height rescales by the intrinsic ratio
		if s.hPx == 0 && ratio {
			h = int(math.Round(float64(w) * float64(ih) / float64(iw)))
		}
	}
	if b.res != nil {
		b.res.uW, b.res.uH, b.res.uSet = w, h, true
	}
	return w, h
}

// ResolveImages walks the box tree once and fills each RoleImg box's
// intrinsic size from the caller's load. load maps the img's src attribute
// to its pixel dimensions; the caller owns src resolution and image decode
// (privacy and trust boundary: image bytes never enter this package). A
// nil load, an empty src, an ok=false, or a src that is not a decodable
// image leaves the box with no intrinsic (0) - it still lays out from
// CSS/attribute sizes or as a zero-width placeholder. O(#imgs); decode cost
// is the caller's. Layout without a prior ResolveImages measures every
// image at zero intrinsic.
func ResolveImages(boxes []*Box, load func(src string) (w, h int, ok bool)) {
	if load == nil {
		return
	}
	var walk func(cs []*Box)
	walk = func(cs []*Box) {
		for _, b := range cs {
			if b.Role == RoleImg && b.res == nil {
				b.res = &imgRes{}
			}
			if b.Role == RoleImg && b.res != nil && b.Node != nil {
				if src := Attr(b.Node, "src"); src != "" {
					if w, h, ok := load(src); ok && w > 0 && h > 0 {
						b.res.iw, b.res.ih = w, h
					}
				}
			}
			walk(b.Children)
		}
	}
	walk(boxes)
}
