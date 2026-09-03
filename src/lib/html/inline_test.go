// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import "testing"

func TestWSBehavior(t *testing.T) {
	tests := []struct {
		ws           WS
		wrap         bool
		collapse     bool
		newlineBreak bool
	}{
		{WSNormal, true, true, false},
		{WSNowrap, false, true, false},
		{WSPre, false, false, true},
		{WSPreWrap, true, false, true},
		{WSPreLine, true, true, true},
	}
	for _, tc := range tests {
		b := wsBehavior(tc.ws)
		if b.wrap != tc.wrap || b.collapse != tc.collapse || b.newlineBreak != tc.newlineBreak {
			t.Errorf("wsBehavior(%d) = %+v, want wrap=%v collapse=%v newlineBreak=%v",
				tc.ws, b, tc.wrap, tc.collapse, tc.newlineBreak)
		}
	}
}
