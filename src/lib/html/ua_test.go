// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import (
	"testing"

	xhtml "golang.org/x/net/html"
)

func el(tag string) *xhtml.Node {
	return &xhtml.Node{Type: xhtml.ElementNode, Data: tag}
}

func TestParseWhiteSpaceValues(t *testing.T) {
	cases := map[string]WS{
		"normal": WSNormal, "nowrap": WSNowrap, "pre": WSPre,
		"pre-wrap": WSPreWrap, "pre-line": WSPreLine,
		"Normal": WSNormal, " pre-wrap ": WSPreWrap, "break-spaces": WSNormal,
	}
	for in, want := range cases {
		if got := parseWS(in); got != want {
			t.Errorf("parseWS(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestApplyWhiteSpaceSetsEnum(t *testing.T) {
	var s Style
	s.apply(ParseDecls("white-space: nowrap"))
	if s.WS != WSNowrap || !s.WSSet {
		t.Fatalf("nowrap: WS=%d WSSet=%v, want WSNowrap set", s.WS, s.WSSet)
	}
	if s.Pre {
		t.Fatal("nowrap must not set the legacy Pre flag")
	}
	s = Style{}
	s.apply(ParseDecls("white-space: pre-wrap"))
	if s.WS != WSPreWrap || !s.Pre {
		t.Fatalf("pre-wrap must set WS=%d and legacy Pre, got WS=%d Pre=%v", WSPreWrap, s.WS, s.Pre)
	}
}

func TestWhiteSpaceInheritsButFlagDoesNot(t *testing.T) {
	parent := &Style{WS: WSPre, WSSet: true}
	child := StyleOf(el("p"), parent, nil)
	if child.WS != WSPre {
		t.Fatalf("white-space must inherit, child WS=%d", child.WS)
	}
	if child.WSSet {
		t.Fatal("WSSet must not inherit (it marks an explicit declaration at this node)")
	}
}
