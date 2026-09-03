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

func TestWhiteSpaceApplyTriple(t *testing.T) {
	cases := map[string]struct {
		ws  WS
		wss bool
		pre bool
	}{
		"normal":   {WSNormal, true, false},
		"nowrap":   {WSNowrap, true, false},
		"pre":      {WSPre, true, true},
		"pre-wrap": {WSPreWrap, true, true},
		"pre-line": {WSPreLine, true, true},
	}
	for in, want := range cases {
		var s Style
		s.apply(ParseDecls("white-space: " + in))
		if s.WS != want.ws || s.WSSet != want.wss || s.Pre != want.pre {
			t.Errorf("white-space %q: WS=%d WSSet=%v Pre=%v, want WS=%d WSSet=%v Pre=%v",
				in, s.WS, s.WSSet, s.Pre, want.ws, want.wss, want.pre)
		}
	}
}

func TestExplicitNormalClearsInheritedWrap(t *testing.T) {
	s := StyleOf(el("span"), &Style{WS: WSPreWrap, WSSet: true}, nil)
	s.apply(ParseDecls("white-space: normal"))
	if s.WS != WSNormal || !s.WSSet || s.Pre {
		t.Fatalf("explicit normal on pre-wrap child: WS=%d WSSet=%v Pre=%v, want WSNormal set, Pre=false", s.WS, s.WSSet, s.Pre)
	}
}

func TestUADisplayDefaults(t *testing.T) {
	cases := map[string]string{
		"p": "block", "div": "block", "span": "", "b": "", "a": "",
		"table": "table", "tr": "table-row", "td": "table-cell",
		"thead": "table-row-group", "caption": "table-caption",
		"script": "none", "title": "none", "style": "none",
		"li": "block", "img": "", "br": "",
	}
	for tag, want := range cases {
		if got := uaDisplay(tag); got != want {
			t.Errorf("uaDisplay(%q) = %q, want %q", tag, got, want)
		}
	}
}

func TestEffectiveWhiteSpacePreTag(t *testing.T) {
	// a bare <pre> with no author declaration must behave as pre
	s := StyleOf(el("pre"), &Style{}, nil)
	if got := effectiveWS("pre", s); got != WSPre {
		t.Fatalf("bare pre: effectiveWS = %d, want WSPre", got)
	}
	// an explicit author value wins over the tag default
	s2 := StyleOf(el("pre"), &Style{}, nil)
	s2.apply(ParseDecls("white-space: nowrap"))
	if got := effectiveWS("pre", s2); got != WSNowrap {
		t.Fatalf("author nowrap on pre: effectiveWS = %d, want WSNowrap", got)
	}
	// an inline element inherits the parent class
	parent := &Style{WS: WSPreWrap}
	s3 := StyleOf(el("span"), parent, nil)
	if got := effectiveWS("span", s3); got != WSPreWrap {
		t.Fatalf("inherited pre-wrap: effectiveWS = %d, want WSPreWrap", got)
	}
}

func TestListMarkerByDepth(t *testing.T) {
	if got := listMarker("ul", 1); got != "disc" {
		t.Errorf("ul depth1 = %q, want disc", got)
	}
	if got := listMarker("ul", 2); got != "circle" {
		t.Errorf("ul depth2 = %q, want circle", got)
	}
	if got := listMarker("ul", 3); got != "square" {
		t.Errorf("ul depth3 = %q, want square", got)
	}
	if got := listMarker("ol", 2); got != "decimal" {
		t.Errorf("ol = %q, want decimal", got)
	}
}
