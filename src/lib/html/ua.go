// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

// The UA floor (html5_ua.css analog): tag defaults the cascade does not
// carry. uaDisplay and effectiveWS are layered over StyleOf by the box
// builder; the running mail walker never reads them.

// uaDisplay is the UA default display keyword for a tag, "" when the
// default is inline. The head/script/skip set mirrors mail's skipTags.
func uaDisplay(tag string) string {
	switch tag {
	case "address", "article", "aside", "blockquote", "body", "dd",
		"details", "dialog", "div", "dl", "dt", "fieldset", "figcaption",
		"figure", "footer", "form", "h1", "h2", "h3", "h4", "h5", "h6",
		"header", "hr", "html", "legend", "main", "nav", "ol", "p",
		"pre", "section", "ul":
		return "block"
	case "li":
		return "list-item"
	case "table":
		return "table"
	case "thead", "tbody", "tfoot":
		return "table-row-group"
	case "tr":
		return "table-row"
	case "td", "th":
		return "table-cell"
	case "caption":
		return "table-caption"
	case "colgroup":
		return "table-column-group"
	case "col":
		return "table-column"
	case "base", "head", "iframe", "link", "meta", "noscript",
		"script", "style", "template", "title":
		return "none"
	}
	return ""
}

// effectiveWS resolves a node's white-space class: an author
// declaration wins, else the pre-tag UA default, else the inherited
// value. white-space inherits; the UA only overrides inheritance at the
// element whose tag owns a default (pre).
func effectiveWS(tag string, s *Style) WS {
	if s.WSSet {
		return s.WS
	}
	if tag == "pre" {
		return WSPre
	}
	return s.WS
}

// listMarker is the ::marker type for an li under a list of the given
// tag at the given nesting depth (weasyprint html5_ua.css nesting).
func listMarker(tag string, depth int) string {
	if tag == "ol" {
		return "decimal"
	}
	switch {
	case depth >= 3:
		return "square"
	case depth == 2:
		return "circle"
	default:
		return "disc"
	}
}

// uaMargins fills the UA margin defaults for a tag where the author did
// not set the side, and the ul/ol padding-left gutter. depth is the list
// nesting the box sits under (0 = no list ancestor): nested lists drop
// their vertical margins. Layered by the box builder after StyleOf, so
// the running mail walker never sees these. Heading margins are the UA
// em values folded to px at the base-16 ladder (html5_ua.css fonts:
// h1 2em .. h6 .67em), since stage 1 has no font-size property.
func uaMargins(tag string, depth int, s *Style) {
	t, b := 0, 0
	switch tag {
	case "h1":
		t, b = 21, 21
	case "h2":
		t, b = 20, 20
	case "h3":
		t, b = 19, 19
	case "h4":
		t, b = 21, 21
	case "h5":
		t, b = 22, 22
	case "h6":
		t, b = 25, 25
	case "p", "dl", "pre", "blockquote", "figure", "dd":
		t, b = 16, 16
	case "ul", "ol":
		if depth == 0 {
			t, b = 16, 16
		}
		if tag == "ul" || tag == "ol" {
			s.PadLeft = 40 // the hanging-marker gutter
		}
	case "hr":
		t, b = 8, 8
	}
	if !s.MarginTopSet {
		s.MarginTop = t
	}
	if !s.MarginBottomSet {
		s.MarginBottom = b
	}
	l, r := 0, 0
	switch tag {
	case "blockquote", "figure":
		l, r = 40, 40
	case "dd":
		l = 40
	}
	if !s.MarginLeftSet {
		s.MarginLeft = l
	}
	if !s.MarginRightSet {
		s.MarginRight = r
	}
}
