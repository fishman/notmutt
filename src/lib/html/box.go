// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import (
	xhtml "golang.org/x/net/html"
)

// Role is how a box takes part in flow. Tables arrive as leaves until
// the table plan expands them; table rows/cells only appear once that
// expansion runs.
type Role int

const (
	RoleBlock  Role = iota // vertical flow container
	RoleInline             // inline element (flattens into a line at layout)
	RoleText               // raw text leaf
	RoleBR                 // forced break
	RoleImg                // replaced image (atomic; block or inline)
	RoleTable              // table grid (leaf in this plan)
)

// Box is one node of the stage-1 flow tree. St is the computed style;
// WS is the effective white-space class. Text carries raw text
// (whitespace is collapsed at layout, never at build).
type Box struct {
	Role   Role
	Tag    string      // element tag, "" on anonymous boxes
	Node   *xhtml.Node // originating element (img src, table ref, list tag)
	St     *Style
	WS     WS
	Marker string // list-item marker type: disc|circle|square|decimal
	Text   string // RoleText only
	Child  []*Box
}

// ParseStyleSheets gathers every <style> element's text into one
// cascade.
func ParseStyleSheets(doc *xhtml.Node) []CSSRule {
	var rules []CSSRule
	var walk func(n *xhtml.Node)
	walk = func(n *xhtml.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == xhtml.ElementNode {
				if c.Data == "style" && c.FirstChild != nil {
					rules = append(rules, ParseStyleSheet(c.FirstChild.Data)...)
				}
				walk(c)
			}
		}
	}
	walk(doc)
	return rules
}

// Build returns the body's top-level flow boxes under the cascade. A
// document without a body yields nil. display:none and the
// head/script/skip set produce no box.
func Build(doc *xhtml.Node, rules []CSSRule) []*Box {
	var body *xhtml.Node
	var find func(n *xhtml.Node)
	find = func(n *xhtml.Node) {
		for c := n.FirstChild; c != nil && body == nil; c = c.NextSibling {
			if c.Type == xhtml.ElementNode {
				if c.Data == "body" {
					body = c
					return
				}
				find(c)
			}
		}
	}
	find(doc)
	if body == nil {
		return nil
	}
	var out []*Box
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		out = append(out, buildNode(c, &Style{}, rules, 0)...)
	}
	return out
}

// buildNode turns one body child (text or element) into boxes; a
// display:none element or head skip yields none.
func buildNode(n *xhtml.Node, parent *Style, rules []CSSRule, listDepth int) []*Box {
	switch n.Type {
	case xhtml.TextNode:
		return []*Box{{Role: RoleText, Text: n.Data, WS: effectiveWS("", parent)}}
	case xhtml.ElementNode:
		if b := buildElement(n, parent, rules, listDepth); b != nil {
			return []*Box{b}
		}
	}
	return nil
}

// buildElement builds one element into a box (nil when dropped).
func buildElement(n *xhtml.Node, parent *Style, rules []CSSRule, listDepth int) *Box {
	st := StyleOf(n, parent, rules)
	tag := n.Data
	d := st.Display
	if d == "" {
		d = uaDisplay(tag)
	}
	if d == "none" {
		return nil
	}
	// Promote the effective white-space class onto the box's own style
	// so nested descendants inherit it: a bare <pre> must carry WSPre
	// into the spans and text it contains (UA floor), not just its own
	// box. StyleOf copies the parent wholesale, so the promotion reaches
	// every child before their own declarations re-apply.
	st.WS = effectiveWS(tag, st)
	var role Role
	switch tag {
	case "br":
		role = RoleBR
	case "img":
		role = RoleImg
	default:
		role = roleOf(d)
	}
	b := &Box{Role: role, Tag: tag, Node: n, St: st, WS: st.WS}
	// Table-family and replaced/break leaves carry their subtree/attrs
	// on Node; later plans expand them.
	if role != RoleBlock && role != RoleInline {
		return b
	}
	nextDepth := listDepth
	if tag == "ul" || tag == "ol" {
		nextDepth++
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case xhtml.TextNode:
			b.Child = append(b.Child, &Box{Role: RoleText, Text: c.Data, WS: st.WS})
		case xhtml.ElementNode:
			if child := buildElement(c, st, rules, nextDepth); child != nil {
				if tag == "ul" || tag == "ol" {
					child.Marker = listMarker(tag, nextDepth)
				}
				b.Child = append(b.Child, child)
			}
		}
	}
	if role == RoleInline && hasBlockChild(b.Child) {
		b.Role = RoleBlock
		b.Child = splitRuns(b.Child)
	}
	return b
}

// roleOf maps an effective display keyword to a Role. Unknown values
// (flex, grid, inline-block) land on block - the mail-safe default the
// walker already used.
func roleOf(d string) Role {
	switch d {
	case "block", "flex", "grid", "inline-block", "flow-root", "list-item":
		return RoleBlock
	case "table", "table-row-group", "table-header-group",
		"table-footer-group", "table-row", "table-cell", "table-caption",
		"table-column-group", "table-column":
		return RoleTable
	}
	return RoleInline
}

// hasBlockChild reports whether any direct child is a block-level box
// (the block-in-inline trigger).
func hasBlockChild(cs []*Box) bool {
	for _, c := range cs {
		if c.Role == RoleBlock || c.Role == RoleTable {
			return true
		}
	}
	return false
}

// splitRuns wraps consecutive inline-level children of a now-blockified
// container into anonymous blocks, separating them from block children
// (the block-in-inline split): text before a block does not bleed into
// it.
func splitRuns(cs []*Box) []*Box {
	var out []*Box
	var run []*Box
	flush := func() {
		if len(run) == 0 {
			return
		}
		out = append(out, &Box{Role: RoleBlock, Child: run})
		run = nil
	}
	for _, c := range cs {
		if c.Role == RoleBlock || c.Role == RoleTable {
			flush()
			out = append(out, c)
		} else {
			run = append(run, c)
		}
	}
	flush()
	return out
}
