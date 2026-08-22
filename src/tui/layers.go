// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package tui

import "notmutt/core"

// layer caches one frame region's rendered string, rebuilt only when
// its signature changes. The View is one flat string (no layer API in
// tea v2 or tcell), so regions cache model-side (keyhint, status,
// help). Layers are *layer fields on the Model: View runs on a copy,
// so cache writes persist only through reference fields.
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
// widths fits; past that the cache clears wholesale and refills over
// the next paints (once per large scroll, never per press).
const rowCacheMax = 8192

// rowKey identifies one rendered index row: the flatten's address plus
// every style-affecting parameter. Reflattens churn addresses
// (auto-miss); SetAtts mutates the shared message without a reflatten,
// so the atts bool covers it. styleVer bumps on theme changes. pad is
// the style boundary (view width + pan offset), so a pan re-renders.
type rowKey struct {
	row      *core.Row
	numWidth int
	tagWidth int
	pad      int
	styles   int
	selected bool
	atts     bool
	query    string       // the search pattern: a change re-renders every row
	mark     core.MsgMark // the row's thread-position tint (the loaded thread's tail); only the marked rows' keys churn on open
}
