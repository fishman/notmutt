// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import "notmutt/core"

// layer caches one frame region's rendered string; the region rebuilds
// only when its signature changes. The View is one flat string - no
// layer API in the runtime (tea v2 or tcell alike) - so the frame's
// regions are cached model-side: the keyhint row, the status line, the
// help overlay. Layers are *layer fields on the Model: View runs on a
// copy of the model, so render-time cache writes persist only through
// reference fields.
type layer struct {
	sig string
	s   string
}

func (l *layer) get(sig string, build func() string) string {
	if l.sig != sig {
		l.sig = sig
		l.s = build()
	}
	return l.s
}

// rowCacheMax bounds the row cache: a full walk (33k rows) at a few
// window widths fits comfortably; past that the cache clears wholesale
// and refills over the next paints (an overflow happens once per large
// scroll, never per press).
const rowCacheMax = 8192

// rowKey identifies one rendered index row: the flatten's row address
// plus every style-affecting parameter. Merges, tag changes, and staged
// ops reflatten and churn addresses (auto-miss); SetAtts mutates the
// shared message without a reflatten, so the atts bool (the attach
// icon reads only len(Atts) > 0) covers it. styleVer bumps on theme
// changes. pad is the style boundary - the view width plus the pan
// offset - so a pan re-renders every row at the new boundary.
type rowKey struct {
	row      *core.Row
	numWidth int
	tagWidth int
	pad      int
	styles   int
	selected bool
	atts     bool
	query    string // the search pattern: a change re-renders every row
}
