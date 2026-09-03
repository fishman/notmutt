// Copyright 2026 Reza Jelveh
// SPDX-License-Identifier: Apache-2.0

package html

import (
	"strings"

	xhtml "golang.org/x/net/html"
)

// Role is how a box takes part in flow. Table-family boxes (RoleTable)
// carry their grid slot on Tbl.
type Role int

const (
	RoleBlock  Role = iota // vertical flow container
	RoleInline             // inline element (flattens into a line at layout)
	RoleText               // raw text leaf
	RoleBR                 // forced break
	RoleImg                // replaced image (atomic; block or inline)
	RoleTable              // table-family box (table/row-group/row/cell/caption/column)
)

// Box is one node of the stage-1 flow tree. St is the computed style
// the box renders with; text leaves and anonymous run blocks share
// their container's style pointer, never nil. The pointer is shared,
// so computed-style derivations must copy before mutating - StyleOf's
// copy-per-element discipline is deliberately broken for text and
// anonymous boxes. WS is the effective white-space class. Text carries
// raw text (whitespace is collapsed at layout, never at build). A block
// box's children are uniformly block-level or uniformly inline-level;
// mixed content is split into anonymous run blocks.
type Box struct {
	Role     Role
	Tag      string      // element tag, "" on anonymous boxes
	Node     *xhtml.Node // originating element (img src, a href, table)
	St       *Style
	WS       WS
	Tbl      string // table grid slot: table|row-group|row|cell|caption|column-group|column ("" outside tables)
	Marker   string // list-item marker type: disc|circle|square|decimal
	Text     string // RoleText only
	Children []*Box
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
// head/script/skip set produce no box. The body element's own style -
// inherited from the html element when present - is the inheritance
// root, so body/html declarations reach content.
func Build(doc *xhtml.Node, rules []CSSRule) []*Box {
	body := findBody(doc)
	if body == nil {
		return nil
	}
	root := &Style{}
	if body.Parent != nil && body.Parent.Type == xhtml.ElementNode && body.Parent.Data == "html" {
		root = StyleOf(body.Parent, root, rules)
	}
	st := StyleOf(body, root, rules)
	st.WS = effectiveWS("body", st)
	var out []*Box
	for c := body.FirstChild; c != nil; c = c.NextSibling {
		out = append(out, buildNode(c, st, rules, 0)...)
	}
	if hasBlockChild(out) {
		out = splitRuns(out, st)
	}
	return out
}

// findBody descends to the document's body element. Parse yields
// DocumentNode > html > (head, body), so a direct-child scan misses it.
func findBody(n *xhtml.Node) *xhtml.Node {
	if n == nil {
		return nil
	}
	if n.Type == xhtml.ElementNode && n.Data == "body" {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if b := findBody(c); b != nil {
			return b
		}
	}
	return nil
}

// buildNode turns one body child (text or element) into boxes; a
// display:none element or head skip yields none.
func buildNode(n *xhtml.Node, parent *Style, rules []CSSRule, listDepth int) []*Box {
	switch n.Type {
	case xhtml.TextNode:
		return []*Box{{Role: RoleText, St: parent, WS: parent.WS, Text: n.Data}}
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
	uaMargins(tag, listDepth, st)
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
	if role == RoleTable {
		b.Tbl = tableSlot(d)
		switch b.Tbl {
		case "cell":
			if tag == "th" && !st.BoldSet {
				st.Bold = true // UA th bold; stage-1 only (the mail walker never builds)
			}
			fillFlowChildren(b, n, st, rules, listDepth)
		case "caption":
			fillFlowChildren(b, n, st, rules, listDepth) // content built; caption layout deferred
		case "table":
			b.Children = fixTable(tableKids(n, st, rules, listDepth), st, 0)
		case "row-group":
			b.Children = fixTable(tableKids(n, st, rules, listDepth), st, 1)
		case "row":
			b.Children = fixTable(tableKids(n, st, rules, listDepth), st, 2)
		}
		return b
	}
	if role != RoleBlock && role != RoleInline {
		return b // br/img leaves keep their subtree/attrs on Node for later plans
	}
	fillFlowChildren(b, n, st, rules, listDepth)
	if role == RoleInline && hasBlockChild(b.Children) {
		b.Role = RoleBlock
		role = RoleBlock
		st.Display = "block" // blockification rewrites computed display
	}
	return b
}

// fillFlowChildren gathers an element's in-flow children into b: text
// leaves share the parent's style pointer; child elements build
// recursively; a list item under its list gets its marker. Mixed
// block/inline content is split into anonymous runs (blockification), so
// a block or cell box holds uniformly block-level or uniformly
// inline-level children. Geometry (uaMargins) was layered on st before
// the children built, so text leaves inherit it by pointer sharing.
func fillFlowChildren(b *Box, n *xhtml.Node, st *Style, rules []CSSRule, listDepth int) {
	nextDepth := listDepth
	if b.Tag == "ul" || b.Tag == "ol" {
		nextDepth++
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case xhtml.TextNode:
			b.Children = append(b.Children, &Box{Role: RoleText, St: st, WS: st.WS, Text: c.Data})
		case xhtml.ElementNode:
			child := buildElement(c, st, rules, nextDepth)
			if child == nil {
				continue
			}
			if (b.Tag == "ul" || b.Tag == "ol") && isListItem(child) {
				child.Marker = listMarker(b.Tag, nextDepth)
			}
			b.Children = append(b.Children, child)
		}
	}
	if hasBlockChild(b.Children) {
		b.Children = splitRuns(b.Children, st)
	}
}

// tableSlot maps a table-family display keyword to its grid slot. Non-table
// displays return "".
func tableSlot(d string) string {
	switch d {
	case "table":
		return "table"
	case "table-row-group", "table-header-group", "table-footer-group":
		return "row-group"
	case "table-row":
		return "row"
	case "table-cell":
		return "cell"
	case "table-caption":
		return "caption"
	case "table-column-group":
		return "column-group"
	case "table-column":
		return "column"
	}
	return ""
}

// isListItem reports whether a box came from a list-item (li or an
// element with display:list-item) rather than a stray block.
func isListItem(b *Box) bool {
	d := b.St.Display
	if d == "" {
		d = uaDisplay(b.Tag)
	}
	return d == "list-item"
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

// hasBlockChild reports whether any direct child is a block-level box.
func hasBlockChild(cs []*Box) bool {
	for _, c := range cs {
		if c.Role == RoleBlock || c.Role == RoleTable {
			return true
		}
	}
	return false
}

// splitRuns wraps consecutive inline-level children of a block
// container into anonymous run blocks around its block children: text
// before a block does not bleed into it, and the container's children
// come out uniformly block-level. Anonymous runs inherit the
// container's style and white-space class. Whitespace-only runs from
// pretty-printed markup are layout-drop; block flow must not give them
// height.
func splitRuns(cs []*Box, st *Style) []*Box {
	var out []*Box
	var run []*Box
	flush := func() {
		if len(run) == 0 {
			return
		}
		out = append(out, &Box{Role: RoleBlock, St: st, WS: st.WS, Children: run})
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

// tableKids gathers a table-context element's children into boxes,
// dropping whitespace-only text nodes (anonymous-table repair) and the
// display:none/head skips; non-whitespace text is kept so a stray run gets
// a cell instead of vanishing.
func tableKids(n *xhtml.Node, parent *Style, rules []CSSRule, listDepth int) []*Box {
	var out []*Box
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case xhtml.TextNode:
			if strings.TrimSpace(c.Data) == "" {
				continue
			}
			out = append(out, &Box{Role: RoleText, St: parent, WS: parent.WS, Text: c.Data})
		case xhtml.ElementNode:
			if b := buildElement(c, parent, rules, listDepth); b != nil {
				out = append(out, b)
			}
		}
	}
	return out
}

// anonTableBox builds one anonymous grid wrapper sharing the container's
// style pointer (anonymous boxes carry no margins or geometry of their own).
func anonTableBox(kind string, st *Style) *Box {
	return &Box{Role: RoleTable, Tbl: kind, St: st, WS: st.WS}
}

// fixTable repairs a table-context child list into the grid children of a
// box at the given level (0: a table collecting row-groups/captions; 1: a
// row-group collecting rows; 2: a row collecting cells). Anonymous repair
// (weasyprint formatting_structure/build.py): a stray row gets an anonymous
// group, a stray cell gets an anonymous row (and group at level 0/1), and a
// stray run of inline content gets an anonymous cell->row(->group) chain.
// Whitespace-only text is already gone (tableKids).
func fixTable(cs []*Box, st *Style, level int) []*Box {
	var out []*Box
	var run []*Box
	flush := func() {
		if len(run) == 0 {
			return
		}
		cell := anonTableBox("cell", st)
		cell.Children = run
		if hasBlockChild(run) {
			cell.Children = splitRuns(run, st)
		}
		row := anonTableBox("row", st)
		row.Children = []*Box{cell}
		switch level {
		case 0:
			g := anonTableBox("row-group", st)
			g.Children = []*Box{row}
			out = append(out, g)
		case 1:
			out = append(out, row)
		default:
			out = append(out, cell)
		}
		run = nil
	}
	wrapCell := func(cell *Box) {
		row := anonTableBox("row", st)
		row.Children = []*Box{cell}
		switch level {
		case 0:
			g := anonTableBox("row-group", st)
			g.Children = []*Box{row}
			out = append(out, g)
		case 1:
			out = append(out, row)
		default:
			out = append(out, cell)
		}
	}
	for _, c := range cs {
		switch {
		case c.Tbl == "cell" && level == 2: // a proper td under its row
			flush()
			out = append(out, c)
		case level == 0 && (c.Tbl == "row-group" || c.Tbl == "caption"):
			flush()
			out = append(out, c)
		case level == 0 && c.Tbl == "row": // stray tr under a table
			flush()
			g := anonTableBox("row-group", st)
			g.Children = []*Box{c}
			out = append(out, g)
		case level == 1 && c.Tbl == "row":
			flush()
			out = append(out, c)
		case c.Tbl == "cell": // stray td under a table or row-group
			flush()
			wrapCell(c)
		default: // inline runs, stray block/nested-table content: an anonymous cell
			run = append(run, c)
		}
	}
	flush()
	return out
}
